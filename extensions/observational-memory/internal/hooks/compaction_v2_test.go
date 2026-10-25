package hooks

import (
	"context"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/selection"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// v2Model is a session model with a known window and output limit.
func v2Model() host.Model {
	return testfix.Model("anthropic", "session", map[string]any{"contextWindow": 200_000, "maxTokens": 32_000})
}

// v2Setup is a runtime over entries with the session model registered with auth, so any
// request the extension made would reach the fake registry. Level 1 is off (consolidate:
// false), so these are level 0's tests; the level-1 tests turn it on (level1Setup).
func v2Setup(t *testing.T, entries []ledger.Entry) (*runtime.Runtime, *testfix.FakeHost) {
	t.Helper()
	cfg := config.Defaults
	cfg.ConsolidateOff = true
	rt := runtime.New()
	rt.SetConfig(cfg)
	h := testfix.NewFakeHost(entries...)
	h.Model = v2Model()
	h.ModelsFake.Add(h.Model, testfix.KeyAuth("key"))
	return rt, h
}

func v2Event(entries []ledger.Entry, firstKept string, tokensBefore int, reason string) BeforeCompactEvent {
	return BeforeCompactEvent{
		FirstKeptEntryID: firstKept,
		TokensBefore:     tokensBefore,
		BranchEntries:    entries,
		Reason:           reason,
		Preparation:      CompactionPreparation{Settings: CompactionSettings{Enabled: true, ReserveTokens: 16_384, KeepRecentTokens: 20_000}},
	}
}

func runV2Hook(rt *runtime.Runtime, h *testfix.FakeHost, event BeforeCompactEvent) BeforeCompactOutcome {
	return HandleBeforeCompact(context.Background(), rt, h, event)
}

// expectV2Compaction asserts a compaction that keeps PiG's cut, folds through it and makes no
// request, and returns it.
func expectV2Compaction(t *testing.T, outcome BeforeCompactOutcome, firstKept string, h *testfix.FakeHost) CompactionResult {
	t.Helper()
	if outcome.Cancel || outcome.Compaction == nil || outcome.ToResult() == nil {
		t.Fatalf("expected a compaction, got %+v", outcome)
	}
	c := *outcome.Compaction
	if c.FirstKeptEntryID != firstKept || c.Details.FoldThroughEntryID != firstKept || !c.Details.FullFold || c.Details.Engine != config.EngineV2 {
		t.Fatalf("cut %q fold %q full %v engine %q, want PiG's %q", c.FirstKeptEntryID, c.Details.FoldThroughEntryID, c.Details.FullFold, c.Details.Engine, firstKept)
	}
	if strings.TrimSpace(c.Summary) == "" {
		t.Fatal("empty summary")
	}
	if c.Usage != nil {
		t.Fatalf("level 0 reported usage %v", c.Usage)
	}
	if _, has := c.ToMap()["usage"]; has {
		t.Fatal("level 0 sent a usage member")
	}
	if h.ModelsFake.Streams.Calls() != 0 {
		t.Fatalf("the compaction made %d requests", h.ModelsFake.Streams.Calls())
	}
	return c
}

// The records fit: the summary is the ledger's records, PiG's cut is kept although v1's
// observer coverage is behind it (v1 moved the cut or caught up), and no request is made.
func TestV2HookKeepsPiGCutAndRendersRecords(t *testing.T) {
	counter := testfix.CountRequests(t)
	entries := laggingBranch(strings.Repeat("unobserved tool output ", 2_000))
	rt, h := v2Setup(t, entries)

	outcome := runV2Hook(rt, h, v2Event(entries, "u2", 20_000, "threshold"))

	c := expectV2Compaction(t, outcome, "u2", h)
	if !strings.Contains(c.Summary, "aaaaaaaaaaaa") || !strings.Contains(c.Summary, "bbbbbbbbbbbb") {
		t.Fatalf("records not rendered:\n%s", c.Summary)
	}
	if got := observationIDs(c.Details.Observations); len(got) < 2 || got[0] != "aaaaaaaaaaaa" || got[1] != "bbbbbbbbbbbb" {
		t.Fatalf("details %v", got)
	}
	// Level 0 appends only its extraction of the unextracted cut range, through the last entry
	// PiG removes, and makes no request.
	if counter.Total() != 0 {
		t.Fatalf("requests %v", counter.ByStage())
	}
	for _, a := range h.Appended {
		if a.CustomType != ledger.OMObservationsRecorded && a.CustomType != ledger.OMExtracted {
			t.Fatalf("appended %+v", a)
		}
	}
	if progress := ledger.ExtractionProgressIndex(h.Entries); progress != 5 {
		t.Fatalf("extraction reaches index %d, want a2 (5)", progress)
	}
	if ledger.LatestFullFoldBoundaryID(append(entries, testfix.CompactionEntry("c1", testfix.CompactionArgs{FirstKeptEntryID: "u2", Details: testfix.ToJSONValue(c.Details)}))) != "u2" {
		t.Fatal("the fold boundary is not PiG's cut")
	}
}

// An empty ledger still gets a compaction: the minimal brief of the cut range.
func TestV2HookEmptyLedgerRendersBrief(t *testing.T) {
	entries := []ledger.Entry{
		userMessage("u1", "Please rename the config loader"),
		testfix.MessageEntry("a1", testfix.AssistantToolCall("call-t1", "bash", map[string]any{"command": "grep -rn Load ."})),
		toolResultMessage("t1", strings.Repeat("match ", 500)),
		assistantMessage("a2", "Renamed."),
		userMessage("u2", "thanks"),
	}
	rt, h := v2Setup(t, entries)

	c := expectV2Compaction(t, runV2Hook(rt, h, v2Event(entries, "u2", 20_000, "threshold")), "u2", h)

	if !strings.HasPrefix(c.Summary, serialize.BriefHeading) || !strings.Contains(c.Summary, "user: Please rename the config loader (u1)") || !strings.Contains(c.Summary, `* bash "grep -rn Load ." (a1)`) || strings.Contains(c.Summary, "match match") {
		t.Fatalf("brief:\n%s", c.Summary)
	}
}

// Overflow, and any budget at or below zero, get the pointer form: the opening request line
// and an omitted pointer, smaller than the range it replaces.
func TestV2HookOverflowAndNoBudget(t *testing.T) {
	entries := laggingBranch(strings.Repeat("unobserved tool output ", 2_000))
	for _, c := range []struct {
		reason       string
		tokensBefore int
	}{
		{"overflow", 260_000},  // past the window
		{"threshold", 199_000}, // 200k - 16,384 - 199k + removed < 0
	} {
		rt, h := v2Setup(t, entries)
		compaction := expectV2Compaction(t, runV2Hook(rt, h, v2Event(entries, "u2", c.tokensBefore, c.reason)), "u2", h)
		if !strings.HasPrefix(compaction.Summary, "user: user u0 (u0)\nomitted: 6 entries") {
			t.Fatalf("%s: summary\n%s", c.reason, compaction.Summary)
		}
		if removed := v2SourceTokens(entries[:6]); tokens.EstimateStringTokens(compaction.Summary) >= removed {
			t.Fatalf("%s: summary of %d tokens for %d removed", c.reason, tokens.EstimateStringTokens(compaction.Summary), removed)
		}
	}
}

// A range smaller than the brief's pointer (overflow on a huge tool result PiG cannot cut at)
// never gets a summary larger than itself: the compaction must still free room.
func TestV2HookTinyRangeNeverGrows(t *testing.T) {
	for _, opening := range []string{"hi", "please read the config file and fix the loader bug in it"} {
		entries := []ledger.Entry{
			userMessage("u1", opening),
			testfix.MessageEntry("a1", testfix.AssistantToolCall("c1", "read", map[string]any{"path": "config.go"})),
			toolResultMessage("t1", strings.Repeat("huge ", 30_000)),
		}
		rt, h := v2Setup(t, entries)
		c := expectV2Compaction(t, runV2Hook(rt, h, v2Event(entries, "a1", 260_000, "overflow")), "a1", h)
		removed := v2SourceTokens(entries[:1])
		if got := tokens.EstimateStringTokens(c.Summary); got > removed && c.Summary != serialize.BriefSmallest {
			t.Fatalf("%q: summary of %d tokens for %d removed: %q", opening, got, removed, c.Summary)
		}
	}
}

// The brief counts and names the conversation removed: the previous compaction entry and
// the extension's record entries inside the range are not entries of it.
func TestV2HookBriefCountsSourceEntriesOnly(t *testing.T) {
	entries := []ledger.Entry{
		userMessage("u0"),
		assistantMessage("a0"),
		testfix.CompactionEntry("c0", testfix.CompactionArgs{FirstKeptEntryID: "a0"}),
		coverage("om-a0", "a0", "aaaaaaaaaaaa"),
		userMessage("u1", "Set up the release workflow"),
		assistantMessage("a1"),
		coverage("om-a1", "a1", "bbbbbbbbbbbb"),
		userMessage("u2"),
	}
	rt, h := v2Setup(t, entries)
	c := expectV2Compaction(t, runV2Hook(rt, h, v2Event(entries, "u2", 260_000, "overflow")), "u2", h)
	if !strings.Contains(c.Summary, "omitted: 3 entries") || !strings.Contains(c.Summary, "(a0 to a1)") {
		t.Fatalf("summary\n%s", c.Summary)
	}
}

// The headroom for the next run comes off the budget: a branch whose runs grew by more than
// the room left gets the pointer form where the records would otherwise fit.
func TestV2HookHeadroomShrinksBudget(t *testing.T) {
	usage := func(id string, input int) ledger.Entry {
		return testfix.MessageEntry(id, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "ok"}}, "stopReason": "stop", "usage": map[string]any{"input": input, "output": 1, "cacheRead": 0, "cacheWrite": 0}})
	}
	entries := append(laggingBranch(strings.Repeat("unobserved tool output ", 2_000)), usage("a3", 10_000), userMessage("u3"), usage("a4", 190_000))
	rt, h := v2Setup(t, entries)
	c := expectV2Compaction(t, runV2Hook(rt, h, v2Event(entries, "u2", 20_000, "threshold")), "u2", h)
	if strings.Contains(c.Summary, "aaaaaaaaaaaa") || !strings.Contains(c.Summary, "omitted:") {
		t.Fatalf("headroom ignored:\n%s", c.Summary)
	}
}

// A panic while rendering is recovered into the minimal brief: PiG never falls through to its
// summarizer.
func TestV2HookRecoversPanic(t *testing.T) {
	old := renderV2Records
	renderV2Records = func(selection.Input) selection.Result {
		panic("renderer bug")
	}
	t.Cleanup(func() { renderV2Records = old })
	entries := laggingBranch(strings.Repeat("unobserved tool output ", 2_000))
	rt, h := v2Setup(t, entries)

	c := expectV2Compaction(t, runV2Hook(rt, h, v2Event(entries, "u2", 20_000, "threshold")), "u2", h)

	if !strings.Contains(c.Summary, "omitted: 6 entries") {
		t.Fatalf("recovered summary:\n%s", c.Summary)
	}
}

// Only the request's own cancellation makes the hook answer nothing; the hook is never handed
// the agent run's signal, which a manual /compact aborts.
func TestV2HookCancellation(t *testing.T) {
	entries := laggingBranch()
	rt, h := v2Setup(t, entries)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	if outcome := HandleBeforeCompactRequest(cancelled, rt, h, v2Event(entries, "u2", 20_000, "manual")); outcome.Cancel || outcome.Compaction != nil || outcome.ToResult() != nil {
		t.Fatalf("cancelled request answered %+v", outcome)
	}
	expectV2Compaction(t, HandleBeforeCompactRequest(context.Background(), rt, h, v2Event(entries, "u2", 20_000, "manual")), "u2", h)
}

// A second hook while one runs is not cancelled under v2.
func TestV2HookNeverCancelsDuplicate(t *testing.T) {
	entries := laggingBranch()
	rt, h := v2Setup(t, entries)
	rt.SetCompactHookInFlight(true)
	expectV2Compaction(t, runV2Hook(rt, h, v2Event(entries, "u2", 20_000, "threshold")), "u2", h)
}

// PiG's cut off the handed branch: the range comes from the preparation's messages.
func TestV2HookCutFromPreparation(t *testing.T) {
	entries := []ledger.Entry{userMessage("u9", "later")}
	rt, h := v2Setup(t, entries)
	event := v2Event(entries, "gone", 20_000, "threshold")
	event.Preparation.MessagesToSummarize = []map[string]any{{"role": "user", "content": "Set up the CI pipeline " + strings.Repeat("with every check ", 100)}}
	c := expectV2Compaction(t, runV2Hook(rt, h, event), "gone", h)
	if !strings.Contains(c.Summary, "Set up the CI pipeline") {
		t.Fatalf("summary:\n%s", c.Summary)
	}
}

// Custom instructions ride along after the memory when they fit the budget.
func TestV2HookCustomInstructions(t *testing.T) {
	entries := laggingBranch(strings.Repeat("unobserved tool output ", 2_000))
	rt, h := v2Setup(t, entries)
	event := v2Event(entries, "u2", 20_000, "manual")
	event.CustomInstructions = "Keep the deploy checklist."
	c := expectV2Compaction(t, runV2Hook(rt, h, event), "u2", h)
	if !strings.HasSuffix(c.Summary, "\n\n---\n\nKeep the deploy checklist.") {
		t.Fatalf("summary:\n%s", c.Summary)
	}
}

// Between compactions the extension makes no request: the turn, run and settle handlers start
// no model call, and the trigger is off while PiG's own compaction is on; the compaction itself
// makes none at level 0.
func TestV2MakesNoRequestsBetweenCompactions(t *testing.T) {
	entries := []ledger.Entry{
		userMessage("u1", strings.Repeat("a long request ", 400)),
		assistantMessage("a1", strings.Repeat("a long answer ", 400)),
		coverage("om-u0", "u1", "aaaaaaaaaaaa"),
		userMessage("u2", strings.Repeat("more ", 400)),
		assistantMessage("a2", strings.Repeat("more answer ", 400)),
	}
	counter := testfix.CountRequests(t)
	rt, h := v2Setup(t, entries)
	OnAgentStart(rt, h)
	OnTurnEnd(rt, h)
	OnAgentSettledEvent(rt, h)
	if done := MaybeTriggerCompaction(rt, h); done != nil {
		t.Fatal("the trigger fired with PiG's auto-compaction on")
	}
	expectV2Compaction(t, runV2Hook(rt, h, v2Event(entries, "u2", 20_000, "threshold")), "u2", h)
	if counter.Total() != 0 || h.ModelsFake.Streams.Calls() != 0 || h.CompactCalls != 0 {
		t.Fatalf("requests %v calls %d compactions %d", counter.ByStage(), h.ModelsFake.Streams.Calls(), h.CompactCalls)
	}
}
