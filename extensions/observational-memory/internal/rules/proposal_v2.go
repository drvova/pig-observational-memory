package rules

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/codepolicy"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
)

// Definitions proposed only in level 1, and their settlement (docs/memory-v2.md, "Self-built
// harness"). No build or revision pass runs: the model defines part of its own
// harness only inside the compaction's one level-1 request, and only when its harness evidence
// changed since the last offer (ProposalDue), so a definition never adds a request. A held
// definition is settled only on evidence its proposing request did not produce, after
// nodecode's provisional definitions ("a call from inside a form the model is evaluating is the
// model calling its own work"): the detector runs over the turns that ran under it and the code
// checks of a later compaction's level-1 output under it (SettleV2). Evidence is attributed, not
// timed: only a compaction pass that records having run with exactly its held text
// (curated.ModelEvent.RanHeld) counts, so the compaction that held it (which read its layer
// before the hold) never does, nor another session's compaction that never ran under it, and an
// empty compaction is no evidence.

// V2DefinitionKinds are the definition kinds level 1 may propose: the ones that change what
// memory v2 does and that a later compaction's code evidence can judge. The consolidation
// instructions extend the kernel's level-1 rules and the seed, a detector counts beside the
// kernel's checks, and a remedy catalogue names what the harness tries for a v2 detector. A
// format is not among them: under v2 nothing reads a summary back in its codec (level 1 writes
// tool fields, never rows), so no decode failure is ever counted against one, and the profile,
// which switches codecs on those failures alone, never reaches a format by a new name.
var V2DefinitionKinds = []string{layer.KindInstructions, layer.KindDetector, layer.KindRemedy}

// CheckV2Definition refuses a definition of kind named name that changes nothing memory v2
// does, that no v2 evidence can judge, or that is made elsewhere: instructions other than the
// consolidation instructions (no worker stage runs), a remedy catalogue not named after a v2
// remedy (V2Remedies; a rule is proposed as a rule), a format (no v2 count judges a codec), a
// summary (selection's ordered classes are the kernel's under v2), a schema (level 0 is code and
// level 1's tool fields are the kernel's), and a tool (made with define).
func CheckV2Definition(kind, name string) error {
	switch kind {
	case layer.KindDetector:
		return nil
	case layer.KindInstructions:
		if name == agents.InstructionsConsolidation {
			return nil
		}
		return fmt.Errorf("the only instructions are %q, the level-1 request's; no worker stage runs", agents.InstructionsConsolidation)
	case layer.KindRemedy:
		if slices.Contains(V2Remedies, name) {
			return nil
		}
		return fmt.Errorf("a remedy definition is a catalogue named after a code remedy (%s); a rule is proposed in rules", strings.Join(V2Remedies, ", "))
	case layer.KindFormat:
		return fmt.Errorf("a format is not defined: nothing decodes a summary back in its codec, so no count could prove or revert one")
	case layer.KindSummary:
		return fmt.Errorf("a summary definition does not select: selection's ordered classes are the kernel's (anchors, runnable omissions, drops over what it shows)")
	case layer.KindSchema:
		return fmt.Errorf("a schema changes nothing: level 0 is code and level 1's tool fields are the kernel's")
	case layer.KindTool:
		return fmt.Errorf("defined tools are retired: the agent keeps code definitions with the memory tool")
	}
	return fmt.Errorf("unknown definition kind %q; kinds are %s", kind, strings.Join(layer.Kinds, ", "))
}

// CheckV2Proposal is CheckV2Definition for a definition level 1 proposed, with its text: the
// consolidation instructions extend the seed and never replace it (docs/memory-v2.md: the seed
// is one "the model may extend"), so the seed's guidance stands after the kernel's rules
// whatever the model adds; "mode: replace" is refused.
func CheckV2Proposal(kind, name, text string) error {
	if err := CheckV2Definition(kind, name); err != nil {
		return err
	}
	if kind == layer.KindDetector {
		d, err := ParseDetector(name, text)
		if err != nil {
			return err
		}
		if d.Retired() {
			return fmt.Errorf("the query language is removed: a detector declares Go code in its code column (func %s() int over the detector's om), or a kernel check", codepolicy.DetectorEntry)
		}
	}
	if kind == layer.KindInstructions {
		mode, _, err := layer.SplitMode(text)
		if err != nil {
			return err
		}
		if mode == layer.ModeReplace {
			return fmt.Errorf("the %q instructions extend the seed and never replace it; drop the \"%s %s\" line", agents.InstructionsConsolidation, layer.ModeLine, layer.ModeReplace)
		}
	}
	return nil
}

// SettlesUnderV2 reports whether held definition d is one memory v2 runs with and settles
// (SettleV2): one memory v2 held (layer.Definition.Engine), of a kind CheckV2Definition
// accepts, or a rule written for memory v2. A definition v1 held is v1's to settle, and never
// blocks or answers a v2 offer.
func SettlesUnderV2(d layer.Definition) bool {
	if d.Engine != curated.EngineV2 {
		return false
	}
	if IsRule(d.Kind, d.Name) {
		r, err := parse(d.Kind+"/"+d.Name, d.Text)
		return err == nil && r.Engine == curated.EngineV2
	}
	if d.Kind == layer.KindDetector {
		// A held query detector is retired: it stays as held data, never run, never judged, and
		// never blocks an offer.
		if def, err := ParseDetector(d.Name, d.Text); err == nil && def.Retired() {
			return false
		}
	}
	return CheckV2Definition(d.Kind, d.Name) == nil
}

// V2EvidenceOf is model's memory v2 harness evidence: the counts of its last MinEvidence
// compactions that carried a rate (the detector runs they summed, the decode failures and level
// 1's code checks), the source tokens those runs read, the counts of the model's own Go
// detectors in those compactions' detector passes (definedCounts), and its v2 rules with their
// states.
func V2EvidenceOf(store curated.Store, model string) (Evidence, error) {
	events, err := store.ModelEvents(model)
	if err != nil {
		return Evidence{}, err
	}
	e := Evidence{Model: model, Counts: map[string]int{}, Events: len(events)}
	passes := passesFrom(events, 0)
	if len(passes) > MinEvidence {
		passes = passes[len(passes)-MinEvidence:]
	}
	e.Passes = len(passes)
	for _, i := range passes {
		e.Tokens += events[i].Tokens
		for k, n := range events[i].Counts {
			if n > 0 {
				e.Counts[k] += n
			}
		}
	}
	for k, n := range definedCounts(events, passes) {
		e.Counts[k] += n
	}
	for _, r := range For(store, model).Rules {
		e.Rules = append(e.Rules, r.Describe()+" "+r.State)
	}
	sort.Strings(e.Rules)
	return e, nil
}

// definedCounts are the non-zero defined:<name> counts of the detector pass events
// (curated.ModelKindDetectorPass) that belong to one of the compaction passes window indexes. A
// detector pass belongs to the memory v2 compaction recorded last before it: the hook starts it
// once its compaction was recorded, and the next compaction stops it, writing its event, before
// recording its own pass. Only the model's detector counts are taken; a pass's other counts
// (rules.CountLevel1Refused for a refused smoke run) are not offer evidence. Its source tokens
// are the compaction's, already counted.
func definedCounts(events []curated.ModelEvent, window []int) map[string]int {
	in := make(map[int]bool, len(window))
	for _, i := range window {
		in[i] = true
	}
	out := map[string]int{}
	owner := -1
	for i, ev := range events {
		if isV2Compaction(ev) {
			owner = i
			continue
		}
		if ev.Kind != curated.ModelKindDetectorPass || ev.Engine != curated.EngineV2 || !in[owner] {
			continue
		}
		for k, n := range ev.Counts {
			if n > 0 && strings.HasPrefix(k, DefinedCountKey("")) {
				out[k] += n
			}
		}
	}
	return out
}

// ProposalDue reports whether model's next level-1 request may define, with the evidence it
// would be shown. The rule is code's: its evidence (V2EvidenceOf, compared by Evidence.Digest:
// which counts are non-zero and the rules' states) differs from the evidence the last level-1
// proposal was shown (layer.Store.LastProposal), or, before any, from no evidence at all, and
// none of the model's held definitions memory v2 settles (SettlesUnderV2) waits for its proof,
// so a proposal is never replaced before a later compaction judged it. Nothing is due otherwise,
// and no request is ever made for a definition.
func ProposalDue(store curated.Store, model string) (Evidence, bool, error) {
	if model == "" {
		return Evidence{}, false, nil
	}
	e, err := V2EvidenceOf(store, model)
	if err != nil {
		return Evidence{}, false, err
	}
	ls := store.Layer()
	held, err := ls.Held(model)
	if err != nil {
		return e, false, err
	}
	for _, d := range held {
		if SettlesUnderV2(d) {
			return e, false, nil
		}
	}
	baseline := Evidence{}.Digest()
	last, ok, err := ls.LastProposal(model)
	if err != nil {
		return e, false, err
	}
	if ok {
		baseline = last.Evidence
	}
	return e, e.Digest() != baseline, nil
}

// level1Losses are the level-1 counts that settle a held consolidation instructions definition
// as not clean: an output nothing was recorded from, a proposal that would have lost a specific,
// and a fact the union rule had to keep back.
var level1Losses = []string{CountLevel1Failed, CountLevel1SpecificsRefused, CountLevel1UnionKept}

// SettleV2 decides held definition d (SettlesUnderV2) from model's usage ledger events, on
// evidence its proposing request did not produce: the memory v2 compaction passes that record
// having run with exactly d's held text (curated.ModelEvent.RanHeld names d.HeldID), and of
// those only the ones that were not empty. The compaction that held d read its layer before its
// level 1, so its pass never names d; neither does another session's compaction whose layer was
// read before the hold, wherever its pass lands in the shared ledger. decided is false while
// there is no such evidence, so d stays held:
//
//   - the consolidation instructions need a compaction whose level-1 request answered under
//     them (curated.ModelEvent.Level1); they are clean unless its code checks counted a loss
//     (level1-failed, level1-specifics-refused, level1-union-kept);
//   - a detector, a remedy catalogue and a rule need detector runs over turns that ran under
//     them (the passes' source tokens), and are clean: they change only what code counts or
//     keeps, and the kernel's counts judge rules from there.
//
// clean writes it (layer.Store.ClaimHeld), and otherwise it is reverted with reason.
func SettleV2(events []curated.ModelEvent, d layer.Definition) (decided, clean bool, reason string) {
	id := d.HeldID()
	if d.Kind == layer.KindDetector {
		def, err := ParseDetector(d.Name, d.Text)
		switch {
		case err != nil:
		case def.Retired():
			// Retired with the query language: no pass runs it, and it is never judged.
			return false, false, ""
		case def.IsCode():
			// A Go detector's evidence is its own runs, in the detector pass events.
			return settleCodeDetector(events, d, id)
		}
	}
	tokens, answered, compactions := 0, 0, 0
	counts := map[string]int{}
	for _, e := range events {
		if !isV2Compaction(e) || e.Outcome == curated.OutcomeEmpty || !slices.Contains(e.RanHeld, id) {
			continue
		}
		if t, ok := eventTime(e); !ok || !t.After(d.Time) {
			continue
		}
		compactions++
		tokens += e.Tokens
		if e.Level1 {
			answered++
		}
		for k, n := range e.Counts {
			if n > 0 {
				counts[k] += n
			}
		}
	}
	losses := func(keys ...string) string {
		parts := []string{}
		for _, k := range keys {
			if counts[k] > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", k, counts[k]))
			}
		}
		return strings.Join(parts, ", ")
	}
	switch {
	case d.Kind == layer.KindInstructions:
		if answered == 0 {
			return false, false, ""
		}
		if lost := losses(level1Losses...); lost != "" {
			return true, false, fmt.Sprintf("level 1 ran under it at %d later %s and its code checks counted %s", answered, pluralWord(answered, "compaction"), lost)
		}
		return true, true, fmt.Sprintf("level 1 ran under it at %d later %s with no loss counted", answered, pluralWord(answered, "compaction"))
	case tokens == 0:
		return false, false, ""
	}
	return true, true, fmt.Sprintf("the turns under it (%d source tokens over %d later %s) counted no failure it could cause", tokens, compactions, pluralWord(compactions, "compaction"))
}

// settleCodeDetector decides held Go detector d (held id id) from the detector pass events
// whose run started with exactly its held text (RanHeld) after it was held: reverted when any of
// those runs gave no count (a compile failure, a panic, a crash, a disallowed method, or a run
// killed unfinished at the next compaction or at session end), written when at least one
// counted and none failed, and left held while none has run. A pass that never started it
// names it nowhere and is no evidence.
func settleCodeDetector(events []curated.ModelEvent, d layer.Definition, id string) (decided, clean bool, reason string) {
	counted := 0
	for _, e := range events {
		if e.Kind != curated.ModelKindDetectorPass || e.Engine != curated.EngineV2 || !slices.Contains(e.RanHeld, id) {
			continue
		}
		if t, ok := eventTime(e); !ok || !t.After(d.Time) {
			continue
		}
		if why, failed := e.Errors[d.Name]; failed {
			return true, false, fmt.Sprintf("its run after the compaction at %s gave no count: %s", e.Cut, why)
		}
		if _, ok := e.Counts[DefinedCountKey(d.Name)]; ok {
			counted++
		}
	}
	if counted == 0 {
		return false, false, ""
	}
	return true, true, fmt.Sprintf("it counted at %d later detector %s with no error", counted, pluralWord(counted, "pass"))
}

// pluralWord is word for n, with an s unless n is 1.
func pluralWord(n int, word string) string {
	switch {
	case n == 1:
		return word
	case strings.HasSuffix(word, "s"):
		return word + "es"
	}
	return word + "s"
}

// V2UnitsSinceProposal is the number of model's memory v2 evidence units (non-empty compaction
// passes) counted since its last level-1 proposal (layer.OpPropose, read from the usage ledger
// length it recorded), or since the start with none; offered reports whether there was one.
// /om:status shows it (docs/memory-v2.md, "Status").
func V2UnitsSinceProposal(store curated.Store, model string) (units int, offered bool, err error) {
	events, err := store.ModelEvents(model)
	if err != nil {
		return 0, false, err
	}
	from := 0
	last, ok, err := store.Layer().LastProposal(model)
	if err != nil {
		return 0, false, err
	}
	if ok {
		from = min(last.Events, len(events))
	}
	return len(passesFrom(events, from)), ok, nil
}
