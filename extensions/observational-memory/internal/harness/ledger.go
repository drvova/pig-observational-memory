package harness

import (
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
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

// StoreOf is the curated store the harness keeps its ledgers in. There is none when curated
// memory is off or the host does not know PiG's config root (host.ConfigHomer); the harness
// then records nothing and every model gets the default profile.
func StoreOf(h host.Host, cfg config.Config) (curated.Store, bool) {
	homer, ok := h.(host.ConfigHomer)
	if !ok || !cfg.CuratedMemory {
		return curated.Store{}, false
	}
	home := homer.ConfigHome()
	if home == "" {
		return curated.Store{}, false
	}
	return curated.New(curated.DefaultDir(home)), true
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
// profile rules that apply to the model (ApplyRules). A ledger
// that cannot be read gives the default profile: the harness never fails the memory.
func (l Ledger) Profile() Profile {
	events, err := l.Store.ModelEvents(l.Model)
	if err != nil {
		return Default(l.Model)
	}
	definitions, _ := l.Store.ListDefinitions()
	return ApplyRules(Compute(l.Model, events, definitions), rules.For(l.Store, l.Model))
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
