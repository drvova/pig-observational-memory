package bench

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
)

func TestToChatRequestConvertsAPiToolLoop(t *testing.T) {
	request := map[string]any{
		"systemPrompt": agents.ObserverSystem,
		"maxTokens":    32000,
		"reasoning":    "low",
		"tools":        []any{map[string]any{"name": "record_observations", "description": "d", "parameters": map[string]any{"type": "object"}}},
		"messages": []any{
			map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "chunk"}}},
			map[string]any{"role": "assistant", "content": []any{
				map[string]any{"type": "thinking", "thinking": "hmm"},
				map[string]any{"type": "toolCall", "id": "c1", "name": "record_observations", "arguments": map[string]any{"observations": []any{}}},
			}},
			map[string]any{"role": "toolResult", "toolCallId": "c1", "toolName": "record_observations", "content": []any{map[string]any{"type": "text", "text": "ok"}}},
		},
	}
	chat := ToChatRequest("m", request)
	if chat.Model != "m" || chat.MaxTokens != 32000 || chat.ReasoningEffort != "low" || len(chat.Tools) != 1 || chat.Tools[0].Function.Name != "record_observations" {
		t.Fatalf("request = %+v", chat)
	}
	if len(chat.Messages) != 4 || chat.Messages[0].Role != "system" || chat.Messages[1].Text() != "chunk" {
		t.Fatalf("messages = %+v", chat.Messages)
	}
	assistant := chat.Messages[2]
	if assistant.ReasoningContent != "hmm" || len(assistant.ToolCalls) != 1 || assistant.ToolCalls[0].Function.Arguments != `{"observations":[]}` {
		t.Fatalf("assistant = %+v", assistant)
	}
	if tool := chat.Messages[3]; tool.Role != "tool" || tool.ToolCallID != "c1" || tool.Text() != "ok" {
		t.Fatalf("tool = %+v", tool)
	}
	if WorkerRole(request) != RoleObserver || WorkerRole(map[string]any{"systemPrompt": agents.ReflectorSystem}) != RoleReflector {
		t.Fatal("worker role not detected")
	}
}

func TestFromChatResponseBuildsAPiAssistantMessage(t *testing.T) {
	resp := ChatResponse{Choices: []ChatChoice{{FinishReason: "tool_calls", Message: ChatMessage{
		Content:   Text(""),
		Reasoning: "thinking",
		ToolCalls: []ToolCall{{ID: "c1", Function: FunctionCall{Name: "record_observations", Arguments: `{"observations":[{"content":"x"}]}`}}},
	}}}, Usage: ChatUsage{PromptTokens: 10, CompletionTokens: 5}}
	message := FromChatResponse(resp)
	if message["stopReason"] != "toolUse" {
		t.Fatalf("stopReason = %v", message["stopReason"])
	}
	blocks := message["content"].([]any)
	if len(blocks) != 2 || blocks[0].(map[string]any)["type"] != "thinking" {
		t.Fatalf("blocks = %v", blocks)
	}
	call := blocks[1].(map[string]any)
	if _, ok := call["arguments"].(map[string]any); !ok || call["name"] != "record_observations" {
		t.Fatalf("call = %v", call)
	}
	text := FromChatResponse(ChatResponse{Choices: []ChatChoice{{FinishReason: "stop", Message: ChatMessage{Content: Text("done")}}}})
	if text["stopReason"] != "stop" || len(text["content"].([]any)) != 1 {
		t.Fatalf("text message = %v", text)
	}
}

func TestClientRetriesTransientFailuresAndMetersUsage(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		body, _ := io.ReadAll(r.Body)
		var req ChatRequest
		_ = json.Unmarshal(body, &req)
		if req.Model != "m" {
			t.Errorf("model = %q", req.Model)
		}
		switch calls.Add(1) {
		case 1:
			w.WriteHeader(http.StatusTooManyRequests)
		case 2:
			w.WriteHeader(http.StatusBadGateway)
		default:
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"yes"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":3,"completion_tokens_details":{"reasoning_tokens":2}}}`)
		}
	}))
	defer server.Close()
	meter := NewMeter(nil)
	client := NewClient(server.URL, "k", meter)
	client.Sleep = func(context.Context, time.Duration) error { return nil }
	resp, err := client.Chat(context.Background(), RoleJudge, ChatRequest{Model: "m"})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Choices[0].Message.Text() != "yes" {
		t.Fatalf("resp = %+v", resp)
	}
	got := meter.Snapshot()[RoleJudge]
	if got.Calls != 1 || got.Retries != 2 || got.PromptTokens != 7 || got.CompletionTokens != 3 || got.ReasoningTokens != 2 {
		t.Fatalf("usage = %+v", got)
	}
}

func TestClientDoesNotRetryClientErrors(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()
	meter := NewMeter(nil)
	client := NewClient(server.URL, "k", meter)
	client.Sleep = func(context.Context, time.Duration) error { return nil }
	if _, err := client.Chat(context.Background(), RoleActor, ChatRequest{Model: "m"}); err == nil {
		t.Fatal("400 succeeded")
	}
	if calls.Load() != 1 || meter.Snapshot()[RoleActor].FailedCalls != 1 {
		t.Fatalf("calls = %d, usage = %+v", calls.Load(), meter.Snapshot()[RoleActor])
	}
}

func TestAskRepeatsAReplyWithoutContent(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":null,"reasoning":"thinking only"},"finish_reason":"stop"}]}`)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"Paris"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()
	client := NewClient(server.URL, "", NewMeter(nil))
	answer, err := Ask(context.Background(), client, []ChatMessage{{Role: "user", Content: Text("q")}}, AnswerOptions{Model: "m"})
	if err != nil || answer.Hypothesis != "Paris" || calls.Load() != 2 {
		t.Fatalf("answer = %+v, err = %v, calls = %d", answer, err, calls.Load())
	}
}
