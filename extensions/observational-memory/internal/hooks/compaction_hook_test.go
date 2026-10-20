package hooks

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/testfix"
)

type hookArgs struct {
	entries           []ledger.Entry
	poolMaxTokens     int
	hookInFlight      bool
	maxRetainedTokens int
	summaryMaxTokens  int
	model             host.Model
}

// hookSetup mirrors the TypeScript fixture: no catch-up, so the model is never resolved.
func hookSetup(a hookArgs) (*runtime.Runtime, *testfix.FakeHost) {
	cfg := config.Defaults
	cfg.ObservationsPoolMaxTokens = 20_000
	if a.poolMaxTokens > 0 {
		cfg.ObservationsPoolMaxTokens = a.poolMaxTokens
	}
	cfg.CompactionCatchUpMaxChunks = 0
	cfg.CompactionMaxRetainedTokens = a.maxRetainedTokens
	cfg.CompactionSummaryMaxTokens = a.summaryMaxTokens
	rt := runtime.New()
	rt.SetConfig(cfg)
	rt.SetCompactHookInFlight(a.hookInFlight)
	h := testfix.NewFakeHost(a.entries...)
	h.Model = a.model
	return rt, h
}

func runHook(rt *runtime.Runtime, h *testfix.FakeHost, entries []ledger.Entry, firstKeptEntryID, reason string) BeforeCompactOutcome {
	return HandleBeforeCompact(context.Background(), rt, h, BeforeCompactEvent{FirstKeptEntryID: firstKeptEntryID, TokensBefore: 123, BranchEntries: entries, Reason: reason})
}

func lastID(entries []ledger.Entry) string {
	if len(entries) == 0 {
		return "missing"
	}
	return entries[len(entries)-1].ID
}

func expectDelegated(t *testing.T, outcome BeforeCompactOutcome) {
	t.Helper()
	if outcome.Cancel || outcome.Compaction != nil || outcome.ToResult() != nil {
		t.Fatalf("expected delegation, got %+v", outcome)
	}
}

func compactionOf(t *testing.T, outcome BeforeCompactOutcome) CompactionResult {
	t.Helper()
	if outcome.Compaction == nil {
		t.Fatalf("expected a compaction, got %+v", outcome)
	}
	return *outcome.Compaction
}

func TestHookDelegatesWithoutV3Memory(t *testing.T) {
	entries := []ledger.Entry{text("raw-1", "aaaa")}
	rt, h := hookSetup(hookArgs{entries: entries})

	outcome := runHook(rt, h, entries, "raw-1", "")

	expectDelegated(t, outcome)
	if len(h.Appended) != 0 || rt.Snapshot().CompactHookInFlight || len(h.ModelsFake.FindCalls) != 0 {
		t.Fatalf("appended %d hook in flight %v finds %v", len(h.Appended), rt.Snapshot().CompactHookInFlight, h.ModelsFake.FindCalls)
	}
}

func TestHookFirstCompactionWritesCoveredObservationsWithoutOrphanReflections(t *testing.T) {
	obs1 := testfix.Observation("aaaaaaaaaaaa", func(o *ledger.Observation) { o.SourceEntryIDs = []string{"raw-1"}; o.TokenCount = 10 })
	ref1 := testfix.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})
	entries := []ledger.Entry{
		text("raw-1", "aaaa"),
		testfix.ObservationsRecordedEntry("om-aaaaaaaaaaaa", []ledger.Observation{obs1}, "raw-1"),
		testfix.ReflectionsRecordedEntry("om-eeeeeeeeeeee", []ledger.Reflection{ref1}, "raw-1"),
	}
	rt, h := hookSetup(hookArgs{entries: entries, poolMaxTokens: 100})

	result := compactionOf(t, runHook(rt, h, entries, "raw-1", ""))

	if result.Details.FullFold || !reflect.DeepEqual(observationIDs(result.Details.Observations), []string{"aaaaaaaaaaaa"}) || len(result.Details.Reflections) != 0 {
		t.Fatalf("details %+v", result.Details)
	}
	if !strings.Contains(result.Summary, "## Observations") || strings.Contains(result.Summary, "## Reflections") {
		t.Fatalf("summary %q", result.Summary)
	}
}

func foldedBranch(obs1Tokens, obs2Tokens int) []ledger.Entry {
	obs1 := testfix.Observation("aaaaaaaaaaaa", func(o *ledger.Observation) { o.TokenCount = obs1Tokens })
	obs2 := testfix.Observation("bbbbbbbbbbbb", func(o *ledger.Observation) { o.TokenCount = obs2Tokens })
	ref1 := testfix.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})
	ref2 := testfix.Reflection("ffffffffffff", []string{"bbbbbbbbbbbb"})
	return []ledger.Entry{
		text("raw-1", "aaaa"),
		testfix.ObservationsRecordedEntry("om-aaaaaaaaaaaa", []ledger.Observation{obs1}, "raw-1"),
		testfix.ReflectionsRecordedEntry("om-eeeeeeeeeeee", []ledger.Reflection{ref1}, "raw-1"),
		testfix.CompactionEntry("cmp-full", testfix.CompactionArgs{FirstKeptEntryID: "raw-1", Details: testfix.MemoryDetails(testfix.MemoryDetailsArgs{FullFold: true, Observations: []ledger.Observation{obs1}, Reflections: []ledger.Reflection{ref1}})}),
		text("raw-2", "bbbb"),
		testfix.ObservationsRecordedEntry("om-bbbbbbbbbbbb", []ledger.Observation{obs2}, "raw-2"),
		testfix.ReflectionsRecordedEntry("om-ffffffffffff", []ledger.Reflection{ref2}, "raw-2"),
		testfix.ObservationsDroppedEntry("om-drop-2", []string{"aaaaaaaaaaaa"}, "raw-2"),
	}
}

func TestHookWritesNormalProjectionWithoutApplyingNewReflectionsOrDrops(t *testing.T) {
	entries := foldedBranch(5, 5)
	rt, h := hookSetup(hookArgs{entries: entries, poolMaxTokens: 100})

	result := compactionOf(t, runHook(rt, h, entries, "raw-2", ""))

	if result.Details.Type != "om.folded" || result.Details.Version != 1 || result.Details.FullFold {
		t.Fatalf("details %+v", result.Details)
	}
	if !reflect.DeepEqual(observationIDs(result.Details.Observations), []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb"}) || !reflect.DeepEqual(testfix.RefIDs(result.Details.Reflections), []string{"eeeeeeeeeeee"}) {
		t.Fatalf("details %+v", result.Details)
	}
	if !strings.Contains(result.Summary, "## Reflections\n[eeeeeeeeeeee]") || !strings.Contains(result.Summary, "## Observations") {
		t.Fatalf("summary %q", result.Summary)
	}
}

func TestHookWritesFullProjectionAtPoolPressure(t *testing.T) {
	entries := foldedBranch(80, 30)
	rt, h := hookSetup(hookArgs{entries: entries, poolMaxTokens: 100})

	result := compactionOf(t, runHook(rt, h, entries, "raw-2", ""))

	if !result.Details.FullFold || !reflect.DeepEqual(observationIDs(result.Details.Observations), []string{"bbbbbbbbbbbb"}) || !reflect.DeepEqual(testfix.RefIDs(result.Details.Reflections), []string{"eeeeeeeeeeee", "ffffffffffff"}) {
		t.Fatalf("details %+v", result.Details)
	}
}

func TestHookDelegatesWhenOnlyV2MemoryExists(t *testing.T) {
	entries := []ledger.Entry{
		text("raw-1", "aaaa"),
		testfix.OldV2ObservationEntry("v2-obs"),
		testfix.CompactionEntry("cmp-v2", testfix.CompactionArgs{FirstKeptEntryID: "raw-1", Details: testfix.OldV2CompactionDetails()}),
	}
	rt, h := hookSetup(hookArgs{entries: entries})

	expectDelegated(t, runHook(rt, h, entries, "cmp-v2", ""))
}

func TestHookCancelsDuplicateCompaction(t *testing.T) {
	entries := []ledger.Entry{text("raw-1", "aaaa")}
	rt, h := hookSetup(hookArgs{entries: entries, hookInFlight: true})

	outcome := runHook(rt, h, entries, "raw-1", "")

	if !outcome.Cancel || !reflect.DeepEqual(outcome.ToResult(), map[string]any{"cancel": true}) {
		t.Fatalf("outcome %+v", outcome)
	}
	expectNotification(t, h, "Observational memory: another compaction is already in progress; cancelling duplicate", "warning")
}

func TestHookResultWireShape(t *testing.T) {
	entries := foldedBranch(5, 5)
	rt, h := hookSetup(hookArgs{entries: entries, poolMaxTokens: 100})

	result := runHook(rt, h, entries, lastID(entries[:5]), "").ToResult()

	compaction, ok := result["compaction"].(map[string]any)
	if !ok || compaction["firstKeptEntryId"] != "raw-2" || compaction["tokensBefore"] != 123 {
		t.Fatalf("result %v", result)
	}
}

// Observer behind Pi's cut.

type lagArgs struct {
	entries           []ledger.Entry
	maxRetainedTokens int
	summaryMaxTokens  int
	model             host.Model
}

func lagSetup(a lagArgs) (*runtime.Runtime, *testfix.FakeHost) {
	return hookSetup(hookArgs{entries: a.entries, maxRetainedTokens: a.maxRetainedTokens, summaryMaxTokens: a.summaryMaxTokens, model: a.model})
}

func TestLagKeepsPiCutWhenCoverageReachesIt(t *testing.T) {
	entries := append(laggingBranch(), coverage("om-a2", "a2", "cccccccccccc"))
	rt, h := lagSetup(lagArgs{entries: entries})

	result := compactionOf(t, runHook(rt, h, entries, "u2", "threshold"))

	if result.FirstKeptEntryID != "u2" || !reflect.DeepEqual(observationIDs(result.Details.Observations), []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc"}) || len(h.Notifications) != 0 {
		t.Fatalf("result %+v notifications %+v", result, h.Notifications)
	}
}

func TestLagMovesRetentionBoundaryBackToValidCutPoint(t *testing.T) {
	entries := laggingBranch()
	rt, h := lagSetup(lagArgs{entries: entries})

	result := compactionOf(t, runHook(rt, h, entries, "u2", "threshold"))

	// t1 is a tool result, so the cut lands on the assistant message that
	// issued it; a1 stays with its result.
	if result.FirstKeptEntryID != "a1" || result.TokensBefore != 123 {
		t.Fatalf("result %+v", result)
	}
	// Every recorded observation is folded, including the one whose coverage
	// marker (a1) is now the first retained entry.
	if !reflect.DeepEqual(observationIDs(result.Details.Observations), []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb"}) || !strings.Contains(result.Summary, "[bbbbbbbbbbbb]") {
		t.Fatalf("result %+v", result)
	}
	expectNotification(t, h, "Observational memory: observer has not reached 2 source entries (~7 tokens) before the compaction cut; keeping them in context until they are observed", "info")
}

func TestLagDelegatesWhenRetainingExceedsBudget(t *testing.T) {
	entries := laggingBranch()
	rt, h := lagSetup(lagArgs{entries: entries, maxRetainedTokens: 1})

	expectDelegated(t, runHook(rt, h, entries, "u2", "threshold"))

	if len(h.Notifications) != 1 || h.Notifications[0].Level != "warning" {
		t.Fatalf("notifications %+v", h.Notifications)
	}
	message := h.Notifications[0].Message
	for _, fragment := range []string{"observer has not reached 2 source entries (~7 tokens)", "exceeds the ~1-token budget", "delegating to Pi's native summarizer"} {
		if !strings.Contains(message, fragment) {
			t.Fatalf("message %q lacks %q", message, fragment)
		}
	}
}

func TestLagDelegatesWhenTailPlusSummaryExceedBudget(t *testing.T) {
	// The retained source alone (~14 tokens) fits a 100-token budget, but the
	// rendered summary adds a few hundred tokens of instructions and lines.
	entries := laggingBranch()
	rt, h := lagSetup(lagArgs{entries: entries, maxRetainedTokens: 100})

	expectDelegated(t, runHook(rt, h, entries, "u2", "threshold"))

	if len(h.Notifications) == 0 || h.Notifications[0].Level != "warning" || !strings.Contains(h.Notifications[0].Message, "plus a ~") || !strings.Contains(h.Notifications[0].Message, "memory summary exceeds the ~100-token budget") {
		t.Fatalf("notifications %+v", h.Notifications)
	}
}

func TestLagDerivesRetainedBudgetFromSessionModelWindow(t *testing.T) {
	// Half of a 4-token window is 2; the retained tail is larger than that.
	entries := laggingBranch()
	rt, h := lagSetup(lagArgs{entries: entries, model: host.Model{"contextWindow": 4}})
	expectDelegated(t, runHook(rt, h, entries, "u2", "threshold"))

	rt, h = lagSetup(lagArgs{entries: entries, model: host.Model{"contextWindow": 4_000}})
	if compactionOf(t, runHook(rt, h, entries, "u2", "threshold")).FirstKeptEntryID != "a1" {
		t.Fatal("roomy window should retain the tail")
	}
}

func TestLagDelegatesDuringOverflowRecovery(t *testing.T) {
	entries := laggingBranch()
	rt, h := lagSetup(lagArgs{entries: entries})

	expectDelegated(t, runHook(rt, h, entries, "u2", "overflow"))

	if len(h.Notifications) == 0 || !strings.Contains(h.Notifications[0].Message, "overflow recovery") {
		t.Fatalf("notifications %+v", h.Notifications)
	}
}

func TestLagCutsInsideSingleLongTurn(t *testing.T) {
	// One user turn followed by a long tool loop; the observer covered the
	// first two tool calls only. Pi proposes to keep from a3.
	entries := []ledger.Entry{
		userMessage("u0"), assistantMessage("a0"), toolResultMessage("t0"),
		assistantMessage("a1"), toolResultMessage("t1"),
		assistantMessage("a2"), toolResultMessage("t2"),
		assistantMessage("a3"), toolResultMessage("t3"),
		coverage("om-t1", "t1", "aaaaaaaaaaaa"),
	}
	rt, h := lagSetup(lagArgs{entries: entries})

	result := compactionOf(t, runHook(rt, h, entries, "a3", "threshold"))

	if result.FirstKeptEntryID != "a2" || !reflect.DeepEqual(observationIDs(result.Details.Observations), []string{"aaaaaaaaaaaa"}) {
		t.Fatalf("result %+v", result)
	}
}

func TestLagDelegatesWhenFrontierBeforeCompactionRange(t *testing.T) {
	entries := []ledger.Entry{
		userMessage("u0"), assistantMessage("a0"), userMessage("u1"), assistantMessage("a1"), toolResultMessage("t1"),
		assistantMessage("a2"), toolResultMessage("t2"), assistantMessage("a3"),
		coverage("om-a0", "a0", "aaaaaaaaaaaa"),
		testfix.CompactionEntry("cmp-0", testfix.CompactionArgs{FirstKeptEntryID: "a1"}),
	}
	rt, h := lagSetup(lagArgs{entries: entries})

	expectDelegated(t, runHook(rt, h, entries, "a3", "threshold"))

	if len(h.Notifications) == 0 || !strings.Contains(h.Notifications[0].Message, "nothing observed can be compacted") {
		t.Fatalf("notifications %+v", h.Notifications)
	}
}

func TestLagDelegatesWhenRangeStartsOnMetadataEntry(t *testing.T) {
	// Pi may place the retention boundary on a zero-token ledger entry that
	// precedes the first kept message. Cutting one entry later would free
	// nothing and re-trigger Pi's threshold immediately.
	entries := []ledger.Entry{
		userMessage("u0"), assistantMessage("a0"), toolResultMessage("t0"),
		coverage("om-t0", "t0", "aaaaaaaaaaaa"),
		assistantMessage("a1"), toolResultMessage("t1"),
		coverage("om-marker", "t0", "bbbbbbbbbbbb"),
		testfix.CompactionEntry("cmp-0", testfix.CompactionArgs{FirstKeptEntryID: "om-marker"}),
		assistantMessage("a2"), toolResultMessage("t2"), assistantMessage("a3"), toolResultMessage("t3"), assistantMessage("a4"),
	}
	rt, h := lagSetup(lagArgs{entries: entries})

	expectDelegated(t, runHook(rt, h, entries, "a4", "threshold"))

	if len(h.Notifications) == 0 || !strings.Contains(h.Notifications[0].Message, "nothing observed can be compacted") {
		t.Fatalf("notifications %+v", h.Notifications)
	}
}

func TestLagDelegatesWhenNothingObserved(t *testing.T) {
	entries := []ledger.Entry{userMessage("u0"), assistantMessage("a0"), userMessage("u1")}
	rt, h := lagSetup(lagArgs{entries: entries})

	expectDelegated(t, runHook(rt, h, entries, "u1", "threshold"))

	if len(h.Notifications) == 0 || !strings.Contains(h.Notifications[0].Message, "no observation coverage") {
		t.Fatalf("notifications %+v", h.Notifications)
	}
}

func TestLagIgnoresUnobservedEntriesBeforePreviousBoundary(t *testing.T) {
	entries := []ledger.Entry{
		userMessage("u0"), assistantMessage("a0"),
		testfix.CompactionEntry("cmp-0", testfix.CompactionArgs{FirstKeptEntryID: "u1"}),
		userMessage("u1"), assistantMessage("a1"), userMessage("u2"),
		coverage("om-a1", "a1", "bbbbbbbbbbbb"),
	}
	rt, h := lagSetup(lagArgs{entries: entries})

	result := compactionOf(t, runHook(rt, h, entries, "u2", "threshold"))

	if result.FirstKeptEntryID != "u2" || len(h.Notifications) != 0 {
		t.Fatalf("result %+v notifications %+v", result, h.Notifications)
	}
}

func TestResolveCompactionCutReturnsUnknownCutUnchanged(t *testing.T) {
	resolution := ResolveCompactionCut(laggingBranch(), "missing", CutOptions{MaxRetainedTokens: 1000})

	want := CutResolution{Kind: "cut", Cut: CompactionCut{FirstKeptEntryID: "missing", FoldThroughEntryID: "missing"}, RetainedTokens: -1}
	if !reflect.DeepEqual(resolution, want) {
		t.Fatalf("resolution %+v", resolution)
	}
}

func TestResolveCompactionCutReportsRetainedSpan(t *testing.T) {
	resolution := ResolveCompactionCut(laggingBranch(), "u2", CutOptions{MaxRetainedTokens: 1000})

	if resolution.Kind != "cut" || resolution.Gap == nil || resolution.Gap.FirstIndex != 4 || resolution.Gap.LastIndex != 5 || resolution.Gap.EntryCount != 2 {
		t.Fatalf("resolution %+v gap %+v", resolution, resolution.Gap)
	}
	if resolution.Cut != (CompactionCut{FirstKeptEntryID: "a1", FoldThroughEntryID: "a1"}) {
		t.Fatalf("cut %+v", resolution.Cut)
	}
}

func TestHookDelegatesInsteadOfInstructionsOnlySummary(t *testing.T) {
	huge := testfix.ObservationsRecordedEntry("om-huge", []ledger.Observation{testfix.Observation("dddddddddddd", func(o *ledger.Observation) {
		o.SourceEntryIDs = []string{"a0"}
		o.Content = strings.Repeat("x", 8000)
		o.TokenCount = 2000
	})}, "u2")
	entries := []ledger.Entry{userMessage("u0"), assistantMessage("a0"), userMessage("u1"), assistantMessage("a1"), userMessage("u2"), huge}
	rt, h := lagSetup(lagArgs{entries: entries, summaryMaxTokens: 500})

	expectDelegated(t, runHook(rt, h, entries, "u2", "threshold"))

	expectNotification(t, h, "Observational memory: no memory record fits the ~500-token summary budget; delegating to Pi's native summarizer", "warning")
}
