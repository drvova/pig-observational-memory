package harness

import (
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
)

// StatusLines is the harness section of /om:status: the profile of the session model with the
// switches that made it and their reasons and the evidence since its last level-1 offer, then the active rules with their counts and
// each model's recent detector counts (internal/rules), the kernel floor and each model's
// layer (generation, held definitions, skipped files), then the defined tools and their
// standing. It is empty when there is no store.
func StatusLines(h host.Host, cfg config.Config) []string {
	store, ok := StoreOf(h, cfg)
	if !ok {
		return nil
	}
	lines := []string{"── Harness ──"}
	keys := []string{}
	if key := ModelKey(h.SessionModel()); key != "" {
		keys = append(keys, key)
		p := Ledger{Store: store, Model: key}.Profile()
		spec := "short spec"
		if p.LongSpec {
			spec = "long spec"
		}
		summary := "the whole room"
		if p.SummaryHalvings > 0 {
			summary = fmt.Sprintf("1/%d of the room", 1<<p.halvings())
		}
		lines = append(lines, fmt.Sprintf("Profile (session model %s, %d events): codec %s, layout %s, summary %s, %s", key, p.Events, p.Codec, p.SummaryFormat(cfg), summary, spec))
		if units, offered, err := rules.V2UnitsSinceProposal(store, key); err == nil {
			since := "since the start (no level-1 offer yet)"
			if offered {
				since = "since the last level-1 offer"
			}
			lines = append(lines, fmt.Sprintf("  Evidence: %d compactions counted %s", units, since))
		}
		if len(p.DisabledTools) > 0 {
			lines = append(lines, "  Defined tools off for this model: "+strings.Join(p.DisabledTools, ", "))
		}
		for _, s := range p.Switches {
			lines = append(lines, "  "+s.String())
		}
	}
	lines = append(lines, rules.StatusLines(store, keys)...)
	lines = append(lines, FloorStatus(store))
	for _, key := range append([]string{layer.AllModels}, keys...) {
		lines = append(lines, layerStatusLines(store, key)...)
	}
	if definitions, err := store.ListDefinitions(); err == nil && len(definitions) > 0 {
		parts := make([]string, 0, len(definitions))
		for _, d := range definitions {
			s := StandingOf(store, d.Name)
			parts = append(parts, fmt.Sprintf("%s %s (%d calls: %d with results, %d empty, %d errors)", d.Name, s.State, s.Calls, s.Results, s.Empty, s.Errors))
		}
		lines = append(lines, "Defined tools: "+strings.Join(parts, "; "))
	}
	if refused, err := store.RefusedDefinitions(); err == nil {
		for _, r := range refused {
			lines = append(lines, strings.TrimPrefix(r.Message(), "Observational memory: "))
		}
	}
	return lines
}
