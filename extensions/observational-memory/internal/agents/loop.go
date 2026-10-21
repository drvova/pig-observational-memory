package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/host"
)

// Now is the clock for message timestamps; tests replace it.
var Now = time.Now

// AgentLoopMaxTokens is the default output budget of a worker loop.
const AgentLoopMaxTokens = 32_000

// BoundedMaxTokens clamps the requested output budget to the model's own limit.
func BoundedMaxTokens(model host.Model, requested int) int {
	if limit := host.ModelMaxTokens(model); limit > 0 && limit < requested {
		return limit
	}
	return requested
}

// StreamFunc starts one model request. request is Pi's stream request: the
// system prompt, messages, tools and request options.
type StreamFunc func(model host.Model, request map[string]any) host.ModelStream

// ToolResult is what a worker tool answers.
type ToolResult struct {
	Text    string
	Details any
}

// Tool is a tool a worker loop offers the model.
type Tool struct {
	Name        string
	Label       string
	Description string
	Parameters  map[string]any
	Execute     func(ctx context.Context, args map[string]any) (ToolResult, error)
}

// LoopConfig is one worker loop's request configuration.
type LoopConfig struct {
	Stage   string
	Model   host.Model
	APIKey  *string
	Headers map[string]any
	Env     map[string]string
	// MaxTokens is the output budget; 0 uses AgentLoopMaxTokens.
	MaxTokens int
	// ThinkingLevel is the reasoning level; "" or "off" sends none.
	ThinkingLevel string
	// MaxTurns ends the loop after that many completed turns; 0 is unlimited.
	MaxTurns int
}

// StreamError is an assistant message that ended with stopReason "error" or
// "aborted": the loop returns normally, so callers need it to tell a hard
// failure from a deliberate empty result.
type StreamError struct {
	Stage        string
	StopReason   string
	ErrorMessage string
}

func (e *StreamError) Error() string {
	message := fmt.Sprintf("%s stream ended with stopReason %q", e.Stage, e.StopReason)
	if e.ErrorMessage != "" {
		message += ": " + e.ErrorMessage
	}
	return message
}

// ErrAborted reports a loop its context cancelled.
var ErrAborted = errors.New("agent loop aborted")

// LoopResult is what a finished loop reports.
type LoopResult struct {
	// StreamError is set when the run ended with a failed or aborted model call.
	StreamError *StreamError
	Turns       int
	// HitMaxTurns is set when the turn cap ended a run whose last turn still
	// called tools: the model was cut short rather than finished.
	HitMaxTurns bool
}

func textContent(text string) []any {
	return []any{map[string]any{"type": "text", "text": text}}
}

func stopReasonOf(message map[string]any) string {
	stop, _ := message["stopReason"].(string)
	return stop
}

func toolCallsOf(message map[string]any) []map[string]any {
	blocks, _ := message["content"].([]any)
	calls := []map[string]any{}
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if block != nil && block["type"] == "toolCall" {
			calls = append(calls, block)
		}
	}
	return calls
}

func toolArguments(block map[string]any) map[string]any {
	switch args := block["arguments"].(type) {
	case map[string]any:
		return args
	case string:
		var decoded map[string]any
		if json.Unmarshal([]byte(args), &decoded) == nil && decoded != nil {
			return decoded
		}
	}
	return map[string]any{}
}

// RunLoop runs a worker agent loop: it streams the model, runs the tools the
// assistant requested one at a time, feeds their results back, and repeats
// until the assistant answers without tool calls, a turn fails, the turn
// limit is reached, or ctx is cancelled. Tool execution collects the records;
// the loop itself keeps no transcript beyond the request.
func RunLoop(ctx context.Context, stream StreamFunc, cfg LoopConfig, systemPrompt, userText string, tools []Tool) (LoopResult, error) {
	if stream == nil {
		return LoopResult{}, errors.New("agent loop needs a model stream")
	}
	messages := []any{map[string]any{"role": "user", "content": textContent(userText), "timestamp": Now().UnixMilli()}}
	toolSchemas := make([]any, 0, len(tools))
	byName := map[string]Tool{}
	for _, tool := range tools {
		toolSchemas = append(toolSchemas, map[string]any{"name": tool.Name, "description": tool.Description, "parameters": tool.Parameters})
		byName[tool.Name] = tool
	}
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = AgentLoopMaxTokens
	}
	result := LoopResult{}
	for {
		if ctx.Err() != nil {
			return result, ErrAborted
		}
		request := map[string]any{
			"systemPrompt": systemPrompt,
			"messages":     append([]any{}, messages...),
			"tools":        toolSchemas,
			"maxTokens":    BoundedMaxTokens(cfg.Model, maxTokens),
		}
		if cfg.APIKey != nil {
			request["apiKey"] = *cfg.APIKey
		}
		if len(cfg.Headers) > 0 {
			request["headers"] = cfg.Headers
		}
		if len(cfg.Env) > 0 {
			request["env"] = cfg.Env
		}
		if host.ModelReasoning(cfg.Model) && cfg.ThinkingLevel != "" && cfg.ThinkingLevel != "off" {
			request["reasoning"] = cfg.ThinkingLevel
		}
		modelStream := stream(cfg.Model, request)
		if modelStream == nil {
			return result, errors.New("model stream unavailable")
		}
		for range modelStream.Events(ctx) {
			// Drain: the terminal event carries the message.
		}
		if ctx.Err() != nil {
			return result, ErrAborted
		}
		message := modelStream.Result()
		if message == nil {
			message = map[string]any{"role": "assistant", "content": []any{}, "stopReason": "error", "errorMessage": "model stream ended without a message"}
		}
		if _, hasRole := message["role"]; !hasRole {
			message["role"] = "assistant"
		}
		stop := stopReasonOf(message)
		if stop == "error" || stop == "aborted" {
			errorMessage, _ := message["errorMessage"].(string)
			debuglog.Log(ctx, cfg.Stage+".stream_error", map[string]any{"stopReason": stop, "errorMessage": errorMessage})
			result.StreamError = &StreamError{Stage: cfg.Stage, StopReason: stop, ErrorMessage: errorMessage}
			return result, nil
		}
		messages = append(messages, message)
		calls := toolCallsOf(message)
		for _, call := range calls {
			if ctx.Err() != nil {
				return result, ErrAborted
			}
			name, _ := call["name"].(string)
			id, _ := call["id"].(string)
			text := ""
			isError := false
			if tool, ok := byName[name]; ok && tool.Execute != nil {
				answer, err := tool.Execute(ctx, toolArguments(call))
				if err != nil {
					text, isError = err.Error(), true
				} else {
					text = answer.Text
				}
			} else {
				text, isError = fmt.Sprintf("Tool %s not found", name), true
			}
			messages = append(messages, map[string]any{
				"role":       "toolResult",
				"toolCallId": id,
				"toolName":   name,
				"content":    textContent(text),
				"isError":    isError,
				"timestamp":  Now().UnixMilli(),
			})
		}
		result.Turns++
		if cfg.MaxTurns > 0 && result.Turns >= cfg.MaxTurns {
			// A turn without tool calls would have ended the loop anyway; one
			// with tool calls means the model still had work to do.
			result.HitMaxTurns = len(calls) > 0
			return result, nil
		}
		if len(calls) == 0 {
			return result, nil
		}
	}
}

func joinOrEmpty(items []string) string {
	if len(items) == 0 {
		return "(none yet)"
	}
	out := ""
	for i, item := range items {
		if i > 0 {
			out += "\n"
		}
		out += item
	}
	return out
}

// StringList reads a JSON array of strings from decoded tool arguments.
func StringList(v any) ([]string, bool) {
	switch list := v.(type) {
	case []string:
		return list, true
	case []any:
		out := make([]string, 0, len(list))
		for _, item := range list {
			s, ok := item.(string)
			if !ok {
				return nil, false
			}
			out = append(out, s)
		}
		return out, true
	}
	return nil, false
}
