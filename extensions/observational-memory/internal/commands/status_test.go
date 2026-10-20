package commands_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/commands"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/testfix"
)

func statusConfig() config.Config {
	cfg := config.Defaults
	cfg.ObserveAfterTokens = 10
	cfg.ReflectAfterTokens = 20
	cfg.CompactAfterTokens = 30
	cfg.CompactAfterTokensMode = config.ModeCalibrated
	cfg.ObservationsPoolMaxTokens = 40
	cfg.ObservationsPoolTargetTokens = 20
	cfg.Passive = false
	return cfg
}

func statusRuntime(cfg config.Config) *runtime.Runtime {
	rt := runtime.New()
	rt.SetConfig(cfg)
	return rt
}

func statusOutput(t *testing.T, rt *runtime.Runtime, h *testfix.FakeHost) string {
	lines, err := commands.StatusLines(rt, h)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Join(lines, "\n")
}

func expectContains(t *testing.T, output string, fragments ...string) {
	for _, fragment := range fragments {
		if !strings.Contains(output, fragment) {
			t.Fatalf("output lacks %q:\n%s", fragment, output)
		}
	}
}

func expectLacks(t *testing.T, output string, fragments ...string) {
	for _, fragment := range fragments {
		if strings.Contains(output, fragment) {
			t.Fatalf("output has %q:\n%s", fragment, output)
		}
	}
}

func withTokenCount(n int) func(*ledger.Observation) {
	return func(o *ledger.Observation) { o.TokenCount = n }
}

func TestStatusRendersConciseNoMemoryStatus(t *testing.T) {
	output := statusOutput(t, statusRuntime(statusConfig()), testfix.NewFakeHost())

	expectContains(t, output, "── Memory ──", "Observations: 0 recorded / 0 dropped / 0 active / 0 visible", "Reflections:  0 recorded / 0 visible", "Next observation:", "Next compaction:")
	expectLacks(t, output, "Visible:", "Drift:", "committed", "pending")
}

func TestStatusReportsLedgerCountsAndDriftIgnoringV2(t *testing.T) {
	obsA := testfix.Observation("aaaaaaaaaaaa", withTokenCount(5))
	obsB := testfix.Observation("bbbbbbbbbbbb", withTokenCount(7))
	ref := testfix.Reflection("eeeeeeeeeeee", []string{"bbbbbbbbbbbb"}, func(r *ledger.Reflection) { r.TokenCount = 3 })
	entries := []ledger.Entry{
		testfix.TextCustomMessage("raw-1", "aaaa"),
		testfix.OldV2ObservationEntry("v2-obs"),
		testfix.CompactionEntry("cmp-v2", testfix.CompactionArgs{FirstKeptEntryID: "raw-1", Details: testfix.OldV2CompactionDetails()}),
		testfix.CompactionEntry("cmp-visible", testfix.CompactionArgs{FirstKeptEntryID: "raw-1", Details: testfix.MemoryDetails(testfix.MemoryDetailsArgs{Observations: []ledger.Observation{obsA}})}),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA, obsB}, "raw-1"),
		testfix.ReflectionsRecordedEntry("om-ref", []ledger.Reflection{ref}, "om-obs"),
		testfix.ObservationsDroppedEntry("om-drop", []string{"aaaaaaaaaaaa"}, "om-ref"),
	}

	output := statusOutput(t, statusRuntime(statusConfig()), testfix.NewFakeHost(entries...))

	expectContains(t, output,
		"Observations: 2 recorded / 1 dropped / 1 active / 1 visible +1 -1",
		"Reflections:  1 recorded / 0 visible +1",
		"Visible observation pool: ~5 / 40 tokens (13%)",
		// The active pool counts the full rendered line (id + timestamp + relevance + content).
		"Active observation pool: ~19 / 20 target tokens (95%)",
	)
	expectLacks(t, output, "Visible:", "Drift:", "full truth", "v2-obs", "observational-memory")
}

func TestStatusShowsSeparateClocksAndPools(t *testing.T) {
	obs := testfix.Observation("aaaaaaaaaaaa", withTokenCount(5))
	ref := testfix.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"}, func(r *ledger.Reflection) { r.TokenCount = 3 })
	entries := []ledger.Entry{
		testfix.TextCustomMessage("raw-1", "aaaaaaaa"),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obs}, "raw-1"),
		testfix.ReflectionsRecordedEntry("om-ref", []ledger.Reflection{ref}, "raw-1"),
		testfix.TextCustomMessage("raw-2", "bbbbbbbb"),
		testfix.CompactionEntry("cmp", testfix.CompactionArgs{FirstKeptEntryID: "raw-2", Details: testfix.MemoryDetails(testfix.MemoryDetailsArgs{Observations: []ledger.Observation{obs}, Reflections: []ledger.Reflection{ref}})}),
	}

	output := statusOutput(t, statusRuntime(statusConfig()), testfix.NewFakeHost(entries...))

	expectContains(t, output,
		"Next observation:", "/ 10 tokens",
		"Next reflection:", "/ 20 observed tokens",
		"Next compaction:", "/ 30 observed source tokens",
		"Visible observation pool: ~5 / 40 tokens (13%)",
		"Active observation pool: ~19 / 20 target tokens (95%)",
		"Reflection pool:         ~3 tokens",
	)
	expectLacks(t, output, "Observation pool:", "Full fold pool:", "visible observation tokens")
}

func TestStatusShowsRawSourceProgressIgnoringProviderContext(t *testing.T) {
	entries := []ledger.Entry{
		testfix.CompactionEntry("cmp-1", testfix.CompactionArgs{FirstKeptEntryID: "raw-1"}),
		testfix.RawMessage("assistant-1", "done", map[string]any{"message": map[string]any{"role": "assistant", "content": "done", "stopReason": "end_turn", "usage": map[string]any{"totalTokens": 60072}}}),
		testfix.TextCustomMessage("raw-1", "aaaaaaaaaaaa"),
		testfix.ObservationsRecordedEntry("om-cov", []ledger.Observation{testfix.Observation("aaaaaaaaaaaa")}, "raw-1"),
	}
	h := testfix.NewFakeHost(entries...)
	tokens := 135636
	h.Usage = &host.ContextUsage{Tokens: &tokens, ContextWindow: 200000}

	output := statusOutput(t, statusRuntime(statusConfig()), h)

	expectContains(t, output, "Next compaction:  ~3 / 30 observed source tokens", "Observer backlog: ~0 unobserved source tokens since the compaction boundary")
}

func TestStatusCountsOnlyObservedTokensTowardCompaction(t *testing.T) {
	entries := []ledger.Entry{
		testfix.CompactionEntry("cmp-1", testfix.CompactionArgs{FirstKeptEntryID: "raw-1"}),
		testfix.TextCustomMessage("raw-1", "aaaaaaaaaaaa"),
		testfix.ObservationsRecordedEntry("om-cov", []ledger.Observation{testfix.Observation("aaaaaaaaaaaa")}, "raw-1"),
		testfix.TextCustomMessage("raw-2", "bbbbbbbbbbbbbbbb"),
	}

	output := statusOutput(t, statusRuntime(statusConfig()), testfix.NewFakeHost(entries...))

	expectContains(t, output, "Next compaction:  ~3 / 30 observed source tokens (10%)", "Observer backlog: ~4 unobserved source tokens since the compaction boundary")
}

func TestStatusShowsOverTargetActivePool(t *testing.T) {
	// Pad content so the rendered line is exactly 25 tokens (100 chars).
	obs := testfix.Observation("aaaaaaaaaaaa", func(o *ledger.Observation) { o.Content = strings.Repeat("x", 51) })
	entries := []ledger.Entry{
		testfix.TextCustomMessage("raw-1", "aaaaaaaa"),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obs}, "raw-1"),
	}

	output := statusOutput(t, statusRuntime(statusConfig()), testfix.NewFakeHost(entries...))

	expectContains(t, output, "Active observation pool: ~25 / 20 target tokens (125%)")
}

func TestStatusShowsPassiveInFlightAndLastErrors(t *testing.T) {
	cfg := statusConfig()
	cfg.Passive = true
	rt := statusRuntime(cfg)
	release := make(chan struct{})
	started := make(chan struct{})
	done := rt.LaunchConsolidationTask(nil, func(ctx context.Context) error {
		rt.SetConsolidationPhase(runtime.PhaseReflector)
		close(started)
		<-release
		return nil
	})
	<-started
	defer func() {
		close(release)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("consolidation did not finish")
		}
	}()
	rt.SetCompactInFlight(true)
	rt.SetCompactHookInFlight(true)
	rt.SetLastError(runtime.PhaseObserver, "observer failed")
	rt.SetLastError(runtime.PhaseReflector, "reflect failed")
	rt.SetLastError(runtime.PhaseDropper, "drop failed")

	output := statusOutput(t, rt, testfix.NewFakeHost())

	expectContains(t, output,
		"Passive: automatic memory workers and auto-compaction disabled",
		"Consolidation: running (reflector)",
		"Auto-compaction: running",
		"Compaction hook: running",
		"Observer: observer failed",
		"Reflector: reflect failed",
		"Dropper: drop failed",
	)
	expectLacks(t, output, "Observer: running", "Reflect/drop: running")
}

func TestStatusShowsConsolidationWithoutPhase(t *testing.T) {
	rt := statusRuntime(statusConfig())
	release := make(chan struct{})
	done := rt.LaunchConsolidationTask(nil, func(ctx context.Context) error { <-release; return nil })
	defer func() {
		close(release)
		<-done
	}()

	output := statusOutput(t, rt, testfix.NewFakeHost())

	expectContains(t, output, "Consolidation: running")
	expectLacks(t, output, "Consolidation: running (")
}

func ratioConfig() config.Config {
	cfg := statusConfig()
	cfg.CompactAfterTokensMode = config.ModeRatio
	cfg.CompactAfterTokensRatio = 0.5
	return cfg
}

func TestStatusRatioModeScalesThreshold(t *testing.T) {
	h := testfix.NewFakeHost()
	h.Model = host.Model{"contextWindow": 1_000_000}
	h.Usage = &host.ContextUsage{ContextWindow: 1_000_000}

	expectContains(t, statusOutput(t, statusRuntime(ratioConfig()), h), "Next compaction:  ~0 / 500,000 observed source tokens (0%)")
}

func TestStatusRatioModeUsesModelContextWindow(t *testing.T) {
	h := testfix.NewFakeHost()
	h.Model = host.Model{"contextWindow": 100_000}
	h.Usage = &host.ContextUsage{ContextWindow: 200_000}

	expectContains(t, statusOutput(t, statusRuntime(ratioConfig()), h), "Next compaction:  ~0 / 50,000 observed source tokens (0%)")
}

func TestStatusRatioModeFallsBackWithoutModel(t *testing.T) {
	expectContains(t, statusOutput(t, statusRuntime(ratioConfig()), testfix.NewFakeHost()), "Next compaction:  ~0 / 30 observed source tokens (0%)")

	h := testfix.NewFakeHost()
	h.Model = host.Model{"contextWindow": 0}
	expectContains(t, statusOutput(t, statusRuntime(ratioConfig()), h), "Next compaction:  ~0 / 30 observed source tokens (0%)")
}
