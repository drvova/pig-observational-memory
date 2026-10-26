package harness

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omhost"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
)

// The memory tool's harness writes (forget and rollback) and its harness source
// (docs/memory-interface.md, section 8). A definition is proposed only inside the compaction's
// level-1 request (docs/memory-v2.md, "Self-built harness"), where it passes
// CheckModelDefinition and is held until a later compaction's evidence settles it.

// CheckModelDefinition is what every definition from the model must pass before it is held:
// a kind the model may define (a tool is made with define, a rule is proposed), the provenance
// check against the branch (rules.CheckFromModel), and the layer's checks.
func CheckModelDefinition(store curated.Store, branch []ledger.Entry, d layer.Definition) error {
	switch {
	case d.Kind == layer.KindTool:
		return errors.New("a tool is defined with define <name> = <query> \"what it returns\", which registers it")
	case rules.IsRule(d.Kind, d.Name):
		return fmt.Errorf("a remedy definition is a catalogue named after a catalogue remedy (%s); a rule is proposed with propose", strings.Join(rules.Remedies, ", "))
	}
	if err := store.Layer().Check(d); err != nil {
		return err
	}
	return rules.CheckFromModel(branch, d)
}

// layerWriter is the memory tool's Layerer for one call.
type layerWriter struct {
	h     host.ToolHost
	store curated.Store
	// session is the session model's key: the model whose harness the memory tool reads,
	// forgets in and rolls back.
	session string
	// worker is the key memory v1 kept its memory-worker model's definitions under
	// (WorkerModelKey), "" for none: listed beside the session's, and rolled back with
	// model=worker, so the definitions v1 wrote there stay reachable.
	worker string
}

func (w layerWriter) notify(message string) { w.h.Notify(message, "info") }

// ErrDefinedInLevel1 refuses the memory tool's harness and propose writes, and om.Propose: the
// model defines part of its harness only inside the compaction's level-1 request
// (docs/memory-v2.md, "Self-built harness"); forget and rollback still apply. It is
// omhost.ErrDefinedInLevel1, so both tools answer with one error.
var ErrDefinedInLevel1 = omhost.ErrDefinedInLevel1

// Define refuses the memory tool's harness write (ErrDefinedInLevel1).
func (w layerWriter) Define(kind, name, text string) (string, error) {
	return "", ErrDefinedInLevel1
}

// Forget removes the session model's definition of kind named name: only a kind the model may
// define (CheckModelDefinition). A tool is forgotten with forget name=..., which
// unregisters it; a rule is the harness's to establish or retire, so the model cannot remove one.
func (w layerWriter) Forget(kind, name string) (string, error) {
	kind, name = strings.TrimSpace(kind), strings.TrimSpace(name)
	switch {
	case kind == layer.KindTool:
		return "", fmt.Errorf("a defined tool is forgotten with forget name=%s, which unregisters it", name)
	case rules.IsRule(kind, name):
		return "", fmt.Errorf("refused: %s is a rule, which the harness establishes or retires by its detector's counts; a remedy definition you can forget is a catalogue (%s)", name, strings.Join(rules.Remedies, ", "))
	}
	model := w.session
	seed, seeded, err := w.store.Layer().Forget(model, kind, name, w.h.SessionID())
	if errors.Is(err, layer.ErrNotFound) {
		return "", fmt.Errorf("%s has no %s definition %q to forget", model, kind, name)
	}
	if err != nil {
		return "", err
	}
	stands := "nothing stands for it"
	if seeded {
		stands = "the seed " + seed.Key() + " stands again"
	}
	w.notify(fmt.Sprintf("Observational memory: %s forgot %s/%s; %s", model, kind, name, stands))
	return fmt.Sprintf("Forgot %s/%s for %s; %s.", kind, name, model, stands), nil
}

// Rollback rolls the session model's layer back to generation n (model, when given, must name
// it, or memory v1's memory-worker model, whose layer v1 kept the session's definitions in). The rules (rules.IsRule) are left as they are: the harness wrote and decided them, so no
// rollback of the model's removes, restores or drops one.
func (w layerWriter) Rollback(n int, model string) (string, error) {
	model = strings.TrimSpace(model)
	switch {
	case model == "":
		model = w.session
	case model != w.session && (w.worker == "" || model != w.worker):
		if w.worker != "" {
			return "", fmt.Errorf("model=%q is neither the session model (%s) nor memory v1's memory model (%s); you can roll back only your own harness", model, w.session, w.worker)
		}
		return "", fmt.Errorf("model=%q is not the session model (%s); you can roll back only your own harness", model, w.session)
	}
	gen, changes, err := w.store.Layer().RollbackKeeping(model, n, w.h.SessionID(), rules.IsRule)
	if err != nil {
		return "", err
	}
	message := RollbackMessage(model, gen, changes)
	w.notify(message)
	return strings.TrimPrefix(message, "Observational memory: "), nil
}

// RollbackMessage is a rollback as the user is told it.
func RollbackMessage(model string, gen layer.Generation, changes []layer.Change) string {
	parts := make([]string, 0, len(changes))
	for _, c := range changes {
		if c.Removed {
			parts = append(parts, c.Kind+"/"+c.Name+" removed")
		} else {
			parts = append(parts, c.Kind+"/"+c.Name+" restored")
		}
	}
	changed := "nothing changed"
	if len(parts) > 0 {
		changed = strings.Join(parts, ", ")
	}
	return fmt.Sprintf("Observational memory: %s's harness rolled back to generation %d as generation %d (%s; held definitions dropped)", model, gen.Target, gen.N, changed)
}

// Listing is the session model's layer as the harness source lists it, then the definitions
// memory v1 wrote for its memory-worker model (worker), which only a rollback with model=worker
// changes.
func (w layerWriter) Listing() ([]om.LayerRow, error) {
	rows, err := LayerRows(w.store, w.session)
	if err != nil || w.worker == "" {
		return rows, err
	}
	workerRows, err := LayerRows(w.store, w.worker)
	if err != nil {
		return nil, err
	}
	for _, r := range workerRows {
		if r.Model == w.worker {
			rows = append(rows, r)
		}
	}
	return rows, nil
}

// The origins the harness source lists an instructions definition of a seed's name with:
// OriginExtends for an addition appended after the seed, OriginReplaces for a replacement in
// its place. Any other definition of a seed's name is an "override".
const (
	OriginExtends  = "extends"
	OriginReplaces = "replaces"
)

// OriginOf is the origin of d, a definition of a seed's name: an instructions definition
// extends the seed (also one recorded before modes) or replaces it; any other overrides it.
func OriginOf(d layer.Definition) string {
	switch {
	case d.Replaces():
		return OriginReplaces
	case d.Kind == layer.KindInstructions:
		return OriginExtends
	}
	return "override"
}

// The probation states a written definition is listed with.
const (
	stateProbation   = "probation"
	stateEstablished = "established"
)

// LayerRows lists model's layer: every seed and every effective definition (held or written,
// kept for model or for every model) by kind and name, then every file its load skipped, a stale
// override with the seed standing in its place. A written definition probation judges, and a
// written instructions replacement of the seed until probation decides it, is listed in its
// probation state.
func LayerRows(store curated.Store, model string) ([]om.LayerRow, error) {
	ls := store.Layer()
	h, reports, err := ls.Load(model)
	if err != nil {
		return nil, err
	}
	history, err := layerHistory(ls, model)
	if err != nil {
		return nil, err
	}
	seeds := layer.Shipped()
	rows := map[string]om.LayerRow{}
	for key := range seeds {
		kind, name, _ := strings.Cut(key, "/")
		rows[key] = om.LayerRow{Model: model, Kind: kind, Name: name, State: layer.StateSeed, Origin: layer.StateSeed, Text: seeds[key]}
	}
	for _, d := range h.List("") {
		row := om.LayerRow{Model: d.Model, Kind: d.Kind, Name: d.Name, State: d.State, Origin: "own", Engine: d.Engine, Text: d.Text}
		if _, ok := seeds[d.Key()]; ok {
			row.Origin = OriginOf(d)
		}
		if d.State == layer.StateWritten {
			if w, ok := history[d.Key()]; ok {
				row.Generation = strconv.Itoa(w.Generation)
				switch {
				case w.Established:
					row.State = stateEstablished
				case !w.Decided && (rules.JudgedKind(d.Kind, d.Name) || d.Replaces()):
					row.State = stateProbation
				}
			}
		}
		rows[d.Key()] = row
	}
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]om.LayerRow, 0, len(keys)+len(reports))
	for _, key := range keys {
		out = append(out, rows[key])
	}
	for _, r := range reports {
		row := om.LayerRow{Model: r.Model, Kind: r.Kind, Name: r.Name, State: "skipped", Origin: "own"}
		if _, ok := seeds[layer.SeedKey(r.Kind, r.Name)]; ok {
			row.Origin = "override"
		}
		if r.Stale {
			row.Stale = r.Reason
		}
		out = append(out, row)
	}
	return out, nil
}

// layerHistory reads model's manifest (and every model's): what wrote each name's written
// content (layer.Store.WrittenBy), the model's own over every model's.
func layerHistory(ls layer.Store, model string) (map[string]layer.Written, error) {
	out := map[string]layer.Written{}
	models := []string{layer.AllModels}
	if model != layer.AllModels {
		models = append(models, model)
	}
	for _, m := range models {
		written, err := ls.WrittenBy(m)
		if err != nil {
			return nil, err
		}
		for key, w := range written {
			out[key] = w
		}
	}
	return out, nil
}

// MigrateInstructions makes the instructions definitions recorded before modes, for model and
// for every model (the layers model's harness loads), additions (layer.Store.MigrateInstructions:
// each recorded as a generation with its reason), and tells the user of each. A failure is
// logged by nothing and changes nothing: such a definition still reads as an addition.
func MigrateInstructions(h host.Host, store curated.Store, model string) {
	models := []string{layer.AllModels}
	if model != layer.AllModels && model != "" {
		models = append(models, model)
	}
	for _, m := range models {
		migrated, err := store.Layer().MigrateInstructions(m)
		if err != nil {
			continue
		}
		for _, mg := range migrated {
			if h.HasUI() {
				h.Notify(mg.Message(), "info")
			}
		}
	}
}

// MigrateLayerV2 migrates the written v1 worker instructions, for model and for every model, to
// memory v2 (layer.Store.MigrateToV2, docs/memory-v2.md, "Migration > Layer definitions"): the
// observer's and reflector's additions fold into the consolidation instructions as an
// addition, and a replace-mode text and the dropper's are retired, and tells the user of each.
// worker is the key memory v1 kept its memory-worker model's definitions under
// (WorkerModelKey), "" for none: its own additions fold into model's consolidation
// instructions too (layer.Store.MigrateWorkerToV2), since v1 kept every kind but a summary
// for the worker. The v1 files stay as they are (unused, their text kept in the layer's
// history). It makes no request. A failure changes nothing and is retried at the next session
// start.
func MigrateLayerV2(h host.Host, store curated.Store, model, worker string) []layer.V2Migration {
	MigrateInstructions(h, store, model)
	models := []string{layer.AllModels}
	if model != layer.AllModels && model != "" {
		models = append(models, model)
	}
	extend := []string{agents.InstructionsObserver, agents.InstructionsReflector}
	retire := []string{agents.InstructionsDropper}
	var out []layer.V2Migration
	report := func(migrated []layer.V2Migration, err error) {
		if err != nil {
			return
		}
		for _, mg := range migrated {
			if h.HasUI() {
				h.Notify(mg.Message(), "info")
			}
		}
		out = append(out, migrated...)
	}
	for _, m := range models {
		report(store.Layer().MigrateToV2(m, agents.InstructionsConsolidation, extend, retire))
	}
	if model != layer.AllModels && model != "" {
		report(store.Layer().MigrateWorkerToV2(model, worker, agents.InstructionsConsolidation, extend, retire))
	}
	return out
}
