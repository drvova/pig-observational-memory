package curated

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
)

// ModelEvent is one counted event in a model's usage ledger (docs/memory-interface.md,
// sections 6 and 7): a memory tool call, a rows decode, a defined tool's call, a compaction
// with its detector counts (memory v1 counted its worker passes here too). The per-model harness computes the model's
// profile, and the rules decide their state, from these events alone.
type ModelEvent struct {
	Time string `json:"time"`
	// Model is the model id the event is counted for (provider/id).
	Model string `json:"model"`
	// Kind is one of the ModelKind constants.
	Kind string `json:"kind"`
	// Outcome is one of the Outcome constants.
	Outcome string `json:"outcome"`
	// Codec is the codec in use: a decode's, or a memory call's result codec.
	Codec string `json:"codec,omitempty"`
	// Syntax marks a memory call that failed to parse.
	Syntax bool `json:"syntax,omitempty"`
	// Legacy marks a memory tool code call (ModelKindCode) that was a query of the removed
	// query language, answered with the removal notice and run as nothing.
	Legacy bool `json:"legacy,omitempty"`
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
	// Engine is the memory engine the event was counted under (EngineV2); "" for v1. A count
	// of one engine is never evidence for the other: v2's passes are compactions, its detectors
	// read level-0 and level-1 output, and v1's are worker passes (docs/memory-v2.md,
	// "Migration > Layer definitions").
	Engine string `json:"engine,omitempty"`
	// Level1 marks a memory v2 compaction pass whose level-1 request answered, so Counts carry
	// the code checks of its output (the level-1 counts, zero ones left out): the evidence a
	// definition shaping level 1 is settled on (docs/memory-v2.md, "Self-built harness").
	Level1 bool `json:"level1,omitempty"`
	// RanHeld are, for a memory v2 compaction pass, the held definitions the compaction ran with
	// (layer.Definition.HeldID: kind/name and the digest of the text), read from its layer
	// before its level 1, so a held definition is settled only on passes that ran under exactly
	// its text, whichever session or process recorded others in between (rules.SettleV2).
	RanHeld []string `json:"ranHeld,omitempty"`
	// Cut is, for a detector pass (ModelKindDetectorPass), the id of the compaction's
	// firstKeptEntryId: the cut whose removed range the pass's detectors read.
	Cut string `json:"cut,omitempty"`
	// Errors are, for a detector pass, the detectors whose run gave no count, by name, with the
	// reason (a compile failure, a panic, a crash, a disallowed method, or killed unfinished).
	Errors map[string]string `json:"errors,omitempty"`
	// Unfinished is, for a detector pass, the detector whose run was killed unfinished, so the
	// next pass starts its order after it (docs/code-tool.md, "Running, and the time bound").
	Unfinished string `json:"unfinished,omitempty"`
	// Pending are, for a detector pass, the detectors level 1 proposed at the compaction whose
	// smoke run the pass made first: held when it returned a count, refused otherwise.
	Pending []string `json:"pending,omitempty"`
}

// EngineV2 is ModelEvent.Engine for an event counted under memory v2 (config.EngineV2).
const EngineV2 = "v2"

// The model event kinds.
const (
	// ModelKindMemory is a call of the memory tool.
	ModelKindMemory = "memory"
	// ModelKindDecode is one decode of a rows table the observer was sent.
	ModelKindDecode = "decode"
	// ModelKindDefined is a call of a tool the agent defined with the removed query language:
	// history, never graded (the defined tools are retired, docs/code-tool.md).
	ModelKindDefined = "defined"
	// ModelKindCode is a call of the memory tool since it runs Go code (docs/code-tool.md):
	// Outcome error for code that did not compile, panicked or crashed the child, ok for a
	// write, empty when every read returned nothing, else shown or omitted against the summary
	// and the index. It counts only toward the summary-size switch.
	ModelKindCode = "code"
	// ModelKindPass is one compaction (Stage "compaction"), with what the rules' detectors
	// counted since the previous one (docs/memory-interface.md, section 7); memory v1 recorded
	// one per worker pass, without an engine.
	ModelKindPass = "pass"
	// ModelKindDetect is one run of memory v2's detectors over the source entries a level-0
	// extraction read, at a turn end or inside a compaction, with its counts and the source
	// tokens it read. It is no evidence on its own: the next compaction's pass sums the runs
	// since the previous one, so one evidence unit is one compaction (docs/memory-v2.md,
	// "Self-built harness").
	ModelKindDetect = "detect"
	// ModelKindDetectorPass is one detector pass (docs/code-tool.md, "Running, and the time
	// bound"): the Go detectors a model defined, run in the background after a compaction over
	// what it covered, each in a detector child of its own. Counts carry each detector's count
	// under rules.DefinedCountKey (zero included, so a run that counted is told from one that
	// did not run) and the smoke runs refused (rules.CountLevel1Refused); Errors the runs that
	// gave no count; RanHeld the held texts whose run started. Never a judge of a rule: the
	// kernel's findings stay on the compaction pass.
	ModelKindDetectorPass = "detector-pass"
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

// UsageModels are the models whose usage ledger holds at least one event, in name order: the
// models /om:status and om.Info give a block each, whichever model is the session model.
func (s Store) UsageModels() ([]string, error) {
	entries, err := os.ReadDir(s.modelsDir())
	if errors.Is(err, os.ErrNotExist) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".jsonl")
		if !ok || e.IsDir() {
			continue
		}
		model, ok := layer.ModelOfDir(name)
		if !ok {
			continue
		}
		if events, err := s.ModelEvents(model); err == nil && len(events) > 0 {
			out = append(out, model)
		}
	}
	sort.Strings(out)
	return out, nil
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
	// User and Record are the usage line's (usageLine): a helped event quoting a user, and the
	// record a corrected, cited or promoted event names.
	User   bool
	Record string
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
	return UsageEvent{Time: t, Name: line.Name, Event: line.Event, Quote: line.Quote, User: line.User, Record: line.Record}
}
