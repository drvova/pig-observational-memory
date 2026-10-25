package commands_test

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/commands"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

func statusConfig() config.Config {
	cfg := config.Defaults
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

func TestStatusRendersConciseNoMemoryStatus(t *testing.T) {
	output := statusOutput(t, statusRuntime(statusConfig()), testfix.NewFakeHost())

	expectContains(t, output, "── Memory ──", "Records: 0 recorded (0 by code, 0 by the model) / 0 dropped / 0 active", "Reflections: 0 recorded / 0 standing", "── Compaction ──")
	expectLacks(t, output, "Next observation", "Next compaction:", "── Settings ──", "Passive:")
}

// The compaction line follows a mid-session toggle of Pi's auto-compaction: status re-reads
// compaction.enabled for a configuration loaded from disk.
func TestStatusRereadsPiAutoCompactionMidSession(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	paths := config.Paths{AgentDir: filepath.Join(root, "agent"), ProjectDirName: ".pi"}
	rt := runtime.New()
	rt.LoadConfig = func(dir string, trust config.TrustState) config.Config {
		return config.Load(dir, paths, trust, func(string) (string, bool) { return "", false })
	}
	rt.ReadPiAutoCompaction = func(dir string, trust config.TrustState) (bool, bool) {
		return config.ReadPiAutoCompaction(dir, paths, trust)
	}
	setPiCompaction := func(enabled bool) {
		t.Helper()
		data, err := json.Marshal(map[string]any{"compaction": map[string]any{"enabled": enabled}})
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
	h := testfix.NewFakeHost(testfix.TextCustomMessage("raw-1", "aaaaaaaaaaaa"))
	h.WorkDir = cwd
	h.Trusted = true

	setPiCompaction(true)
	expectContains(t, statusOutput(t, rt, h), "Compaction: PiG's auto-compaction is on")

	setPiCompaction(false)
	expectContains(t, statusOutput(t, rt, h), "Compaction: PiG's auto-compaction is off; only the extension's level-0 compaction runs")
}

func TestStatusShowsPassiveAndInFlight(t *testing.T) {
	cfg := statusConfig()
	cfg.Passive = true
	rt := statusRuntime(cfg)
	rt.SetCompactInFlight(true)
	rt.SetCompactHookInFlight(true)

	output := statusOutput(t, rt, testfix.NewFakeHost())

	expectContains(t, output,
		"Passive: turn-end extraction, level 1, thinning and the extension's compaction trigger are off",
		"── In flight ──",
		"Auto-compaction: running",
		"Compaction hook: running",
	)
	expectLacks(t, output, "Consolidation: running", "── Last error ──")
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
		_ = store.RecordModel(curated.ModelEvent{Model: "p/m", Kind: curated.ModelKindMemory, Outcome: curated.OutcomeShown, Target: curated.TargetSummary, Engine: curated.EngineV2})
		_ = store.RecordModel(curated.ModelEvent{Model: "p/m", Kind: curated.ModelKindPass, Stage: "compaction", Engine: curated.EngineV2, Tokens: 100})
	}
	if _, err := store.Define(curated.Definition{Name: "bakes", Query: "notes", Description: "Notes."}, "s"); err != nil {
		t.Fatal(err)
	}
	output = statusOutput(t, statusRuntime(statusConfig()), h)
	expectContains(t, output, "── Harness ──",
		"Profile (session model p/m, 6 events): codec rows, layout tables, summary 1/2 of the room, short spec",
		"summary → 1/2 of the room: 3 queries for records the summary showed",
		"Defined tools: bakes probation (0 calls")
}

// Project settings apply only in a trusted project, and status says when they
// are ignored. The trust is read once per session: a session start
// (ReloadConfig) asks the host again.
func TestStatusReportsProjectSettingsIgnoredWhenUntrusted(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	paths := config.Paths{AgentDir: filepath.Join(root, "agent"), ProjectDirName: ".pi"}
	path := filepath.Join(cwd, paths.ProjectDirName, "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"observational-memory": {"passive": true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	rt := runtime.New()
	rt.LoadConfig = func(dir string, trust config.TrustState) config.Config {
		return config.Load(dir, paths, trust, func(string) (string, bool) { return "", false })
	}
	rt.ReadPiAutoCompaction = func(dir string, trust config.TrustState) (bool, bool) {
		return config.ReadPiAutoCompaction(dir, paths, trust)
	}
	h := testfix.NewFakeHost(testfix.TextCustomMessage("raw-1", "aaaaaaaaaaaa"))
	h.WorkDir = cwd
	const untrusted = "Project settings ignored: the project is not trusted, so only global settings apply (a /trust decision applies after PiG restarts)"
	const failed = "Project settings ignored: the project trust check failed, so only global settings apply"

	output := statusOutput(t, rt, h)
	expectContains(t, output, "── Settings ──", untrusted)
	expectLacks(t, output, "Passive:")
	statusOutput(t, rt, h)
	if h.TrustCalls != 1 {
		t.Fatalf("trust checked %d times in one session", h.TrustCalls)
	}

	rt.ReloadConfig()
	h.TrustErr = errors.New("host unavailable")
	output = statusOutput(t, rt, h)
	expectContains(t, output, failed)
	expectLacks(t, output, untrusted, "Passive:")

	rt.ReloadConfig()
	h.TrustErr = nil
	h.Trusted = true
	output = statusOutput(t, rt, h)
	expectContains(t, output, "Passive:")
	expectLacks(t, output, "── Settings ──", "Project settings ignored")
	if h.TrustCalls != 3 {
		t.Fatalf("trust checked %d times over three sessions", h.TrustCalls)
	}
}

// The status shows PiG's threshold and the context against it, and says when
// PiG's auto-compaction is off and only the extension's level-0 compaction runs.
func TestStatusV2Compaction(t *testing.T) {
	cfg := statusConfig()
	tokens := 92_000
	h := testfix.NewFakeHost(testfix.RawMessage("u1", "hello"))
	h.Model = testfix.Model("anthropic", "m", map[string]any{"contextWindow": 100_000})
	h.Usage = &host.ContextUsage{Tokens: &tokens, ContextWindow: 100_000}
	cfg.PiCompaction = config.PiCompactionTokens{ReserveOverrides: map[string]int{"anthropic/m": 10_000}}
	output := statusOutput(t, statusRuntime(cfg), h)
	expectContains(t, output, "Compaction: PiG's auto-compaction is on; PiG's threshold 90,000 (window 100,000 - reserveTokens 10,000), context ~92,000 (102%)")
	expectLacks(t, output, "Next compaction:")

	cfg.PiAutoCompactionEnabled = false
	h.Usage = &host.ContextUsage{ContextWindow: 100_000}
	output = statusOutput(t, statusRuntime(cfg), h)
	expectContains(t, output, "Compaction: PiG's auto-compaction is off; only the extension's level-0 compaction runs, at PiG's threshold 90,000", "context unknown until the next response reports usage")
}

// The status says the latest compaction's level, its request count and why
// level 1 did not run or failed, read from the compaction's details.
func TestStatusV2LastCompactionLevel(t *testing.T) {
	cfg := statusConfig()
	compacted := func(report ledger.ConsolidationReport) *testfix.FakeHost {
		details := ledger.MemoryDetails{Type: ledger.OMFolded, Version: 1, FullFold: true, Observations: []ledger.Observation{}, Reflections: []ledger.Reflection{}, FoldThroughEntryID: "u2", Engine: config.EngineV2, Consolidation: &report}
		return testfix.NewFakeHost(testfix.RawMessage("u1", "hello"), testfix.CompactionEntry("c1", testfix.CompactionArgs{FirstKeptEntryID: "u2", Details: testfix.ToJSONValue(details)}), testfix.RawMessage("u2", "next"))
	}
	for _, c := range []struct {
		name   string
		report ledger.ConsolidationReport
		want   string
	}{
		{"applied", ledger.ConsolidationReport{Requested: true, Applied: true, Records: 2, Merges: 1, Reflections: 1, Dropped: 3, Refused: 1}, "Last compaction: level 1, 1 request; level 1 recorded 2 labelled records, 1 merges, 1 reflections, 0 aliases, dropped 3 replaced records, refused 1 proposals"},
		{"settled links", ledger.ConsolidationReport{Requested: true, Applied: true, Confirmed: 2, Rejected: 1}, "dropped 0 replaced records, settled 3 possible updates (2 confirmed, 1 rejected)"},
		{"curated citations", ledger.ConsolidationReport{Requested: true, Applied: true, Cited: 2, Promoted: 1}, "dropped 0 replaced records, cited 2 curated memories (1 staged made live)"},
		{"failed", ledger.ConsolidationReport{Requested: true, Reason: "the output accounts for nothing"}, "Last compaction: level 0, 1 request; level 1 failed (the output accounts for nothing), level 0 stands"},
		{"skipped", ledger.ConsolidationReport{Reason: "consolidate is false"}, "Last compaction: level 0, 0 requests; level 1 not run: consolidate is false"},
	} {
		t.Run(c.name, func(t *testing.T) {
			expectContains(t, statusOutput(t, statusRuntime(cfg), compacted(c.report)), c.want)
		})
	}
	expectLacks(t, statusOutput(t, statusRuntime(cfg), testfix.NewFakeHost(testfix.RawMessage("u1", "hello"))), "Last compaction:")
	// A compaction memory v2 did not write (memory v1's, or PiG's own) says so.
	v1 := ledger.MemoryDetails{Type: ledger.OMFolded, Version: 1, FullFold: true, Observations: []ledger.Observation{}, Reflections: []ledger.Reflection{}, FoldThroughEntryID: "u2"}
	h := testfix.NewFakeHost(testfix.RawMessage("u1", "hello"), testfix.CompactionEntry("c1", testfix.CompactionArgs{FirstKeptEntryID: "u2", Details: testfix.ToJSONValue(v1)}), testfix.RawMessage("u2", "next"))
	expectContains(t, statusOutput(t, statusRuntime(cfg), h), "Last compaction: not written by memory v2")
}

// The status shows the Status section's contents only: the records by who
// appended them (level 1's labels of a code record and of a host summary are the model's, and
// the host summary counts once), the reflections standing (one replaced by another is not), the
// removed settings still set, the compaction lines and the harness evidence since the last
// level-1 offer, and none of v1's worker clocks or pools.
func TestStatusV2ShowsOnlyV2Contents(t *testing.T) {
	cfg := statusConfig()
	cfg.RemovedSet = []string{"observeAfterTokens", "fallbackModel", "memoryEngine"}
	code := testfix.Observation("aaaaaaaaaaa1", func(o *ledger.Observation) { o.Origin = ledger.OriginCode })
	model := testfix.Observation("aaaaaaaaaaa2", func(o *ledger.Observation) { o.Origin = ledger.OriginModel })
	host := testfix.Observation("aaaaaaaaaaa3", func(o *ledger.Observation) { o.Origin = ledger.OriginHost })
	v1 := testfix.Observation("aaaaaaaaaaa4")
	codeLabel := testfix.Observation("aaaaaaaaaaa5", func(o *ledger.Observation) { o.Origin, o.Subject = ledger.OriginCode, "deploy" })
	hostLabel := testfix.Observation("aaaaaaaaaaa6", func(o *ledger.Observation) { o.Origin, o.Subject = ledger.OriginHost, "release" })
	labels := []ledger.Observation{codeLabel, hostLabel}
	r1 := testfix.Reflection("bbbbbbbbbbb1", []string{"aaaaaaaaaaa2"})
	r2 := testfix.Reflection("bbbbbbbbbbb2", []string{"aaaaaaaaaaa2"}, func(r *ledger.Reflection) { r.Supersedes = []string{r1.ID} })
	h := testfix.NewFakeHost(
		testfix.RawMessage("u1", "hello"),
		testfix.ObservationsRecordedEntry("om1", []ledger.Observation{code, model, host, v1}, "u1"),
		testfix.ObservationsDroppedEntry("om2", []string{"aaaaaaaaaaa4"}, "u1"),
		testfix.ObservationsRecordedEntry("om3", labels, "u1", map[string]any{"data": map[string]any{"observations": labels, "coversUpToId": "u1", "consolidated": true}}),
		testfix.ObservationsDroppedEntry("om4", []string{"aaaaaaaaaaa1", "aaaaaaaaaaa3"}, "u1"),
		testfix.ReflectionsRecordedEntry("om5", []ledger.Reflection{r1, r2}, "u1"),
	)
	h.Home = t.TempDir()
	h.Model = testfix.Model("anthropic", "m", map[string]any{"contextWindow": 100_000})
	output := statusOutput(t, statusRuntime(cfg), h)
	expectContains(t, output,
		"Removed settings, ignored: observeAfterTokens, fallbackModel, memoryEngine",
		"Records: 6 recorded (1 by code, 3 by the model, 1 host summary, 1 by memory v1's workers) / 3 dropped / 3 active",
		"Reflections: 2 recorded / 1 standing",
		"Compaction: PiG's auto-compaction is on",
		"Memories: 0 live (0 pinned), 0 staged",
		"Evidence: 0 compactions counted since the start (no level-1 offer yet)",
	)
	expectLacks(t, output, "Next observation", "Next reflection", "Observer backlog", "observation pool", "Reflection pool")
}

// After a level-1 offer, the evidence counts from it.
func TestStatusV2EvidenceSinceTheLastOffer(t *testing.T) {
	cfg := statusConfig()
	h := testfix.NewFakeHost(testfix.RawMessage("u1", "hello"))
	h.Home = t.TempDir()
	h.Model = testfix.Model("anthropic", "m", map[string]any{"contextWindow": 100_000})
	store, ok := harness.StoreOf(h, cfg)
	if !ok {
		t.Fatal("no store")
	}
	if _, _, _, err := store.Layer().HoldPass(harness.ModelKey(h.SessionModel()), nil, layer.Generation{Op: layer.OpPropose}); err != nil {
		t.Fatal(err)
	}
	expectContains(t, statusOutput(t, statusRuntime(cfg), h), "Evidence: 0 compactions counted since the last level-1 offer")
}
