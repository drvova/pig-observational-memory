package config_test

import (
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
)

// memoryEngine defaults to v1 and consolidate to on; both are read through the trusted path,
// so an untrusted project's settings never switch the engine.
func TestMemoryEngineAndConsolidate(t *testing.T) {
	f := setup(t)
	if got := f.load(env{}); got.Engine() != config.EngineV1 || got.V2() || !got.Consolidates() {
		t.Fatalf("defaults: engine %q v2 %v consolidate %v", got.Engine(), got.V2(), got.Consolidates())
	}
	f.writeJSON(t, f.global(), map[string]any{"observational-memory": map[string]any{"memoryEngine": "v1", "consolidate": true}})
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"memoryEngine": "v2", "consolidate": false}})
	if got := f.loadAs(config.ProjectTrusted, env{}); !got.V2() || got.Engine() != config.EngineV2 || got.Consolidates() {
		t.Fatalf("trusted project: engine %q consolidate %v", got.Engine(), got.Consolidates())
	}
	for _, trust := range []config.TrustState{config.ProjectUntrusted, config.ProjectTrustUnknown} {
		if got := f.loadAs(trust, env{}); got.V2() || !got.Consolidates() {
			t.Fatalf("%s project switched the engine: %q consolidate %v", trust, got.Engine(), got.Consolidates())
		}
	}
	// The global settings alone switch it too.
	f.writeJSON(t, f.global(), map[string]any{"observational-memory": map[string]any{"memoryEngine": "v2"}})
	if got := f.loadAs(config.ProjectUntrusted, env{}); !got.V2() {
		t.Fatalf("global v2 ignored: %q", got.Engine())
	}
	// An unknown engine or a non-boolean consolidate is ignored.
	f.writeJSON(t, f.global(), map[string]any{})
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"memoryEngine": "v3", "consolidate": "no"}})
	if got := f.load(env{}); got.Engine() != config.EngineV1 || !got.Consolidates() {
		t.Fatalf("invalid values applied: %q consolidate %v", got.Engine(), got.Consolidates())
	}
}

func TestResolveV2SummaryBudget(t *testing.T) {
	base := config.V2SummaryBudgetInputs{ContextWindow: 200_000, ReserveTokens: 16_000, TokensBefore: 190_000, RemovedTokens: 30_000}
	with := func(edit func(*config.V2SummaryBudgetInputs)) config.V2SummaryBudgetInputs {
		in := base
		edit(&in)
		return in
	}
	capped := config.Defaults
	cases := []struct {
		name string
		cap  int
		in   config.V2SummaryBudgetInputs
		want int
	}{
		// 200k - 16k - 190k + 30k = 24k.
		{"PiG's threshold", 0, base, 24_000},
		// 200k - 16k - 180k + 30k = 34k, above the 30k removed.
		{"never larger than removed", 0, with(func(in *config.V2SummaryBudgetInputs) { in.TokensBefore = 180_000 }), 30_000},
		{"output limit", 0, with(func(in *config.V2SummaryBudgetInputs) { in.MaxOutputTokens = 8_000 }), 8_000},
		// The headroom belongs to PiG's threshold alone: 24k - 5k = 19k.
		{"headroom", 0, with(func(in *config.V2SummaryBudgetInputs) { in.NextRunHeadroom = 5_000 }), 19_000},
		// It leaves the output bound alone: min(24k - 5k, 8k, 30k) = 8k.
		{"headroom under the output limit", 0, with(func(in *config.V2SummaryBudgetInputs) { in.MaxOutputTokens = 8_000; in.NextRunHeadroom = 5_000 }), 8_000},
		// And the removed bound: min(200k - 16k - 170k + 150k - 30k, 32k, 150k) = 32k.
		{"headroom under the removed and output bounds", 0, config.V2SummaryBudgetInputs{ContextWindow: 200_000, ReserveTokens: 16_000, TokensBefore: 170_000, RemovedTokens: 150_000, MaxOutputTokens: 32_000, NextRunHeadroom: 30_000}, 32_000},
		{"headroom with no window", 0, config.V2SummaryBudgetInputs{TokensBefore: 190_000, RemovedTokens: 12_000, MaxOutputTokens: 32_000, NextRunHeadroom: 5_000}, 12_000},
		{"cap after headroom", 2_000, with(func(in *config.V2SummaryBudgetInputs) { in.NextRunHeadroom = 4_000 }), 2_000},
		{"cap above the budget", 50_000, base, 24_000},
		{"headroom past the budget", 0, with(func(in *config.V2SummaryBudgetInputs) { in.NextRunHeadroom = 30_000 }), -6_000},
		// Overflow: the context is already past the window.
		{"overflow", 0, config.V2SummaryBudgetInputs{ContextWindow: 100_000, ReserveTokens: 16_000, TokensBefore: 130_000, RemovedTokens: 40_000}, -6_000},
		{"unknown window", 0, config.V2SummaryBudgetInputs{TokensBefore: 190_000, RemovedTokens: 12_000, MaxOutputTokens: 32_000}, 12_000},
		{"nothing removed", 0, with(func(in *config.V2SummaryBudgetInputs) { in.RemovedTokens = 0; in.TokensBefore = 100_000 }), 0},
		{"negative headroom ignored", 0, with(func(in *config.V2SummaryBudgetInputs) { in.NextRunHeadroom = -5_000 }), 24_000},
		// An unknown reserve fails closed to PiG's default, never to no reserve:
		// 200k - 16,384 - 190k + 30k = 23,616.
		{"unknown reserve", 0, with(func(in *config.V2SummaryBudgetInputs) { in.ReserveTokens = 0 }), 200_000 - config.PiGDefaultReserveTokens - 190_000 + 30_000},
		{"negative reserve", 0, with(func(in *config.V2SummaryBudgetInputs) { in.ReserveTokens = -1 }), 200_000 - config.PiGDefaultReserveTokens - 190_000 + 30_000},
		// Overflow with the reserve unknown still gives no budget.
		{"overflow, unknown reserve", 0, config.V2SummaryBudgetInputs{ContextWindow: 100_000, TokensBefore: 130_000, RemovedTokens: 40_000}, 100_000 - config.PiGDefaultReserveTokens - 130_000 + 40_000},
		{"cap below the output limit", 1_000, with(func(in *config.V2SummaryBudgetInputs) { in.MaxOutputTokens = 8_000 }), 1_000},
	}
	for _, c := range cases {
		capped.CompactionSummaryMaxTokens = c.cap
		if got := config.ResolveV2SummaryBudget(capped, c.in); got != c.want {
			t.Errorf("%s: budget %d, want %d", c.name, got, c.want)
		}
	}
}
