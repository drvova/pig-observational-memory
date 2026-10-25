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

// load reads the settings of a trusted project; loadAs reads them under trust.
func (f fixture) load(e env) config.Config { return f.loadAs(config.ProjectTrusted, e) }

// trustedDefaults is what a trusted project with no settings loads.
func trustedDefaults() config.Config {
	cfg := config.Defaults
	cfg.ProjectTrust = config.ProjectTrusted
	return cfg
}

func (f fixture) loadAs(trust config.TrustState, e env) config.Config {
	return config.Load(f.cwd, f.paths, trust, e.lookup)
}

func TestDefaults(t *testing.T) {
	f := setup(t)
	want := config.Config{PiAutoCompactionEnabled: true, CuratedMemory: true, CuratedMemoryIndexTokens: 2000, MemoryFormat: "tables"}
	if !reflect.DeepEqual(config.Defaults, want) {
		t.Fatalf("defaults %+v", config.Defaults)
	}
	if got := f.load(env{}); !reflect.DeepEqual(got, trustedDefaults()) {
		t.Fatalf("empty load %+v", got)
	}
}

func TestMergeOrder(t *testing.T) {
	f := setup(t)
	f.writeJSON(t, f.global(), map[string]any{"observational-memory": map[string]any{
		"compactionSummaryMaxTokens": 10, "curatedMemoryIndexTokens": 20, "memoryFormat": "lines", "passive": false, "debugLog": true,
	}})
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{
		"compactionSummaryMaxTokens": 100, "consolidate": false,
	}})
	got := f.load(env{"PI_OBSERVATIONAL_MEMORY_PASSIVE": "true"})
	if got.CompactionSummaryMaxTokens != 100 || got.CuratedMemoryIndexTokens != 20 || got.MemoryFormat != "lines" {
		t.Fatalf("numbers %+v", got)
	}
	if !got.Passive || !got.DebugLog || got.Consolidates() {
		t.Fatalf("merged %+v", got)
	}
}

func TestInvalidValues(t *testing.T) {
	f := setup(t)
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{
		"compactionSummaryMaxTokens": -1, "curatedMemoryIndexTokens": 1.5, "memoryFormat": 3, "passive": "yes", "debugLog": "true", "curatedMemory": nil, "consolidate": "no",
	}})
	if got := f.load(env{}); !reflect.DeepEqual(got, trustedDefaults()) {
		t.Fatalf("invalid values applied %+v", got)
	}
}

// Settings older designs had (the TypeScript extension's, and memory v1's beyond the removed
// list) are ignored and not listed.
func TestIgnoresOldSettings(t *testing.T) {
	f := setup(t)
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{
		"observationThresholdTokens": 10, "compactionThresholdTokens": 20, "reflectionThresholdTokens": 30, "compactionModel": map[string]any{"provider": "anthropic", "id": "old"},
		"thinkingLevel": "high", "observerMaxTurnsPerRun": 2, "reflectorMaxTurnsPerPass": 3, "prunerMaxTurnsPerPass": 4, "compactionMaxToolCalls": 5,
	}})
	if got := f.load(env{}); !reflect.DeepEqual(got, trustedDefaults()) {
		t.Fatal("old settings applied")
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

// Pi's compaction.enabled is readable on its own, project over global.
func TestReadPiAutoCompaction(t *testing.T) {
	f := setup(t)
	if _, ok := config.ReadPiAutoCompaction(f.cwd, f.paths, config.ProjectTrusted); ok {
		t.Fatal("unset setting reported")
	}
	f.writeJSON(t, f.global(), map[string]any{"compaction": map[string]any{"enabled": false}})
	if enabled, ok := config.ReadPiAutoCompaction(f.cwd, f.paths, config.ProjectTrusted); !ok || enabled {
		t.Fatalf("global %v %v", enabled, ok)
	}
	f.writeJSON(t, f.project(), map[string]any{"compaction": map[string]any{"enabled": true}})
	if enabled, ok := config.ReadPiAutoCompaction(f.cwd, f.paths, config.ProjectTrusted); !ok || !enabled {
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

// Only the user's own settings lower the kernel floor; a project's settings, which travel with
// its repository, can keep it engaged but never lower it.
func TestKernelFloor(t *testing.T) {
	f := setup(t)
	request := map[string]any{"lower": true, "requestedBy": "drvova", "reason": "rebuilding the memory tool"}
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"kernelFloor": request}})
	if got := f.load(env{}); got.KernelFloor != nil {
		t.Fatalf("a project lowered the floor: %+v", got.KernelFloor)
	}
	f.writeJSON(t, f.global(), map[string]any{"observational-memory": map[string]any{"kernelFloor": request}})
	got := f.load(env{})
	if got.KernelFloor == nil || *got.KernelFloor != (config.FloorRequest{RequestedBy: "drvova", Reason: "rebuilding the memory tool"}) {
		t.Fatalf("global request %+v", got.KernelFloor)
	}
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"kernelFloor": map[string]any{"lower": false}}})
	if got := f.load(env{}); got.KernelFloor != nil {
		t.Fatalf("a project could not keep the floor engaged: %+v", got.KernelFloor)
	}
	// A request without who or why is kept, so lowering refuses it and says so.
	f.writeJSON(t, f.project(), map[string]any{})
	f.writeJSON(t, f.global(), map[string]any{"observational-memory": map[string]any{"kernelFloor": map[string]any{"lower": true}}})
	if got := f.load(env{}); got.KernelFloor == nil || got.KernelFloor.RequestedBy != "" {
		t.Fatalf("bare request %+v", got.KernelFloor)
	}
}

// PiG uses the global configuration alone until the user trusts a project, so
// project settings, Pi's compaction.enabled among them, are read only from a
// trusted project; an untrusted one, or one whose trust check failed, is
// ignored and reported.
func TestProjectSettingsNeedTrust(t *testing.T) {
	f := setup(t)
	f.writeJSON(t, f.global(), map[string]any{"observational-memory": map[string]any{"compactionSummaryMaxTokens": 111}, "compaction": map[string]any{"enabled": true}})
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"compactionSummaryMaxTokens": 222, "passive": true}, "compaction": map[string]any{"enabled": false}})

	trusted := f.loadAs(config.ProjectTrusted, env{})
	if trusted.CompactionSummaryMaxTokens != 222 || !trusted.Passive || trusted.PiAutoCompactionEnabled || trusted.ProjectSettingsIgnored || trusted.ProjectTrust != config.ProjectTrusted {
		t.Fatalf("trusted project settings not read: %+v", trusted)
	}
	if enabled, ok := config.ReadPiAutoCompaction(f.cwd, f.paths, config.ProjectTrusted); !ok || enabled {
		t.Fatalf("trusted compaction.enabled %v %v", enabled, ok)
	}
	for _, trust := range []config.TrustState{config.ProjectUntrusted, config.ProjectTrustUnknown, ""} {
		got := f.loadAs(trust, env{})
		if got.CompactionSummaryMaxTokens != 111 || got.Passive || !got.PiAutoCompactionEnabled {
			t.Fatalf("%q: project settings read: %+v", trust, got)
		}
		if !got.ProjectSettingsIgnored || got.ProjectTrust != trust {
			t.Fatalf("%q: ignored project settings not reported: %+v", trust, got)
		}
		if enabled, ok := config.ReadPiAutoCompaction(f.cwd, f.paths, trust); !ok || !enabled {
			t.Fatalf("%q: compaction.enabled read from the project: %v %v", trust, enabled, ok)
		}
	}
	// The environment override applies whatever the trust.
	if got := f.loadAs(config.ProjectUntrusted, env{config.PassiveEnv: "1"}); !got.Passive {
		t.Fatal("PI_OBSERVATIONAL_MEMORY_PASSIVE ignored in an untrusted project")
	}
	// Without a project settings file there is nothing to report.
	if err := os.Remove(f.project()); err != nil {
		t.Fatal(err)
	}
	if got := f.loadAs(config.ProjectUntrusted, env{}); got.ProjectSettingsIgnored {
		t.Fatal("missing project settings reported as ignored")
	}
}

// The global kernelFloor request applies whatever the project's trust, and
// an untrusted project cannot keep the floor engaged either: its settings are
// not read at all.
func TestKernelFloorUnderTrust(t *testing.T) {
	f := setup(t)
	request := map[string]any{"lower": true, "requestedBy": "drvova", "reason": "rebuilding the memory tool"}
	f.writeJSON(t, f.global(), map[string]any{"observational-memory": map[string]any{"kernelFloor": request}})
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"kernelFloor": map[string]any{"lower": false}}})
	if got := f.loadAs(config.ProjectTrusted, env{}); got.KernelFloor != nil {
		t.Fatalf("a trusted project could not keep the floor engaged: %+v", got.KernelFloor)
	}
	if got := f.loadAs(config.ProjectUntrusted, env{}); got.KernelFloor == nil || got.KernelFloor.RequestedBy != "drvova" {
		t.Fatalf("global request under an untrusted project %+v", got.KernelFloor)
	}
	f.writeJSON(t, f.global(), map[string]any{})
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"kernelFloor": request}})
	for _, trust := range []config.TrustState{config.ProjectTrusted, config.ProjectUntrusted, config.ProjectTrustUnknown} {
		if got := f.loadAs(trust, env{}); got.KernelFloor != nil {
			t.Fatalf("%q: a project lowered the floor: %+v", trust, got.KernelFloor)
		}
	}
}

func TestTrustFrom(t *testing.T) {
	cases := []struct {
		trusted bool
		err     error
		want    config.TrustState
	}{
		{true, nil, config.ProjectTrusted},
		{false, nil, config.ProjectUntrusted},
		{true, os.ErrPermission, config.ProjectTrustUnknown},
		{false, os.ErrPermission, config.ProjectTrustUnknown},
	}
	for _, c := range cases {
		if got := config.TrustFrom(c.trusted, c.err); got != c.want {
			t.Fatalf("TrustFrom(%v, %v) = %q, want %q", c.trusted, c.err, got, c.want)
		}
	}
}

// PiG's compaction.reserveTokens resolves as PiG's GetCompactionSettings does: the model's
// modelOverrides entry, else the ordinary value, else PiG's default; the project's values lie
// over the global ones field by field, and only a trusted project's are read.
func TestReadPiCompactionTokens(t *testing.T) {
	f := setup(t)
	if got := config.ReadPiCompactionTokens(f.cwd, f.paths, config.ProjectTrusted); got.ReserveTokens != nil || got.ReserveOverrides != nil || got.ReserveTokensFor("anthropic", "m") != config.PiGDefaultReserveTokens {
		t.Fatalf("unset %+v", got)
	}
	f.writeJSON(t, f.global(), map[string]any{"compaction": map[string]any{"reserveTokens": 20000, "modelOverrides": map[string]any{
		"anthropic/m": map[string]any{"reserveTokens": 8000}, "openai/x": map[string]any{"reserveTokens": 1000}, "bad/y": map[string]any{"reserveTokens": -1},
	}}})
	f.writeJSON(t, f.project(), map[string]any{"compaction": map[string]any{"modelOverrides": map[string]any{"openai/x": map[string]any{"reserveTokens": 0}}}})
	trusted := config.ReadPiCompactionTokens(f.cwd, f.paths, config.ProjectTrusted)
	for _, c := range []struct {
		provider, id string
		want         int
	}{{"anthropic", "m", 8000}, {"openai", "x", 0}, {"bad", "y", 20000}, {"other", "z", 20000}, {"", "", 20000}} {
		if got := trusted.ReserveTokensFor(c.provider, c.id); got != c.want {
			t.Fatalf("%s/%s: %d, want %d", c.provider, c.id, got, c.want)
		}
	}
	if got := config.ReadPiCompactionTokens(f.cwd, f.paths, config.ProjectUntrusted).ReserveTokensFor("openai", "x"); got != 1000 {
		t.Fatalf("an untrusted project's override was read: %d", got)
	}
	if got := f.load(env{}).PiCompaction.ReserveTokensFor("openai", "x"); got != 0 {
		t.Fatalf("Load: %d", got)
	}
}
