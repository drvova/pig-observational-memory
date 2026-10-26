package harness

import (
	"slices"
	"sort"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/declare"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omtool"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
)

// ModelKey is the id a model's ledger and profile are kept under: provider/id, or the id
// alone when the provider is unknown; "" for no model.
func ModelKey(model host.Model) string {
	id := host.ModelID(model)
	if id == "" {
		return ""
	}
	if provider := host.ModelProvider(model); provider != "" {
		return provider + "/" + id
	}
	return id
}

// WorkerModelKey is the key memory v1 kept its memory-worker model's definitions under, read
// from the removed `model` setting (config.Config.RemovedWorkerModel) as v1 resolved it: the
// registry's model for the setting's provider (else the session model's) and id when the
// registry has it, else provider/id; "" with no such setting or when it names the session
// model, whose layer is migrated anyway. It is read only to migrate and list that layer.
func WorkerModelKey(h host.Host, cfg config.Config) string {
	ref := cfg.RemovedWorkerModel
	if ref == nil || ref.ID == "" {
		return ""
	}
	session := h.SessionModel()
	provider := ref.Provider
	if provider == "" {
		provider = host.ModelProvider(session)
	}
	key := ref.ID
	if provider != "" {
		key = provider + "/" + ref.ID
	}
	if models := h.Models(); models != nil {
		if configured := models.Find(provider, ref.ID); configured != nil {
			key = ModelKey(configured)
		}
	}
	if key == ModelKey(session) {
		return ""
	}
	return key
}

// StoreOf is the curated store the harness keeps its ledgers in, with this process's kernel
// floor for it (floorOf). There is none when curated memory is off or the host does not know
// PiG's config root (host.ConfigHomer); the harness then records nothing and every model gets
// the default profile.
func StoreOf(h host.Host, cfg config.Config) (curated.Store, bool) {
	homer, ok := h.(host.ConfigHomer)
	if !ok || !cfg.CuratedMemory {
		return curated.Store{}, false
	}
	home := homer.ConfigHome()
	if home == "" {
		return curated.Store{}, false
	}
	store := curated.New(curated.DefaultDir(home))
	store.Floor = floorOf(store, cfg.KernelFloor)
	return store, true
}

// Ledger is one model's usage ledger in the store.
type Ledger struct {
	Store curated.Store
	Model string
}

// For is the ledger of model in h's store; false when there is no store or no model.
func For(h host.Host, cfg config.Config, model host.Model) (Ledger, bool) {
	store, ok := StoreOf(h, cfg)
	key := ModelKey(model)
	if !ok || key == "" {
		return Ledger{}, false
	}
	return Ledger{Store: store, Model: key}, true
}

// Profile computes the model's profile from its ledger, with the
// profile rules that apply to the model (ApplyRules), its codecs read from the model's layer
// (Layer): the formats it declared are among the codecs it can be switched to, and a name it
// redeclared stands for its own declaration. A ledger that cannot be read gives the default
// profile: the harness never fails the memory.
func (l Ledger) Profile() Profile { return l.ProfileWith(l.Layer()) }

// ProfileWith is Profile with the model's layer already loaded (active), so one pass reads its
// codecs from the same generation as the rest of its layer. It reads memory v2's events only
// (ComputeWith): memory v1's tallies never carry over.
func (l Ledger) ProfileWith(active declare.Active) Profile {
	events, err := l.Store.ModelEvents(l.Model)
	if err != nil {
		return Default(l.Model)
	}
	p := ApplyRules(ComputeWith(l.Model, events, active.CodecNames()), rules.For(l.Store, l.Model))
	if c, ok := active.Codec(p.Codec); ok {
		p.codec = c
	}
	return p
}

// Layer is the model's effective layer, interpreted (docs/memory-interface.md, section 8): its
// own definitions over the ones kept for every model, over the seeds. A layer that cannot be
// read stands as its seeds, so the memory behaves as it shipped.
func (l Ledger) Layer() declare.Active {
	h, _, err := l.Store.Layer().Load(l.Model)
	if err != nil {
		return declare.SeedsOnly(l.Model)
	}
	return declare.Of(h)
}

// LayerFor is model's effective layer in h's store; the seeds alone without a store.
func LayerFor(h host.Host, cfg config.Config, model host.Model) declare.Active {
	if l, ok := For(h, cfg, model); ok {
		return l.Layer()
	}
	return declare.SeedsOnly(ModelKey(model))
}

// Rules are the rules that apply to the ledger's model.
func (l Ledger) Rules() rules.Active { return rules.For(l.Store, l.Model) }

// Record appends e for the ledger's model, marked as counted under memory v2
// (curated.ModelEvent.Engine), so memory v1's evidence never mixes in. A failed write is
// dropped: counting must never fail the call it counts.
func (l Ledger) Record(e curated.ModelEvent) {
	e.Model = l.Model
	e.Engine = curated.EngineV2
	_ = l.Store.RecordModel(e)
}

// DefinedQueries are the stored queries of memoryTools (MemoryToolNames) by name: a call the
// retired defined tool of that name left on the branch is a memory query, read but never run
// (rules.MemoryQueries, dropped-then-queried). A stored definition whose name another tool holds
// is not among them (that tool's calls are conversation), and neither is a forgotten one (it has
// no stored query). None without a store.
func DefinedQueries(h host.Host, cfg config.Config, memoryTools []string) map[string]string {
	store, ok := StoreOf(h, cfg)
	if !ok {
		return nil
	}
	definitions, err := store.ListDefinitions()
	if err != nil || len(definitions) == 0 {
		return nil
	}
	out := make(map[string]string, len(definitions))
	for _, d := range definitions {
		if d.Name != omtool.ToolName && slices.Contains(memoryTools, d.Name) {
			out[d.Name] = d.Query
		}
	}
	return out
}

// MemoryToolNames are the tools whose calls are memory plumbing, not conversation: the memory
// tool and the retired defined queries' names (runtime.Runtime.SkipTools,
// harness.Manager.SkipNames), the memory tool first and the rest in name order. Level-0
// extraction leaves their calls and results out (extract.Input's SkipTools), and so do the
// minimal brief, the level-1 input, thinning and the curated evidence: the memory tool's writes
// persist in the curated store, and extracting them would feed memory back into itself, and the
// calls the removed defined tools left on the branch stay what they were when they were made.
//
// A retired name another tool holds is left out, so that tool's calls stay conversation. The
// list only grows within a process: a forgotten definition's name stays, so its calls already on
// the branch stay skipped and needing no coverage. An extraction resolves it once and uses that
// list throughout.
func MemoryToolNames(rt *runtime.Runtime) []string {
	defined := rt.SkipTools()
	names := make([]string, 0, len(defined)+1)
	for _, name := range defined {
		if name != omtool.ToolName && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return append([]string{omtool.ToolName}, names...)
}
