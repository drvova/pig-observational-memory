package harness

import (
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/declare"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
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

// WorkerModelKey is the memory-worker model's key for the session: config model when the
// registry has it, else the session model, as the runtime's primary resolution picks it. The
// observer and dropper passes are recorded under this model, so the rules they read and the
// rules a probation can decide are this model's.
func WorkerModelKey(h host.Host, cfg config.Config) string {
	session := h.SessionModel()
	if cfg.Model != nil && cfg.Model.ID != "" {
		if models := h.Models(); models != nil {
			if configured := models.Find(runtime.EffectiveProvider(cfg.Model, session), cfg.Model.ID); configured != nil {
				return ModelKey(configured)
			}
		}
	}
	return ModelKey(session)
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

// Profile computes the model's profile from its ledger and the store's definitions, with the
// profile rules that apply to the model (ApplyRules), its codecs read from the model's layer
// (Layer): the formats it declared are among the codecs it can be switched to, and a name it
// redeclared stands for its own declaration. A ledger that cannot be read gives the default
// profile: the harness never fails the memory.
func (l Ledger) Profile() Profile { return l.ProfileWith(l.Layer()) }

// ProfileWith is Profile with the model's layer already loaded (active), so one pass reads its
// codecs from the same generation as the rest of its layer.
func (l Ledger) ProfileWith(active declare.Active) Profile {
	events, err := l.Store.ModelEvents(l.Model)
	if err != nil {
		return Default(l.Model)
	}
	definitions, _ := l.Store.ListDefinitions()
	p := ApplyRules(ComputeWith(l.Model, events, definitions, active.CodecNames()), rules.For(l.Store, l.Model))
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

// Record appends e for the ledger's model. A failed write is dropped: counting must never
// fail the call it counts.
func (l Ledger) Record(e curated.ModelEvent) {
	e.Model = l.Model
	_ = l.Store.RecordModel(e)
}

// ProfileFor is model's profile in h's store, the default profile when there is no ledger.
func ProfileFor(h host.Host, cfg config.Config, model host.Model) Profile {
	if l, ok := For(h, cfg, model); ok {
		return l.Profile()
	}
	return Default(ModelKey(model))
}

// DefinedQueries are the store's definitions by name, each with its stored query: a call of a
// defined tool is a memory query (rules.MemoryQueries). None without a store.
func DefinedQueries(h host.Host, cfg config.Config) map[string]string {
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
		out[d.Name] = d.Query
	}
	return out
}
