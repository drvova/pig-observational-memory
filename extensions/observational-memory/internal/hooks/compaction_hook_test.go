package hooks

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

type hookArgs struct {
	entries           []ledger.Entry
	poolMaxTokens     int
	hookInFlight      bool
	maxRetainedTokens int
	summaryMaxTokens  int
	compactAfter      int
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
	if a.compactAfter > 0 {
		cfg.CompactAfterTokens = a.compactAfter
	}
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
	if !strings.Contains(result.Summary, "\nnotes[") || strings.Contains(result.Summary, "\nreflections[") {
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
	if !strings.Contains(result.Summary, "\nreflections[1]{id,dates,content}:\n  eeeeeeeeeeee,") || !strings.Contains(result.Summary, "\nnotes[") {
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
	compactAfter      int
	model             host.Model
}

func lagSetup(a lagArgs) (*runtime.Runtime, *testfix.FakeHost) {
	return hookSetup(hookArgs{entries: a.entries, maxRetainedTokens: a.maxRetainedTokens, summaryMaxTokens: a.summaryMaxTokens, compactAfter: a.compactAfter, model: a.model})
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
	if !reflect.DeepEqual(observationIDs(result.Details.Observations), []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb"}) || !strings.Contains(result.Summary, "\n  bbbbbbbbbbbb,") {
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

func TestLagCapsSummaryAtRetainedBudgetRemainder(t *testing.T) {
	// The retained source alone fits a 100-token budget, so the summary gets
	// what it leaves, which holds no record: the hook delegates.
	entries := laggingBranch()
	retained := ResolveCompactionCut(entries, "u2", CutOptions{MaxRetainedTokens: 100}).RetainedTokens
	rt, h := lagSetup(lagArgs{entries: entries, maxRetainedTokens: 100})

	expectDelegated(t, runHook(rt, h, entries, "u2", "threshold"))

	expectNotification(t, h, fmt.Sprintf("Observational memory: no memory record fits the ~%d-token summary budget; delegating to Pi's native summarizer", 100-retained), "warning")
}

func TestLagDelegatesWhenTailPlusSummaryAndInstructionsExceedBudget(t *testing.T) {
	// The memory fits what the retained tail leaves, but instructions ride
	// along in full and push retained source plus summary over the budget.
	entries := laggingBranch()
	rt, h := lagSetup(lagArgs{entries: entries, maxRetainedTokens: 700})
	instructions := strings.TrimSpace(strings.Repeat("Keep the deploy checklist and the open questions. ", 40))

	expectDelegated(t, HandleBeforeCompact(context.Background(), rt, h, BeforeCompactEvent{FirstKeptEntryID: "u2", TokensBefore: 123, BranchEntries: entries, Reason: "threshold", CustomInstructions: instructions}))

	if len(h.Notifications) == 0 || h.Notifications[0].Level != "warning" || !strings.Contains(h.Notifications[0].Message, "plus a ~") || !strings.Contains(h.Notifications[0].Message, "memory summary exceeds the ~700-token budget") {
		t.Fatalf("notifications %+v", h.Notifications)
	}
}

func TestLagDerivesRetainedBudgetFromSessionModelWindow(t *testing.T) {
	// Half of a 4-token window is 2; the retained tail is larger than that.
	entries := laggingBranch()
	rt, h := lagSetup(lagArgs{entries: entries, model: host.Model{"contextWindow": 4}})
	expectDelegated(t, runHook(rt, h, entries, "u2", "threshold"))

	rt, h = lagSetup(lagArgs{entries: entries, compactAfter: 1_000, model: host.Model{"contextWindow": 4_000}})
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

// memoryFormat picks the summary layout: tables by default, the earlier lines
// when configured.
func TestHookRendersConfiguredMemoryFormat(t *testing.T) {
	obs1 := testfix.Observation("aaaaaaaaaaaa", func(o *ledger.Observation) { o.SourceEntryIDs = []string{"raw-1"}; o.TokenCount = 10 })
	entries := []ledger.Entry{
		text("raw-1", "aaaa"),
		testfix.ObservationsRecordedEntry("om-aaaaaaaaaaaa", []ledger.Observation{obs1}, "raw-1"),
	}
	rt, h := hookSetup(hookArgs{entries: entries, poolMaxTokens: 100})
	if summary := compactionOf(t, runHook(rt, h, entries, "raw-1", "")).Summary; !strings.HasPrefix(summary, ledger.TablesUsageInstructions) || !strings.Contains(summary, "\nnotes[1]{id,when,relevance,content}:\n  aaaaaaaaaaaa,") {
		t.Fatalf("default summary %q", summary)
	}
	cfg := rt.Config()
	cfg.MemoryFormat = config.MemoryFormatLines
	rt.SetConfig(cfg)
	if summary := compactionOf(t, runHook(rt, h, entries, "raw-1", "")).Summary; !strings.HasPrefix(summary, ledger.ContextUsageInstructions) || !strings.Contains(summary, "## Observations\n[aaaaaaaaaaaa] ") {
		t.Fatalf("lines summary %q", summary)
	}
}

// customInstructions attached to the compaction ride along after the memory
// (upstream issue #55).
func TestHookAppendsCustomInstructions(t *testing.T) {
	obs1 := testfix.Observation("aaaaaaaaaaaa", func(o *ledger.Observation) { o.SourceEntryIDs = []string{"raw-1"}; o.TokenCount = 10 })
	entries := []ledger.Entry{
		text("raw-1", "aaaa"),
		testfix.ObservationsRecordedEntry("om-aaaaaaaaaaaa", []ledger.Observation{obs1}, "raw-1"),
	}
	rt, h := hookSetup(hookArgs{entries: entries, poolMaxTokens: 100})
	event := BeforeCompactEvent{FirstKeptEntryID: "raw-1", TokensBefore: 123, BranchEntries: entries, CustomInstructions: "  Keep the deploy checklist.  "}

	result := compactionOf(t, HandleBeforeCompact(context.Background(), rt, h, event))

	if !strings.HasSuffix(result.Summary, "\n\n---\n\nKeep the deploy checklist.") || !strings.Contains(result.Summary, "\nnotes[") {
		t.Fatalf("summary %q", result.Summary)
	}

	plain := compactionOf(t, runHook(rt, h, entries, "raw-1", ""))
	if strings.Contains(plain.Summary, "---") {
		t.Fatalf("summary without instructions %q", plain.Summary)
	}
}

func TestHookDecodesCustomInstructionsFromWire(t *testing.T) {
	event := BeforeCompactEvent{}
	if event.CustomInstructions != "" {
		t.Fatal("zero value")
	}
}

// Markers past Pi's cut that cover only source Pi keeps wait for the next
// compaction: with no marker straddling the cut, the fold goes through it.
func TestHookFoldsThroughPiCutWhenNoMarkerStraddlesIt(t *testing.T) {
	entries := append(laggingBranch(), coverage("om-a2", "a2", "cccccccccccc"))
	rt, h := lagSetup(lagArgs{entries: entries})

	// Pi cuts at u1 (index 2); the markers cover a0 (1), a1 (3) and a2 (5),
	// so om-a0 covers all the removed source and the others only kept source.
	result := compactionOf(t, runHook(rt, h, entries, "u1", "threshold"))

	if result.FirstKeptEntryID != "u1" || result.Details.FoldThroughEntryID != "u1" {
		t.Fatalf("result %+v", result)
	}
	if !reflect.DeepEqual(observationIDs(result.Details.Observations), []string{"aaaaaaaaaaaa"}) {
		t.Fatalf("observations %v", observationIDs(result.Details.Observations))
	}
	resolution := ResolveCompactionCut(entries, "u1", CutOptions{MaxRetainedTokens: 1000})
	if resolution.Kind != "cut" || resolution.Cut != (CompactionCut{FirstKeptEntryID: "u1", FoldThroughEntryID: "u1"}) || resolution.Gap != nil {
		t.Fatalf("resolution %+v", resolution)
	}

	// An entry the observer could never read does not make om-a1 straddle.
	empty := testfix.RawMessage("empty", "", map[string]any{"message": map[string]any{"role": "assistant", "content": []any{}, "stopReason": "aborted"}})
	withEmpty := append(append(append([]ledger.Entry{}, entries[:2]...), empty), entries[2:]...)
	if resolution := ResolveCompactionCut(withEmpty, "u1", CutOptions{MaxRetainedTokens: 1000}); resolution.Cut.FoldThroughEntryID != "u1" {
		t.Fatalf("resolution %+v", resolution)
	}
}

// A marker straddling Pi's cut read source this compaction removes, so the
// fold goes through it; a later marker covering only kept source stays out.
func TestHookFoldsThroughMarkerStraddlingPiCut(t *testing.T) {
	entries := []ledger.Entry{
		userMessage("u0"), assistantMessage("a0"), userMessage("u1"), assistantMessage("a1"), toolResultMessage("t1"),
		assistantMessage("a2"), userMessage("u2"), assistantMessage("a3"),
		coverage("om-a0", "a0", "aaaaaaaaaaaa"),
		coverage("om-a2", "a2", "bbbbbbbbbbbb"),
		coverage("om-a3", "a3", "cccccccccccc"),
	}
	rt, h := lagSetup(lagArgs{entries: entries})

	// Pi cuts at a1 (index 3). om-a2 covers u1 (2), which is removed, through
	// a2 (5); om-a3 covers only u2 and a3.
	result := compactionOf(t, runHook(rt, h, entries, "a1", "threshold"))

	if result.FirstKeptEntryID != "a1" || result.Details.FoldThroughEntryID != "a2" {
		t.Fatalf("result %+v", result)
	}
	if !reflect.DeepEqual(observationIDs(result.Details.Observations), []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb"}) {
		t.Fatalf("observations %v", observationIDs(result.Details.Observations))
	}

	// With no marker before the cut, the first marker past it straddles.
	firstMarkerPastCut := append(laggingBranch()[:7], coverage("om-a1", "a1", "bbbbbbbbbbbb"), coverage("om-a2", "a2", "cccccccccccc"))
	resolution := ResolveCompactionCut(firstMarkerPastCut, "u1", CutOptions{MaxRetainedTokens: 1000})
	if resolution.Cut != (CompactionCut{FirstKeptEntryID: "u1", FoldThroughEntryID: "a1"}) || resolution.Gap != nil {
		t.Fatalf("resolution %+v", resolution)
	}
}

// Under a tight summary budget, observations of kept source must not push out
// observations of the source this compaction removes.
func TestHookKeepsRemovedSourceObservationsUnderTightBudget(t *testing.T) {
	observationsOf := func(first int, sourceID string) []ledger.Observation {
		observations := []ledger.Observation{}
		for i := first; i < first+20; i++ {
			observations = append(observations, testfix.Observation(fmt.Sprintf("%012x", i+1), func(o *ledger.Observation) {
				o.SourceEntryIDs = []string{sourceID}
				o.Content = fmt.Sprintf("Observation %d: %s", i, strings.Repeat("detail ", 14))
			}))
		}
		return observations
	}
	removed, kept := observationsOf(0, "a0"), observationsOf(20, "a2")
	entries := []ledger.Entry{
		userMessage("u0"), assistantMessage("a0"),
		testfix.ObservationsRecordedEntry("om-removed", removed, "a0"),
		userMessage("u1"), assistantMessage("a1"), userMessage("u2"), assistantMessage("a2"),
		testfix.ObservationsRecordedEntry("om-kept", kept, "a2"),
	}
	const budget = 1200
	if both := ledger.RenderSummaryWithBudget(nil, append(append([]ledger.Observation{}, removed...), kept...), budget); both.OmittedObservations == 0 {
		t.Fatal("test needs both observation sets to overflow the summary budget together")
	}
	rt, h := lagSetup(lagArgs{entries: entries, summaryMaxTokens: budget})

	// Pi cuts at u1, so u0 and a0 are removed and om-kept covers only kept source.
	result := compactionOf(t, runHook(rt, h, entries, "u1", "threshold"))

	if result.Details.FoldThroughEntryID != "u1" || !reflect.DeepEqual(observationIDs(result.Details.Observations), observationIDs(removed)) {
		t.Fatalf("details %+v", result.Details)
	}
	for _, observation := range removed {
		if !strings.Contains(result.Summary, "\n  "+observation.ID+",") {
			t.Fatalf("summary omits removed-source observation %s: %q", observation.ID, result.Summary)
		}
	}
	if strings.Contains(result.Summary, "\nomitted[") {
		t.Fatalf("summary %q", result.Summary)
	}
}

// The fold boundary never moves back past the latest full fold's, even when
// Pi's cut comes before it: later compactions apply drops and reflections
// through that boundary.
func TestHookNeverFoldsBehindLatestFullFold(t *testing.T) {
	entries := []ledger.Entry{
		userMessage("u0"), assistantMessage("a0"), userMessage("u1"), assistantMessage("a1"), toolResultMessage("t1"),
		assistantMessage("a2"), userMessage("u2"), assistantMessage("a3"),
		coverage("om-a0", "a0", "aaaaaaaaaaaa"),
		coverage("om-a2", "a2", "bbbbbbbbbbbb"),
		coverage("om-a3", "a3", "cccccccccccc"),
		testfix.CompactionEntry("cmp-full", testfix.CompactionArgs{FirstKeptEntryID: "u1", Details: testfix.MemoryDetails(testfix.MemoryDetailsArgs{FullFold: true, FoldThroughEntryID: "a3"})}),
		userMessage("u4"), assistantMessage("a4"),
	}
	rt, h := lagSetup(lagArgs{entries: entries})

	// Pi cuts at u2 (index 6), which no marker straddles, but cmp-full folded
	// through a3 (7).
	result := compactionOf(t, runHook(rt, h, entries, "u2", "threshold"))

	if result.FirstKeptEntryID != "u2" || result.Details.FoldThroughEntryID != "a3" {
		t.Fatalf("result %+v", result)
	}
	if !reflect.DeepEqual(observationIDs(result.Details.Observations), []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc"}) {
		t.Fatalf("observations %v", observationIDs(result.Details.Observations))
	}
}

// An unobserved entry the observer could never read does not hold the cut.
func TestLagIgnoresContentlessEntryBeforeCut(t *testing.T) {
	entries := laggingBranch()
	empty := testfix.RawMessage("empty", "", map[string]any{"message": map[string]any{"role": "assistant", "content": []any{}, "stopReason": "aborted"}})
	entries = append(entries[:6], append([]ledger.Entry{empty}, entries[6:]...)...) // before u2
	entries = append(entries, coverage("om-a2", "a2", "cccccccccccc"))
	rt, h := lagSetup(lagArgs{entries: entries})

	result := compactionOf(t, runHook(rt, h, entries, "u2", "threshold"))

	if result.FirstKeptEntryID != "u2" || len(h.Notifications) != 0 {
		t.Fatalf("result %+v notifications %+v", result, h.Notifications)
	}
}

// A record that fits the summary budget but not the memory's share of it names
// the share, and that the compaction instructions took the rest.
func TestHookNamesMemoryShareWhenInstructionsCrowdOutEveryRecord(t *testing.T) {
	big := testfix.ObservationsRecordedEntry("om-big", []ledger.Observation{testfix.Observation("dddddddddddd", func(o *ledger.Observation) {
		o.SourceEntryIDs = []string{"a0"}
		o.Content = strings.Repeat("x", 3200)
	})}, "u2")
	entries := []ledger.Entry{userMessage("u0"), assistantMessage("a0"), userMessage("u1"), assistantMessage("a1"), userMessage("u2"), big}
	const budget = 2000
	// ~1,500 tokens of instructions leave the memory its half-budget floor.
	instructions := strings.TrimSpace(strings.Repeat("Keep the deploy checklist and the open questions. ", 120))

	rt, h := lagSetup(lagArgs{entries: entries, summaryMaxTokens: budget})
	compactionOf(t, runHook(rt, h, entries, "u2", "threshold"))
	rt, h = lagSetup(lagArgs{entries: entries, summaryMaxTokens: budget})
	expectDelegated(t, HandleBeforeCompact(context.Background(), rt, h, BeforeCompactEvent{FirstKeptEntryID: "u2", TokensBefore: 1, BranchEntries: entries, Reason: "threshold", CustomInstructions: instructions}))

	expectNotification(t, h, "Observational memory: no memory record fits its ~1,000-token share of the ~2,000-token summary budget (the compaction instructions take the rest); delegating to Pi's native summarizer", "warning")
}

func instructionBudgetBranch() []ledger.Entry {
	observations := []ledger.Observation{}
	for i := 0; i < 30; i++ {
		observations = append(observations, testfix.Observation(fmt.Sprintf("%012x", i+1), func(o *ledger.Observation) {
			o.Content = fmt.Sprintf("Observation %d: %s", i, strings.Repeat("detail ", 10))
		}))
	}
	return []ledger.Entry{text("raw-1", "aaaa"), testfix.ObservationsRecordedEntry("om-many", observations, "raw-1")}
}

// Instructions longer than half the summary budget do not crowd the memory
// out: its render keeps the half-budget floor.
func TestHookKeepsHalfTheBudgetForMemoryUnderLongInstructions(t *testing.T) {
	entries := instructionBudgetBranch()
	const budget = 1200
	instructions := strings.TrimSpace(strings.Repeat("Keep the deploy checklist and the open questions. ", 64))
	instructionTokens := tokens.EstimateStringTokens("\n\n---\n\n" + instructions)
	if instructionTokens <= budget/2 {
		t.Fatalf("test needs instructions over half the budget; got %d tokens", instructionTokens)
	}
	rt, h := hookSetup(hookArgs{entries: entries, poolMaxTokens: 100_000, summaryMaxTokens: budget})

	result := compactionOf(t, HandleBeforeCompact(context.Background(), rt, h, BeforeCompactEvent{FirstKeptEntryID: "raw-1", TokensBefore: 1, BranchEntries: entries, CustomInstructions: instructions}))

	memoryPart := strings.Split(result.Summary, "\n\n---\n\n")[0]
	if !strings.HasSuffix(result.Summary, instructions) || !strings.Contains(memoryPart, "Observation 29: ") {
		t.Fatalf("summary %q", result.Summary)
	}
	// The floor, not the budget minus the instructions, sized the memory.
	if got := tokens.EstimateStringTokens(memoryPart); got > budget/2 || got <= budget-instructionTokens {
		t.Fatalf("memory part %d tokens, want more than %d and at most %d", got, budget-instructionTokens, budget/2)
	}
}

// Instructions share the summary budget: the memory render gets what they leave.
func TestHookReservesBudgetForCustomInstructions(t *testing.T) {
	entries := instructionBudgetBranch()
	const budget = 600
	instructions := strings.TrimSpace(strings.Repeat("Keep the deploy checklist and the open questions. ", 16))

	rt, h := hookSetup(hookArgs{entries: entries, poolMaxTokens: 100_000, summaryMaxTokens: budget})
	plain := compactionOf(t, runHook(rt, h, entries, "raw-1", "threshold"))
	rt, h = hookSetup(hookArgs{entries: entries, poolMaxTokens: 100_000, summaryMaxTokens: budget})
	withInstructions := compactionOf(t, HandleBeforeCompact(context.Background(), rt, h, BeforeCompactEvent{FirstKeptEntryID: "raw-1", TokensBefore: 1, BranchEntries: entries, CustomInstructions: instructions}))

	memoryPart := strings.Split(withInstructions.Summary, "\n\n---\n\n")[0]
	instructionTokens := tokens.EstimateStringTokens("\n\n---\n\n" + instructions)
	if !strings.HasSuffix(withInstructions.Summary, instructions) {
		t.Fatalf("instructions missing from %q", withInstructions.Summary)
	}
	if got := tokens.EstimateStringTokens(memoryPart); got > budget-instructionTokens {
		t.Fatalf("memory part %d tokens exceeds its %d-token share", got, budget-instructionTokens)
	}
	if got := tokens.EstimateStringTokens(plain.Summary); got <= budget-instructionTokens {
		t.Fatalf("test needs a memory render larger than the reserved share; got %d tokens", got)
	}
	if got := tokens.EstimateStringTokens(withInstructions.Summary); got > budget+2 {
		t.Fatalf("summary %d tokens exceeds the %d-token budget", got, budget)
	}
}

// roomBranch is instructionBudgetBranch with tailTokens of source after its
// first kept entry, raw-1.
func roomBranch(tailTokens int) []ledger.Entry {
	entries := instructionBudgetBranch()
	if tailTokens > 0 {
		entries = append(entries, text("raw-2", strings.Repeat("abcd", tailTokens)))
	}
	return entries
}

func TestSummaryBudgetIsTheRoomTheNextContextHas(t *testing.T) {
	cfg := config.Defaults
	cfg.CompactAfterTokens = 1_000
	entries := roomBranch(2_000)
	cut := cutAt("raw-1", "raw-1")
	retained := ledger.RawTokensAfterIndex(entries, -1)
	branch := branchContextTokens(entries)
	if retained < 2_000 || branch != retained {
		t.Fatalf("retained %d branch %d", retained, branch)
	}
	// Unknown window: the fallback.
	if got := summaryBudget(cfg, 0, 0, entries, cut, 100_000); got != config.CompactionSummaryFallbackMaxTokens {
		t.Fatalf("unknown window budget %d", got)
	}
	// Overhead is tokensBefore beyond the branch's own estimate.
	// The threshold counts the retained tail, so the larger of the two is subtracted.
	if got := summaryBudget(cfg, 10_000, branch+3_000, entries, cut, 100_000); got != 10_000-3_000-retained {
		t.Fatalf("budget with overhead %d", got)
	}
	// A tokensBefore below the branch estimate measures no overhead.
	if got := summaryBudget(cfg, 10_000, 1, entries, cut, 100_000); got != 10_000-retained {
		t.Fatalf("budget without overhead %d", got)
	}
	// A cut Pi proposes on an unknown entry retains nothing measurable.
	if got := summaryBudget(cfg, 10_000, 0, entries, cutAt("missing", "missing"), 100_000); got != 9_000 {
		t.Fatalf("unknown cut budget %d", got)
	}
	// A moved cut keeps retained source plus summary within the retained budget.
	moved := cut
	moved.Gap = &ledger.UnobservedSourceSpan{}
	moved.RetainedTokens = retained
	if got := summaryBudget(cfg, 100_000, 0, entries, moved, retained+500); got != 500 {
		t.Fatalf("moved-cut budget %d", got)
	}
	if got := summaryBudget(cfg, 100_000, 0, entries, moved, retained); got != 1 {
		t.Fatalf("exhausted moved-cut budget %d", got)
	}
}

func TestHookRendersEveryRecordWhenRoomAllows(t *testing.T) {
	for _, window := range []host.Model{nil, {"contextWindow": 6_000}} {
		entries := roomBranch(0)
		rt, h := hookSetup(hookArgs{entries: entries, poolMaxTokens: 100_000, compactAfter: 1_000, model: window})

		result := compactionOf(t, runHook(rt, h, entries, "raw-1", "threshold"))

		if !strings.Contains(result.Summary, "Observation 0: ") || !strings.Contains(result.Summary, "Observation 29: ") || strings.Contains(result.Summary, "\nomitted[") {
			t.Fatalf("window %v summary %q", window, result.Summary)
		}
	}
}

func TestHookShrinksSummaryForALargeRetainedTail(t *testing.T) {
	// 6,000 - a ~5,000-token tail (above the 1,000 threshold, which counts
	// it) leaves room for only part of the memory.
	entries := roomBranch(5_000)
	rt, h := hookSetup(hookArgs{entries: entries, poolMaxTokens: 100_000, compactAfter: 1_000, model: host.Model{"contextWindow": 6_000}})

	result := compactionOf(t, runHook(rt, h, entries, "raw-1", "threshold"))

	if !strings.Contains(result.Summary, "Observation 29: ") || strings.Contains(result.Summary, "Observation 0: ") || !strings.Contains(result.Summary, "\nomitted[") {
		t.Fatalf("summary %q", result.Summary)
	}
	if got, room := tokens.EstimateStringTokens(result.Summary), 6_000-ledger.RawTokensAfterIndex(entries, -1); got > room {
		t.Fatalf("summary %d tokens exceeds the %d-token room", got, room)
	}

	// No room at all: nothing renders and the hook delegates.
	entries = roomBranch(6_000)
	rt, h = hookSetup(hookArgs{entries: entries, poolMaxTokens: 100_000, compactAfter: 1_000, model: host.Model{"contextWindow": 6_000}})
	expectDelegated(t, runHook(rt, h, entries, "raw-1", "threshold"))
	expectNotification(t, h, "Observational memory: no memory record fits the ~1-token summary budget; delegating to Pi's native summarizer", "warning")
}

// A 64k model with the default 81k threshold: the proactive trigger cannot
// fire before Pi's own threshold, so the room is negative, but the floor still
// gives the summary its fallback budget and the extension owns the compaction.
func TestHookOwnsCompactionOnASmallWindowWithTheDefaultThreshold(t *testing.T) {
	entries := roomBranch(20_000)
	rt, h := hookSetup(hookArgs{entries: entries, poolMaxTokens: 100_000, model: host.Model{"contextWindow": 64_000}})
	if threshold := config.ResolveCompactAfterTokens(rt.Config(), 64_000); threshold <= 64_000 {
		t.Fatalf("test needs a threshold above the window; got %d", threshold)
	}

	result := compactionOf(t, runHook(rt, h, entries, "raw-1", "threshold"))

	if !strings.Contains(result.Summary, "Observation 0: ") || !strings.Contains(result.Summary, "Observation 29: ") {
		t.Fatalf("summary %q", result.Summary)
	}
	if len(h.Notifications) != 0 {
		t.Fatalf("notifications %+v", h.Notifications)
	}
}

func TestHookCapsRoomAtCompactionSummaryMaxTokens(t *testing.T) {
	entries := roomBranch(0)
	rt, h := hookSetup(hookArgs{entries: entries, poolMaxTokens: 100_000, compactAfter: 1_000, summaryMaxTokens: 400, model: host.Model{"contextWindow": 100_000}})

	result := compactionOf(t, runHook(rt, h, entries, "raw-1", "threshold"))

	if got := tokens.EstimateStringTokens(result.Summary); got > 400 || !strings.Contains(result.Summary, "\nomitted[") {
		t.Fatalf("summary %d tokens: %q", got, result.Summary)
	}
}
