package config_test

import (
	"testing"

	"github.com/drvova/pig-observational-memory/memory/config"
)

// The working-memory caps, the raw floor and the quality threshold default per model, are set
// globally and per model ("<provider>/<id>"), field by field, the project over the global file;
// the floor and the trigger are off until set.
func TestWorkingMemoryLimits(t *testing.T) {
	f := setup(t)
	defaults := f.load(env{}).LimitsFor("anthropic", "m")
	if defaults != (config.ModelLimits{Constraints: 10, ScopeExclusions: 10, Actions: 5, RawFloorTokens: 32_000, QualityTriggerTokens: 64_000}) {
		t.Fatalf("defaults %+v", defaults)
	}
	if cfg := f.load(env{}); cfg.RawFloor || cfg.QualityTrigger {
		t.Fatal("the floor or the trigger is on by default")
	}
	f.writeJSON(t, f.global(), map[string]any{"observational-memory": map[string]any{
		"workingMemoryConstraints": 12, "rawFloorTokens": 40_000, "qualityTrigger": true,
		"modelLimits": map[string]any{"anthropic/m": map[string]any{"workingMemoryActions": 3, "qualityTriggerTokens": 90_000}, "x/y": "bad"},
	}})
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{
		"workingMemoryScopeExclusions": 4, "rawFloor": true, "workingMemoryConstraints": -1,
		"modelLimits": map[string]any{"anthropic/m": map[string]any{"rawFloorTokens": 50_000}},
	}})
	cfg := f.load(env{})
	if !cfg.RawFloor || !cfg.QualityTrigger {
		t.Fatalf("switches %+v", cfg)
	}
	if got := cfg.LimitsFor("anthropic", "m"); got != (config.ModelLimits{Constraints: 12, ScopeExclusions: 4, Actions: 3, RawFloorTokens: 50_000, QualityTriggerTokens: 90_000}) {
		t.Fatalf("model limits %+v", got)
	}
	if got := cfg.LimitsFor("other", "m"); got != (config.ModelLimits{Constraints: 12, ScopeExclusions: 4, Actions: 5, RawFloorTokens: 40_000, QualityTriggerTokens: 64_000}) {
		t.Fatalf("global limits %+v", got)
	}
	// An untrusted project's settings are not read.
	if got := f.loadAs(config.ProjectUntrusted, env{}); got.RawFloor || got.LimitsFor("anthropic", "m").ScopeExclusions != 10 {
		t.Fatalf("untrusted %+v", got)
	}
}
