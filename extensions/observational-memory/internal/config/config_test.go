package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
)

type env map[string]string

func (e env) lookup(key string) (string, bool) {
	v, ok := e[key]
	return v, ok
}

type fixture struct {
	cwd   string
	paths config.Paths
}

func setup(t *testing.T) fixture {
	root := t.TempDir()
	cwd := filepath.Join(root, "project")
	agentDir := filepath.Join(root, "agent")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return fixture{cwd: cwd, paths: config.Paths{AgentDir: agentDir, ProjectDirName: ".pi"}}
}

func (f fixture) writeJSON(t *testing.T, path string, value any) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (f fixture) global() string  { return filepath.Join(f.paths.AgentDir, "settings.json") }
func (f fixture) project() string { return filepath.Join(f.cwd, ".pi", "settings.json") }

func (f fixture) load(e env) config.Config { return config.Load(f.cwd, f.paths, e.lookup) }

func TestDefaults(t *testing.T) {
	f := setup(t)
	want := config.Config{
		ObserveAfterTokens: 10000, ReflectAfterTokens: 20000, CompactAfterTokens: 81000, CompactAfterTokensMode: "calibrated", CompactAfterTokensRatio: 0.68,
		ObservationsPoolMaxTokens: 20000, ObservationsPoolTargetTokens: 10000, AgentMaxTurns: 16, AgentMaxTokens: 32000, CompactionCatchUpMaxChunks: 2,
		ShowWorkerNotifications: true, PiAutoCompactionEnabled: true, CuratedMemory: true, CuratedMemoryIndexTokens: 2000, MemoryFormat: "tables",
	}
	if !reflect.DeepEqual(config.Defaults, want) {
		t.Fatalf("defaults %+v", config.Defaults)
	}
	if got := f.load(env{}); !reflect.DeepEqual(got, config.Defaults) {
		t.Fatalf("empty load %+v", got)
	}
}

func TestMergeOrder(t *testing.T) {
	f := setup(t)
	f.writeJSON(t, f.global(), map[string]any{"observational-memory": map[string]any{
		"observeAfterTokens": 10, "reflectAfterTokens": 20, "compactAfterTokens": 30, "observationsPoolMaxTokens": 40, "observationsPoolTargetTokens": 15,
		"agentMaxTurns": 5, "agentMaxTokens": 8192, "model": map[string]any{"provider": "anthropic", "id": "global", "thinking": "medium"},
		"showWorkerNotifications": true, "passive": false, "debugLog": true,
	}})
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{
		"observeAfterTokens": 100, "model": map[string]any{"provider": "openai", "id": "project", "thinking": "low"}, "showWorkerNotifications": false,
	}})
	got := f.load(env{"PI_OBSERVATIONAL_MEMORY_PASSIVE": "true"})
	if got.ObserveAfterTokens != 100 || got.ReflectAfterTokens != 20 || got.CompactAfterTokens != 30 || got.ObservationsPoolMaxTokens != 40 || got.ObservationsPoolTargetTokens != 15 || got.AgentMaxTurns != 5 || got.AgentMaxTokens != 8192 {
		t.Fatalf("numbers %+v", got)
	}
	if got.Model == nil || *got.Model != (config.ConfiguredModel{Provider: "openai", ID: "project", Thinking: "low"}) || got.ShowWorkerNotifications || !got.Passive || !got.DebugLog {
		t.Fatalf("merged %+v", got)
	}
}

func TestThinkingLevels(t *testing.T) {
	f := setup(t)
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"model": map[string]any{"provider": "anthropic", "id": "claude", "thinking": "max"}}})
	if got := f.load(env{}); got.Model == nil || got.Model.Thinking != "max" {
		t.Fatal("max thinking level rejected")
	}
}

func TestFallbackModel(t *testing.T) {
	f := setup(t)
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{
		"model":         map[string]any{"provider": "anthropic", "id": "claude-haiku-4-5-20251001", "thinking": "low"},
		"fallbackModel": map[string]any{"provider": "opencode-go", "id": "deepseek-v4.1-flash", "thinking": "low"},
	}})
	got := f.load(env{})
	if got.Model == nil || got.FallbackModel == nil || *got.FallbackModel != (config.ConfiguredModel{Provider: "opencode-go", ID: "deepseek-v4.1-flash", Thinking: "low"}) {
		t.Fatalf("fallback %+v", got.FallbackModel)
	}
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"fallbackModel": map[string]any{"provider": "opencode-go", "id": "deepseek-v4.1-flash", "thinking": "huge"}}})
	if got := f.load(env{}); got.FallbackModel == nil || got.FallbackModel.Thinking != "" || got.FallbackModel.ID != "deepseek-v4.1-flash" {
		t.Fatalf("invalid thinking %+v", got.FallbackModel)
	}
	// An id without a provider names a model on the session's provider.
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"fallbackModel": map[string]any{"provider": "", "id": "deepseek-v4.1-flash"}}})
	if got := f.load(env{}); got.FallbackModel == nil || *got.FallbackModel != (config.ConfiguredModel{ID: "deepseek-v4.1-flash"}) {
		t.Fatalf("id-only fallback %+v", got.FallbackModel)
	}
}

func TestInvalidValues(t *testing.T) {
	f := setup(t)
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{
		"observeAfterTokens": -1, "reflectAfterTokens": 0, "compactAfterTokens": 1.5, "observationsPoolMaxTokens": "20000", "observationsPoolTargetTokens": "10000",
		"agentMaxTurns": nil, "model": map[string]any{"provider": "anthropic", "id": "", "thinking": "huge"}, "showWorkerNotifications": "no", "passive": "yes", "debugLog": "true",
	}})
	if got := f.load(env{}); !reflect.DeepEqual(got, config.Defaults) {
		t.Fatalf("invalid values applied %+v", got)
	}
}

func TestPoolTarget(t *testing.T) {
	f := setup(t)
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"observationsPoolMaxTokens": 40}})
	if got := f.load(env{}); got.ObservationsPoolMaxTokens != 40 || got.ObservationsPoolTargetTokens != 20 {
		t.Fatalf("derived target %+v", got)
	}
	f.writeJSON(t, f.global(), map[string]any{"observational-memory": map[string]any{"observationsPoolMaxTokens": 100, "observationsPoolTargetTokens": 80}})
	if got := f.load(env{}); got.ObservationsPoolMaxTokens != 40 || got.ObservationsPoolTargetTokens != 20 {
		t.Fatalf("invalid explicit target %+v", got)
	}
}

func TestIgnoresV2Settings(t *testing.T) {
	f := setup(t)
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{
		"observationThresholdTokens": 10, "compactionThresholdTokens": 20, "reflectionThresholdTokens": 30, "compactionModel": map[string]any{"provider": "anthropic", "id": "old"},
		"thinkingLevel": "high", "observerMaxTurnsPerRun": 2, "reflectorMaxTurnsPerPass": 3, "prunerMaxTurnsPerPass": 4, "compactionMaxToolCalls": 5,
	}})
	if got := f.load(env{}); !reflect.DeepEqual(got, config.Defaults) {
		t.Fatal("V2 settings applied")
	}
}

func TestEnvPassive(t *testing.T) {
	if passive, set := config.ReadEnvPassive(env{"PI_OBSERVATIONAL_MEMORY_PASSIVE": "on"}.lookup); !set || !passive {
		t.Fatal("on should enable passive")
	}
	if passive, set := config.ReadEnvPassive(env{"PI_OBSERVATIONAL_MEMORY_PASSIVE": "0"}.lookup); !set || passive {
		t.Fatal("0 should disable passive")
	}
	if _, set := config.ReadEnvPassive(env{"PI_OBSERVATIONAL_MEMORY_PASSIVE": "maybe"}.lookup); set {
		t.Fatal("maybe should be ignored")
	}
}

func TestMemoryFormat(t *testing.T) {
	if !reflect.DeepEqual(config.MemoryFormatValues, ledger.FormatValues) {
		t.Fatalf("config formats %v, renderer formats %v", config.MemoryFormatValues, ledger.FormatValues)
	}
	f := setup(t)
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"memoryFormat": "lines"}})
	if got := f.load(env{}); got.MemoryFormat != "lines" {
		t.Fatalf("memoryFormat %q", got.MemoryFormat)
	}
	for _, invalid := range []any{"toon", "Lines", "", 1, true} {
		f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"memoryFormat": invalid}})
		if got := f.load(env{}); got.MemoryFormat != "tables" {
			t.Fatalf("memoryFormat %v accepted as %q", invalid, got.MemoryFormat)
		}
	}
}

func TestRatioMode(t *testing.T) {
	f := setup(t)
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"compactAfterTokensMode": "ratio", "compactAfterTokensRatio": 0.5}})
	if got := f.load(env{}); got.CompactAfterTokensMode != "ratio" || got.CompactAfterTokensRatio != 0.5 {
		t.Fatalf("ratio mode %+v", got)
	}
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"compactAfterTokensMode": "auto"}})
	if got := f.load(env{}); got.CompactAfterTokensMode != "calibrated" {
		t.Fatal("invalid mode accepted")
	}
	for _, ratio := range []any{0, 1, 1.5, -0.2, "0.5"} {
		f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"compactAfterTokensRatio": ratio}})
		if got := f.load(env{}); got.CompactAfterTokensRatio != 0.68 {
			t.Fatalf("ratio %v accepted", ratio)
		}
	}
}

func TestResolveCompactAfterTokens(t *testing.T) {
	calibrated := config.Defaults
	if config.ResolveCompactAfterTokens(calibrated, 1_000_000) != 81000 || config.ResolveCompactAfterTokens(calibrated, 0) != 81000 {
		t.Fatal("calibrated mode wrong")
	}
	ratio := config.Defaults
	ratio.CompactAfterTokensMode = "ratio"
	ratio.CompactAfterTokensRatio = 0.5
	if config.ResolveCompactAfterTokens(ratio, 1_000_000) != 500_000 || config.ResolveCompactAfterTokens(ratio, 200_000) != 100_000 {
		t.Fatal("ratio scaling wrong")
	}
	if config.ResolveCompactAfterTokens(ratio, 3) != 1 || config.ResolveCompactAfterTokens(ratio, 1) != 1 {
		t.Fatal("floor to >= 1 wrong")
	}
	if config.ResolveCompactAfterTokens(ratio, 0) != 81000 || config.ResolveCompactAfterTokens(ratio, -1) != 81000 {
		t.Fatal("unknown window fallback wrong")
	}
}

func TestPiCompactionEnabled(t *testing.T) {
	f := setup(t)
	f.writeJSON(t, f.global(), map[string]any{"compaction": map[string]any{"enabled": false}})
	if f.load(env{}).PiAutoCompactionEnabled {
		t.Fatal("global compaction.enabled ignored")
	}
	f.writeJSON(t, f.project(), map[string]any{"compaction": map[string]any{"enabled": true}})
	if !f.load(env{}).PiAutoCompactionEnabled {
		t.Fatal("project compaction.enabled should win")
	}
}

func TestDerivedBudgets(t *testing.T) {
	cfg := config.Defaults
	if config.ResolveCompactionMaxRetainedTokens(cfg, 0) != 60_000 || config.ResolveCompactionMaxRetainedTokens(cfg, 200_000) != 100_000 {
		t.Fatal("retained budget wrong")
	}
	cfg.CompactionMaxRetainedTokens = 7
	if config.ResolveCompactionMaxRetainedTokens(cfg, 200_000) != 7 {
		t.Fatal("explicit retained budget ignored")
	}
	cfg = config.Defaults
	if config.ResolveWorkerMemoryMaxTokens(cfg, 0) != 16_000 || config.ResolveWorkerMemoryMaxTokens(cfg, 200_000) != 50_000 {
		t.Fatal("worker memory budget wrong")
	}
	if config.ResolveObserverChunkMaxTokens(cfg, 0) != 60_000 || config.ResolveObserverChunkMaxTokens(cfg, 200_000) != 40_000 || config.ResolveObserverChunkMaxTokens(cfg, 100) != 256 {
		t.Fatal("observer chunk cap wrong")
	}
	cfg.ObserverChunkMaxTokens = 10
	if config.ResolveObserverChunkMaxTokens(cfg, 200_000) != 256 {
		t.Fatal("explicit chunk cap should respect the minimum")
	}
}

// The compaction and worker budget keys parse from settings, including the
// documented way to disable catch-up.
func TestBudgetKeysParse(t *testing.T) {
	f := setup(t)
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{
		"observerChunkMaxTokens": 4000, "compactionMaxRetainedTokens": 30000, "compactionSummaryMaxTokens": 5000,
		"workerMemoryMaxTokens": 9000, "compactionCatchUpMaxChunks": 0, "consolidateWhenIdle": true,
	}})
	got := f.load(env{})
	if got.ObserverChunkMaxTokens != 4000 || got.CompactionMaxRetainedTokens != 30000 || got.CompactionSummaryMaxTokens != 5000 ||
		got.WorkerMemoryMaxTokens != 9000 || got.CompactionCatchUpMaxChunks != 0 || !got.ConsolidateWhenIdle {
		t.Fatalf("config %+v", got)
	}
}

// Pi's compaction.enabled is readable on its own, project over global.
func TestReadPiAutoCompaction(t *testing.T) {
	f := setup(t)
	if _, ok := config.ReadPiAutoCompaction(f.cwd, f.paths); ok {
		t.Fatal("unset setting reported")
	}
	f.writeJSON(t, f.global(), map[string]any{"compaction": map[string]any{"enabled": false}})
	if enabled, ok := config.ReadPiAutoCompaction(f.cwd, f.paths); !ok || enabled {
		t.Fatalf("global %v %v", enabled, ok)
	}
	f.writeJSON(t, f.project(), map[string]any{"compaction": map[string]any{"enabled": true}})
	if enabled, ok := config.ReadPiAutoCompaction(f.cwd, f.paths); !ok || !enabled {
		t.Fatalf("project %v %v", enabled, ok)
	}
}

func TestCuratedMemorySettings(t *testing.T) {
	f := setup(t)
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"curatedMemory": false, "curatedMemoryIndexTokens": 512}})
	got := f.load(env{})
	if got.CuratedMemory || got.CuratedMemoryIndexTokens != 512 {
		t.Fatalf("curated settings not applied: %+v", got)
	}
}

func TestResolveSummaryBudget(t *testing.T) {
	cfg := config.Defaults
	// Unknown window: the fallback, or the explicit setting when there is one.
	if got := config.ResolveSummaryBudget(cfg, 0, 5_000, 30_000); got != config.CompactionSummaryFallbackMaxTokens {
		t.Fatalf("unknown window budget = %d", got)
	}
	cfg.CompactionSummaryMaxTokens = 20_000
	if got := config.ResolveSummaryBudget(cfg, 0, 0, 0); got != 20_000 {
		t.Fatalf("unknown window with explicit budget = %d", got)
	}
	cfg = config.Defaults
	// Known window: the room after overhead and the larger of the retained
	// tail and the threshold (81k calibrated), which already counts the tail.
	if got := config.ResolveSummaryBudget(cfg, 200_000, 10_000, 5_000); got != 109_000 {
		t.Fatalf("small retained tail budget = %d", got)
	}
	if got := config.ResolveSummaryBudget(cfg, 200_000, 10_000, 100_000); got != 90_000 {
		t.Fatalf("large retained tail budget = %d", got)
	}
	// A threshold the window cannot reach leaves the floor: the fallback,
	// at most half of what the window has after the retained tail.
	if got := config.ResolveSummaryBudget(cfg, 64_000, 8_000, 20_000); got != config.CompactionSummaryFallbackMaxTokens {
		t.Fatalf("small window budget = %d", got)
	}
	if got := config.ResolveSummaryBudget(cfg, 32_000, 8_000, 14_000); got != 5_000 {
		t.Fatalf("tight small window budget = %d", got)
	}
	// No room left still yields a positive budget: the renderer reads 0 as unlimited.
	if got := config.ResolveSummaryBudget(cfg, 200_000, 10_000, 195_000); got != 1 {
		t.Fatalf("no-room budget = %d", got)
	}
	// Negative inputs measure nothing.
	if got := config.ResolveSummaryBudget(cfg, 200_000, -50, -50); got != 119_000 {
		t.Fatalf("negative inputs budget = %d", got)
	}
	// Ratio mode derives the threshold from the window.
	cfg.CompactAfterTokensMode = config.ModeRatio
	cfg.CompactAfterTokensRatio = 0.5
	if got := config.ResolveSummaryBudget(cfg, 100_000, 0, 10_000); got != 50_000 {
		t.Fatalf("ratio mode budget = %d", got)
	}
	// The explicit setting caps the room but never raises it.
	cfg = config.Defaults
	cfg.CompactionSummaryMaxTokens = 12_000
	if got := config.ResolveSummaryBudget(cfg, 200_000, 10_000, 5_000); got != 12_000 {
		t.Fatalf("capped budget = %d", got)
	}
	if got := config.ResolveSummaryBudget(cfg, 200_000, 10_000, 185_000); got != 5_000 {
		t.Fatalf("cap raised the room: %d", got)
	}
	cfg.CompactionSummaryMaxTokens = 2_000
	if got := config.ResolveSummaryBudget(cfg, 64_000, 8_000, 20_000); got != 2_000 {
		t.Fatalf("cap did not bound the floor: %d", got)
	}
}
