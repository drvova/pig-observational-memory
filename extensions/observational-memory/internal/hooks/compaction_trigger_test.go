package hooks

import (
	"strings"
	"testing"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/testfix"
)

type triggerArgs struct {
	compactAfterTokens int
	mode               config.CompactAfterTokensMode
	ratio              float64
	passive            bool
	compactInFlight    bool
	piAutoDisabled     bool
}

func triggerRuntime(a triggerArgs) *runtime.Runtime {
	cfg := config.Defaults
	cfg.CompactAfterTokens = 3
	if a.compactAfterTokens > 0 {
		cfg.CompactAfterTokens = a.compactAfterTokens
	}
	cfg.CompactAfterTokensMode = config.ModeCalibrated
	if a.mode != "" {
		cfg.CompactAfterTokensMode = a.mode
	}
	cfg.CompactAfterTokensRatio = 0.68
	if a.ratio > 0 {
		cfg.CompactAfterTokensRatio = a.ratio
	}
	cfg.Passive = a.passive
	cfg.PiAutoCompactionEnabled = !a.piAutoDisabled
	rt := runtime.New()
	rt.SetConfig(cfg)
	rt.SetCompactInFlight(a.compactInFlight)
	return rt
}

func triggerHost(branches ...[]ledger.Entry) *testfix.FakeHost {
	h := testfix.NewFakeHost()
	h.BranchQueue = branches
	h.Idle = true
	return h
}

func usage(tokens int, window int) *host.ContextUsage {
	return &host.ContextUsage{Tokens: &tokens, ContextWindow: window}
}

var sourceTypes = map[string]bool{"message": true, "custom_message": true, "branch_summary": true}

// covered appends an observation coverage marker so the branch counts as observed.
func covered(branch []ledger.Entry, coversUpToID string) []ledger.Entry {
	target := coversUpToID
	if target == "" {
		for i := len(branch) - 1; i >= 0; i-- {
			if sourceTypes[branch[i].Type] {
				target = branch[i].ID
				break
			}
		}
	}
	if target == "" {
		panic("branch has no source entry to cover")
	}
	out := append([]ledger.Entry{}, branch...)
	return append(out, testfix.ObservationsRecordedEntry("om-cov-"+target, []ledger.Observation{testfix.Observation("aaaaaaaaaaaa")}, target))
}

func dueBranch() []ledger.Entry   { return covered([]ledger.Entry{text("raw-1", "aaaaaaaaaaaa")}, "") } // 3 tokens, observed
func belowBranch() []ledger.Entry { return covered([]ledger.Entry{text("raw-1", "aaaa")}, "") }         // 1 token, observed

func fireTrigger(t *testing.T, rt *runtime.Runtime, h *testfix.FakeHost) {
	t.Helper()
	waitDone(t, MaybeTriggerCompaction(rt, h))
}

func TestTriggerDoesNothingBelowThreshold(t *testing.T) {
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(belowBranch())
	fireTrigger(t, rt, h)
	if rt.CompactInFlight() || h.CompactCalls != 0 {
		t.Fatalf("in flight %v compact calls %d", rt.CompactInFlight(), h.CompactCalls)
	}
}

func TestTriggerCallsCompactWhenThresholdReached(t *testing.T) {
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(dueBranch())
	done := MaybeTriggerCompaction(rt, h)
	if !rt.CompactInFlight() {
		t.Fatal("compactInFlight should be set synchronously")
	}
	waitDone(t, done)
	if h.CompactCalls != 1 {
		t.Fatalf("compact calls %d", h.CompactCalls)
	}
	expectNotification(t, h, "Observational memory: compaction threshold reached (~3 observed source tokens); triggering compaction", "info")
}

func TestTriggerSkipsPassiveMode(t *testing.T) {
	rt, h := triggerRuntime(triggerArgs{passive: true}), triggerHost(dueBranch())
	fireTrigger(t, rt, h)
	if rt.CompactInFlight() || h.GetBranchCalls != 0 || h.CompactCalls != 0 {
		t.Fatalf("in flight %v branch calls %d compact calls %d", rt.CompactInFlight(), h.GetBranchCalls, h.CompactCalls)
	}
}

func TestTriggerSkipsWhenCompactionInFlight(t *testing.T) {
	rt, h := triggerRuntime(triggerArgs{compactInFlight: true}), triggerHost(dueBranch())
	fireTrigger(t, rt, h)
	if h.GetBranchCalls != 0 || h.CompactCalls != 0 {
		t.Fatalf("branch calls %d compact calls %d", h.GetBranchCalls, h.CompactCalls)
	}
}

func TestTriggerDefersWhenNoLongerIdle(t *testing.T) {
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(dueBranch())
	h.Idle = false
	fireTrigger(t, rt, h)
	if h.CompactCalls != 0 || rt.CompactInFlight() {
		t.Fatalf("compact calls %d in flight %v", h.CompactCalls, rt.CompactInFlight())
	}
	expectNotification(t, h, "Observational memory: compaction deferred — agent became busy before compaction", "info")
}

func TestTriggerRechecksThresholdAfterDeferral(t *testing.T) {
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(dueBranch(), belowBranch())
	fireTrigger(t, rt, h)
	if h.CompactCalls != 0 || rt.CompactInFlight() {
		t.Fatalf("compact calls %d in flight %v", h.CompactCalls, rt.CompactInFlight())
	}
	expectNotification(t, h, "Observational memory: compaction skipped — another compaction already ran before deferred compaction", "info")
}

func TestTriggerCountsRawTokensSinceLatestCompaction(t *testing.T) {
	branch := covered([]ledger.Entry{
		text("raw-1", "aaaaaaaaaaaa"),
		testfix.CompactionEntry("cmp-1", testfix.CompactionArgs{FirstKeptEntryID: "raw-2"}),
		text("raw-2", "aaaa"),
		text("raw-3", "bbbbbbbb"),
	}, "")
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(branch)
	fireTrigger(t, rt, h)
	if h.CompactCalls != 1 {
		t.Fatalf("compact calls %d", h.CompactCalls)
	}
}

func TestTriggerIgnoresProviderGrowthWhenRawProgressBelowThreshold(t *testing.T) {
	branch := covered([]ledger.Entry{
		testfix.CompactionEntry("cmp-1", testfix.CompactionArgs{FirstKeptEntryID: "baseline"}),
		testfix.RawMessage("baseline", "baseline", map[string]any{"message": map[string]any{"role": "assistant", "content": "baseline", "stopReason": "end_turn", "usage": map[string]any{"totalTokens": 5000}}}),
		text("raw-1", strings.Repeat("a", 302_248)), // 75,562 tokens plus the 2-token baseline message
	}, "")
	rt, h := triggerRuntime(triggerArgs{compactAfterTokens: 130000}), triggerHost(branch)
	h.Usage = usage(135636, 200000)
	fireTrigger(t, rt, h)
	if h.CompactCalls != 0 || rt.CompactInFlight() {
		t.Fatalf("compact calls %d in flight %v", h.CompactCalls, rt.CompactInFlight())
	}
}

func assistantDone(totalTokens int) ledger.Entry {
	return testfix.RawMessage("assistant-1", "done", map[string]any{"message": map[string]any{"role": "assistant", "content": "done", "stopReason": "end_turn", "usage": map[string]any{"totalTokens": totalTokens}}})
}

func TestTriggerUsesRawProgressWhenProviderGrowthLower(t *testing.T) {
	branch := covered([]ledger.Entry{
		testfix.CompactionEntry("cmp-1", testfix.CompactionArgs{FirstKeptEntryID: "raw-1"}),
		assistantDone(100),
		text("raw-1", "aaaaaaaaaaaa"),
	}, "")
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(branch)
	h.Usage = usage(101, 200000)
	fireTrigger(t, rt, h)
	if h.CompactCalls != 1 {
		t.Fatalf("compact calls %d", h.CompactCalls)
	}
}

func TestTriggerUsesRawProgressFromFirstKeptEntry(t *testing.T) {
	branch := covered([]ledger.Entry{
		text("old", "bbbbbbbbbbbb"),
		testfix.CompactionEntry("cmp-1", testfix.CompactionArgs{FirstKeptEntryID: "kept"}),
		text("kept", "aaaaaaaa"),
		text("new", "bbbbbbbbbbbb"),
	}, "")
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(branch)
	h.Usage = usage(1, 200000)
	fireTrigger(t, rt, h)
	if h.CompactCalls != 1 {
		t.Fatalf("compact calls %d", h.CompactCalls)
	}
}

func TestTriggerUsesRawProgressBeforeFirstCompaction(t *testing.T) {
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(dueBranch())
	h.Usage = usage(1, 200000)
	fireTrigger(t, rt, h)
	if h.CompactCalls != 1 {
		t.Fatalf("compact calls %d", h.CompactCalls)
	}
}

func TestTriggerUsesSameRawMetricAfterDeferredRecheck(t *testing.T) {
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(dueBranch(), dueBranch())
	h.Usage = usage(1, 200000)
	fireTrigger(t, rt, h)
	if h.CompactCalls != 1 || !rt.CompactInFlight() {
		t.Fatalf("compact calls %d in flight %v", h.CompactCalls, rt.CompactInFlight())
	}
}

func TestTriggerIgnoresHighProviderContextBeforeFirstCompaction(t *testing.T) {
	rt, h := triggerRuntime(triggerArgs{compactAfterTokens: 130000}), triggerHost(dueBranch())
	h.Usage = usage(130000, 200000)
	fireTrigger(t, rt, h)
	if h.CompactCalls != 0 {
		t.Fatalf("compact calls %d", h.CompactCalls)
	}
}

func TestTriggerCompactsWhenRawProgressEqualsThreshold(t *testing.T) {
	branch := covered([]ledger.Entry{
		testfix.CompactionEntry("cmp-1", testfix.CompactionArgs{FirstKeptEntryID: "raw-1"}),
		assistantDone(100),
		text("raw-1", "aaaaaaaaaaaa"),
	}, "")
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(branch)
	h.Usage = usage(101, 200000)
	fireTrigger(t, rt, h)
	if h.CompactCalls != 1 {
		t.Fatalf("compact calls %d", h.CompactCalls)
	}
}

func TestTriggerUsesRawProgressWhenProviderUsageUnknown(t *testing.T) {
	branch := covered([]ledger.Entry{testfix.CompactionEntry("cmp-1", testfix.CompactionArgs{}), text("raw-1", "aaaa")}, "")
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(branch)
	h.Usage = &host.ContextUsage{ContextWindow: 200000}
	fireTrigger(t, rt, h)
	if h.CompactCalls != 0 {
		t.Fatalf("compact calls %d", h.CompactCalls)
	}
}

func TestTriggerRechecksRawProgressAfterDeferral(t *testing.T) {
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(dueBranch(), belowBranch())
	h.Usage = usage(1, 200000)
	fireTrigger(t, rt, h)
	if h.CompactCalls != 0 || rt.CompactInFlight() {
		t.Fatalf("compact calls %d in flight %v", h.CompactCalls, rt.CompactInFlight())
	}
}

func TestTriggerFallsBackToRawProgressAfterModelChange(t *testing.T) {
	branch := covered([]ledger.Entry{
		testfix.CompactionEntry("cmp-1", testfix.CompactionArgs{}),
		assistantDone(60000),
		testfix.Entry(map[string]any{"type": "model_change", "id": "model-1", "timestamp": testfix.DefaultTimestamp}),
		text("raw-1", "aaaa"),
	}, "")
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(branch)
	h.Usage = usage(190000, 200000)
	fireTrigger(t, rt, h)
	if h.CompactCalls != 0 {
		t.Fatalf("compact calls %d", h.CompactCalls)
	}
}

func TestTriggerWaitsForObservationCoverage(t *testing.T) {
	// 4 raw tokens in total, but only the first entry (1 token) is observed.
	branch := covered([]ledger.Entry{text("raw-1", "aaaa"), text("raw-2", "aaaaaaaaaaaa")}, "raw-1")
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(branch)
	fireTrigger(t, rt, h)
	if h.CompactCalls != 0 || rt.CompactInFlight() {
		t.Fatalf("compact calls %d in flight %v", h.CompactCalls, rt.CompactInFlight())
	}
}

func TestTriggerDoesNotCompactWithoutCoverage(t *testing.T) {
	rt, h := triggerRuntime(triggerArgs{}), triggerHost([]ledger.Entry{text("raw-1", "aaaaaaaaaaaa")})
	fireTrigger(t, rt, h)
	if h.CompactCalls != 0 || rt.CompactInFlight() {
		t.Fatalf("compact calls %d in flight %v", h.CompactCalls, rt.CompactInFlight())
	}
}

func TestTriggerIgnoresCoverageBeforeCompactionBoundary(t *testing.T) {
	branch := []ledger.Entry{
		text("raw-0", "aaaaaaaaaaaa"),
		testfix.ObservationsRecordedEntry("om-cov-raw-0", []ledger.Observation{testfix.Observation("aaaaaaaaaaaa")}, "raw-0"),
		testfix.CompactionEntry("cmp-1", testfix.CompactionArgs{FirstKeptEntryID: "raw-1"}),
		text("raw-1", "aaaaaaaaaaaa"),
	}
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(branch)
	fireTrigger(t, rt, h)
	if h.CompactCalls != 0 {
		t.Fatalf("compact calls %d", h.CompactCalls)
	}
}

func TestTriggerCompactsOnceCoverageCatchesUp(t *testing.T) {
	branch := covered([]ledger.Entry{text("raw-1", "aaaa"), text("raw-2", "aaaaaaaaaaaa")}, "")
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(branch)
	fireTrigger(t, rt, h)
	if h.CompactCalls != 1 {
		t.Fatalf("compact calls %d", h.CompactCalls)
	}
}

func TestTriggerCountsRawTokensWhenPiAutoCompactionDisabled(t *testing.T) {
	// No observation coverage at all: with Pi's auto-compaction off nothing
	// else would compact, so the raw metric applies.
	rt, h := triggerRuntime(triggerArgs{piAutoDisabled: true}), triggerHost([]ledger.Entry{text("raw-1", "aaaaaaaaaaaa")})
	fireTrigger(t, rt, h)
	if h.CompactCalls != 1 {
		t.Fatalf("compact calls %d", h.CompactCalls)
	}
	expectNotification(t, h, "Observational memory: compaction threshold reached (~3 estimated source tokens); triggering compaction", "info")
}

func ratioTrigger() *runtime.Runtime {
	return triggerRuntime(triggerArgs{compactAfterTokens: 81000, mode: config.ModeRatio, ratio: 0.5})
}

func TestTriggerRatioModeScalesByContextWindow(t *testing.T) {
	// 3 tokens raw; ratio 0.5 of a 4-token window is 2, so 3 >= 2 fires.
	h := triggerHost(dueBranch())
	h.Model = host.Model{"contextWindow": 4}
	fireTrigger(t, ratioTrigger(), h)
	if h.CompactCalls != 1 {
		t.Fatalf("compact calls %d", h.CompactCalls)
	}
}

func TestTriggerRatioModeBelowScaledThreshold(t *testing.T) {
	h := triggerHost(belowBranch())
	h.Model = host.Model{"contextWindow": 4}
	fireTrigger(t, ratioTrigger(), h)
	if h.CompactCalls != 0 {
		t.Fatalf("compact calls %d", h.CompactCalls)
	}
}

func TestTriggerRatioModeUsesModelWindowNotUsageWindow(t *testing.T) {
	h := triggerHost(dueBranch())
	h.Model = host.Model{"contextWindow": 4}
	h.Usage = usage(2, 10)
	fireTrigger(t, ratioTrigger(), h)
	if h.CompactCalls != 1 {
		t.Fatalf("compact calls %d", h.CompactCalls)
	}
}

func TestTriggerRatioModeFallsBackWithoutModel(t *testing.T) {
	h := triggerHost(dueBranch())
	fireTrigger(t, ratioTrigger(), h)
	if h.CompactCalls != 0 {
		t.Fatalf("compact calls %d", h.CompactCalls)
	}

	h = triggerHost(dueBranch())
	h.Model = host.Model{"contextWindow": 0}
	fireTrigger(t, ratioTrigger(), h)
	if h.CompactCalls != 0 {
		t.Fatalf("compact calls %d", h.CompactCalls)
	}
}

func TestTriggerRatioModeUsesSameThresholdOnDeferredRecheck(t *testing.T) {
	rt, h := ratioTrigger(), triggerHost(dueBranch(), belowBranch())
	h.Model = host.Model{"contextWindow": 4}
	h.Idle = false
	fireTrigger(t, rt, h)
	if h.CompactCalls != 0 || rt.CompactInFlight() {
		t.Fatalf("compact calls %d in flight %v", h.CompactCalls, rt.CompactInFlight())
	}
}

// The deferred step re-polls idleness before deferring: another extension's
// settled handler may still be running when it first looks (upstream issue #41).
func TestTriggerRepollsIdleBeforeDeferring(t *testing.T) {
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(dueBranch())
	polls := 0
	h.IdleFn = func() (bool, error) {
		polls++
		return polls >= 3, nil
	}
	fireTrigger(t, rt, h)
	if h.CompactCalls != 1 || polls != 3 {
		t.Fatalf("compact calls %d polls %d", h.CompactCalls, polls)
	}
	if hasNotificationContaining(h, "deferred", "info") {
		t.Fatalf("notifications %+v", h.Notifications)
	}
}

func TestTriggerDefersAfterBoundedRepolls(t *testing.T) {
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(dueBranch())
	polls := 0
	h.IdleFn = func() (bool, error) { polls++; return false, nil }
	fireTrigger(t, rt, h)
	if h.CompactCalls != 0 || polls != IdleRecheckAttempts || rt.CompactInFlight() {
		t.Fatalf("compact calls %d polls %d in flight %v", h.CompactCalls, polls, rt.CompactInFlight())
	}
	expectNotification(t, h, "Observational memory: compaction deferred — agent became busy before compaction", "info")
}
