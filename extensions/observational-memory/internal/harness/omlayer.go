package harness

import (
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omhost"
)

// The code tool's layer access (docs/code-tool.md, "Writes"): om.LayerForget and
// om.LayerRollback reach the same code as the memory tool's harness forget and rollback writes
// (layerWriter), on the session model's layer through layer.Store, never on a rule or another
// model's layer, and their result names the model; om.Layer lists every model's layer, so the
// listing never depends on the session model.

// omLayer is omhost.Layer over layerWriter.
type omLayer struct {
	w layerWriter
}

// OMLayer is the layer access of a code tool call made under the session model key session
// (ModelKey), in store; h tells the user of each change. The session model's name appears only
// as data in a write's result.
func OMLayer(h host.ToolHost, store curated.Store, session string) omhost.Layer {
	return omLayer{w: layerWriter{h: h, store: store, session: session}}
}

// Forget forgets one definition from the session model's layer. A retired defined tool is
// listed by om.Retired and stays stored; a rule is the harness's.
func (l omLayer) Forget(kind, name string) (string, error) {
	if strings.TrimSpace(kind) == layer.KindTool {
		return "", fmt.Errorf("defined tools are retired and stay stored (om.Retired lists %s); nothing is forgotten from the layer", strings.TrimSpace(name))
	}
	if l.w.session == "" {
		return "", fmt.Errorf("no session model is known, so there is no layer to forget %s/%s from", kind, name)
	}
	return l.w.Forget(kind, name)
}

// Rollback makes the session model's layer what it was at generation gen (the old rollback N's
// meaning, which the result text names), rules left as they are.
func (l omLayer) Rollback(gen int) (string, error) {
	if l.w.session == "" {
		return "", fmt.Errorf("no session model is known, so there is no layer to roll back")
	}
	return l.w.Rollback(gen, "")
}

// Listing is every model's layer (AllLayerRows).
func (l omLayer) Listing() ([]om.LayerRow, error) { return AllLayerRows(l.w.store) }

// AllLayerRows lists the shared layer (seeds and the definitions kept for every model, Model
// "") and then every model's own layer found in the store, by model name, each row labelled with
// its model: the same rows whichever model is the session model.
func AllLayerRows(store curated.Store) ([]om.LayerRow, error) {
	rows, err := LayerRows(store, layer.AllModels)
	if err != nil {
		return nil, err
	}
	models, err := store.Layer().Models()
	if err != nil {
		return nil, err
	}
	for _, m := range models {
		if m == layer.AllModels {
			continue
		}
		own, err := LayerRows(store, m)
		if err != nil {
			return nil, err
		}
		for _, r := range own {
			// The seeds are the shared layer's, listed once above.
			if r.Model == m && r.State != layer.StateSeed {
				rows = append(rows, r)
			}
		}
	}
	for i := range rows {
		if rows[i].Model == layer.AllModels {
			rows[i].Model = ""
		}
	}
	return rows, nil
}
