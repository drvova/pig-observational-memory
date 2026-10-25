package rules

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
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

// isObserverPass reports whether e is an observer pass: a pass that observed source and recorded
// notes, the only kind with a rate the rules are decided by. An observer pass that recorded
// nothing (isEmptyPass) is counted for probation only (gradedPasses).
func isObserverPass(e curated.ModelEvent) bool {
	return e.Kind == curated.ModelKindPass && e.Tokens > 0 && e.Outcome != curated.OutcomeEmpty
}

// isEmptyPass reports whether e is a worker pass that ran and recorded nothing (Pass.Empty).
func isEmptyPass(e curated.ModelEvent) bool {
	return e.Kind == curated.ModelKindPass && e.Outcome == curated.OutcomeEmpty
}

// isGradedPass reports whether e is a pass probation and the revision pass read: an observer
// pass, recorded or empty, so a definition that silences the observer is counted
// (CountEmptyPass) rather than invisible.
func isGradedPass(e curated.ModelEvent) bool {
	return e.Kind == curated.ModelKindPass && e.Tokens > 0
}

// gradedPasses are the indexes of the graded passes (isGradedPass) in events at or after from.
func gradedPasses(events []curated.ModelEvent, from int) []int {
	out := []int{}
	for i := max(0, from); i < len(events); i++ {
		if isGradedPass(events[i]) {
			out = append(out, i)
		}
	}
	return out
}

// EmptyPassesSince counts model's passes of stage that ran and recorded nothing (Pass.Empty)
// after since, with no recorded pass of stage between them and now: the passes a held
// definition ran through without anything proving it.
func EmptyPassesSince(store curated.Store, model, stage string, since time.Time) (int, error) {
	events, err := store.ModelEvents(model)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range events {
		if e.Kind != curated.ModelKindPass || e.Stage != stage {
			continue
		}
		if t, ok := eventTime(e); !ok || !t.After(since) {
			continue
		}
		if isEmptyPass(e) {
			n++
		} else {
			n = 0
		}
	}
	return n, nil
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

// countRevisions are the revisions of the pass counts whose meaning changed after the usage
// ledger had recorded them. uncited-assistant-specifics is at 2: from then on it counts only the
// specifics a reply added to its exchange (UncitedAssistantSpecifics), where revision 1 counted
// every uncited reply with specifics, echoes included.
var countRevisions = map[string]int{DetectUncitedAssistantSpecifics: 2}

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

// countedPasses are the observer passes at or after from that counted key by its current
// revision (countedNow): the passes a rule for key is written, baselined and judged on.
func countedPasses(events []curated.ModelEvent, from int, key string) []int {
	out := []int{}
	for _, i := range observerPasses(events, from) {
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

// baseline is the detector's events and tokens over the last MinEvidence observer passes
// before end that counted it by its current revision (countedPasses).
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

// Evaluate decides the rules of model from its usage ledger events (curated.Store.ModelEvents,
// in the order written) and every rule in the store, at now. It is pure. It returns the rules
// it changed or added, and the transitions to notify. The rules:
//
//   - Every window below holds only the observer passes that counted the rule's count by its
//     current revision (countedPasses): a pass counted by an earlier revision of a count whose
//     meaning changed is no evidence for it, so a baseline never mixes two meanings of a count.
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
	return EvaluateWith(model, events, all, now, SeedCatalog())
}

// EvaluateWith is Evaluate with the model's own remedy catalogue (CatalogOf its layer): the
// remedies the harness tries for each detector, in its order and with its parameters.
func EvaluateWith(model string, events []curated.ModelEvent, all []Rule, now time.Time, catalog Catalog) ([]Rule, []Transition) {
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
		r, err := Check(catalog.Rule(detector, remedy, kinds))
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
			Models: []string{AllModels}, Excluded: retired[sig], State: StateEstablished, Proposer: proposer, Created: now}
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
	// Layer is the model's effective layer the pass ran with, whose catalogue the rules are
	// decided by; nil loads it from the store.
	Layer *layer.Harness
	// Empty marks a pass that ran and recorded nothing (an observer chunk with no notes, a
	// reflector with no reflections, a dropper that dropped nothing). It is recorded with the
	// outcome empty and, for an observer pass, CountEmptyPass and the detectors' findings over
	// the chunk it left unnoted, so probation sees a definition that silences a worker; it
	// decides no rule (it is no observer pass the rules count).
	Empty bool
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
	if pass.Empty {
		// An empty pass is evidence for probation only: no rule is decided by it. An observer
		// one carries what the kernel's checks found in the chunk it left unnoted.
		e.Outcome = curated.OutcomeEmpty
		if pass.Stage != StageObserver || pass.Tokens <= 0 {
			e.Tokens = 0
			return nil, store.RecordModel(e)
		}
		e.Counts[CountEmptyPass] = 1
		if pass.Findings != nil {
			e.Revisions = Revisions()
			for d, n := range pass.Findings.Counts {
				if n > 0 {
					e.Counts[d] = n
				}
			}
			for d, n := range pass.Findings.Defined {
				if n > 0 {
					e.Counts[DefinedCountKey(d)] = n
				}
			}
		}
		return nil, store.RecordModel(e)
	}
	if pass.Findings != nil {
		e.Revisions = Revisions()
		for d, n := range pass.Findings.Counts {
			if n > 0 {
				e.Counts[d] = n
			}
		}
		if len(pass.Findings.DroppedKinds) > 0 {
			e.Kinds = pass.Findings.DroppedKinds
		}
		for d, n := range pass.Findings.Defined {
			if n > 0 {
				e.Counts[DefinedCountKey(d)] = n
			}
		}
	}
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
		all, err := list(store)
		if err != nil {
			return err
		}
		changed, ts := EvaluateWith(model, events, all, store.Now().UTC(), catalog)
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

// Propose holds a rule model proposed, on probation for model: it applies to the model's next
// pass and is written once that pass is clean (layer.Store.Claim), as every definition from the
// model is (docs/memory-interface.md, section 8). It is checked like a harness
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
// tool result, a custom message or a summary on the branch: output the model read, not words
// it wrote.
func copiedFromOutput(branch []ledger.Entry, text string) bool {
	return containedIn(outputTexts(branch), normalizeText(text))
}

// normalizeText is text with whitespace collapsed and case ignored, as the provenance checks
// compare it.
func normalizeText(text string) string {
	return strings.ToLower(strings.Join(strings.Fields(text), " "))
}

// outputTexts are the texts on branch the model read rather than wrote (tool results, custom
// messages, summaries, `!command` runs), each normalized (normalizeText).
func outputTexts(branch []ledger.Entry) []string {
	out := []string{}
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
		if haystack = normalizeText(haystack); haystack != "" {
			out = append(out, haystack)
		}
	}
	return out
}

// containedIn reports whether needle (normalized) appears in any of haystacks.
func containedIn(haystacks []string, needle string) bool {
	if needle == "" {
		return false
	}
	for _, h := range haystacks {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}

// For reads the rules that apply to model in store, from its effective layer: the written and
// held rules kept for every model and for model, so a held proposal applies to the pass that
// proves it. None when the store cannot be read.
func For(store curated.Store, model string) Active {
	migrate(store)
	h, _, err := store.Layer().Load(model)
	if err != nil {
		return Active{}
	}
	return ActiveFor(rulesOf(h.List(layer.KindRemedy)), model)
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
