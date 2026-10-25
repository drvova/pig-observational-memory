package agents_test

import (
	"context"
	"reflect"
	"runtime"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

var consolidateTool = agents.Tool{
	Name:        "consolidate",
	Description: "Record the consolidation.",
	Parameters:  map[string]any{"type": "object", "properties": map[string]any{"labels": map[string]any{"type": "array"}}},
	Execute: func(context.Context, map[string]any) (agents.ToolResult, error) {
		panic("the single-request path never executes its tool")
	},
}

func singleConfig(model map[string]any) agents.SingleRequestConfig {
	return agents.SingleRequestConfig{Stage: "consolidate", Engine: config.EngineV2, Model: model, MaxTokens: 1000, ThinkingLevel: "low"}
}

// One completion, whatever the response: a tool call, two tool calls, text, a wrong tool, a
// failed or aborted stream, or nothing. The tool is never executed, no tool result follows and
// nothing is retried.
func TestSingleRequestMakesExactlyOneRequest(t *testing.T) {
	call := tf.AssistantToolCall("c1", "consolidate", map[string]any{"labels": []any{"a"}})
	twoCalls := tf.ToJSONValue(map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []any{
		map[string]any{"type": "toolCall", "id": "c1", "name": "consolidate", "arguments": map[string]any{"labels": []any{"first"}}},
		map[string]any{"type": "toolCall", "id": "c2", "name": "consolidate", "arguments": map[string]any{"labels": []any{"second"}}},
	}}).(map[string]any)
	cases := []struct {
		name     string
		reply    map[string]any
		wantArgs map[string]any
		wantErr  bool
	}{
		{"tool call", call, map[string]any{"labels": []any{"a"}}, false},
		{"two tool calls take the first", twoCalls, map[string]any{"labels": []any{"first"}}, false},
		{"text", tf.AssistantText("no tool"), nil, false},
		{"another tool", tf.AssistantToolCall("c1", "other", map[string]any{"x": 1}), nil, false},
		{"stream error", tf.AssistantError("error", "boom"), nil, true},
		{"aborted", tf.AssistantError("aborted", "stop"), nil, true},
		{"no message", nil, nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			counter := tf.CountRequests(t)
			rec := &tf.StreamRecorder{Replies: []map[string]any{c.reply}}
			result, err := agents.RunSingleRequest(context.Background(), rec.Stream, singleConfig(tf.Model("p", "m")), "system", "user text", consolidateTool)
			if err != nil {
				t.Fatal(err)
			}
			if rec.Calls() != 1 || counter.Total() != 1 || counter.Stage("consolidate") != 1 || counter.Engine(config.EngineV2) != 1 {
				t.Fatalf("calls %d, counted %v", rec.Calls(), counter.ByStage())
			}
			if !reflect.DeepEqual(result.Arguments, c.wantArgs) {
				t.Fatalf("arguments %#v, want %#v", result.Arguments, c.wantArgs)
			}
			if (result.StreamError != nil) != c.wantErr {
				t.Fatalf("stream error %+v", result.StreamError)
			}
			messages, _ := rec.Requests[0]["messages"].([]any)
			if len(messages) != 1 {
				t.Fatalf("the request carried %d messages", len(messages))
			}
		})
	}
}

// A cancelled context makes no request.
func TestSingleRequestCancelledMakesNone(t *testing.T) {
	counter := tf.CountRequests(t)
	rec := &tf.StreamRecorder{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := agents.RunSingleRequest(ctx, rec.Stream, singleConfig(tf.Model("p", "m")), "system", "user", consolidateTool); err != agents.ErrAborted {
		t.Fatalf("err %v", err)
	}
	if rec.Calls() != 0 || counter.Total() != 0 {
		t.Fatalf("calls %d counted %d", rec.Calls(), counter.Total())
	}
}

// The tool is forced where the provider takes a named or required choice, and reasoning is
// then left off; elsewhere the tool is offered alone with the instruction.
func TestSingleRequestForcesTheTool(t *testing.T) {
	cases := []struct {
		api        string
		wantChoice any
	}{
		{"anthropic-messages", map[string]any{"type": "tool", "name": "consolidate"}},
		{"bedrock-converse-stream", map[string]any{"type": "tool", "name": "consolidate"}},
		{"openai-completions", "required"},
		{"openai-responses", "required"},
		{"azure-openai-responses", "required"},
		{"openai-codex-responses", "required"},
		{"mistral-conversations", "required"},
		{"google-generative-ai", "any"},
		{"google-vertex", "any"},
		{"test-api", nil},
	}
	for _, c := range cases {
		rec := &tf.StreamRecorder{Replies: []map[string]any{tf.AssistantText("x")}}
		model := tf.Model("p", "m", map[string]any{"api": c.api, "reasoning": true})
		if _, err := agents.RunSingleRequest(context.Background(), rec.Stream, singleConfig(model), "system", "user", consolidateTool); err != nil {
			t.Fatal(err)
		}
		request := rec.Requests[0]
		if !reflect.DeepEqual(request["toolChoice"], c.wantChoice) {
			t.Fatalf("%s: toolChoice %#v", c.api, request["toolChoice"])
		}
		if _, reasoning := request["reasoning"]; reasoning != (c.wantChoice == nil) {
			t.Fatalf("%s: reasoning %v with toolChoice %v", c.api, request["reasoning"], c.wantChoice)
		}
		if tools, _ := request["tools"].([]any); len(tools) != 1 {
			t.Fatalf("%s: %d tools offered", c.api, len(tools))
		}
	}
}

// Every worker loop request goes through the same call site, is counted per stage, and is
// labelled with the engine its WorkerArgs carry, never with a default: a v1 worker running
// under v2 reports v2, and one whose caller set no engine reports "".
func TestLoopRequestsAreCounted(t *testing.T) {
	for _, engine := range []string{config.EngineV1, config.EngineV2, ""} {
		counter := tf.CountRequests(t)
		rec := &tf.StreamRecorder{Replies: []map[string]any{dropCall("aaaaaaaaaaaa"), tf.AssistantText("done")}}
		args := dropperArgs(rec, tf.Model("p", "m"))
		args.Engine = engine
		if _, err := agents.RunDropper(context.Background(), args); err != nil {
			t.Fatal(err)
		}
		if rec.Calls() == 0 || counter.Total() != rec.Calls() || counter.Stage("dropper") != rec.Calls() || counter.Engine(engine) != rec.Calls() {
			t.Fatalf("engine %q: counted %v for %d calls", engine, counter.ByStage(), rec.Calls())
		}
	}
}

// The output cap is the configured one, else the model's output limit; with neither the
// request is not made, so no hand-picked default stands in for the cap level 1 derives.
func TestSingleRequestOutputCap(t *testing.T) {
	cases := []struct {
		name    string
		max     int
		model   map[string]any
		want    int
		wantErr error
	}{
		{"configured", 1000, tf.Model("p", "m"), 1000, nil},
		{"clamped to the model", 50_000, tf.Model("p", "m", map[string]any{"maxTokens": 8_000}), 8_000, nil},
		{"the model's limit", 0, tf.Model("p", "m", map[string]any{"maxTokens": 8_000}), 8_000, nil},
		{"none known", 0, tf.Model("p", "m"), 0, agents.ErrNoOutputCap},
	}
	for _, c := range cases {
		counter := tf.CountRequests(t)
		rec := &tf.StreamRecorder{Replies: []map[string]any{tf.AssistantText("x")}}
		cfg := singleConfig(c.model)
		cfg.MaxTokens = c.max
		_, err := agents.RunSingleRequest(context.Background(), rec.Stream, cfg, "system", "user", consolidateTool)
		if err != c.wantErr {
			t.Fatalf("%s: err %v, want %v", c.name, err, c.wantErr)
		}
		if c.wantErr != nil {
			if rec.Calls() != 0 || counter.Total() != 0 {
				t.Fatalf("%s: %d requests made without a cap", c.name, rec.Calls())
			}
			continue
		}
		if got, _ := rec.Requests[0]["maxTokens"].(int); got != c.want {
			t.Fatalf("%s: maxTokens %v, want %d", c.name, rec.Requests[0]["maxTokens"], c.want)
		}
	}
}

// A context cancelled while the response streams stops the draining and returns ErrAborted,
// but the request was made: it is counted, flagged aborted. Cancelling ctx does not reach the
// provider (SDK v0.5.1 streams take no signal); only the cancellation of the PiG request that
// owns the stream does, so the count must never treat an aborted request as one not made.
func TestSingleRequestAbortedMidStreamIsCounted(t *testing.T) {
	counter := tf.CountRequests(t)
	aborted := 0
	unsubscribe := debuglog.Subscribe(func(event string, data map[string]any) {
		if debuglog.IsRequest(data) && data["aborted"] == true {
			aborted++
		}
	})
	defer unsubscribe()
	rec := &tf.StreamRecorder{Block: true}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := agents.RunSingleRequest(ctx, rec.Stream, singleConfig(tf.Model("p", "m")), "system", "user", consolidateTool)
		done <- err
	}()
	for rec.Calls() == 0 {
		runtime.Gosched()
	}
	cancel()
	if err := <-done; err != agents.ErrAborted {
		t.Fatalf("err %v", err)
	}
	if rec.Calls() != 1 || counter.Total() != 1 || aborted != 1 {
		t.Fatalf("calls %d counted %d aborted %d", rec.Calls(), counter.Total(), aborted)
	}
}
