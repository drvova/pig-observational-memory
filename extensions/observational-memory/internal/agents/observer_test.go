package agents_test

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/host"
	tf "github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/testfix"
)

func observerArgs(rec *tf.StreamRecorder, model host.Model) agents.ObserverArgs {
	key := "test"
	return agents.ObserverArgs{
		WorkerArgs:            agents.WorkerArgs{Model: model, APIKey: &key, Stream: rec.Stream},
		PriorReflections:      []string{},
		PriorObservations:     []string{},
		Chunk:                 "[Source entry id: entry-a]\nUser asked for a memory update.",
		AllowedSourceEntryIDs: []string{"entry-a"},
	}
}

func recordCall(observations ...map[string]any) map[string]any {
	items := make([]any, 0, len(observations))
	for _, o := range observations {
		items = append(items, o)
	}
	return tf.AssistantToolCall("tool-1", "record_observations", map[string]any{"observations": items})
}

func TestObserverMaxTokens(t *testing.T) {
	rec := &tf.StreamRecorder{}
	args := observerArgs(rec, tf.Model("p", "m", map[string]any{"maxTokens": 8192}))
	args.MaxOutputTokens = 32_000
	if _, err := agents.RunObserver(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if rec.Requests[0]["maxTokens"] != 8192 {
		t.Fatalf("maxTokens %v", rec.Requests[0]["maxTokens"])
	}
	rec = &tf.StreamRecorder{}
	args = observerArgs(rec, tf.Model("p", "m"))
	args.MaxOutputTokens = 8192
	_, _ = agents.RunObserver(context.Background(), args)
	if rec.Requests[0]["maxTokens"] != 8192 {
		t.Fatal("configured budget not passed through")
	}
	rec = &tf.StreamRecorder{}
	_, _ = agents.RunObserver(context.Background(), observerArgs(rec, tf.Model("p", "m")))
	if rec.Requests[0]["maxTokens"] != agents.AgentLoopMaxTokens {
		t.Fatal("default budget wrong")
	}
}

func TestObservationTimestampPattern(t *testing.T) {
	if strings.Contains(agents.ObservationTimestampPattern, `\d`) {
		t.Fatal("pattern uses regex shorthand")
	}
	pattern := regexp.MustCompile(agents.ObservationTimestampPattern)
	if !pattern.MatchString("2026-05-02 10:30") || pattern.MatchString("2026-5-02 10:30") || pattern.MatchString("2026-05-02T10:30") || pattern.MatchString("2026-05-02 10:30:00") {
		t.Fatal("pattern matches wrong")
	}
}

func TestObserverPromptRules(t *testing.T) {
	rec := &tf.StreamRecorder{}
	_, _ = agents.RunObserver(context.Background(), observerArgs(rec, tf.Model("p", "m")))
	system, _ := rec.Requests[0]["systemPrompt"].(string)
	for _, want := range []string{"Preserve user assertions exactly", "Detail preservation", "Frame state changes as supersession", "sourceEntryIds", "zero observations", "The dropper will drop these first", "highest-resistance, load-bearing observations"} {
		if !strings.Contains(system, want) {
			t.Fatalf("system prompt missing %q", want)
		}
	}
	if strings.Contains(system, "will NEVER be dropped") || strings.Contains(system, "pruner") {
		t.Fatal("system prompt carries removed guidance")
	}
	tools, _ := rec.Requests[0]["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "record_observations" {
		t.Fatal("record_observations tool missing")
	}
}

func TestObserverRecords(t *testing.T) {
	content := "User asked for a memory update."
	rec := &tf.StreamRecorder{Replies: []map[string]any{recordCall(map[string]any{"timestamp": "2026-05-02 10:30", "content": content, "relevance": "high", "sourceEntryIds": []any{"entry-a"}}), tf.AssistantText("done")}}
	observations, err := agents.RunObserver(context.Background(), observerArgs(rec, tf.Model("p", "m")))
	if err != nil || len(observations) != 1 {
		t.Fatalf("observations %v err %v", observations, err)
	}
	o := observations[0]
	if o.Content != content || o.Timestamp != "2026-05-02 10:30" || o.Relevance != "high" || len(o.SourceEntryIDs) != 1 || o.SourceEntryIDs[0] != "entry-a" || o.TokenCount != 18 || !regexp.MustCompile(`^[a-f0-9]{12}$`).MatchString(o.ID) {
		t.Fatalf("observation %+v", o)
	}
	if rec.Calls() != 2 {
		t.Fatalf("expected the loop to feed the tool result back, got %d calls", rec.Calls())
	}
	second := rec.Requests[1]["messages"].([]any)
	if len(second) != 3 || second[2].(map[string]any)["role"] != "toolResult" {
		t.Fatalf("second request messages %v", second)
	}
}

func TestObserverRejectsInventedSources(t *testing.T) {
	rec := &tf.StreamRecorder{Replies: []map[string]any{recordCall(map[string]any{"timestamp": "2026-05-02 10:30", "content": "Bad source", "relevance": "medium", "sourceEntryIds": []any{"missing"}}), tf.AssistantText("done")}}
	observations, err := agents.RunObserver(context.Background(), observerArgs(rec, tf.Model("p", "m")))
	if err != nil || observations != nil {
		t.Fatalf("expected a deliberate empty, got %v %v", observations, err)
	}
}

func TestObserverDedupes(t *testing.T) {
	rec := &tf.StreamRecorder{Replies: []map[string]any{recordCall(
		map[string]any{"timestamp": "2026-05-02 10:30", "content": "Same content", "relevance": "medium", "sourceEntryIds": []any{"entry-a"}},
		map[string]any{"timestamp": "2026-05-02 10:31", "content": "Same content", "relevance": "high", "sourceEntryIds": []any{"entry-a"}},
	), tf.AssistantText("done")}}
	observations, err := agents.RunObserver(context.Background(), observerArgs(rec, tf.Model("p", "m")))
	if err != nil || len(observations) != 1 || observations[0].Content != "Same content" {
		t.Fatalf("observations %v err %v", observations, err)
	}
}

func TestObserverNoToolCall(t *testing.T) {
	rec := &tf.StreamRecorder{}
	observations, err := agents.RunObserver(context.Background(), observerArgs(rec, tf.Model("p", "m")))
	if err != nil || observations != nil {
		t.Fatal("expected nil, nil")
	}
}

func TestObserverStreamError(t *testing.T) {
	for _, stop := range []string{"error", "aborted"} {
		rec := &tf.StreamRecorder{Replies: []map[string]any{tf.AssistantError(stop, "prompt is too long")}}
		_, err := agents.RunObserver(context.Background(), observerArgs(rec, tf.Model("p", "m")))
		var streamErr *agents.StreamError
		if !errors.As(err, &streamErr) || streamErr.StopReason != stop || !strings.Contains(err.Error(), "prompt is too long") || streamErr.Stage != "observer" {
			t.Fatalf("stop %s: err %v", stop, err)
		}
	}
}

func TestObserverKeepsPartialOnLaterError(t *testing.T) {
	rec := &tf.StreamRecorder{Replies: []map[string]any{recordCall(map[string]any{"timestamp": "2026-05-02 10:30", "content": "Kept despite later error", "relevance": "high", "sourceEntryIds": []any{"entry-a"}}), tf.AssistantError("error", "gateway timeout")}}
	observations, err := agents.RunObserver(context.Background(), observerArgs(rec, tf.Model("p", "m")))
	if err != nil || len(observations) != 1 || observations[0].Content != "Kept despite later error" {
		t.Fatalf("observations %v err %v", observations, err)
	}
}

func TestObserverTurnCap(t *testing.T) {
	call := recordCall(map[string]any{"timestamp": "2026-05-02 10:30", "content": "Fact", "relevance": "low", "sourceEntryIds": []any{"entry-a"}})
	rec := &tf.StreamRecorder{Replies: []map[string]any{call, call, call, call}}
	args := observerArgs(rec, tf.Model("p", "m"))
	args.MaxTurns = 2
	if _, err := agents.RunObserver(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if rec.Calls() != 2 {
		t.Fatalf("turn cap ignored: %d calls", rec.Calls())
	}
	// A failed turn is a hard exit, not a counted turn.
	rec = &tf.StreamRecorder{Replies: []map[string]any{tf.AssistantError("error", "boom"), call, call}}
	args = observerArgs(rec, tf.Model("p", "m"))
	args.MaxTurns = 5
	_, _ = agents.RunObserver(context.Background(), args)
	if rec.Calls() != 1 {
		t.Fatalf("loop continued after a failed turn: %d calls", rec.Calls())
	}
}

func TestObserverReasoning(t *testing.T) {
	rec := &tf.StreamRecorder{}
	args := observerArgs(rec, tf.Model("p", "m", map[string]any{"reasoning": true}))
	args.ThinkingLevel = "minimal"
	_, _ = agents.RunObserver(context.Background(), args)
	if rec.Requests[0]["reasoning"] != "minimal" {
		t.Fatalf("reasoning %v", rec.Requests[0]["reasoning"])
	}
	rec = &tf.StreamRecorder{}
	args = observerArgs(rec, tf.Model("p", "m", map[string]any{"reasoning": true}))
	args.ThinkingLevel = "off"
	_, _ = agents.RunObserver(context.Background(), args)
	if _, present := rec.Requests[0]["reasoning"]; present {
		t.Fatal("reasoning sent when off")
	}
	rec = &tf.StreamRecorder{}
	args = observerArgs(rec, tf.Model("p", "m"))
	args.ThinkingLevel = "high"
	_, _ = agents.RunObserver(context.Background(), args)
	if _, present := rec.Requests[0]["reasoning"]; present {
		t.Fatal("reasoning sent for a non-reasoning model")
	}
}

func TestObserverAuthAndAbort(t *testing.T) {
	rec := &tf.StreamRecorder{}
	args := observerArgs(rec, tf.Model("p", "m"))
	args.Headers = map[string]any{"Authorization": "Bearer x"}
	args.Env = map[string]string{"A": "b"}
	_, _ = agents.RunObserver(context.Background(), args)
	if rec.Requests[0]["apiKey"] != "test" || rec.Requests[0]["headers"].(map[string]any)["Authorization"] != "Bearer x" || rec.Requests[0]["env"].(map[string]string)["A"] != "b" {
		t.Fatalf("auth not forwarded: %v", rec.Requests[0])
	}
	blocking := &tf.StreamRecorder{Block: true}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := agents.RunObserver(ctx, observerArgs(blocking, tf.Model("p", "m")))
		done <- err
	}()
	cancel()
	if err := <-done; !errors.Is(err, agents.ErrAborted) {
		t.Fatalf("expected ErrAborted, got %v", err)
	}
}

func TestNormalizeSourceEntryIDs(t *testing.T) {
	allowed := []string{"entry-a", "entry-b", "entry-c"}
	if got, ok := agents.NormalizeSourceEntryIDs([]string{"entry-c", "entry-a"}, allowed); !ok || strings.Join(got, ",") != "entry-a,entry-c" {
		t.Fatalf("order %v", got)
	}
	if got, ok := agents.NormalizeSourceEntryIDs([]string{"entry-b", "entry-b", "entry-a"}, allowed); !ok || strings.Join(got, ",") != "entry-a,entry-b" {
		t.Fatalf("dedupe %v", got)
	}
	for _, bad := range [][]string{nil, {}, {"entry-a", "not-in-the-chunk"}} {
		if _, ok := agents.NormalizeSourceEntryIDs(bad, allowed); ok {
			t.Fatalf("accepted %v", bad)
		}
	}
	if _, ok := agents.NormalizeSourceEntryIDs([]string{"entry-a"}, nil); ok {
		t.Fatal("accepted with no allowed ids")
	}
}

func observerCall(content string) map[string]any {
	return tf.AssistantToolCall("tool-1", "record_observations", map[string]any{"observations": []any{map[string]any{
		"timestamp": "2026-05-02 10:00", "content": content, "relevance": "medium", "sourceEntryIds": []any{"raw-1"},
	}}})
}

func detailedArgs(rec *tf.StreamRecorder) agents.ObserverArgs {
	key := "k"
	return agents.ObserverArgs{WorkerArgs: agents.WorkerArgs{Model: tf.Model("p", "m"), APIKey: &key, Stream: rec.Stream}, Chunk: "[Source entry id: raw-1]\n[User @ 2026-05-02 10:00]: hello", AllowedSourceEntryIDs: []string{"raw-1"}}
}

// Content is single-line prose; runs that end early are flagged partial.
func TestObserverNormalizesContentAndFlagsPartialRuns(t *testing.T) {
	rec := &tf.StreamRecorder{Replies: []map[string]any{observerCall("  line one\nline   two  "), tf.AssistantText("done")}}
	result, err := agents.RunObserverDetailed(context.Background(), detailedArgs(rec))
	if err != nil || result.Partial || len(result.Observations) != 1 || result.Observations[0].Content != "line one line two" {
		t.Fatalf("result %+v err %v", result, err)
	}

	// A stream failure after a recorded batch is a partial run, not a failure.
	rec = &tf.StreamRecorder{Replies: []map[string]any{observerCall("fact"), tf.AssistantError("error", "connection reset")}}
	result, err = agents.RunObserverDetailed(context.Background(), detailedArgs(rec))
	if err != nil || !result.Partial || result.StreamError == nil || len(result.Observations) != 1 {
		t.Fatalf("result %+v err %v", result, err)
	}

	// The turn cap ending a turn that still called tools is a partial run.
	rec = &tf.StreamRecorder{Replies: []map[string]any{observerCall("fact"), observerCall("another fact")}}
	args := detailedArgs(rec)
	args.MaxTurns = 1
	result, err = agents.RunObserverDetailed(context.Background(), args)
	if err != nil || !result.Partial || result.StreamError != nil || len(result.Observations) != 1 {
		t.Fatalf("result %+v err %v", result, err)
	}

	// A run whose final turn is a text reply finished, whether that turn lands
	// on the cap or before it.
	for _, maxTurns := range []int{2, 3} {
		rec = &tf.StreamRecorder{Replies: []map[string]any{observerCall("fact"), tf.AssistantText("done")}}
		args = detailedArgs(rec)
		args.MaxTurns = maxTurns
		result, err = agents.RunObserverDetailed(context.Background(), args)
		if err != nil || result.Partial || result.StreamError != nil || len(result.Observations) != 1 || rec.Calls() != 2 {
			t.Fatalf("max turns %d: result %+v err %v calls %d", maxTurns, result, err, rec.Calls())
		}
	}

	// Whitespace-only content is rejected like empty content.
	rec = &tf.StreamRecorder{Replies: []map[string]any{observerCall("   \n  "), tf.AssistantText("done")}}
	if got, err := agents.RunObserver(context.Background(), detailedArgs(rec)); err != nil || got != nil {
		t.Fatalf("blank content accepted: %v err %v", got, err)
	}
}

// A timestamp outside the declared pattern is rejected, as Pi's schema
// validation would reject it.
func TestObserverRejectsMalformedTimestamps(t *testing.T) {
	bad := tf.AssistantToolCall("tool-1", "record_observations", map[string]any{"observations": []any{map[string]any{
		"timestamp": "2026-05-02T10:00:00Z", "content": "fact", "relevance": "medium", "sourceEntryIds": []any{"raw-1"},
	}}})
	rec := &tf.StreamRecorder{Replies: []map[string]any{bad, tf.AssistantText("done")}}
	if got, err := agents.RunObserver(context.Background(), detailedArgs(rec)); err != nil || got != nil {
		t.Fatalf("malformed timestamp accepted: %v err %v", got, err)
	}
	messages, _ := rec.Requests[1]["messages"].([]any)
	last, _ := messages[len(messages)-1].(map[string]any)
	content, _ := last["content"].([]any)
	block, _ := content[0].(map[string]any)
	if text, _ := block["text"].(string); !strings.Contains(text, "rejected for empty content, a timestamp not in 'YYYY-MM-DD HH:MM' form") {
		t.Fatalf("ack %q", text)
	}
}
