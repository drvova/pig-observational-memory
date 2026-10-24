package bench

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
)

// Provider is the registry provider id the bench model is served under.
const Provider = "bench"

// ModelSpec describes the one model the bench serves.
type ModelSpec struct {
	ID            string
	ContextWindow int
	MaxTokens     int
	Reasoning     bool
}

// HostModel is the spec as a Pi registry model.
func (s ModelSpec) HostModel() host.Model {
	return host.Model{
		"provider":      Provider,
		"id":            s.ID,
		"api":           "openai-completions",
		"contextWindow": float64(s.ContextWindow),
		"maxTokens":     float64(s.MaxTokens),
		"reasoning":     s.Reasoning,
	}
}

// Models is a host.Models registry with one model, whose streams are
// chat-completions calls through Client.
type Models struct {
	Spec   ModelSpec
	Client *Client
}

// Find returns the bench model for its own provider/id.
func (m *Models) Find(provider, id string) host.Model {
	if provider == Provider && id == m.Spec.ID {
		return m.Spec.HostModel()
	}
	return nil
}

// GetApiKeyAndHeaders reports usable auth without exposing the key: the
// client adds it to each request itself.
func (m *Models) GetApiKeyAndHeaders(host.Model) (host.Auth, error) {
	return host.Auth{OK: true, Headers: map[string]any{"x-bench": "1"}}, nil
}

func (m *Models) IsUsingOAuth(host.Model) bool                       { return false }
func (m *Models) HasConfiguredAuth(host.Model) bool                  { return true }
func (m *Models) Refresh(context.Context, host.RefreshOptions) error { return nil }

// Stream runs the request to completion and serves it as a finished stream.
func (m *Models) Stream(model host.Model, request map[string]any) host.ModelStream {
	role := WorkerRole(request)
	chat := ToChatRequest(host.ModelID(model), request)
	resp, err := m.Client.Chat(context.Background(), role, chat)
	if err != nil {
		return doneStream{message: map[string]any{"role": "assistant", "content": []any{}, "stopReason": "error", "errorMessage": err.Error()}}
	}
	return doneStream{message: FromChatResponse(resp)}
}

type doneStream struct{ message map[string]any }

func (s doneStream) Events(ctx context.Context) <-chan map[string]any {
	out := make(chan map[string]any, 1)
	out <- map[string]any{"type": "done", "reason": s.message["stopReason"], "message": s.message}
	close(out)
	return out
}

func (s doneStream) Result() map[string]any { return s.message }

// WorkerRole names the worker a request comes from by its system prompt. Every worker prompt
// opens with its stage's fixed kernel text (agents.Compose), with the seed or the model's own
// instructions and any rule lines after it, so the role is matched on that prefix.
func WorkerRole(request map[string]any) string {
	system, _ := request["systemPrompt"].(string)
	for _, role := range []struct{ id, role string }{
		{kernel.TextObserverRules, RoleObserver},
		{kernel.TextReflectorRules, RoleReflector},
		{kernel.TextDropperRules, RoleDropper},
	} {
		if strings.HasPrefix(system, strings.TrimRight(kernel.Text(role.id), "\n")) {
			return role.role
		}
	}
	return RoleWorker
}

// ReasoningEffort maps a Pi thinking level onto reasoning_effort ("" for off).
func ReasoningEffort(level string) string { return reasoningEffort(level) }

// reasoningEffort maps Pi thinking levels onto OpenAI reasoning_effort.
func reasoningEffort(level string) string {
	switch level {
	case "", "off":
		return ""
	case "minimal", "low":
		return "low"
	case "medium":
		return "medium"
	}
	return "high"
}

// ToChatRequest converts a worker's Pi stream request into chat completions.
func ToChatRequest(modelID string, request map[string]any) ChatRequest {
	chat := ChatRequest{Model: modelID}
	if system, _ := request["systemPrompt"].(string); system != "" {
		chat.Messages = append(chat.Messages, ChatMessage{Role: "system", Content: Text(system)})
	}
	messages, _ := request["messages"].([]any)
	for _, raw := range messages {
		msg, _ := raw.(map[string]any)
		if msg == nil {
			continue
		}
		switch msg["role"] {
		case "user":
			chat.Messages = append(chat.Messages, ChatMessage{Role: "user", Content: Text(blockText(msg["content"]))})
		case "assistant":
			chat.Messages = append(chat.Messages, assistantToChat(msg))
		case "toolResult":
			id, _ := msg["toolCallId"].(string)
			chat.Messages = append(chat.Messages, ChatMessage{Role: "tool", ToolCallID: id, Content: Text(blockText(msg["content"]))})
		}
	}
	tools, _ := request["tools"].([]any)
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		if tool == nil {
			continue
		}
		name, _ := tool["name"].(string)
		description, _ := tool["description"].(string)
		params, _ := tool["parameters"].(map[string]any)
		chat.Tools = append(chat.Tools, ToolSpec{Type: "function", Function: FunctionSpec{Name: name, Description: description, Parameters: params}})
	}
	switch n := request["maxTokens"].(type) {
	case int:
		chat.MaxTokens = n
	case float64:
		chat.MaxTokens = int(n)
	}
	if level, _ := request["reasoning"].(string); level != "" {
		chat.ReasoningEffort = reasoningEffort(level)
	}
	return chat
}

func assistantToChat(msg map[string]any) ChatMessage {
	out := ChatMessage{Role: "assistant"}
	texts := []string{}
	thinking := []string{}
	blocks, _ := msg["content"].([]any)
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if block == nil {
			continue
		}
		switch block["type"] {
		case "text":
			if text, _ := block["text"].(string); text != "" {
				texts = append(texts, text)
			}
		case "thinking":
			if text, _ := block["thinking"].(string); text != "" {
				thinking = append(thinking, text)
			}
		case "toolCall":
			id, _ := block["id"].(string)
			name, _ := block["name"].(string)
			args := "{}"
			switch a := block["arguments"].(type) {
			case string:
				args = a
			case nil:
			default:
				if encoded, err := json.Marshal(a); err == nil {
					args = string(encoded)
				}
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: id, Type: "function", Function: FunctionCall{Name: name, Arguments: args}})
		}
	}
	content := strings.Join(texts, "\n")
	out.Content = &content
	// Thinking models that take tools expect their reasoning back within the
	// same tool loop.
	out.ReasoningContent = strings.Join(thinking, "\n")
	return out
}

func blockText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		parts := []string{}
		for _, raw := range c {
			block, _ := raw.(map[string]any)
			if block != nil && block["type"] == "text" {
				if text, _ := block["text"].(string); text != "" {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// FromChatResponse converts a completion into a Pi assistant message.
func FromChatResponse(resp ChatResponse) map[string]any {
	choice := resp.Choices[0]
	content := []any{}
	if reasoning := choice.Message.ReasoningText(); reasoning != "" {
		content = append(content, map[string]any{"type": "thinking", "thinking": reasoning})
	}
	if text := choice.Message.Text(); strings.TrimSpace(text) != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	for _, call := range choice.Message.ToolCalls {
		var args any = call.Function.Arguments
		var decoded map[string]any
		if json.Unmarshal([]byte(call.Function.Arguments), &decoded) == nil && decoded != nil {
			args = decoded
		}
		content = append(content, map[string]any{"type": "toolCall", "id": call.ID, "name": call.Function.Name, "arguments": args})
	}
	stop := "stop"
	switch {
	case len(choice.Message.ToolCalls) > 0:
		stop = "toolUse"
	case choice.FinishReason == "length":
		stop = "length"
	}
	return map[string]any{
		"role":       "assistant",
		"content":    content,
		"stopReason": stop,
		"usage": map[string]any{
			"input":       float64(resp.Usage.PromptTokens),
			"output":      float64(resp.Usage.CompletionTokens),
			"cacheRead":   float64(resp.Usage.Cached()),
			"cacheWrite":  float64(0),
			"totalTokens": float64(resp.Usage.TotalTokens),
		},
	}
}
