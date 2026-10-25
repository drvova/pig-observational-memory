package rules

import (
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
)

// Memory v2's harness (docs/memory-v2.md, "Self-built harness"): the detectors are code, run
// over what each level-0 extraction read (at a turn end and inside a compaction) and over the
// code checks of the compaction's level-1 output, counted per model; one evidence unit is one
// compaction. The remedies are code only: keep-head adds notes to the extraction that flagged
// them, and switch-codec and long-spec change the session model's profile. No v2 remedy asks a
// model anything, so the rules never add a request.

// StageCompaction is the stage of a memory v2 pass: one compaction.
const StageCompaction = "compaction"

// The kernel counts of the code checks of a compaction's level-1 output (docs/memory-v2.md,
// "Level 1"): counted on the compaction's pass beside the detectors, read by the profile's and
// the definitions' evidence, never a detector of their own (no rule answers one).
const (
	// CountLevel1Refused is the level-1 proposals validation refused.
	CountLevel1Refused = "level1-refused"
	// CountLevel1SpecificsRefused is the refused proposals that would have lost a specific: a
	// merge or supersede dropping one, or a reflection citing records whose specifics it does
	// not carry.
	CountLevel1SpecificsRefused = "level1-specifics-refused"
	// CountLevel1UnionKept is the replacements and supports the union rule kept back after
	// validation (consolidate.Union): a fact level 1 would otherwise have lost.
	CountLevel1UnionKept = "level1-union-kept"
	// CountLevel1Failed is a level-1 response nothing could be recorded from: no tool call,
	// output that accounts for nothing or has no valid record, or one cut at the output cap.
	// A stream error or an abort is no output of the model's and is not counted.
	CountLevel1Failed = "level1-failed"
)

// Level1Counts lists the level-1 counts.
var Level1Counts = []string{CountLevel1Refused, CountLevel1SpecificsRefused, CountLevel1UnionKept, CountLevel1Failed}

// V2Detectors are the kernel checks that keep a meaning under memory v2, in the spec's order:
//
//   - uncited-assistant-specifics: assistant entries a level-0 extraction read whose added
//     specifics no record of that extraction cites (UncitedAssistantSpecifics, the extraction's
//     records as the pass's notes);
//   - cross-scope-supersede: the supersedes level 1 proposed across scopes, which validation
//     refuses (level 0 sets no scope, so its corrections never cross one);
//   - dropped-then-queried: memory queries in the read entries asking for records a
//     compaction dropped, by rule or by level 1 (DroppedThenQueried);
//   - decode-failure: the session model's memory queries that did not parse, and rows that
//     did not decode, since the previous compaction.
//
// The other two have no v2 meaning: unresolved-relative-date counts phrases a worker left
// relative, but level 0 picks lines verbatim and every relative date is resolved by code when
// it renders (ledger.ResolveRelativeDates, the record's event date), and thin-chunk measures an
// observer chunk's compression, but level 0 has no chunks and keeps every line carrying
// specifics, and what level 1 would lose is the union rule's count (CountLevel1UnionKept).
var V2Detectors = []string{DetectUncitedAssistantSpecifics, DetectCrossScopeSupersede, DetectDroppedThenQueried, DetectDecodeFailure}

// V2Remedies are the remedies memory v2 runs, all code: keep-head (which replaces
// require-kind's re-ask, a request v2 never makes) and the profile switches. prompt-line has
// no worker prompt to join (the level-1 prompt's additions are the model's consolidation
// instructions, a definition), protect-kind no dropper, block-link nothing to block (level-1
// validation refuses a cross-scope supersede already), and split-chunk no chunk. switch-codec
// stays in the list but the harness never writes it under v2: its evidence is the codec decode
// failures (CountCodecDecodeFailure), and under v2 nothing decodes rows the model wrote (only
// v1's observer records them), so a v2 pass never counts one.
var V2Remedies = []string{RemedyKeepHead, RemedySwitchCodec, RemedyLongSpec}

// ofV2 are the rules of all memory v2 wrote (Rule.Engine): the only ones that apply, are
// decided and are counted (StatusLines). A rule memory v1 wrote still loads (its remedy may be
// one only v1 ran) and is listed by the harness source, but is skipped here: never applied,
// decided or counted, as active or retired.
func ofV2(all []Rule) []Rule {
	out := make([]Rule, 0, len(all))
	for _, r := range all {
		if r.Engine == curated.EngineV2 {
			out = append(out, r)
		}
	}
	return out
}

// isCompactionPass reports whether e is a memory v2 compaction that carries a rate: one that
// summed the detector runs of the source entries it folded (Tokens) and was not empty.
func isCompactionPass(e curated.ModelEvent) bool {
	return e.Kind == curated.ModelKindPass && e.Engine == curated.EngineV2 && e.Stage == StageCompaction && e.Tokens > 0 && e.Outcome != curated.OutcomeEmpty
}

// isV2Compaction reports whether e is any memory v2 compaction pass, empty ones included: the
// boundary a detector run, a decode failure and a profile's evidence window close at.
func isV2Compaction(e curated.ModelEvent) bool {
	return e.Kind == curated.ModelKindPass && e.Engine == curated.EngineV2 && e.Stage == StageCompaction
}

// IsV2Compaction reports whether e is a memory v2 compaction pass (empty ones included): the
// event that closes one evidence unit, for the profile's evidence windows too
// (harness.ComputeWith).
func IsV2Compaction(e curated.ModelEvent) bool { return isV2Compaction(e) }

// DetectV2 runs memory v2's detectors over one level-0 extraction: p.Covered are the source
// entries it read, p.Notes its records (before any remedy), p.Known the memory before it. It
// counts uncited-assistant-specifics and dropped-then-queried, and the detectors the model
// defined (p.Detectors) beside them; cross-scope-supersede is counted from level 1's refusals,
// and decode-failure from the usage ledger, when the compaction is recorded (afterCompaction).
// It is pure: the same extraction always gives the same findings.
func DetectV2(p ObserverPass) Findings {
	f := Findings{Counts: map[string]int{}, DroppedKinds: map[string]int{}}
	f.Uncited = UncitedAssistantSpecifics(p)
	f.Counts[DetectUncitedAssistantSpecifics] = len(f.Uncited)
	asked := DroppedThenQueried(p.Branch, p.Covered, p.Defined)
	f.Counts[DetectDroppedThenQueried] = len(asked)
	for _, o := range asked {
		f.DroppedKinds[kindName(o.Kind)]++
	}
	for _, d := range p.Detectors {
		if f.Defined == nil {
			f.Defined = map[string]int{}
		}
		f.Defined[d.Name] = d.Count(p, f.Counts)
	}
	return f
}

// RecordDetection records one run of memory v2's detectors for model (a curated.ModelKindDetect
// event, counted under v2) with the source tokens the extraction read. It is no evidence on its
// own: the next compaction's pass sums it (afterCompaction). Nothing is recorded for a run that
// read no source.
func RecordDetection(store curated.Store, model string, f Findings, tokens int) error {
	if model == "" || tokens <= 0 {
		return nil
	}
	e := curated.ModelEvent{Model: model, Kind: curated.ModelKindDetect, Engine: curated.EngineV2, Tokens: tokens, Counts: map[string]int{}, Revisions: Revisions()}
	for d, n := range f.Counts {
		if n > 0 {
			e.Counts[d] = n
		}
	}
	for d, n := range f.Defined {
		if n > 0 {
			e.Counts[DefinedCountKey(d)] = n
		}
	}
	if len(f.DroppedKinds) > 0 {
		e.Kinds = f.DroppedKinds
	}
	return store.RecordModel(e)
}

// sinceV2Compaction is the index after model's latest memory v2 compaction pass in events (0
// for none): where the current evidence unit starts.
func sinceV2Compaction(events []curated.ModelEvent) int {
	start := 0
	for i, e := range events {
		if isV2Compaction(e) {
			start = i + 1
		}
	}
	return start
}

// afterCompaction records one compaction of model as its evidence unit and decides the v2 rules
// for model (evaluate, with its catalogue from pass.Layer or the store). The pass sums the detector runs recorded since the previous compaction (their
// counts and source tokens), adds pass.Counts (level 1's code checks) and the decode failures
// counted under v2 since then (memory queries that did not parse, rows that did not decode).
// An empty pass (one whose cut range level 0 recorded nothing from) is recorded with no tokens,
// so it is no evidence and the runs before it count for none. The ledger is read, summed,
// appended to and evaluated under one hold of the store's lock.
func afterCompaction(store curated.Store, model string, pass Pass) ([]Transition, error) {
	e := curated.ModelEvent{Model: model, Kind: curated.ModelKindPass, Stage: StageCompaction, Engine: curated.EngineV2, Counts: map[string]int{}, RanHeld: pass.RanHeld}
	catalog := SeedCatalog()
	if pass.Layer != nil {
		catalog = CatalogOf(*pass.Layer)
	} else if h, _, err := store.Layer().Load(model); err == nil {
		catalog = CatalogOf(h)
	}
	var transitions []Transition
	migrate(store)
	err := store.Locked(func() error {
		events, err := store.ModelEvents(model)
		if err != nil {
			return err
		}
		if pass.Empty {
			e.Outcome = curated.OutcomeEmpty
			return store.RecordModelLocked(e)
		}
		e.Revisions = Revisions()
		e.Level1 = pass.Level1
		codec, syntax := 0, 0
		for _, ev := range events[sinceV2Compaction(events):] {
			if ev.Engine != curated.EngineV2 {
				continue
			}
			switch {
			case ev.Kind == curated.ModelKindDetect:
				e.Tokens += ev.Tokens
				for k, n := range ev.Counts {
					e.Counts[k] += n
				}
				for k, n := range ev.Kinds {
					if e.Kinds == nil {
						e.Kinds = map[string]int{}
					}
					e.Kinds[k] += n
				}
			case ev.Kind == curated.ModelKindDecode && ev.Outcome == curated.OutcomeError:
				codec++
			case ev.Kind == curated.ModelKindMemory && ev.Syntax:
				syntax++
			}
		}
		for k, n := range pass.Counts {
			if n > 0 {
				e.Counts[k] += n
			}
		}
		if e.Tokens > 0 && codec+syntax > 0 {
			e.Counts[DetectDecodeFailure] += codec + syntax
			if codec > 0 {
				e.Counts[CountCodecDecodeFailure] += codec
			}
		}
		if err := store.RecordModelLocked(e); err != nil {
			return err
		}
		if events, err = store.ModelEvents(model); err != nil {
			return err
		}
		all, err := list(store)
		if err != nil {
			return err
		}
		changed, ts := evaluate(model, events, all, store.Now().UTC(), catalog)
		for _, r := range changed {
			if err := write(store, r); err != nil {
				return err
			}
		}
		transitions = ts
		return nil
	})
	return transitions, err
}
