package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
)

// Now is the clock for message timestamps; tests replace it.
var Now = time.Now

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

// ToolResult is what a tool answers.
type ToolResult struct {
	Text    string
	Details any
}

// Tool is a tool a request offers the model. The single-request path never executes it
// (RunSingleRequest).
type Tool struct {
	Name        string
	Label       string
	Description string
	Parameters  map[string]any
	Execute     func(ctx context.Context, args map[string]any) (ToolResult, error)
}

// StreamError is an assistant message that ended with stopReason "error" or
// "aborted": the request returns normally, so callers need it to tell a hard
// failure from a model answer.
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

// ErrAborted reports a request its context cancelled.
var ErrAborted = errors.New("agent loop aborted")

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
