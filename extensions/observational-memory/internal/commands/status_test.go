package commands_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/commands"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
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

// A !!command run PiG keeps out of context is not backlog: the observer never
// reads it and the compaction cut does not wait for it.
func TestStatusBacklogLeavesOutExcludedBashRuns(t *testing.T) {
	entries := []ledger.Entry{
		testfix.CompactionEntry("cmp-1", testfix.CompactionArgs{FirstKeptEntryID: "raw-1"}),
		testfix.TextCustomMessage("raw-1", "aaaaaaaaaaaa"),
		testfix.ObservationsRecordedEntry("om-cov", []ledger.Observation{testfix.Observation("aaaaaaaaaaaa")}, "raw-1"),
		testfix.TextCustomMessage("raw-2", "bbbbbbbbbbbbbbbb"),
		testfix.MessageEntry("bash-1", map[string]any{"role": "bashExecution", "command": "cat log", "output": strings.Repeat("x", 400), "excludeFromContext": true}),
	}

	output := statusOutput(t, statusRuntime(statusConfig()), testfix.NewFakeHost(entries...))

	expectContains(t, output, "Next observation: ~4 / ", "Observer backlog: ~4 unobserved source tokens since the compaction boundary")
}

// The compaction line follows a mid-session toggle of Pi's auto-compaction:
// status re-reads compaction.enabled for a configuration loaded from disk.
func TestStatusRereadsPiAutoCompactionMidSession(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	paths := config.Paths{AgentDir: filepath.Join(root, "agent"), ProjectDirName: ".pi"}
	rt := runtime.New()
	rt.LoadConfig = func(dir string) config.Config {
		return config.Load(dir, paths, func(string) (string, bool) { return "", false })
	}
	rt.ReadPiAutoCompaction = func(dir string) (bool, bool) { return config.ReadPiAutoCompaction(dir, paths) }
	setPiCompaction := func(enabled bool) {
		t.Helper()
		data, err := json.Marshal(map[string]any{config.SettingsKey: map[string]any{"compactAfterTokens": 30}, "compaction": map[string]any{"enabled": enabled}})
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(cwd, paths.ProjectDirName, "settings.json")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 3 raw tokens, none of them observed.
	h := testfix.NewFakeHost(testfix.TextCustomMessage("raw-1", "aaaaaaaaaaaa"))
	h.WorkDir = cwd

	setPiCompaction(true)
	expectContains(t, statusOutput(t, rt, h), "Next compaction:  ~0 / 30 observed source tokens (0%)")

	setPiCompaction(false)
	expectContains(t, statusOutput(t, rt, h), "Next compaction:  ~3 / 30 estimated source tokens (10%)")
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

func assistantUsage(id string, totalTokens int) ledger.Entry {
	return testfix.RawMessage(id, "done", map[string]any{"message": map[string]any{"role": "assistant", "content": "done", "stopReason": "end_turn", "usage": map[string]any{"totalTokens": totalTokens}}})
}

func withContextTokens(h *testfix.FakeHost, tokens int) *testfix.FakeHost {
	h.Usage = &host.ContextUsage{Tokens: &tokens, ContextWindow: 200000}
	return h
}

// Status reports what gates the workers: provider growth counts, and the
// observer backoff is shown only while the observer would honor it, on the raw
// backlog it is measured on.
func TestStatusProgressMirrorsWorkerGating(t *testing.T) {
	entries := []ledger.Entry{
		assistantUsage("assistant-1", 100),
		testfix.TextCustomMessage("raw-1", "aaaa"),
		testfix.ObservationsRecordedEntry("om-cov", []ledger.Observation{testfix.Observation("aaaaaaaaaaaa")}, "raw-1"),
		testfix.TextCustomMessage("raw-2", "bbbbbbbbbbbbbbbb"), // 4 tokens, judged empty
	}
	// What the observer records after an empty verdict over raw-2.
	judged := runtime.ObserverEmptyBackoff{SessionIdentity: "session-1", CoverageID: "raw-1", TokensAtEmpty: 4, ChunkEndID: "raw-2"}
	status := func(backoff runtime.ObserverEmptyBackoff, branch []ledger.Entry) string {
		rt := statusRuntime(statusConfig())
		rt.SetObserverEmptyBackoff(&backoff)
		return statusOutput(t, rt, withContextTokens(testfix.NewFakeHost(branch...), 5100))
	}

	expectContains(t, status(judged, entries), "Next observation: ~5,000 / 10 tokens", "Observer backoff: nothing new at ~4 backlog tokens; resumes at ~14 (backlog now ~4)")

	// The observer discards the backoff in each of these, so status does not show it.
	otherSession := judged
	otherSession.SessionIdentity = "session-2"
	sibling := append(append([]ledger.Entry{}, entries[:3]...), testfix.TextCustomMessage("raw-2b", "cccccccccccccccc"))
	grown := append(append([]ledger.Entry{}, entries...), testfix.TextCustomMessage("raw-3", strings.Repeat("c", 40)))
	for name, output := range map[string]string{
		"another session":      status(otherSession, entries),
		"chunk off the branch": status(judged, sibling),
		"backlog at resume":    status(judged, grown),
	} {
		t.Run(name, func(t *testing.T) { expectLacks(t, output, "Observer backoff") })
	}
}

// The reflector is not due until an observation lands after its coverage, so
// provider growth alone does not move its progress.
func TestStatusReflectionProgressWaitsForNewObservations(t *testing.T) {
	obs := testfix.Observation("aaaaaaaaaaaa")
	entries := []ledger.Entry{
		assistantUsage("assistant-1", 100),
		testfix.TextCustomMessage("raw-1", "aaaa"),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obs}, "raw-1"),
		testfix.ReflectionsRecordedEntry("om-ref", []ledger.Reflection{testfix.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})}, "raw-1"),
	}
	output := statusOutput(t, statusRuntime(statusConfig()), withContextTokens(testfix.NewFakeHost(entries...), 5100))
	expectContains(t, output, "Next reflection:  ~0 / 20 observed tokens (0%)")

	// A new observation makes it due on the same basis, provider growth included.
	entries = append(entries,
		testfix.TextCustomMessage("raw-2", "bbbb"),
		testfix.ObservationsRecordedEntry("om-obs-2", []ledger.Observation{testfix.Observation("bbbbbbbbbbbb")}, "raw-2"),
	)
	output = statusOutput(t, statusRuntime(statusConfig()), withContextTokens(testfix.NewFakeHost(entries...), 5100))
	expectContains(t, output, "Next reflection:  ~5,000 / 20 observed tokens")
}

// /om:status shows the session model's harness profile with the reasons for each switch, and
// the defined tools' standing.
func TestStatusShowsHarnessProfile(t *testing.T) {
	h := testfix.NewFakeHost()
	h.Model = testfix.Model("p", "m")
	output := statusOutput(t, statusRuntime(statusConfig()), h)
	expectLacks(t, output, "── Harness ──")

	h.Home = t.TempDir()
	store := curated.New(curated.DefaultDir(h.Home))
	for i := 0; i < harness.MinEvidence; i++ {
		_ = store.RecordModel(curated.ModelEvent{Model: "p/m", Kind: curated.ModelKindMemory, Outcome: curated.OutcomeShown, Target: curated.TargetSummary})
	}
	if _, err := store.Define(curated.Definition{Name: "bakes", Query: "notes", Description: "Notes."}, "s"); err != nil {
		t.Fatal(err)
	}
	output = statusOutput(t, statusRuntime(statusConfig()), h)
	expectContains(t, output, "── Harness ──",
		"Profile (session model p/m, 3 events): codec rows, layout tables, summary 1/2 of the room, short spec",
		"summary → 1/2 of the room: 3 queries for records the summary showed",
		"Defined tools: bakes probation (0 calls")

	// A separate memory worker shows only what it writes with: its layout switch feeds no
	// surface, so it is not listed.
	for i := 0; i < harness.MinEvidence; i++ {
		_ = store.RecordModel(curated.ModelEvent{Model: "p/worker", Kind: curated.ModelKindDecode, Outcome: curated.OutcomeColumns, Codec: "rows"})
	}
	cfg := statusConfig()
	cfg.Model = &config.ConfiguredModel{ID: "worker"}
	output = statusOutput(t, statusRuntime(cfg), h)
	expectContains(t, output, "Profile (memory worker p/worker, 3 events): codec rows, short spec")
	expectLacks(t, output, "layout → lines")
}
