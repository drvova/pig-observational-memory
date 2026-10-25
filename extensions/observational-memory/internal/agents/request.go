package agents

import (
	"context"
	"errors"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
)

// RequestEvent is the debug event every model request the extension makes logs, from its
// single stream call site (Request): the stage, the memory engine, and the input and output
// tokens when the provider reported them (docs/memory-v2.md, "Measuring it").
const RequestEvent = debuglog.RequestEvent

// requestFor is Pi's stream request for one completion.
func requestFor(systemPrompt string, messages, toolSchemas []any, maxTokens int, apiKey *string, headers map[string]any, env map[string]string) map[string]any {
	request := map[string]any{
		"systemPrompt": systemPrompt,
		"messages":     messages,
		"tools":        toolSchemas,
		"maxTokens":    maxTokens,
	}
	if apiKey != nil {
		request["apiKey"] = *apiKey
	}
	if len(headers) > 0 {
		request["headers"] = headers
	}
	if len(env) > 0 {
		request["env"] = env
	}
	return request
}

// Request makes one model request: it starts the stream, drains it and returns the
// assistant message, and logs one event for it (RequestEvent, or the stage's stream_error
// event for a failed response; logRequest). It is the extension's only stream call
// site: the worker loop and the single-request path both go through it. A nil stream is an
// error with no request made; a context cancelled before the result is ErrAborted.
//
// Cancelling ctx only stops the draining here: the SDK's stream (ModelRegistry.StreamSimple,
// SDK v0.5.1) takes no signal and never sends cancelModelStream. The provider request is
// cancelled in PiG only when the PiG request that owns the stream's sdk.Context is
// cancelled (PiG cancels that request's host calls, modelStream included). So a request
// whose ctx is the handler's own request context (memory v2's session_before_compact hook,
// sdkhost.Host.RequestContext) stops in PiG with it, while one cancelled only on the
// extension's side (a worker run aborted by Runtime.AbortConsolidation) still runs to its
// end and is billed. Either way it was made: an aborted request still logs its event, with
// "aborted": true, and counts as a request.
func Request(ctx context.Context, stream StreamFunc, stage, engine string, model host.Model, request map[string]any) (map[string]any, error) {
	if stream == nil {
		return nil, errors.New("model stream unavailable")
	}
	modelStream := stream(model, request)
	if modelStream == nil {
		return nil, errors.New("model stream unavailable")
	}
	for range modelStream.Events(ctx) {
		// Drain: the terminal event carries the message.
	}
	if ctx.Err() != nil {
		logRequest(ctx, stage, engine, model, nil, true)
		return nil, ErrAborted
	}
	message := modelStream.Result()
	if message == nil {
		message = map[string]any{"role": "assistant", "content": []any{}, "stopReason": "error", "errorMessage": "model stream ended without a message"}
	}
	if _, hasRole := message["role"]; !hasRole {
		message["role"] = "assistant"
	}
	logRequest(ctx, stage, engine, model, message, false)
	return message, nil
}

// logRequest logs the request's event: RequestEvent, or for a response that ended with
// stopReason "error" or "aborted" the stage's stream_error event, which carries the same
// request fields, so each request logs exactly one event. Both carry "request": true.
// The engine is reported as the caller gave it: a caller that left it unset shows as "",
// never as either engine.
func logRequest(ctx context.Context, stage, engine string, model host.Model, message map[string]any, aborted bool) {
	data := map[string]any{"request": true, "stage": stage, "engine": engine, "model": host.ModelProvider(model) + "/" + host.ModelID(model)}
	event := RequestEvent
	if aborted {
		data["aborted"] = true
	}
	if message != nil {
		stop := stopReasonOf(message)
		data["stopReason"] = stop
		if stop == "error" || stop == "aborted" {
			event = stage + ".stream_error"
			data["errorMessage"], _ = message["errorMessage"].(string)
		}
		if usage, ok := message["usage"].(map[string]any); ok {
			for key, field := range map[string]string{"input": "inputTokens", "output": "outputTokens", "cacheRead": "cacheReadTokens", "cacheWrite": "cacheWriteTokens"} {
				if n, ok := ledger.UsageNumber(usage[key]); ok {
					data[field] = n
				}
			}
		}
	}
	debuglog.Log(ctx, event, data)
}

// ForcedToolChoice is the request's toolChoice that makes model call the tool named name,
// for the provider APIs whose choice PiG passes through: a named tool for Anthropic and
// Bedrock, "required" for the OpenAI completions and responses APIs (Azure's and Codex's
// included) and Mistral, "any" for Google (one tool offered, so any is that one). Other APIs
// get nil: the tool is then offered alone and the instructions ask for it.
func ForcedToolChoice(model host.Model, name string) any {
	switch host.ModelString(model, "api") {
	case "anthropic-messages", "bedrock-converse-stream":
		return map[string]any{"type": "tool", "name": name}
	case "openai-completions", "openai-responses", "azure-openai-responses", "openai-codex-responses", "mistral-conversations":
		return "required"
	case "google-generative-ai", "google-vertex":
		return "any"
	}
	return nil
}

// SingleRequestConfig is one single-request completion: one forced tool, one response.
type SingleRequestConfig struct {
	Stage   string
	Engine  string
	Model   host.Model
	APIKey  *string
	Headers map[string]any
	Env     map[string]string
	// MaxTokens is the output cap, clamped to the model's own limit; 0 uses the model's
	// output limit (host.ModelMaxTokens). With neither known the request is not made
	// (ErrNoOutputCap): the caller derives the cap (docs/memory-v2.md, level 1's "Budgets":
	// the model's output limit, else the summary budget) and checks it against the window,
	// so no hand-picked default may stand in for it.
	MaxTokens int
	// ThinkingLevel is the reasoning level; "" or "off" sends none. It is sent only when the
	// tool choice is not forced, since providers refuse a forced tool with reasoning on.
	ThinkingLevel string
}

// SingleRequestResult is what the one response gave.
type SingleRequestResult struct {
	// Arguments are the forced tool's arguments, nil when the response did not call it.
	Arguments map[string]any
	// Message is the assistant message, nil when the request was aborted or never made.
	Message map[string]any
	// Usage is the provider-reported usage of the response, nil when none was reported.
	Usage map[string]any
	// StreamError is set when the response ended with stopReason "error" or "aborted".
	StreamError *StreamError
}

// ErrNoOutputCap is RunSingleRequest's error when neither the configuration nor the model
// gives an output cap; no request is made.
var ErrNoOutputCap = errors.New("single request has no output cap: set MaxTokens or use a model with a known output limit")

// SingleToolInstruction is appended to the system prompt of a single-request completion:
// the response is read once, so the tool call must carry everything.
const SingleToolInstruction = "Answer with exactly one call to the %s tool carrying your whole answer. Its result is not shown to you and nothing follows it: there is no second turn."

// RunSingleRequest makes one completion offering one tool, forced where the provider allows
// it (ForcedToolChoice), and parses the tool's arguments from that one response
// (docs/memory-v2.md, "Request cost, per compaction"): MaxTurns is 1, the tool is never
// executed and no tool result is sent back, and nothing is retried, whatever the response.
// The first call of the tool in the response is the answer; a response without one has nil
// Arguments.
func RunSingleRequest(ctx context.Context, stream StreamFunc, cfg SingleRequestConfig, systemPrompt, userText string, tool Tool) (SingleRequestResult, error) {
	if ctx.Err() != nil {
		return SingleRequestResult{}, ErrAborted
	}
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = host.ModelMaxTokens(cfg.Model)
	}
	if maxTokens <= 0 {
		return SingleRequestResult{}, ErrNoOutputCap
	}
	system := strings.TrimRight(systemPrompt, "\n") + "\n\n" + strings.ReplaceAll(SingleToolInstruction, "%s", tool.Name) + "\n"
	messages := []any{map[string]any{"role": "user", "content": textContent(userText), "timestamp": Now().UnixMilli()}}
	schemas := []any{map[string]any{"name": tool.Name, "description": tool.Description, "parameters": tool.Parameters}}
	request := requestFor(system, messages, schemas, BoundedMaxTokens(cfg.Model, maxTokens), cfg.APIKey, cfg.Headers, cfg.Env)
	if choice := ForcedToolChoice(cfg.Model, tool.Name); choice != nil {
		request["toolChoice"] = choice
	} else if host.ModelReasoning(cfg.Model) && cfg.ThinkingLevel != "" && cfg.ThinkingLevel != "off" {
		request["reasoning"] = cfg.ThinkingLevel
	}
	message, err := Request(ctx, stream, cfg.Stage, cfg.Engine, cfg.Model, request)
	if err != nil {
		return SingleRequestResult{}, err
	}
	result := SingleRequestResult{Message: message}
	if usage, ok := message["usage"].(map[string]any); ok {
		result.Usage = usage
	}
	if stop := stopReasonOf(message); stop == "error" || stop == "aborted" {
		errorMessage, _ := message["errorMessage"].(string)
		result.StreamError = &StreamError{Stage: cfg.Stage, StopReason: stop, ErrorMessage: errorMessage}
		return result, nil
	}
	for _, call := range toolCallsOf(message) {
		if name, _ := call["name"].(string); name == tool.Name {
			result.Arguments = toolArguments(call)
			break
		}
	}
	return result, nil
}
