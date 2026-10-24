package rules

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
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

// isObserverPass reports whether e is an observer pass: a pass that observed source, the only
// kind with a rate.
func isObserverPass(e curated.ModelEvent) bool {
	return e.Kind == curated.ModelKindPass && e.Tokens > 0
}

// observerPasses are the indexes of the observer passes in events at or after from.
func observerPasses(events []curated.ModelEvent, from int) []int {
	out := []int{}
	for i := max(0, from); i < len(events); i++ {
		if isObserverPass(events[i]) {
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

// baseline is the detector's events and tokens over the last MinEvidence observer passes
// before end.
func baseline(events []curated.ModelEvent, end int, detector string) (int, int) {
	passes := observerPasses(events[:min(end, len(events))], 0)
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

// Evaluate decides the rules of model from its usage ledger events (curated.Store.ModelEvents,
// in the order written) and every rule in the store, at now. It is pure. It returns the rules
// it changed or added, and the transitions to notify. The rules:
//
//   - Probation: a rule on probation for model is decided once MinEvidence observer passes were
//     counted after it was written. It is established when its detector's rate over them
//     (events per observed source token) is below its baseline rate (the same over the
//     MinEvidence observer passes before it), and retired otherwise: a rule survives only by
//     bringing down the count it was written for. A baseline with no events cannot fall, so a
//     rule for a behaviour the model did not show is retired.
//   - Harness rules: when a detector counted events in each of the model's last MinEvidence
//     observer passes since its last rule for that detector was written or decided (the count
//     kept rising, pass after pass), and no rule for that detector is on probation for the
//     model, the harness writes the first remedy in the Catalogue for that detector that no
//     rule for the model has tried yet (a default the model is not excluded from counts as
//     tried). With every remedy tried, it writes none. switch-codec is skipped while none of
//     the decode failures in those passes were rows the codec could not decode (only memory
//     queries that did not parse), and it is judged on those codec failures alone
//     (CountCodecDecodeFailure).
//   - Defaults: a rule established, with one signature, for DefaultAfterModels models becomes
//     a default for all, established, excluding the models for which it was retired.
func Evaluate(model string, events []curated.ModelEvent, all []Rule, now time.Time) ([]Rule, []Transition) {
	var changed []Rule
	var transitions []Transition
	rules := append([]Rule(nil), all...)
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
		passes := observerPasses(events, r.Start)
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
	for _, detector := range Detectors {
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
		passes := observerPasses(events, floor)
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
		for _, candidate := range Catalogue[detector] {
			if tried[candidate] {
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
		r, err := Check(harnessRule(detector, remedy, kinds))
		if err != nil {
			continue
		}
		r.Models, r.State, r.Created, r.Start = []string{model}, StateProbation, now, len(events)
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
	for _, r := range rules {
		sig := r.Signature()
		switch {
		case r.Default():
			hasDefault[sig] = true
		case r.State == StateEstablished:
			if established[sig] == nil {
				established[sig] = map[string]bool{}
			}
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
		d := Rule{Detector: base.Detector, Remedy: base.Remedy, Kind: base.Kind, Fullness: base.Fullness, Text: base.Text,
			Models: []string{AllModels}, Excluded: retired[sig], State: StateEstablished, Proposer: ProposerHarness, Created: now}
		d.Reason = fmt.Sprintf("established for %d models", len(models))
		d.ID = newID(d)
		replace(d)
		changed = append(changed, d)
		transitions = append(transitions, Transition{Rule: d, To: StateEstablished, Added: true})
	}
	return changed, transitions
}

// Pass is one memory-worker pass, as AfterPass records it.
type Pass struct {
	Stage string
	// Tokens is the pass's observed source tokens (SourceTokens of the covered entries).
	Tokens int
	// Findings are the note detectors' findings; nil for a reflector or dropper pass.
	Findings *Findings
}

// AfterPass records one pass of model in its usage ledger (with decode-failure counted from
// the ledger since its previous observer pass) and evaluates the rules for model, writing the
// ones that changed. The ledger is read, counted, appended to and evaluated under one hold of
// the store's lock, so two processes finishing a pass at once never count the same failures
// twice. It returns the transitions to notify. A failed write returns what was decided so far:
// counting never fails the pass it counts.
func AfterPass(store curated.Store, model string, pass Pass) ([]Transition, error) {
	if model == "" {
		return nil, errors.New("a pass needs a model id")
	}
	e := curated.ModelEvent{Model: model, Kind: curated.ModelKindPass, Stage: pass.Stage, Tokens: pass.Tokens, Counts: map[string]int{}}
	if pass.Findings != nil {
		for d, n := range pass.Findings.Counts {
			if n > 0 {
				e.Counts[d] = n
			}
		}
		if len(pass.Findings.DroppedKinds) > 0 {
			e.Kinds = pass.Findings.DroppedKinds
		}
	}
	var transitions []Transition
	err := store.Locked(func() error {
		events, err := store.ModelEvents(model)
		if err != nil {
			return err
		}
		// Only an observer pass carries a rate, so only one carries the decode failures.
		if codec, syntax := DecodeFailuresSince(events); codec+syntax > 0 && isObserverPass(e) {
			e.Counts[DetectDecodeFailure] = codec + syntax
			if codec > 0 {
				e.Counts[CountCodecDecodeFailure] = codec
			}
		}
		if err := store.RecordModelLocked(e); err != nil {
			return err
		}
		if events, err = store.ModelEvents(model); err != nil {
			return err
		}
		all, err := List(store)
		if err != nil {
			return err
		}
		changed, ts := Evaluate(model, events, all, store.Now().UTC())
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

// Proposal is a rule a model proposes with the memory tool's propose write.
type Proposal struct {
	Detector, Remedy, Text, Kind string
	Fullness                     float64
}

// Propose writes a rule model proposed, on probation for model. It is checked like a harness
// rule (Check), and refused when the same rule is already applied to model or was retired for
// it (the detector decided), or when its text is not the model's own: text found in a tool
// result, a custom message or a summary on the branch came from a file, a web page or tool
// output, and rule text never does. Its baseline is the detector's count over the model's
// last MinEvidence observer passes; only the detector decides it from there.
func Propose(store curated.Store, model string, branch []ledger.Entry, p Proposal) (Rule, error) {
	if model == "" {
		return Rule{}, errors.New("a rule needs a model id, and the session model has none")
	}
	r, err := Check(Rule{Detector: p.Detector, Remedy: p.Remedy, Text: p.Text, Kind: p.Kind, Fullness: p.Fullness})
	if err != nil {
		return Rule{}, err
	}
	if r.Text != "" && copiedFromOutput(branch, r.Text) {
		return Rule{}, errors.New("refused: that text appears in tool output, a file or another message on this branch; a rule's text must be your own words")
	}
	r.Proposer, r.Models, r.State = ProposerModel, []string{model}, StateProbation
	var saved Rule
	err = store.Locked(func() error {
		all, err := List(store)
		if err != nil {
			return err
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
		return write(store, r)
	})
	if err != nil {
		return Rule{}, err
	}
	return saved, nil
}

// copiedFromOutput reports whether text, whitespace collapsed and case ignored, appears in a
// tool result, a custom message or a summary on the branch: output the model read, not words
// it wrote.
func copiedFromOutput(branch []ledger.Entry, text string) bool {
	needle := strings.ToLower(strings.Join(strings.Fields(text), " "))
	for _, entry := range branch {
		var haystack string
		switch {
		case entry.Type == "message" && entry.Message != nil && entry.Message["role"] == "bashExecution":
			// A `!command` run keeps its command and output in their own fields, not content.
			haystack = serialize.BashExecutionText(entry.Message)
		case entry.Type == "message" && entry.Message != nil && entry.Message["role"] != "user" && entry.Message["role"] != "assistant":
			haystack = blockText(entry.Message["content"])
		case entry.Type == "custom_message":
			haystack = blockText(entry.Content)
		case entry.Type == "branch_summary" || entry.Type == "compaction":
			haystack, _ = entry.Summary.(string)
		default:
			continue
		}
		if strings.Contains(strings.ToLower(strings.Join(strings.Fields(haystack), " ")), needle) {
			return true
		}
	}
	return false
}

// For reads the rules that apply to model in store; none when the store cannot be read.
func For(store curated.Store, model string) Active {
	all, err := List(store)
	if err != nil {
		return Active{}
	}
	return ActiveFor(all, model)
}

// StatusLines is the rules section of /om:status: every active rule with its counts, and for
// each of models (in order) its detector counts over its last MinEvidence observer passes.
func StatusLines(store curated.Store, models []string) []string {
	all, err := List(store)
	if err != nil {
		return nil
	}
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
		events, err := store.ModelEvents(model)
		if err != nil {
			continue
		}
		passes := observerPasses(events, 0)
		if len(passes) == 0 {
			continue
		}
		if len(passes) > MinEvidence {
			passes = passes[len(passes)-MinEvidence:]
		}
		parts := []string{}
		for _, d := range Detectors {
			if count, _ := window(events, passes, d); count > 0 {
				parts = append(parts, fmt.Sprintf("%s %d", d, count))
			}
		}
		_, tokens := window(events, passes, "")
		counts := "none"
		if len(parts) > 0 {
			counts = strings.Join(parts, ", ")
		}
		out = append(out, fmt.Sprintf("  Detector counts for %s over its last %d passes (%d tokens): %s", model, len(passes), tokens, counts))
	}
	return out
}
