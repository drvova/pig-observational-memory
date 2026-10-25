package config_test

import (
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
)

// consolidate defaults to on and is read through the trusted path, so an untrusted project's
// settings never turn level 1 off; a non-boolean value is ignored.
func TestConsolidate(t *testing.T) {
	f := setup(t)
	if got := f.load(env{}); !got.Consolidates() {
		t.Fatal("consolidate is off by default")
	}
	f.writeJSON(t, f.global(), map[string]any{"observational-memory": map[string]any{"consolidate": true}})
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"consolidate": false}})
	if got := f.loadAs(config.ProjectTrusted, env{}); got.Consolidates() {
		t.Fatal("a trusted project's consolidate: false ignored")
	}
	for _, trust := range []config.TrustState{config.ProjectUntrusted, config.ProjectTrustUnknown} {
		if got := f.loadAs(trust, env{}); !got.Consolidates() {
			t.Fatalf("%s project turned level 1 off", trust)
		}
	}
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"consolidate": "no"}})
	if got := f.load(env{}); !got.Consolidates() {
		t.Fatal("a non-boolean consolidate applied")
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

// The removed settings are listed while a settings file read sets them to a value memory v1
// applied (an invalid one was ignored by v1 as well), in the Migration table's order, once
// each; an untrusted project's are never read. A stale memoryEngine, v1 or v2, is one of them:
// v2 is the only engine, so the setting is ignored and listed.
func TestRemovedSettingsListed(t *testing.T) {
	f := setup(t)
	if got := f.load(env{}); len(got.RemovedSet) != 0 {
		t.Fatalf("defaults list removed settings: %v", got.RemovedSet)
	}
	f.writeJSON(t, f.global(), map[string]any{"observational-memory": map[string]any{"model": map[string]any{"id": "x"}, "observeAfterTokens": 100, "memoryFormat": "lines"}})
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"fallbackModel": map[string]any{"id": "y"}, "agentMaxTurns": -1, "observeAfterTokens": 5, "compactionSummaryMaxTokens": 900}})
	got := f.loadAs(config.ProjectTrusted, env{})
	if want := []string{"observeAfterTokens", "model", "fallbackModel"}; !equalStrings(got.RemovedSet, want) {
		t.Fatalf("removed settings %v, want %v", got.RemovedSet, want)
	}
	if got := f.loadAs(config.ProjectUntrusted, env{}); !equalStrings(got.RemovedSet, []string{"observeAfterTokens", "model"}) {
		t.Fatalf("untrusted project's removed settings read: %v", got.RemovedSet)
	}
	// The removed model setting's value is kept to migrate v1's worker layer; the project's
	// overrides the global one only when trusted.
	if w := got.RemovedWorkerModel; w == nil || w.ID != "x" || w.Provider != "" {
		t.Fatalf("worker model %+v", w)
	}
	f.writeJSON(t, f.project(), map[string]any{"observational-memory": map[string]any{"model": map[string]any{"provider": "anthropic", "id": "haiku"}}})
	if w := f.loadAs(config.ProjectTrusted, env{}).RemovedWorkerModel; w == nil || w.Provider != "anthropic" || w.ID != "haiku" {
		t.Fatalf("trusted project's worker model %+v", w)
	}
	if w := f.loadAs(config.ProjectUntrusted, env{}).RemovedWorkerModel; w == nil || w.ID != "x" {
		t.Fatalf("untrusted project's worker model read: %+v", w)
	}
	for _, c := range []struct {
		value any
		set   bool
	}{{"v1", true}, {"v2", true}, {"v3", false}, {2, false}} {
		f.writeJSON(t, f.project(), map[string]any{})
		f.writeJSON(t, f.global(), map[string]any{"observational-memory": map[string]any{"memoryEngine": c.value, "compactionCatchUpMaxChunks": 0, "compactAfterTokensRatio": 1.5}})
		got := f.load(env{})
		want := []string{"compactionCatchUpMaxChunks"}
		if c.set {
			want = append(want, "memoryEngine")
		}
		if !equalStrings(got.RemovedSet, want) {
			t.Fatalf("memoryEngine %v: removed settings %v, want %v", c.value, got.RemovedSet, want)
		}
		if g := got; g.Passive || !g.Consolidates() || g.CompactionSummaryMaxTokens != 0 {
			t.Fatalf("memoryEngine %v changed the configuration: %+v", c.value, g)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
