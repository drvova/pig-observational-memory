package harness

import (
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/legacyquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
)

// The origins om.Retired lists a retired defined query with.
const (
	// RetiredOriginLayer is a defined query stored as a tool definition of the layer.
	RetiredOriginLayer = "layer"
	// RetiredOriginLegacy is a definitions/*.rows file the layer refused to migrate.
	RetiredOriginLegacy = "legacy"
)

// RetiredQueries are the defined queries of the removed query language (docs/code-tool.md,
// "Migration"): every stored definition by name, with its query text and, for a pure read the
// strict formatter can translate, the equivalent snippet (legacyquery.Hint), else why there is
// none; then the legacy files the layer refused, with the refusal. None is ever registered,
// run, or turned into a kept unit, and no file is removed.
func RetiredQueries(store curated.Store) ([]om.RetiredQuery, error) {
	definitions, err := store.ListDefinitions()
	if err != nil {
		return nil, err
	}
	out := make([]om.RetiredQuery, 0, len(definitions))
	for _, d := range definitions {
		snippet, reason := legacyquery.Hint(d.Query)
		out = append(out, om.RetiredQuery{Name: d.Name, Origin: RetiredOriginLayer, Query: d.Query, Snippet: snippet, Reason: reason})
	}
	refused, err := store.RefusedDefinitions()
	if err != nil {
		return out, nil
	}
	for _, r := range refused {
		out = append(out, om.RetiredQuery{Name: r.Name, Origin: RetiredOriginLegacy, Reason: "not migrated into the layer: " + r.Reason})
	}
	return out, nil
}
