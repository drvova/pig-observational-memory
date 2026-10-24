package harness

import (
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
)

// StatusLines is the harness section of /om:status: the profile of the session model and, when
// the configuration names another memory-worker model, of that one (only the codec and spec it
// writes with: the summary and the defined tools follow the session model's profile), each
// with the switches that made it and their reasons, then the active rules with their counts and
// each model's recent detector counts (internal/rules), the kernel floor and each model's
// layer (generation, held definitions, skipped files), then the defined tools and their
// standing. It is empty when there is no store.
func StatusLines(h host.Host, cfg config.Config) []string {
	store, ok := StoreOf(h, cfg)
	if !ok {
		return nil
	}
	session := h.SessionModel()
	models := []host.Model{session}
	if cfg.Model != nil && cfg.Model.ID != "" {
		provider := cfg.Model.Provider
		if provider == "" {
			provider = host.ModelProvider(session)
		}
		models = append(models, host.Model{"provider": provider, "id": cfg.Model.ID})
	}
	lines := []string{"── Harness ──"}
	seen := map[string]bool{}
	for i, model := range models {
		key := ModelKey(model)
		if key == "" || seen[key] {
			continue
		}
		seen[key] = true
		role := "session model"
		if i > 0 {
			role = "memory worker"
		}
		p := Ledger{Store: store, Model: key}.Profile()
		spec := "short spec"
		if p.LongSpec {
			spec = "long spec"
		}
		summary := "the whole room"
		if p.SummaryHalvings > 0 {
			summary = fmt.Sprintf("1/%d of the room", 1<<p.halvings())
		}
		if i > 0 {
			// A separate worker only writes rows: the summary layout and size and the
			// defined tools are the session model's, so only its codec and spec apply.
			lines = append(lines, fmt.Sprintf("Profile (%s %s, %d events): codec %s, %s", role, key, p.Events, p.Codec, spec))
			for _, s := range p.Switches {
				if s.Setting == SettingCodec || s.Setting == SettingSpec {
					lines = append(lines, "  "+s.String())
				}
			}
			continue
		}
		lines = append(lines, fmt.Sprintf("Profile (%s %s, %d events): codec %s, layout %s, summary %s, %s", role, key, p.Events, p.Codec, p.SummaryFormat(cfg), summary, spec))
		if len(p.DisabledTools) > 0 {
			lines = append(lines, "  Defined tools off for this model: "+strings.Join(p.DisabledTools, ", "))
		}
		for _, s := range p.Switches {
			lines = append(lines, "  "+s.String())
		}
	}
	keys := make([]string, 0, len(models))
	for _, model := range models {
		if key := ModelKey(model); key != "" && !contains(keys, key) {
			keys = append(keys, key)
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
