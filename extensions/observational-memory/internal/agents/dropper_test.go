package agents_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

func timestamp(ts string) func(*ledger.Observation) {
	return func(o *ledger.Observation) { o.Timestamp = ts }
}

func dropperArgs(rec *tf.StreamRecorder, model host.Model) agents.DropperArgs {
	key := "test"
	return agents.DropperArgs{
		WorkerArgs:   agents.WorkerArgs{Model: model, APIKey: &key, Stream: rec.Stream},
		Reflections:  []ledger.Reflection{tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})},
		Observations: []ledger.Observation{tf.Observation("aaaaaaaaaaaa", relevance("medium")), tf.Observation("bbbbbbbbbbbb", relevance("low")), tf.Observation("cccccccccccc", relevance("critical"))},
		TargetTokens: 20,
	}
}

func dropCall(ids ...string) map[string]any {
	items := make([]any, 0, len(ids))
	for _, id := range ids {
		items = append(items, id)
	}
	return tf.AssistantToolCall("tool-1", "drop_observations", map[string]any{"ids": items})
}

func TestDropperMaxTokens(t *testing.T) {
	rec := &tf.StreamRecorder{}
	args := dropperArgs(rec, tf.Model("p", "m", map[string]any{"maxTokens": 8192}))
	args.MaxOutputTokens = 32_000
	_, _ = agents.RunDropper(context.Background(), args)
	if rec.Requests[0]["maxTokens"] != 8192 {
		t.Fatal("clamp wrong")
	}
	rec = &tf.StreamRecorder{}
	args = dropperArgs(rec, tf.Model("p", "m"))
	args.MaxOutputTokens = 8192
	_, _ = agents.RunDropper(context.Background(), args)
	if rec.Requests[0]["maxTokens"] != 8192 {
		t.Fatal("pass-through wrong")
	}
	rec = &tf.StreamRecorder{}
	_, _ = agents.RunDropper(context.Background(), dropperArgs(rec, tf.Model("p", "m")))
	if rec.Requests[0]["maxTokens"] != agents.AgentLoopMaxTokens {
		t.Fatal("default wrong")
	}
}

func TestPoolFullnessAndMaxDrops(t *testing.T) {
	if agents.ObservationPoolFullness(0, 100) != 0 || agents.ObservationPoolFullness(-1, 100) != 0 || agents.ObservationPoolFullness(10, 0) != 0 || agents.ObservationPoolFullness(25, 100) != 0.25 {
		t.Fatal("fullness wrong")
	}
	observations := []ledger.Observation{}
	for i := 0; i < 10; i++ {
		observations = append(observations, tf.Observation(strings.Repeat("a", 11)+fmt.Sprint(i), relevance("low"), tokenCount(10)))
	}
	if agents.MaxDropCountForPool(observations, 100, 100) != 0 || agents.MaxDropCountForPool(observations, 100, 90) != 1 || agents.MaxDropCountForPool(observations, 100, 50) != 5 || agents.MaxDropCountForPool(observations, 100, 0) != 10 {
		t.Fatal("max drops wrong")
	}
	mixed := []ledger.Observation{
		tf.Observation("aaaaaaaaaaaa", relevance("low"), tokenCount(10)), tf.Observation("bbbbbbbbbbbb", relevance("medium"), tokenCount(10)),
		tf.Observation("cccccccccccc", relevance("critical"), tokenCount(10)), tf.Observation("dddddddddddd", relevance("critical"), tokenCount(10)),
	}
	if agents.MaxDropCountForPool(mixed, 40, 20) != 2 {
		t.Fatal("critical ids should stay eligible")
	}
}

func TestDropperPrompt(t *testing.T) {
	rec := &tf.StreamRecorder{}
	_, _ = agents.RunDropper(context.Background(), dropperArgs(rec, tf.Model("p", "m")))
	system, _ := rec.Requests[0]["systemPrompt"].(string)
	for _, want := range []string{"Active-memory framing", "Age-gradient rule", "highest importance and strongest resistance", "Relevance is importance/resistance, not an absolute keep/drop lock", "Coverage is evidence, not an automatic decision", "age alone is not enough", "Preservation floor", "Do not force drops", "You cannot merge observations", "Default action is KEEP", "When uncertain, keep", "active observation pool target"} {
		if !strings.Contains(system, want) {
			t.Fatalf("system prompt missing %q", want)
		}
	}
	for _, forbidden := range []string{"NEVER drop", "drop freely", "pruner", "drop-priority", "drop-resistance", "Pass strategy", "Urgency guidance"} {
		if strings.Contains(system, forbidden) {
			t.Fatalf("system prompt carries %q", forbidden)
		}
	}
	text := userText(rec)
	for _, want := range []string{"fullness against target: ~280%", "over target by ~36 tokens", "[coverage: partial]", "[coverage: none]", "Maximum drops allowed this run: 2 observations", "sized to move the active pool toward the target", "hard upper bound, not a target", "Drop fewer or none"} {
		if !strings.Contains(text, want) {
			t.Fatalf("user text missing %q", want)
		}
	}
	if strings.Contains(text, "Drop urgency") || strings.Contains(text, "drop-priority") {
		t.Fatal("user text carries removed guidance")
	}
}

func TestNormalizeDropIDs(t *testing.T) {
	observations := dropperArgs(&tf.StreamRecorder{}, nil).Observations
	if got, ok := agents.NormalizeDropObservationIDs([]string{"bbbbbbbbbbbb", "missing", "bbbbbbbbbbbb", "cccccccccccc", "aaaaaaaaaaaa"}, observations); !ok || !reflect.DeepEqual(got, []string{"bbbbbbbbbbbb", "cccccccccccc", "aaaaaaaaaaaa"}) {
		t.Fatalf("normalized %v", got)
	}
	if got, ok := agents.NormalizeDropObservationIDs([]string{"missing", "cccccccccccc"}, observations); !ok || !reflect.DeepEqual(got, []string{"cccccccccccc"}) {
		t.Fatalf("normalized %v", got)
	}
	if _, ok := agents.NormalizeDropObservationIDs([]string{"missing"}, observations); ok {
		t.Fatal("only-invalid accepted")
	}
}

func TestSelectDropCandidates(t *testing.T) {
	observations := []ledger.Observation{
		tf.Observation("aaaaaaaaaaaa", relevance("high")), tf.Observation("bbbbbbbbbbbb", relevance("low")), tf.Observation("dddddddddddd", relevance("medium")),
		tf.Observation("eeeeeeeeeeee", relevance("low")), tf.Observation("ffffffffffff", relevance("high")), tf.Observation("111111111111", relevance("critical")),
	}
	got := agents.SelectDropCandidates([]string{"aaaaaaaaaaaa", "missing", "111111111111", "bbbbbbbbbbbb", "dddddddddddd", "bbbbbbbbbbbb", "eeeeeeeeeeee", "ffffffffffff"}, observations, 3, nil)
	if !reflect.DeepEqual(got, []string{"bbbbbbbbbbbb", "eeeeeeeeeeee", "dddddddddddd"}) {
		t.Fatalf("selected %v", got)
	}
	oldHigh := tf.Observation("999999999999", relevance("high"), timestamp("2026-01-01T00:00:00.000Z"))
	newHigh := tf.Observation("888888888888", relevance("high"), timestamp("2026-02-01T00:00:00.000Z"))
	if got := agents.SelectDropCandidates([]string{"888888888888", "999999999999"}, []ledger.Observation{oldHigh, newHigh}, 1, nil); !reflect.DeepEqual(got, []string{"999999999999"}) {
		t.Fatalf("age ordering %v", got)
	}
	strongCritical := tf.Observation("aaaaaaaaaaaa", relevance("critical"), timestamp("2026-01-01T00:00:00.000Z"))
	partialLow := tf.Observation("bbbbbbbbbbbb", relevance("low"), timestamp("2026-01-01T00:00:00.000Z"))
	noneLow := tf.Observation("cccccccccccc", relevance("low"), timestamp("2026-01-01T00:00:00.000Z"))
	reflections := []ledger.Reflection{tf.Reflection("rrrrrrrrrrr1", []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb"}), tf.Reflection("rrrrrrrrrrr2", []string{"aaaaaaaaaaaa"})}
	if got := agents.SelectDropCandidates([]string{"cccccccccccc", "bbbbbbbbbbbb", "aaaaaaaaaaaa"}, []ledger.Observation{strongCritical, partialLow, noneLow}, 2, reflections); !reflect.DeepEqual(got, []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb"}) {
		t.Fatalf("coverage ordering %v", got)
	}
	critical := tf.Observation("aaaaaaaaaaaa", relevance("critical"), timestamp("2026-01-01T00:00:00.000Z"))
	high := tf.Observation("bbbbbbbbbbbb", relevance("high"), timestamp("2026-01-01T00:00:00.000Z"))
	low := tf.Observation("cccccccccccc", relevance("low"), timestamp("2026-01-01T00:00:00.000Z"))
	all := []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc"}
	equal := []ledger.Reflection{tf.Reflection("rrrrrrrrrrr1", all), tf.Reflection("rrrrrrrrrrr2", all)}
	if got := agents.SelectDropCandidates(all, []ledger.Observation{critical, high, low}, 2, equal); !reflect.DeepEqual(got, []string{"cccccccccccc", "bbbbbbbbbbbb"}) {
		t.Fatalf("relevance ordering %v", got)
	}
}

func TestDropperRuns(t *testing.T) {
	rec := &tf.StreamRecorder{Replies: []map[string]any{dropCall("aaaaaaaaaaaa", "missing", "bbbbbbbbbbbb"), tf.AssistantText("done")}}
	args := dropperArgs(rec, tf.Model("p", "m"))
	args.TargetTokens = 40
	if got, err := agents.RunDropper(context.Background(), args); err != nil || !reflect.DeepEqual(got, []string{"aaaaaaaaaaaa"}) {
		t.Fatalf("capped drops %v err %v", got, err)
	}
	rec = &tf.StreamRecorder{Replies: []map[string]any{dropCall("missing", "cccccccccccc"), tf.AssistantText("done")}}
	if got, err := agents.RunDropper(context.Background(), dropperArgs(rec, tf.Model("p", "m"))); err != nil || !reflect.DeepEqual(got, []string{"cccccccccccc"}) {
		t.Fatalf("critical drops %v err %v", got, err)
	}
	rec = &tf.StreamRecorder{Replies: []map[string]any{dropCall("missing"), tf.AssistantText("done")}}
	if got, err := agents.RunDropper(context.Background(), dropperArgs(rec, tf.Model("p", "m"))); err != nil || got != nil {
		t.Fatalf("invalid drops %v err %v", got, err)
	}
	rec = &tf.StreamRecorder{Replies: []map[string]any{dropCall("aaaaaaaaaaaa"), dropCall("bbbbbbbbbbbb", "aaaaaaaaaaaa"), tf.AssistantText("done")}}
	args = dropperArgs(rec, tf.Model("p", "m"))
	args.TargetTokens = 40
	if got, err := agents.RunDropper(context.Background(), args); err != nil || !reflect.DeepEqual(got, []string{"aaaaaaaaaaaa"}) {
		t.Fatalf("run-level cap %v err %v", got, err)
	}
	rec = &tf.StreamRecorder{}
	if got, err := agents.RunDropper(context.Background(), dropperArgs(rec, tf.Model("p", "m"))); err != nil || got != nil {
		t.Fatal("no tool call should yield nil")
	}
	for _, stop := range []string{"error", "aborted"} {
		rec := &tf.StreamRecorder{Replies: []map[string]any{tf.AssistantError(stop, "rate limited")}}
		_, err := agents.RunDropper(context.Background(), dropperArgs(rec, tf.Model("p", "m")))
		var streamErr *agents.StreamError
		if !errors.As(err, &streamErr) || streamErr.StopReason != stop || streamErr.Stage != "dropper" || !strings.Contains(err.Error(), "rate limited") {
			t.Fatalf("stop %s err %v", stop, err)
		}
	}
	rec = &tf.StreamRecorder{Replies: []map[string]any{dropCall("aaaaaaaaaaaa"), tf.AssistantError("error", "rate limited")}}
	if got, err := agents.RunDropper(context.Background(), dropperArgs(rec, tf.Model("p", "m"))); err != nil || !reflect.DeepEqual(got, []string{"aaaaaaaaaaaa"}) {
		t.Fatalf("drops lost after later failure: %v err %v", got, err)
	}
	rec = &tf.StreamRecorder{}
	args = dropperArgs(rec, tf.Model("p", "m"))
	args.Observations = []ledger.Observation{tf.Observation("aaaaaaaaaaaa", relevance("low"))}
	args.TargetTokens = 18
	if got, err := agents.RunDropper(context.Background(), args); err != nil || got != nil || rec.Calls() != 0 {
		t.Fatal("model called at or below target")
	}
}

func TestStreamErrorLogged(t *testing.T) {
	dir := t.TempDir()
	old := debuglog.AgentDir
	debuglog.AgentDir = func() (string, error) { return dir, nil }
	defer func() { debuglog.AgentDir = old }()
	ctx := debuglog.With(context.Background(), debuglog.Context{Enabled: true, SessionID: "session-stream-4"})
	rec := &tf.StreamRecorder{Replies: []map[string]any{tf.AssistantError("error", "upstream 400")}}
	_, err := agents.RunObserver(ctx, observerArgs(rec, tf.Model("p", "m")))
	var streamErr *agents.StreamError
	if !errors.As(err, &streamErr) {
		t.Fatalf("err %v", err)
	}
	data, readErr := os.ReadFile(filepath.Join(dir, "observational-memory", "debug", "session-stream-4.ndjson"))
	if readErr != nil {
		t.Fatal(readErr)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 1 || !strings.Contains(lines[0], `"event":"observer.stream_error"`) || !strings.Contains(lines[0], "upstream 400") || !strings.Contains(lines[0], `"stopReason":"error"`) {
		t.Fatalf("log %v", lines)
	}
}

func dropperUserText(t *testing.T, rec *tf.StreamRecorder) string {
	t.Helper()
	if rec.Calls() == 0 {
		t.Fatal("model was not called")
	}
	messages, _ := rec.Requests[0]["messages"].([]any)
	if len(messages) == 0 {
		t.Fatalf("request without messages: %v", rec.Requests[0])
	}
	first, _ := messages[0].(map[string]any)
	content, _ := first["content"].([]any)
	if len(content) == 0 {
		t.Fatalf("user message without content: %v", first)
	}
	block, _ := content[0].(map[string]any)
	text, _ := block["text"].(string)
	return text
}

// The over-target gate measures the whole active pool, not the memory-capped
// slice the dropper is shown (upstream issue #102).
func TestDropperGatesOnWholePool(t *testing.T) {
	rec := &tf.StreamRecorder{Replies: []map[string]any{dropCall("aaaaaaaaaaaa"), tf.AssistantText("done")}}
	args := dropperArgs(rec, tf.Model("p", "m"))
	args.Observations = []ledger.Observation{tf.Observation("aaaaaaaaaaaa", relevance("low"))}
	args.ActiveObservations = append([]ledger.Observation{}, args.Observations...)
	for i := 0; i < 8; i++ {
		args.ActiveObservations = append(args.ActiveObservations, tf.Observation(fmt.Sprintf("%012d", i), relevance("medium")))
	}
	args.TargetTokens = 40
	if agents.ObservationTokenSum(args.Observations) >= args.TargetTokens {
		t.Fatal("the visible slice must sit under target for this test")
	}

	got, err := agents.RunDropper(context.Background(), args)

	if err != nil || !reflect.DeepEqual(got, []string{"aaaaaaaaaaaa"}) {
		t.Fatalf("drops %v err %v", got, err)
	}
	text := dropperUserText(t, rec)
	poolTokens := agents.ObservationTokenSum(args.ActiveObservations)
	if !strings.Contains(text, fmt.Sprintf("Active observation pool: ~%d tokens; target: ~40 tokens", poolTokens)) {
		t.Fatalf("prompt does not measure the whole pool:\n%s", text)
	}
	// The allowance is sized by the pool but capped at what the dropper sees.
	if !strings.Contains(text, "Maximum drops allowed this run: 1 observation.") {
		t.Fatalf("prompt allowance:\n%s", text)
	}

	// With no separate pool the slice is the pool, as before.
	rec = &tf.StreamRecorder{}
	args = dropperArgs(rec, tf.Model("p", "m"))
	args.Observations = []ledger.Observation{tf.Observation("aaaaaaaaaaaa", relevance("low"))}
	args.TargetTokens = 40
	if got, err := agents.RunDropper(context.Background(), args); err != nil || got != nil || rec.Calls() != 0 {
		t.Fatal("model called while the pool sits under target")
	}
}

func TestPreserveSpecificsKeepsDetailsNoReflectionCarries(t *testing.T) {
	observations := []ledger.Observation{
		{ID: "o1", Content: "User asked how to organize a closet.", Relevance: "low"},
		{ID: "o2", Content: "User keeps old sneakers under the bed at the Oak Street flat.", Relevance: "low"},
		{ID: "o3", Content: "User bought boots for $500 last month.", Relevance: "medium"},
		{ID: "o4", Content: "User paid $30 for a candle.", Relevance: "low"},
	}
	reflections := []ledger.Reflection{
		{ID: "r1", Content: "User is decluttering their closet.", SupportingObservationIDs: []string{"o1", "o2"}},
		{ID: "r2", Content: "User bought boots for $500 last month.", SupportingObservationIDs: []string{"o3"}},
	}
	remaining, kept := agents.PreserveSpecifics([]string{"o1", "o2", "o3", "o4", "unknown"}, observations, reflections)
	if !reflect.DeepEqual(remaining, []string{"o1", "o3", "unknown"}) || !reflect.DeepEqual(kept, []string{"o2", "o4"}) {
		t.Fatalf("remaining %v, kept %v: o2's street name and o4's price are carried by no citing reflection", remaining, kept)
	}
}
