package curated

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
)

// ModelEvent is one counted event in a model's usage ledger (docs/memory-interface.md,
// sections 6 and 7): a memory tool call, an observer rows decode, a defined tool's call, a
// memory-worker pass with its detector counts. The per-model harness computes the model's
// profile, and the rules decide their state, from these events alone.
type ModelEvent struct {
	Time string `json:"time"`
	// Model is the model id the event is counted for (provider/id).
	Model string `json:"model"`
	// Kind is ModelKindMemory, ModelKindDecode or ModelKindDefined.
	Kind string `json:"kind"`
	// Outcome is one of the Outcome constants.
	Outcome string `json:"outcome"`
	// Codec is the codec in use: a decode's, or a memory call's result codec.
	Codec string `json:"codec,omitempty"`
	// Syntax marks a memory call that failed to parse.
	Syntax bool `json:"syntax,omitempty"`
	// Target is what a shown or omitted hit was checked against: TargetSummary or TargetIndex.
	Target string `json:"target,omitempty"`
	// Tool is a defined tool call's tool name.
	Tool string `json:"tool,omitempty"`
	// Stage is a pass's worker stage: observer, reflector or dropper.
	Stage string `json:"stage,omitempty"`
	// Tokens is a pass's observed source tokens: the estimated tokens of the source entries
	// the pass covered (0 for a reflector or dropper pass, which observes no source).
	Tokens int `json:"tokens,omitempty"`
	// Counts are a pass's detector counts, by detector name.
	Counts map[string]int `json:"counts,omitempty"`
	// Kinds are, for a pass that counted dropped-then-queried records, those records' kinds
	// ("unclassified" for none), so a protect-kind rule can name the kind to protect.
	Kinds map[string]int `json:"kinds,omitempty"`
	// Revisions are, for a pass whose detectors ran, the revision each versioned count in it was
	// counted by (rules.Revisions), so a count whose meaning changed is never compared across
	// the change; a count absent from it was counted by its first revision.
	Revisions map[string]int `json:"revisions,omitempty"`
}

// The model event kinds.
const (
	// ModelKindMemory is a call of the memory tool.
	ModelKindMemory = "memory"
	// ModelKindDecode is one decode of a rows table the observer was sent.
	ModelKindDecode = "decode"
	// ModelKindDefined is a call of a tool the agent defined.
	ModelKindDefined = "defined"
	// ModelKindPass is one memory-worker pass that advanced its coverage, with what the
	// rules' detectors counted in it (docs/memory-interface.md, section 7).
	ModelKindPass = "pass"
)

// The model event outcomes.
const (
	OutcomeOK    = "ok"
	OutcomeError = "error"
	// OutcomeEmpty is a call that matched nothing.
	OutcomeEmpty = "empty"
	// OutcomeShown is a hit on a record the current summary or index already showed.
	OutcomeShown = "shown"
	// OutcomeOmitted is a hit on a record the current summary left out to fit.
	OutcomeOmitted = "omitted"
	// OutcomeColumns is a decode that parsed but had the wrong columns.
	OutcomeColumns = "columns"
)

// The targets a shown or omitted hit is checked against.
const (
	TargetSummary = "summary"
	TargetIndex   = "index"
)

func (s Store) modelsDir() string { return filepath.Join(s.Dir, "models") }

// modelFileName is a model id as a file name (layer.ModelDir), so distinct ids never share a
// file and a model's ledger and layer directory have one name.
func modelFileName(modelID string) string { return layer.ModelDir(modelID) + ".jsonl" }

func (s Store) modelUsagePath(modelID string) string {
	return filepath.Join(s.modelsDir(), modelFileName(modelID))
}

// RecordModel appends e to its model's usage ledger under the store's lock, stamped with the
// store's clock inside the lock, so the ledger's order is its time order. An event without a
// model is refused.
func (s Store) RecordModel(e ModelEvent) error {
	return s.locked(func() error { return s.RecordModelLocked(e) })
}

// RecordModelLocked is RecordModel for a caller that already holds the store's lock (Locked),
// so it can read a ledger and append what it computed from it in one critical section.
func (s Store) RecordModelLocked(e ModelEvent) error {
	if strings.TrimSpace(e.Model) == "" {
		return errors.New("a model event needs a model id")
	}
	e.Time = s.Now().UTC().Format(time.RFC3339Nano)
	line, err := json.Marshal(e)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.modelsDir(), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(s.modelUsagePath(e.Model), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(line, '\n'))
	return err
}

// ModelEvents reads a model's usage ledger in the order it was written. A missing ledger is
// empty; unreadable lines are skipped. Lines parsed by an earlier read in this process are not
// parsed again (appendCache). The events are shared with that cache: callers must not modify
// them.
func (s Store) ModelEvents(modelID string) ([]ModelEvent, error) {
	// The path holds one model id (modelFileName), so the cache entry is that model's alone.
	return modelEvents.read(s.modelUsagePath(modelID), func(raw []byte) (ModelEvent, bool) {
		var e ModelEvent
		if json.Unmarshal(raw, &e) != nil || e.Kind == "" || e.Model != modelID {
			return ModelEvent{}, false
		}
		return e, true
	})
}

// The usage ledger's events for a defined query's calls as a tool (DefinitionUsageKey).
const (
	EventCallOK    = "call-ok"
	EventCallEmpty = "call-empty"
	EventCallError = "call-error"
)

// UsageEvent is one line of the store's usage ledger.
type UsageEvent struct {
	Time  time.Time
	Name  string
	Event string
	Quote string
}

// UsageEventsByName reads the whole usage ledger once and groups its events by
// name, each name's in the order they were written. A missing ledger is empty.
func (s Store) UsageEventsByName() (map[string][]UsageEvent, error) {
	lines, err := s.readUsageLines()
	if err != nil {
		return nil, err
	}
	out := map[string][]UsageEvent{}
	for _, line := range lines {
		out[line.Name] = append(out[line.Name], line.event())
	}
	return out, nil
}

// UsageEvents reads the usage ledger's events for name, in the order they were written.
func (s Store) UsageEvents(name string) ([]UsageEvent, error) {
	lines, err := s.readUsageLines()
	if err != nil {
		return nil, err
	}
	out := []UsageEvent{}
	for _, line := range lines {
		if line.Name == name {
			out = append(out, line.event())
		}
	}
	return out, nil
}

func (line usageLine) event() UsageEvent {
	t, _ := time.Parse(time.RFC3339, line.Time)
	return UsageEvent{Time: t, Name: line.Name, Event: line.Event, Quote: line.Quote}
}
