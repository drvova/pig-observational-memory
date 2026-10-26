package harness

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
)

// The harness's /om:status lines (docs/code-tool.md, "Order of work"), split by what they
// depend on: SharedStatusLines and ModelStatusLines never read the session model, so om.Info,
// which commands.StatusFigures builds from them, is the same under every session model;
// SessionStatusLines frames the session model's profile as /om:status always has.

// profileFigures are a profile's switches as one clause: its summary's codec, layout and room.
func profileFigures(p Profile, cfg config.Config) string {
	summary := "the whole room"
	if p.SummaryHalvings > 0 {
		summary = fmt.Sprintf("1/%d of the room", 1<<p.halvings())
	}
	return fmt.Sprintf("codec %s, layout %s, summary %s", p.Codec, p.SummaryFormat(cfg), summary)
}

// profileDetails are a profile's evidence since its last level-1 offer and the switches that
// made it, each with its reason.
func profileDetails(store curated.Store, key string, p Profile) []string {
	var lines []string
	if units, offered, err := rules.V2UnitsSinceProposal(store, key); err == nil {
		since := "since the start (no level-1 offer yet)"
		if offered {
			since = "since the last level-1 offer"
		}
		lines = append(lines, fmt.Sprintf("Evidence: %d compactions counted %s", units, since))
	}
	for _, s := range p.Switches {
		lines = append(lines, s.String())
	}
	return lines
}

// SharedStatusLines are the harness lines that belong to no model: the rules with their
// counts, the kernel floor, the layer kept for every model, and the retired defined queries
// (retiredStatusLine). None without a store.
func SharedStatusLines(h host.Host, cfg config.Config) []string {
	store, ok := StoreOf(h, cfg)
	if !ok {
		return nil
	}
	lines := []string{"── Harness ──"}
	lines = append(lines, rules.StatusLines(store, nil)...)
	lines = append(lines, FloorStatus(store))
	lines = append(lines, layerStatusLines(store, layer.AllModels)...)
	if line, ok := retiredStatusLine(store); ok {
		lines = append(lines, line)
	}
	return lines
}

// retiredStatusLine lists the retired defined queries (RetiredQueries, docs/code-tool.md,
// "Migration"): each name with its equivalent snippet, or why it has none (a write, a form with
// no equivalent, a legacy file the layer refused). They are never registered or run; the line
// says where to read them from code. false when there are none or the store is unreadable.
func retiredStatusLine(store curated.Store) (string, bool) {
	retired, err := RetiredQueries(store)
	if err != nil || len(retired) == 0 {
		return "", false
	}
	parts := make([]string, 0, len(retired))
	for _, q := range retired {
		if q.Snippet != "" {
			parts = append(parts, q.Name+": "+q.Snippet)
			continue
		}
		parts = append(parts, q.Name+" ("+q.Reason+")")
	}
	return fmt.Sprintf("Retired defined queries (%d; the query language was removed, so none is registered or run; om.Retired() lists them): %s", len(retired), strings.Join(parts, "; ")), true
}

// ModelStatusLines are one model's figures, computed from its own usage ledger and layer: its
// profile, the evidence since its last level-1 offer and the switches that made it, its
// detector counts and its layer. The same whichever model is the session model.
func ModelStatusLines(store curated.Store, cfg config.Config, key string) []string {
	p := Ledger{Store: store, Model: key}.Profile()
	lines := []string{fmt.Sprintf("Profile (%d events): %s", p.Events, profileFigures(p, cfg))}
	lines = append(lines, profileDetails(store, key, p)...)
	if line, ok := rules.DetectorCountsLine(store, key); ok {
		lines = append(lines, line)
	}
	if line, ok := rules.DetectorPassLine(store, key); ok {
		lines = append(lines, line)
	}
	if line, ok := codeCallsLine(store, key); ok {
		lines = append(lines, line)
	}
	return append(lines, layerStatusLines(store, key)...)
}

// codeCallsLine counts the model's memory tool code calls (ModelKindCode events) with the ones
// that failed (compile, runtime or crash errors) and the ones that were queries of the removed
// language (docs/code-tool.md, "Replacing the query language", decode-failure): the signal the
// code tool moved to /om:status. false when the model made none.
func codeCallsLine(store curated.Store, key string) (string, bool) {
	events, err := store.ModelEvents(key)
	if err != nil {
		return "", false
	}
	calls, errs, legacy := 0, 0, 0
	for _, e := range events {
		if e.Kind != curated.ModelKindCode {
			continue
		}
		calls++
		if e.Outcome == curated.OutcomeError {
			errs++
		}
		if e.Legacy {
			legacy++
		}
	}
	if calls == 0 {
		return "", false
	}
	return fmt.Sprintf("Code calls: %d (%d errors, %d queries of the removed language)", calls, errs, legacy), true
}

// UsageModelStatus are the models with usage events or a layer of their own in h's store, each
// with its figures (ModelStatusLines), by model name; none without a store.
func UsageModelStatus(h host.Host, cfg config.Config) []om.ModelStatus {
	store, ok := StoreOf(h, cfg)
	if !ok {
		return nil
	}
	models, err := store.UsageModels()
	if err != nil {
		return nil
	}
	if layered, err := store.Layer().Models(); err == nil {
		for _, m := range layered {
			if m != layer.AllModels && !slices.Contains(models, m) {
				models = append(models, m)
			}
		}
		sort.Strings(models)
	}
	out := make([]om.ModelStatus, 0, len(models))
	for _, m := range models {
		out = append(out, om.ModelStatus{Model: m, Lines: ModelStatusLines(store, cfg, m)})
	}
	return out
}

// SessionStatusLines are the session model's profile as /om:status frames it: the profile of
// the session model with the switches that made it and their reasons, and the evidence since
// its last level-1 offer. None without a store or a session model.
func SessionStatusLines(h host.Host, cfg config.Config) []string {
	store, ok := StoreOf(h, cfg)
	key := ModelKey(h.SessionModel())
	if !ok || key == "" {
		return nil
	}
	p := Ledger{Store: store, Model: key}.Profile()
	lines := []string{"── Session model ──", fmt.Sprintf("Profile (session model %s, %d events): %s", key, p.Events, profileFigures(p, cfg))}
	for _, l := range profileDetails(store, key, p) {
		lines = append(lines, "  "+l)
	}
	return lines
}
