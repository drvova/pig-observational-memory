package agents_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ids"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

func reflectorArgs(rec *tf.StreamRecorder, model host.Model) agents.ReflectorArgs {
	key := "test"
	return agents.ReflectorArgs{
		WorkerArgs:   agents.WorkerArgs{Model: model, APIKey: &key, Stream: rec.Stream},
		Reflections:  []ledger.Reflection{},
		Observations: []ledger.Observation{tf.Observation("aaaaaaaaaaaa"), tf.Observation("bbbbbbbbbbbb")},
	}
}

func reflectCall(reflections ...map[string]any) map[string]any {
	items := make([]any, 0, len(reflections))
	for _, r := range reflections {
		items = append(items, r)
	}
	return tf.AssistantToolCall("tool-1", "record_reflections", map[string]any{"reflections": items})
}

func TestReflectorMaxTokens(t *testing.T) {
	rec := &tf.StreamRecorder{}
	args := reflectorArgs(rec, tf.Model("p", "m", map[string]any{"maxTokens": 8192}))
	args.MaxOutputTokens = 32_000
	_, _ = agents.RunReflector(context.Background(), args)
	if rec.Requests[0]["maxTokens"] != 8192 {
		t.Fatal("clamp wrong")
	}
	rec = &tf.StreamRecorder{}
	_, _ = agents.RunReflector(context.Background(), reflectorArgs(rec, tf.Model("p", "m")))
	if rec.Requests[0]["maxTokens"] != agents.AgentLoopMaxTokens {
		t.Fatal("default wrong")
	}
	call := reflectCall(map[string]any{"content": "Fact", "supportingObservationIds": []any{"aaaaaaaaaaaa"}})
	rec = &tf.StreamRecorder{Replies: []map[string]any{call, call, call}}
	args = reflectorArgs(rec, tf.Model("p", "m"))
	args.MaxTurns = 2
	_, _ = agents.RunReflector(context.Background(), args)
	if rec.Calls() != 2 {
		t.Fatalf("turn cap: %d calls", rec.Calls())
	}
}

func TestReflectorPrompt(t *testing.T) {
	rec := &tf.StreamRecorder{}
	_, _ = agents.RunReflector(context.Background(), reflectorArgs(rec, tf.Model("p", "m")))
	system, _ := rec.Requests[0]["systemPrompt"].(string)
	for _, want := range []string{
		"Your task is different from the observer", "User assertions are authoritative", "supportingObservationIds", "coverage/provenance set",
		"Do not lightly reword existing reflections", "Reflections are scarce, expensive durable orientation anchors", "not a second observation layer",
		"Over-reflection is also memory distortion", "Decision procedure:", "future-agent utility test", "If unsure, emit no reflection",
		"Do not turn each observation into a reflection", "Single-observation reflections are allowed", "Prefer fewer, higher-value reflections",
		"[coverage: none|partial|strong]", "Support ids and coverage stewardship", "False or inflated support ids can cause unsafe downstream dropper pruning",
		"Focus on:", "Reflection content rules", "Lead with the fact or pattern",
	} {
		if !strings.Contains(system, want) {
			t.Fatalf("system prompt missing %q", want)
		}
	}
	for _, forbidden := range []string{"legacy/no-provenance", "pruner", "Pass strategy"} {
		if strings.Contains(system, forbidden) {
			t.Fatalf("system prompt carries %q", forbidden)
		}
	}
}

func userText(rec *tf.StreamRecorder) string {
	messages := rec.Requests[0]["messages"].([]any)
	content := messages[0].(map[string]any)["content"].([]any)
	return content[0].(map[string]any)["text"].(string)
}

func TestReflectorCoverageLines(t *testing.T) {
	rec := &tf.StreamRecorder{}
	args := reflectorArgs(rec, tf.Model("p", "m"))
	args.Observations = []ledger.Observation{
		tf.Observation("aaaaaaaaaaaa", content("Uncovered durable fact")),
		tf.Observation("bbbbbbbbbbbb", content("Partially covered fact")),
		tf.Observation("cccccccccccc", content("Strongly covered fact")),
	}
	args.Reflections = []ledger.Reflection{tf.Reflection("rrrrrrrrrrr1", []string{"bbbbbbbbbbbb", "cccccccccccc"}), tf.Reflection("rrrrrrrrrrr2", []string{"cccccccccccc"})}
	_, _ = agents.RunReflector(context.Background(), args)
	text := userText(rec)
	for _, want := range []string{"[aaaaaaaaaaaa]", "[coverage: none] Uncovered durable fact", "[coverage: partial] Partially covered fact", "[coverage: strong] Strongly covered fact"} {
		if !strings.Contains(text, want) {
			t.Fatalf("user text missing %q", want)
		}
	}
	if strings.Contains(text, "drop-priority") {
		t.Fatal("user text leaked drop metadata")
	}
	line := agents.ObservationToReflectorLine(tf.Observation("aaaaaaaaaaaa", relevance("critical"), content("Important reflected fact")), "partial")
	if !strings.Contains(line, "[critical]") || !strings.Contains(line, "[coverage: partial]") || !strings.Contains(line, "Important reflected fact") {
		t.Fatalf("line %q", line)
	}
}

func TestSummarizeSupportIDCounts(t *testing.T) {
	empty := agents.SummarizeSupportIDCounts(nil)
	if empty.ReflectionCount != 0 || empty.TotalSupportIDs != 0 || empty.MinSupportIDs != 0 || empty.MaxSupportIDs != 0 || empty.AverageSupportIDs != 0 || len(empty.Histogram) != 0 {
		t.Fatalf("empty summary %+v", empty)
	}
	summary := agents.SummarizeSupportIDCounts([]ledger.Reflection{tf.Reflection("rrrrrrrrrrr1", []string{"aaaaaaaaaaaa"}), tf.Reflection("rrrrrrrrrrr2", []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc"})})
	if summary.ReflectionCount != 2 || summary.TotalSupportIDs != 4 || summary.MinSupportIDs != 1 || summary.MaxSupportIDs != 3 || summary.AverageSupportIDs != 2 || !reflect.DeepEqual(summary.Histogram, map[string]int{"1": 1, "3": 1}) {
		t.Fatalf("summary %+v", summary)
	}
}

func TestNormalizeSupportingObservationIDs(t *testing.T) {
	if got, ok := agents.NormalizeSupportingObservationIDs([]string{"bbbbbbbbbbbb", "aaaaaaaaaaaa", "aaaaaaaaaaaa"}, []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb"}); !ok || !reflect.DeepEqual(got, []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb"}) {
		t.Fatalf("normalized %v", got)
	}
	if _, ok := agents.NormalizeSupportingObservationIDs([]string{"aaaaaaaaaaaa", "missing"}, []string{"aaaaaaaaaaaa"}); ok {
		t.Fatal("invented id accepted")
	}
	if _, ok := agents.NormalizeSupportingObservationIDs(nil, []string{"aaaaaaaaaaaa"}); ok {
		t.Fatal("empty accepted")
	}
}

func TestReflectorRecords(t *testing.T) {
	content := "User prefers source-backed memory."
	rec := &tf.StreamRecorder{Replies: []map[string]any{reflectCall(map[string]any{"content": content, "supportingObservationIds": []any{"bbbbbbbbbbbb", "aaaaaaaaaaaa"}}), tf.AssistantText("done")}}
	result, err := agents.RunReflector(context.Background(), reflectorArgs(rec, tf.Model("p", "m")))
	want := []ledger.Reflection{{ID: ids.HashID(content), Content: content, SupportingObservationIDs: []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb"}, TokenCount: tokens.EstimateStringTokens(ledger.ReflectionToSummaryLine(ledger.Reflection{ID: ids.HashID(content), Content: content}))}}
	if err != nil || !reflect.DeepEqual(result, want) {
		t.Fatalf("result %+v err %v", result, err)
	}
}

func TestReflectorRejects(t *testing.T) {
	rec := &tf.StreamRecorder{Replies: []map[string]any{reflectCall(
		map[string]any{"content": "Bad support", "supportingObservationIds": []any{"missing"}},
		map[string]any{"content": "Two\nlines", "supportingObservationIds": []any{"aaaaaaaaaaaa"}},
	), tf.AssistantText("done")}}
	result, err := agents.RunReflector(context.Background(), reflectorArgs(rec, tf.Model("p", "m")))
	if err != nil || result != nil {
		t.Fatalf("result %v err %v", result, err)
	}
}

func TestReflectorDedupes(t *testing.T) {
	content := "User prefers terse updates."
	existing := tf.Reflection(ids.HashID(content), []string{"aaaaaaaaaaaa"}, func(r *ledger.Reflection) { r.Content = content })
	rec := &tf.StreamRecorder{Replies: []map[string]any{reflectCall(
		map[string]any{"content": content, "supportingObservationIds": []any{"aaaaaaaaaaaa"}},
		map[string]any{"content": "New durable fact.", "supportingObservationIds": []any{"aaaaaaaaaaaa"}},
		map[string]any{"content": "New durable fact.", "supportingObservationIds": []any{"bbbbbbbbbbbb"}},
	), tf.AssistantText("done")}}
	args := reflectorArgs(rec, tf.Model("p", "m"))
	args.Reflections = []ledger.Reflection{existing}
	result, err := agents.RunReflector(context.Background(), args)
	if err != nil || len(result) != 1 || result[0].Content != "New durable fact." {
		t.Fatalf("result %+v err %v", result, err)
	}
}

func TestReflectorEmptyAndErrors(t *testing.T) {
	rec := &tf.StreamRecorder{}
	if result, err := agents.RunReflector(context.Background(), reflectorArgs(rec, tf.Model("p", "m"))); err != nil || result != nil {
		t.Fatal("expected nil, nil")
	}
	for _, stop := range []string{"error", "aborted"} {
		rec := &tf.StreamRecorder{Replies: []map[string]any{tf.AssistantError(stop, "rate limited")}}
		_, err := agents.RunReflector(context.Background(), reflectorArgs(rec, tf.Model("p", "m")))
		var streamErr *agents.StreamError
		if !errors.As(err, &streamErr) || streamErr.StopReason != stop || streamErr.Stage != "reflector" || !strings.Contains(err.Error(), "rate limited") {
			t.Fatalf("stop %s err %v", stop, err)
		}
	}
	content := "User prefers source-backed memory."
	rec = &tf.StreamRecorder{Replies: []map[string]any{reflectCall(map[string]any{"content": content, "supportingObservationIds": []any{"aaaaaaaaaaaa"}}), tf.AssistantError("error", "rate limited")}}
	result, err := agents.RunReflector(context.Background(), reflectorArgs(rec, tf.Model("p", "m")))
	if err != nil || len(result) != 1 || result[0].Content != content {
		t.Fatalf("partial result lost: %+v err %v", result, err)
	}
	if result, err := agents.RunReflector(context.Background(), agents.ReflectorArgs{WorkerArgs: agents.WorkerArgs{Stream: rec.Stream}}); err != nil || result != nil {
		t.Fatal("no observations should skip the model")
	}
}
