package rules

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/provenance"
)

// Transition is one change Evaluate made: a rule added, established, retired, or promoted to a
// default. The user is notified of each.
type Transition struct {
	Rule Rule
	// To is the rule's new state; for a new default it is StateEstablished with Default true.
	To    string
	Added bool
}

// Message is the transition's notification.
func (t Transition) Message() string {
	r := t.Rule
	switch {
	case t.Added && r.Default():
		return fmt.Sprintf("Observational memory: rule %s is now a default for all models (%s)", r.Describe(), r.Reason)
	case t.Added:
		return fmt.Sprintf("Observational memory: rule %s added on probation for %s (%s)", r.Describe(), r.Model(), r.Reason)
	}
	return fmt.Sprintf("Observational memory: rule %s is now %s for %s (%s)", r.Describe(), t.To, r.Model(), r.Reason)
}

// countRevisions are the revisions of the pass counts whose meaning changed after the usage
// ledger had recorded them. uncited-assistant-specifics is at 2: from then on it counts only the
// specifics a reply added to its exchange (UncitedAssistantSpecifics), where revision 1 counted
// every uncited reply with specifics, echoes included. dropped-then-queried and decode-failure
// are at 2 since the memory tool runs code (docs/code-tool.md): the first counts from a code
// call's details (recalled ids and searched words) where revision 1 parsed query arguments,
// and the second counts codec decode failures only where revision 1 added memory queries
// that did not parse, so no window mixes counts from query calls with counts from code calls.
var countRevisions = map[string]int{DetectUncitedAssistantSpecifics: 2, DetectDroppedThenQueried: 2, DetectDecodeFailure: 2}

// CountRevision is the revision of what count key counts now: 1 for a count whose meaning never
// changed.
func CountRevision(key string) int {
	if r, ok := countRevisions[key]; ok {
		return r
	}
	return 1
}

// Revisions are the current revisions of the versioned counts, as a pass whose detectors ran
// records them (curated.ModelEvent.Revisions): a fresh map.
func Revisions() map[string]int {
	out := make(map[string]int, len(countRevisions))
	for k, r := range countRevisions {
		out[k] = r
	}
	return out
}

// countedNow reports whether pass e counted key by its current revision (CountRevision). A pass
// counted by an earlier one is no evidence for key, neither events nor tokens: its count meant
// something else, and the usage ledger keeps counts, not the entries they were counted over, so
// it cannot be re-counted.
func countedNow(e curated.ModelEvent, key string) bool {
	r := e.Revisions[key]
	if r == 0 {
		r = 1
	}
	return r == CountRevision(key)
}

// passesFrom are the indexes of the compactions that carry a rate (isCompactionPass) in events
// at or after from. A memory v1 worker pass is never evidence.
func passesFrom(events []curated.ModelEvent, from int) []int {
	out := []int{}
	for i := max(0, from); i < len(events); i++ {
		if isCompactionPass(events[i]) {
			out = append(out, i)
		}
	}
	return out
}

// countedPasses are the compactions at or after from that counted key by its current revision
// (countedNow): the passes a rule for key is written, baselined and judged on.
func countedPasses(events []curated.ModelEvent, from int, key string) []int {
	out := []int{}
	for _, i := range passesFrom(events, from) {
		if countedNow(events[i], key) {
			out = append(out, i)
		}
	}
	return out
}

// countKey is the pass count a rule is written and judged on: the codec decode failures for
// switch-codec (CountCodecDecodeFailure), else its detector's.
func countKey(detector, remedy string) string {
	if detector == DetectDecodeFailure && remedy == RemedySwitchCodec {
		return CountCodecDecodeFailure
	}
	return detector
}

// window sums detector's events and the observed source tokens over the passes at indexes.
func window(events []curated.ModelEvent, indexes []int, detector string) (count, tokens int) {
	for _, i := range indexes {
		count += events[i].Counts[detector]
		tokens += events[i].Tokens
	}
	return count, tokens
}

// baseline is the detector's events and tokens over the last MinEvidence compactions before end
// that counted it by its current revision (countedPasses).
func baseline(events []curated.ModelEvent, end int, detector string) (int, int) {
	passes := countedPasses(events[:min(end, len(events))], 0, detector)
	if len(passes) > MinEvidence {
		passes = passes[len(passes)-MinEvidence:]
	}
	return window(events, passes, detector)
}

// fell reports whether the rate after (events per token) is below the rate before.
func fell(beforeEvents, beforeTokens, afterEvents, afterTokens int) bool {
	if beforeEvents == 0 || beforeTokens == 0 || afterTokens == 0 {
		return false
	}
	return afterEvents*beforeTokens < beforeEvents*afterTokens
}

// evaluate decides the rules of model from its usage ledger events (curated.Store.ModelEvents,
// in the order written) and every rule in the store, at now, with the model's remedy catalogue
// (CatalogOf its layer). It is pure. It returns the rules it changed or added, and the
// transitions to notify. Only memory v2's rules (Rule.Engine) are decided, on compactions
// alone (passesFrom); a rule memory v1 wrote is never decided again. The rules:
//
//   - Every window below holds only the compactions that counted the rule's count by its
//     current revision (countedPasses): a compaction counted by an earlier revision of a count
//     whose meaning changed is no evidence for it, so a baseline never mixes two meanings.
//   - Probation: a rule on probation for model is decided once MinEvidence compactions were
//     counted after it was written. It is established when its detector's rate over them
//     (events per source token) is below its baseline rate (the same over the MinEvidence
//     compactions before it), and retired otherwise: a rule survives only by bringing down the
//     count it was written for. A baseline with no events cannot fall, so a rule for a
//     behaviour the model did not show is retired.
//   - Harness rules: when a detector counted events in each of the model's last MinEvidence
//     compactions since its last rule for that detector was written or decided, and no rule
//     for that detector is on probation for the model, the harness writes the first remedy in
//     the catalogue for that detector that runs (V2Remedies) and no rule for the model has
//     tried yet (a default the model is not excluded from counts as tried). With every remedy
//     tried, it writes none. switch-codec is skipped while none of the decode failures in
//     those compactions were rows the codec could not decode, and it is judged on those codec
//     failures alone (CountCodecDecodeFailure).
//   - Defaults: a rule established, with one signature, for DefaultAfterModels models becomes
//     a default for all, established, excluding the models for which it was retired.
func evaluate(model string, events []curated.ModelEvent, all []Rule, now time.Time, catalog Catalog) ([]Rule, []Transition) {
	var changed []Rule
	var transitions []Transition
	rules := ofV2(all)
	replace := func(r Rule) {
		for i := range rules {
			if rules[i].ID == r.ID {
				rules[i] = r
				return
			}
		}
		rules = append(rules, r)
	}
	// Probation.
	for _, r := range rules {
		if r.State != StateProbation || r.Model() != model {
			continue
		}
		// Grading stays with the kernel: a rule is judged only by a kernel count.
		if !containsString(KernelKeys(), countKey(r.Detector, r.Remedy)) {
			continue
		}
		passes := countedPasses(events, r.Start, countKey(r.Detector, r.Remedy))
		if len(passes) < MinEvidence {
			continue
		}
		passes = passes[:MinEvidence]
		r.AfterEvents, r.AfterTokens = window(events, passes, countKey(r.Detector, r.Remedy))
		r.AfterPasses = len(passes)
		r.End = passes[len(passes)-1] + 1
		if fell(r.BaselineEvents, r.BaselineTokens, r.AfterEvents, r.AfterTokens) {
			r.State = StateEstablished
			r.Reason = fmt.Sprintf("%s fell from %d events in %d tokens to %d in %d over %d passes", r.Detector, r.BaselineEvents, r.BaselineTokens, r.AfterEvents, r.AfterTokens, r.AfterPasses)
		} else {
			r.State = StateRetired
			r.Reason = fmt.Sprintf("%s did not fall: %d events in %d tokens before, %d in %d over %d passes since", r.Detector, r.BaselineEvents, r.BaselineTokens, r.AfterEvents, r.AfterTokens, r.AfterPasses)
		}
		replace(r)
		changed = append(changed, r)
		transitions = append(transitions, Transition{Rule: r, To: r.State})
	}
	// Harness rules.
	for _, detector := range V2Detectors {
		floor := 0
		busy := false
		tried := map[string]bool{}
		for _, r := range rules {
			if r.Detector != detector || !r.AppliesTo(model) {
				continue
			}
			tried[r.Remedy] = true
			if r.Default() {
				continue
			}
			if r.State == StateProbation {
				busy = true
			}
			floor = max(floor, r.Start, r.End)
		}
		if busy {
			continue
		}
		passes := countedPasses(events, floor, detector)
		if len(passes) < MinEvidence {
			continue
		}
		last := passes[len(passes)-MinEvidence:]
		rising := true
		kinds := map[string]int{}
		for _, i := range last {
			if events[i].Counts[detector] == 0 {
				rising = false
				break
			}
			for k, n := range events[i].Kinds {
				kinds[k] += n
			}
		}
		if !rising {
			continue
		}
		remedy := ""
		for _, candidate := range catalog.Tries(detector) {
			if tried[candidate] || !slices.Contains(V2Remedies, candidate) {
				continue
			}
			// A remedy judged on a narrower count needs that count in the evidence: query
			// syntax errors alone never write switch-codec.
			if key := countKey(detector, candidate); key != detector {
				if n, _ := window(events, last, key); n == 0 {
					continue
				}
			}
			remedy = candidate
			break
		}
		if remedy == "" {
			continue
		}
		r, err := Check(catalog.Rule(detector, remedy, kinds))
		if err != nil {
			continue
		}
		r.Models, r.State, r.Created, r.Start, r.Engine = []string{model}, StateProbation, now, len(events), curated.EngineV2
		r.BaselineEvents, r.BaselineTokens = window(events, last, countKey(detector, remedy))
		r.Reason = fmt.Sprintf("%s counted in each of the last %d passes (%d events in %d tokens)", detector, MinEvidence, r.BaselineEvents, r.BaselineTokens)
		r.ID = newID(r)
		replace(r)
		changed = append(changed, r)
		transitions = append(transitions, Transition{Rule: r, To: StateProbation, Added: true})
	}
	// Defaults.
	established := map[string]map[string]bool{}
	retired := map[string][]string{}
	sample := map[string]Rule{}
	hasDefault := map[string]bool{}
	authored := map[string]bool{}
	for _, r := range rules {
		sig := r.Signature()
		switch {
		case r.Default():
			hasDefault[sig] = true
		case r.State == StateEstablished:
			if established[sig] == nil {
				established[sig] = map[string]bool{}
			}
			authored[sig] = authored[sig] || (r.Proposer == ProposerModel && r.Text != "")
			established[sig][r.Model()] = true
			sample[sig] = r
		case r.State == StateRetired:
			retired[sig] = append(retired[sig], r.Model())
		}
	}
	for sig, models := range established {
		if hasDefault[sig] || len(models) < DefaultAfterModels {
			continue
		}
		base := sample[sig]
		// A default keeps the authorship of its text: a line a model wrote stays the model's
		// when it applies to every model.
		proposer := ProposerHarness
		if authored[sig] {
			proposer = ProposerModel
		}
		d := Rule{Detector: base.Detector, Remedy: base.Remedy, Kind: base.Kind, Fullness: base.Fullness, Text: base.Text,
			Models: []string{AllModels}, Excluded: retired[sig], State: StateEstablished, Proposer: proposer, Created: now, Engine: curated.EngineV2}
		d.Reason = fmt.Sprintf("established for %d models", len(models))
		d.ID = newID(d)
		replace(d)
		changed = append(changed, d)
		transitions = append(transitions, Transition{Rule: d, To: StateEstablished, Added: true})
	}
	return changed, transitions
}

// Pass is one compaction, as AfterPass records it: the model's evidence unit.
type Pass struct {
	// Layer is the model's effective layer the compaction ran with, whose catalogue the rules
	// are decided by; nil loads it from the store.
	Layer *layer.Harness
	// Empty marks a compaction whose cut range level 0 recorded nothing from: it is recorded
	// with the outcome empty and no tokens, so it is no evidence.
	Empty bool
	// Counts are kernel counts the compaction adds beside its detector runs: what the code
	// checks of its level-1 output counted (CountLevel1Refused and the rest, and
	// DetectCrossScopeSupersede for the cross-scope supersedes it proposed).
	Counts map[string]int
	// Level1 marks a compaction whose level-1 request answered: Counts carry the code checks
	// of its output (curated.ModelEvent.Level1).
	Level1 bool
	// RanHeld are the held definitions the compaction ran with (curated.ModelEvent.RanHeld).
	RanHeld []string
}

// AfterPass records one compaction of model in its usage ledger as its evidence unit and
// evaluates the rules for model, writing the ones that changed (afterCompaction). It returns
// the transitions to notify. A failed write returns what was decided so far: counting never
// fails the compaction it counts.
func AfterPass(store curated.Store, model string, pass Pass) ([]Transition, error) {
	if model == "" {
		return nil, errors.New("a pass needs a model id")
	}
	return afterCompaction(store, model, pass)
}

// Proposal is a rule a model proposes with the memory tool's propose write.
type Proposal struct {
	Detector, Remedy, Text, Kind string
	Fullness                     float64
}

// Propose holds a rule model proposed, on probation for model: it applies to the model's next
// pass and is written once that pass is clean (layer.Store.Claim), as every definition from the
// model is (docs/memory-interface.md, section 8). It is checked like a harness
// rule (Check), answers a v2 detector with a code-only remedy (V2Remedies), and is refused when
// the same rule is already applied to model or was retired for it (the detector decided), or
// when its text is not the model's own: text found in a tool result, a custom message or a
// summary on the branch came from a file, a web page or tool output, and rule text never does.
// Its baseline is the detector's count over the model's last MinEvidence compactions; only the
// detector decides it from there.
func Propose(store curated.Store, model string, branch []ledger.Entry, p Proposal) (Rule, error) {
	if model == "" {
		return Rule{}, errors.New("a rule needs a model id, and the session model has none")
	}
	r, err := Check(Rule{Detector: p.Detector, Remedy: p.Remedy, Text: p.Text, Kind: p.Kind, Fullness: p.Fullness})
	if err != nil {
		return Rule{}, err
	}
	// A rule answers a v2 detector with a code-only remedy (V2Remedies), and is decided on
	// compactions.
	if !containsString(V2Detectors, r.Detector) || !slices.Contains(V2Remedies, r.Remedy) {
		return Rule{}, fmt.Errorf("a rule answers %s with %s", strings.Join(V2Detectors, ", "), strings.Join(V2Remedies, ", "))
	}
	r.Engine = curated.EngineV2
	// The same provenance check every definition from the model passes (CheckFromModel): the
	// whole text, or any quotation of MinCopiedWords words inside it, found in output on the
	// branch is refused; the catalogue's own line for the remedy is not a quotation.
	if quoted, copied := copiedSegment(branch, r.Text, CatalogueSeeds[r.Remedy]); r.Text != "" && copied {
		return Rule{}, fmt.Errorf("refused: that text quotes tool output, a file or another message on this branch (%q); a rule's text must be your own words", clip(quoted, 80))
	}
	r.Proposer, r.Models, r.State = ProposerModel, []string{model}, StateProbation
	var saved Rule
	migrate(store)
	err = store.Locked(func() error {
		all, err := list(store)
		if err != nil {
			return err
		}
		if held, err := store.Layer().Held(model); err == nil {
			all = append(all, rulesOf(held)...)
		}
		for _, existing := range all {
			if existing.Signature() != r.Signature() || !existing.AppliesTo(model) {
				continue
			}
			if existing.Active() {
				return fmt.Errorf("rule %s already applies to %s (%s)", existing.ID, model, existing.State)
			}
			return fmt.Errorf("rule %s was retired for %s: %s", existing.ID, model, existing.Reason)
		}
		events, err := store.ModelEvents(model)
		if err != nil {
			return err
		}
		r.Created, r.Start = store.Now().UTC(), len(events)
		r.BaselineEvents, r.BaselineTokens = baseline(events, len(events), countKey(r.Detector, r.Remedy))
		r.Reason = fmt.Sprintf("proposed by the model; %d events in %d tokens over its last passes", r.BaselineEvents, r.BaselineTokens)
		r.ID = newID(r)
		saved = r
		// The write follows the proof: the rule is held, applied to the model's next pass,
		// and written (then judged by its detector) once that pass is clean.
		_, err = store.Layer().HoldLocked(layer.Definition{Kind: layer.KindRemedy, Name: r.ID, Model: model, Text: encode(r)})
		return err
	})
	if err != nil {
		return Rule{}, err
	}
	return saved, nil
}

// IsRule reports whether kind named name is a rule kept in the layer (section 7): a remedy
// definition not named after a catalogue remedy. The harness writes, establishes and retires
// rules; the model only proposes them, so no harness write, forget or rollback of the model's
// touches one.
func IsRule(kind, name string) bool {
	return kind == layer.KindRemedy && !containsString(Remedies, name)
}

// copiedFromOutput reports whether text, whitespace collapsed and case ignored, appears in a
// tool result, a custom message or a summary on the branch (provenance.CopiedFromOutput).
func copiedFromOutput(branch []ledger.Entry, text string) bool {
	return provenance.CopiedFromOutput(branch, text)
}

// For reads the rules that apply to model in store, from its effective layer: the written and
// held rules kept for every model and for model, so a held proposal applies to the pass that
// proves it. Only memory v2's rules apply (ofV2), so a rule memory v1 wrote (require-kind,
// split-chunk, a dropper's protect-kind) never acts. None when the store cannot be read.
func For(store curated.Store, model string) Active {
	migrate(store)
	h, _, err := store.Layer().Load(model)
	if err != nil {
		return Active{}
	}
	return ActiveFor(ofV2(rulesOf(h.List(layer.KindRemedy))), model)
}

// DetectorCountsLine is model's detector counts over its last compactions (at most
// MinEvidence), the kernel's detectors and the level-1 counts with a nonzero count; false when
// the model has no compaction pass.
func DetectorCountsLine(store curated.Store, model string) (string, bool) {
	events, err := store.ModelEvents(model)
	if err != nil {
		return "", false
	}
	passes := passesFrom(events, 0)
	if len(passes) == 0 {
		return "", false
	}
	if len(passes) > MinEvidence {
		passes = passes[len(passes)-MinEvidence:]
	}
	parts := []string{}
	for _, d := range append(append([]string{}, V2Detectors...), Level1Counts...) {
		if count, _ := window(events, passes, d); count > 0 {
			parts = append(parts, fmt.Sprintf("%s %d", d, count))
		}
	}
	_, tokens := window(events, passes, "")
	counts := "none"
	if len(parts) > 0 {
		counts = strings.Join(parts, ", ")
	}
	return fmt.Sprintf("Detector counts for %s over its last %d compactions (%d tokens): %s", model, len(passes), tokens, counts), true
}

// StatusLines is the rules section of /om:status: every active memory v2 rule (ofV2) with its
// counts, and for each of models (in order) its detector counts over its last MinEvidence
// compactions.
func StatusLines(store curated.Store, models []string) []string {
	all, err := List(store)
	if err != nil {
		return nil
	}
	all = ofV2(all)
	lines := []string{}
	active := 0
	for _, r := range all {
		if !r.Active() {
			continue
		}
		active++
		applies := r.Model()
		if r.Default() {
			applies = "all models"
			if len(r.Excluded) > 0 {
				applies += " but " + strings.Join(r.Excluded, ", ")
			}
		}
		lines = append(lines, fmt.Sprintf("  %s %s for %s: %s", r.Describe(), r.State, applies, r.Counts()))
	}
	retired := len(all) - active
	header := fmt.Sprintf("Rules: %d active", active)
	if retired > 0 {
		header += fmt.Sprintf(", %d retired", retired)
	}
	out := []string{header}
	out = append(out, lines...)
	for _, model := range models {
		if line, ok := DetectorCountsLine(store, model); ok {
			out = append(out, "  "+line)
		}
	}
	return out
}
