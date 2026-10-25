package harness

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/declare"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/memquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
)

// The memory tool's harness and rollback writes and its harness source (docs/memory-interface.md,
// section 8). A definition comes only from the model's own call: it is refused when its text is
// found in tool output on the branch (rules.CheckFromModel), checked by the kernel and its
// kind's interpreter (layer.Store.Check, inside Hold), and held until a clean pass proves it.

// DefinitionModel is the model a definition of kind is kept for, given the session and the
// memory-worker models: the session model reads the summary, so a summary is the session
// model's; the worker writes the notes, so every other kind is the worker's (the model whose
// passes prove and judge it, as a proposed rule is attached to it).
func DefinitionModel(kind, session, worker string) string {
	if kind == layer.KindSummary || worker == "" {
		return session
	}
	return worker
}

// CheckModelDefinition is what every definition from the model must pass before it is held:
// a kind the model may define here (a tool is made with define, a rule with propose), the
// provenance check against the branch (rules.CheckFromModel), and the layer's checks. Under
// v1 (a store not V2) the consolidation instructions are refused: they shape memory v2's level-1
// request, which only that request proposes (docs/memory-v2.md, "Self-built harness"), and no
// v1 pass runs with them or could settle them.
func CheckModelDefinition(store curated.Store, branch []ledger.Entry, d layer.Definition) error {
	switch {
	case !store.V2 && d.Kind == layer.KindInstructions && d.Name == agents.InstructionsConsolidation:
		return ErrConsolidationV2Only
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

// ErrConsolidationV2Only refuses the consolidation instructions outside memory v2: they are
// proposed only inside memory v2's level-1 request.
var ErrConsolidationV2Only = fmt.Errorf("refused: instructions %q shape memory v2's level-1 request and are proposed only inside it (memoryEngine v2); no memory v1 pass runs with them", agents.InstructionsConsolidation)

// layerWriter is the memory tool's Layerer for one call.
type layerWriter struct {
	h               host.ToolHost
	store           curated.Store
	session, worker string
	entries         []ledger.Entry
}

func (w layerWriter) notify(message string) { w.h.Notify(message, "info") }

// ErrDefinedInLevel1 refuses the memory tool's harness and propose writes under memory v2,
// where the model defines part of its harness only inside the compaction's level-1 request
// (docs/memory-v2.md, "Self-built harness"); forget and rollback still apply.
var ErrDefinedInLevel1 = errors.New("refused: under memoryEngine v2 harness definitions and rules are proposed only inside the compaction's consolidation request, when your harness evidence changed; forget and rollback still apply")

// Define holds the model's definition of kind named name; under memory v2 it is refused
// (ErrDefinedInLevel1).
func (w layerWriter) Define(kind, name, text string) (string, error) {
	if w.store.V2 {
		return "", ErrDefinedInLevel1
	}
	kind, name = strings.TrimSpace(kind), strings.TrimSpace(name)
	if !layer.ValidKind(kind) {
		return "", fmt.Errorf("unknown definition kind %q; kinds are %s", kind, strings.Join(layer.Kinds, ", "))
	}
	model := DefinitionModel(kind, w.session, w.worker)
	d := layer.Definition{Kind: kind, Name: name, Model: model, Text: text, Session: w.h.SessionID()}
	if err := CheckModelDefinition(w.store, w.entries, d); err != nil {
		return "", err
	}
	held, err := w.store.Layer().Hold(d)
	if err != nil {
		return "", err
	}
	what := held.Relation()
	w.notify(fmt.Sprintf("Observational memory: %s defined %s/%s (%s); held until its next pass is clean", model, kind, name, what))
	return fmt.Sprintf("Held %s/%s for %s (%s). It applies to the next memory pass and is written to your layer once that pass completes with no kernel error, decode failure or invariant refusal; otherwise it is reverted and what stood before stands.", kind, name, model, what), nil
}

// Forget removes the model's definition of kind named name: only a kind the model may define
// with the harness write (CheckModelDefinition). A tool is forgotten with forget name=..., which
// unregisters it; a rule is the harness's to establish or retire, so the model cannot remove one.
func (w layerWriter) Forget(kind, name string) (string, error) {
	kind, name = strings.TrimSpace(kind), strings.TrimSpace(name)
	switch {
	case kind == layer.KindTool:
		return "", fmt.Errorf("a defined tool is forgotten with forget name=%s, which unregisters it", name)
	case rules.IsRule(kind, name):
		return "", fmt.Errorf("refused: %s is a rule, which the harness establishes or retires by its detector's counts; a remedy definition you can forget is a catalogue (%s)", name, strings.Join(rules.Remedies, ", "))
	}
	model := DefinitionModel(kind, w.session, w.worker)
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

// Rollback rolls a model's layer back to generation n: model, else the worker's. The rules
// (rules.IsRule) are left as they are: the harness wrote and decided them, so no rollback of the
// model's removes, restores or drops one.
func (w layerWriter) Rollback(n int, model string) (string, error) {
	model = strings.TrimSpace(model)
	switch {
	case model == "":
		model = DefinitionModel(layer.KindInstructions, w.session, w.worker)
	case model != w.session && model != w.worker:
		return "", fmt.Errorf("model=%q is neither the session model (%s) nor the memory worker (%s); you can roll back only your own harness", model, w.session, w.worker)
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

// Listing is the session and worker models' layers as the harness source lists them.
func (w layerWriter) Listing() ([]memquery.HarnessRow, error) {
	models := []string{w.session}
	if w.worker != "" && w.worker != w.session {
		models = append(models, w.worker)
	}
	out := []memquery.HarnessRow{}
	for _, model := range models {
		rows, err := LayerRows(w.store, model)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
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
func LayerRows(store curated.Store, model string) ([]memquery.HarnessRow, error) {
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
	rows := map[string]memquery.HarnessRow{}
	for key := range seeds {
		kind, name, _ := strings.Cut(key, "/")
		rows[key] = memquery.HarnessRow{Model: model, Kind: kind, Name: name, State: layer.StateSeed, Origin: layer.StateSeed}
	}
	for _, d := range h.List("") {
		row := memquery.HarnessRow{Model: d.Model, Kind: d.Kind, Name: d.Name, State: d.State, Origin: "own"}
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
	out := make([]memquery.HarnessRow, 0, len(keys)+len(reports))
	for _, key := range keys {
		out = append(out, rows[key])
	}
	for _, r := range reports {
		row := memquery.HarnessRow{Model: r.Model, Kind: r.Kind, Name: r.Name, State: "skipped", Origin: "own"}
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

// --- reports and notifications ---------------------------------------------------------

var (
	reportedMu sync.Mutex
	reported   = map[string]bool{}
)

// NotifyReports tells the user of every layer file a load skipped (refused, or a stale override
// whose seed now stands), once per process for each report: the layer is loaded on every pass.
func NotifyReports(h host.Host, reports []layer.Report) {
	for _, r := range reports {
		message := r.Message()
		reportedMu.Lock()
		seen := reported[message]
		reported[message] = true
		reportedMu.Unlock()
		if !seen && h.HasUI() {
			h.Notify(message, "warning")
		}
	}
}

// LayerReportsFor is model's effective layer in h's store (LayerFor) with the reports of every
// file its load skipped; the seeds alone, and no reports, without a store.
func LayerReportsFor(h host.Host, cfg config.Config, model host.Model) (declare.Active, []layer.Report) {
	l, ok := For(h, cfg, model)
	if !ok {
		return declare.SeedsOnly(ModelKey(model)), nil
	}
	MigrateInstructions(h, l.Store, l.Model)
	loaded, reports, err := l.Store.Layer().Load(l.Model)
	if err != nil {
		return declare.SeedsOnly(l.Model), nil
	}
	return declare.Of(loaded), reports
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
