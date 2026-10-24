package harness

import (
	"fmt"
	"strings"
	"sync"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
)

// floorState is one store's kernel floor in this process, and why the last configured lowering
// was refused.
type floorState struct {
	floor   *kernel.Floor
	refusal error
}

var (
	floorsMu sync.Mutex
	floors   = map[string]*floorState{}
)

// floorOf is the kernel floor of store in this process, brought in line with the configured
// request (docs/memory-interface.md, section 8): lowered when the user's settings ask, with who
// asked and why recorded in the layer's floor record before it takes effect (again whenever
// who or why changes), and engaged again when they stop asking. A request the floor refuses (no who or why, or a record that cannot be
// written) leaves it engaged, and FloorStatus says why.
func floorOf(store curated.Store, request *config.FloorRequest) *kernel.Floor {
	floorsMu.Lock()
	defer floorsMu.Unlock()
	state := floors[store.Dir]
	if state == nil {
		state = &floorState{floor: &kernel.Floor{Record: store.Layer().RecordFloor, Now: store.Now}}
		floors[store.Dir] = state
	}
	current, lowered := state.floor.Lowered()
	switch {
	case request != nil && !lowered:
		state.refusal = state.floor.Lower(request.RequestedBy, request.Reason)
	case request != nil && !sameRequest(current, request):
		// A changed request is engaged first and then lowered anew, so it is recorded before
		// it takes effect, and one the floor refuses leaves it engaged rather than lowered
		// under the earlier request.
		_ = state.floor.Engage()
		state.refusal = state.floor.Lower(request.RequestedBy, request.Reason)
	case request == nil && lowered:
		state.refusal = nil
		_ = state.floor.Engage()
	case request == nil:
		state.refusal = nil
	}
	return state.floor
}

// sameRequest reports whether the floor's current lowering is the one request asks for.
func sameRequest(current kernel.Lowering, request *config.FloorRequest) bool {
	return current.RequestedBy == strings.TrimSpace(request.RequestedBy) && current.Reason == strings.TrimSpace(request.Reason)
}

// FloorStatus is the kernel floor line of /om:status.
func FloorStatus(store curated.Store) string {
	floorsMu.Lock()
	state := floors[store.Dir]
	floorsMu.Unlock()
	if state == nil {
		return "Kernel floor: engaged"
	}
	if l, lowered := state.floor.Lowered(); lowered {
		return fmt.Sprintf("Kernel floor: LOWERED by %s at %s (%s)", l.RequestedBy, l.Time, l.Reason)
	}
	if state.refusal != nil {
		return "Kernel floor: engaged; the configured lowering was refused: " + state.refusal.Error()
	}
	return "Kernel floor: engaged"
}

// Instructions are the model's own instructions for a worker stage from its layer (an
// instructions definition named after the stage, held or written), "" when the seed stands. A
// stale replacement is skipped, so the seed stands. The worker prompt puts them after the
// kernel's fixed rules (agents.ComposeStage): after the seed, or in its place for a replacement.
func (l Ledger) Instructions(stage string) string {
	return l.Layer().Instructions(stage)
}

// layerStatusLines are a model's harness lines of /om:status (docs/memory-interface.md, section
// 8): its generation and its last build or revision pass, its definitions by kind with their
// state (held, written, probation, established), its held definitions, and every file its load
// skipped (a stale override, whose seed stands, or a refused file).
func layerStatusLines(store curated.Store, model string) []string {
	ls := store.Layer()
	rows, err := LayerRows(store, model)
	if err != nil {
		return []string{fmt.Sprintf("Layer (%s): unreadable: %v", model, err)}
	}
	gens, _ := ls.Generations(model)
	byKind := map[string][]string{}
	held, stale, skipped := []string{}, []string{}, []string{}
	defined := 0
	for _, r := range rows {
		key := r.Kind + "/" + r.Name
		switch {
		case r.State == "skipped" && r.Stale != "":
			stale = append(stale, fmt.Sprintf("%s (%s)", key, r.Stale))
			continue
		case r.State == "skipped":
			skipped = append(skipped, key)
			continue
		case r.State == layer.StateSeed:
			continue
		case r.State == layer.StateHeld:
			held = append(held, key)
		}
		defined++
		item := r.Name + " " + r.State
		if r.Generation != "" {
			item += " gen " + r.Generation
		}
		switch r.Origin {
		case "override":
			item += ", overrides the seed"
		case OriginExtends:
			item += ", extends the seed"
		case OriginReplaces:
			item += ", replaces the seed (mode replace)"
		}
		if r.Model != model {
			item += ", for all models"
		}
		byKind[r.Kind] = append(byKind[r.Kind], item)
	}
	last, built, _ := ls.LastPass(model)
	if len(gens) == 0 && defined == 0 && len(stale)+len(skipped) == 0 && !built {
		return nil
	}
	line := fmt.Sprintf("Layer (%s): generation %d, %d definitions", model, len(gens), defined)
	switch {
	case built && last.Op == layer.OpRevise:
		line += fmt.Sprintf(", last revised at generation %d", last.N)
	case built:
		line += fmt.Sprintf(", built at generation %d", last.N)
	case model != layer.AllModels:
		line += ", no build pass yet"
	}
	if len(held) > 0 {
		line += ", held: " + strings.Join(held, ", ")
	}
	lines := []string{line}
	for _, kind := range layer.Kinds {
		if items := byKind[kind]; len(items) > 0 {
			lines = append(lines, "  "+kind+": "+strings.Join(items, "; "))
		}
	}
	for _, s := range stale {
		lines = append(lines, "  stale override skipped, the seed stands: "+s)
	}
	_, reports, _ := ls.Load(model)
	for _, r := range reports {
		if !r.Stale {
			lines = append(lines, "  "+strings.TrimPrefix(r.Message(), "Observational memory: "))
		}
	}
	return lines
}
