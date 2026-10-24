package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
)

// Grading stays with the kernel (docs/memory-interface.md, section 8): after a held definition
// is written, probation judges it only by the kernel's events and by detectors that existed
// before it, so the model never grades its own change.

// KernelKeys are the counts every grade may read: the kernel's checks (Detectors) and the codec
// decode failures (CountCodecDecodeFailure), whose counting is kernel code.
func KernelKeys() []string {
	return append(append([]string{}, Detectors...), CountCodecDecodeFailure)
}

// GradingKeys are the counts a definition written at judged may be graded by: the kernel's keys,
// and the detectors the model defined whose written text last changed (layer.Harness.Changes:
// a put, revert, forget or rollback) strictly before judged. A detector written, redefined,
// reverted or rolled back at or after it, or held now, never grades it: the model cannot
// grade its own change by rewriting an earlier detector.
func GradingKeys(h layer.Harness, judged time.Time) []string {
	keys := append(KernelKeys(), CountEmptyPass)
	for _, d := range h.List(layer.KindDetector) {
		if unchangedSince(h, d, judged) {
			keys = append(keys, DefinedCountKey(d.Name))
		}
	}
	return keys
}

// unchangedSince reports whether detector d is written and its written text has not changed at
// or after t.
func unchangedSince(h layer.Harness, d layer.Definition, t time.Time) bool {
	if d.State == layer.StateHeld {
		return false
	}
	changes := h.Changes(layer.KindDetector, d.Name)
	return len(changes) > 0 && changes[len(changes)-1].Before(t)
}

// JudgedKind reports whether probation judges a written definition of kind named name: the
// definitions that change what an observer pass does, which the kernel's counts are taken over
// (format, schema, the observer's instructions, and a remedy catalogue). A defined tool
// (section 6) and a rule (section 7) have probations of their own; a detector changes nothing a
// pass does, it only counts; a summary changes no worker pass (the compaction that renders it
// is its proof); and the reflector's and dropper's instructions change no observer pass, so no
// kernel count could judge them: their proof is the recorded pass of their own stage that
// writes them (a held one that only ever runs empty passes is reverted).
func JudgedKind(kind, name string) bool {
	switch kind {
	case layer.KindFormat, layer.KindSchema:
		return true
	case layer.KindInstructions:
		return name == StageObserver
	case layer.KindRemedy:
		return containsString(Remedies, name)
	}
	return false
}

// Verdict is probation's decision about one written definition.
type Verdict struct {
	Model, Kind, Name string
	Established       bool
	Reason            string
	// Generation is the generation that recorded it.
	Generation int
}

// Message is the verdict as the user is told it.
func (v Verdict) Message() string {
	if v.Established {
		return fmt.Sprintf("Observational memory: %s definition %s for %s is established (%s; generation %d)", v.Kind, v.Name, v.Model, v.Reason, v.Generation)
	}
	return fmt.Sprintf("Observational memory: %s definition %s for %s is retired and what stood before it is restored (%s; generation %d)", v.Kind, v.Name, v.Model, v.Reason, v.Generation)
}

// sumKeys sums keys' counts and the observed tokens over the passes at indexes.
func sumKeys(events []curated.ModelEvent, indexes []int, keys []string) (count, tokens int) {
	for _, i := range indexes {
		for _, k := range keys {
			count += events[i].Counts[k]
		}
		tokens += events[i].Tokens
	}
	return count, tokens
}

// eventTime is when e was recorded; false for an unreadable time.
func eventTime(e curated.ModelEvent) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, e.Time)
	return t, err == nil
}

// Judge decides one written definition's probation from its model's usage ledger events and
// effective layer h, at written (the time of the put that wrote it); false while too few passes
// were counted after it. The rule:
//
//   - its evidence is the sum of the counts GradingKeys(h, written) allows, over the first
//     MinEvidence observer passes recorded after written (an empty one counted as
//     CountEmptyPass), against the same over the last MinEvidence observer passes before it
//     (its baseline). A detector the model defined counts
//     only when its text last changed before the baseline's first pass, so both windows count
//     the same detector and neither misses events the other could see;
//   - it is retired when its rate (events per observed source token) rose above the baseline's,
//     and established otherwise: a change to the harness must not make the kernel's counted
//     failures worse. With no baseline pass there is nothing to compare, and it is established.
func Judge(events []curated.ModelEvent, h layer.Harness, written time.Time) (established bool, reason string, decided bool) {
	start := len(events)
	for i, e := range events {
		if t, ok := eventTime(e); ok && t.After(written) {
			start = i
			break
		}
	}
	after := gradedPasses(events, start)
	if len(after) < MinEvidence {
		return false, "", false
	}
	after = after[:MinEvidence]
	before := gradedPasses(events[:start], 0)
	if len(before) > MinEvidence {
		before = before[len(before)-MinEvidence:]
	}
	if len(before) == 0 {
		return true, fmt.Sprintf("no passes before it to compare; %d passes since", len(after)), true
	}
	since := written
	if t, ok := eventTime(events[before[0]]); ok {
		since = t
	}
	keys := []string{}
	for _, k := range GradingKeys(h, written) {
		if name, defined := strings.CutPrefix(k, DefinedCountKey("")); defined {
			if d, ok := h.Get(layer.KindDetector, name); !ok || !unchangedSince(h, d, since) {
				continue
			}
		}
		keys = append(keys, k)
	}
	beforeEvents, beforeTokens := sumKeys(events, before, keys)
	afterEvents, afterTokens := sumKeys(events, after, keys)
	if beforeTokens == 0 || afterTokens == 0 {
		return true, "no observed source to compare", true
	}
	if afterEvents*beforeTokens > beforeEvents*afterTokens {
		return false, fmt.Sprintf("the kernel's counts rose from %d events in %d tokens to %d in %d over %d passes", beforeEvents, beforeTokens, afterEvents, afterTokens, len(after)), true
	}
	return true, fmt.Sprintf("the kernel's counts did not rise: %d events in %d tokens before, %d in %d over %d passes since", beforeEvents, beforeTokens, afterEvents, afterTokens, len(after)), true
}

// JudgeDefinitions decides the probation of model's written definitions that probation judges
// and has not decided (layer.Store.Undecided), by Judge over the model's usage ledger and its
// effective layer, recording each verdict in the layer (layer.Store.Settle: established, or
// retired and reverted to what stood before). It returns the verdicts to notify.
func JudgeDefinitions(store curated.Store, model string) ([]Verdict, error) {
	if model == "" {
		return nil, nil
	}
	ls := store.Layer()
	undecided, err := ls.Undecided(model)
	if err != nil || len(undecided) == 0 {
		return nil, err
	}
	h, _, err := ls.Load(model)
	if err != nil {
		return nil, err
	}
	events, err := store.ModelEvents(model)
	if err != nil {
		return nil, err
	}
	out := []Verdict{}
	for _, u := range undecided {
		if !JudgedKind(u.Kind, u.Name) {
			continue
		}
		written, err := time.Parse(time.RFC3339Nano, u.Put.Time)
		if err != nil {
			continue
		}
		established, reason, decided := Judge(events, h, written)
		if !decided {
			continue
		}
		g, err := ls.Settle(model, u.Kind, u.Name, established, reason)
		if errors.Is(err, layer.ErrNotFound) {
			continue
		}
		if err != nil {
			return out, err
		}
		out = append(out, Verdict{Model: model, Kind: u.Kind, Name: u.Name, Established: established, Reason: reason, Generation: g.N})
	}
	return out, nil
}

// Evidence is a model's counted evidence as a build or revision pass is given it: the events
// each kernel check and each detector it defined counted over its last MinEvidence observer
// passes, with the tokens they observed, and its rules with their states.
type Evidence struct {
	Model          string
	Passes, Tokens int
	Counts         map[string]int
	Rules          []string
	// Events is the length of the model's usage ledger it was read at.
	Events int
}

// EvidenceOf reads model's evidence from its usage ledger and the rules that apply to it.
func EvidenceOf(store curated.Store, model string) (Evidence, error) {
	events, err := store.ModelEvents(model)
	if err != nil {
		return Evidence{}, err
	}
	e := Evidence{Model: model, Counts: map[string]int{}, Events: len(events)}
	passes := gradedPasses(events, 0)
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
	for _, r := range For(store, model).Rules {
		e.Rules = append(e.Rules, r.Describe()+" "+r.State)
	}
	sort.Strings(e.Rules)
	return e, nil
}

// Digest is what a revision compares: which counts are non-zero and the rules' states, not the
// counts themselves, so a revision answers a change in what fails rather than every pass's
// numbers.
func (e Evidence) Digest() string {
	keys := []string{}
	for k, n := range e.Counts {
		if n > 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, ",") + "\x1f" + strings.Join(e.Rules, "\x1f")))
	return hex.EncodeToString(sum[:])[:16]
}

// Text is the evidence as the revision pass's prompt carries it.
func (e Evidence) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Counted over your last %d memory passes (%d observed source tokens):\n", e.Passes, e.Tokens)
	keys := make([]string, 0, len(e.Counts))
	for k := range e.Counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		b.WriteString("- no detector counted anything\n")
	}
	for _, k := range keys {
		fmt.Fprintf(&b, "- %s: %d\n", k, e.Counts[k])
	}
	if len(e.Rules) == 0 {
		b.WriteString("Rules applied to you: none\n")
	} else {
		b.WriteString("Rules applied to you:\n- " + strings.Join(e.Rules, "\n- ") + "\n")
	}
	return b.String()
}

// RevisionDue reports whether a revision pass is due for model after a pass, with the evidence
// it would be given. The rule: the model has had a build pass, at least MinEvidence observer
// passes were counted since its last build or revision pass, and its evidence (Digest) differs
// from the evidence that pass was given. A revision is so at most once per MinEvidence observer
// passes, and only when what fails changed.
func RevisionDue(store curated.Store, model string) (Evidence, bool, error) {
	last, ok, err := store.Layer().LastPass(model)
	if err != nil || !ok {
		return Evidence{}, false, err
	}
	e, err := EvidenceOf(store, model)
	if err != nil {
		return Evidence{}, false, err
	}
	events, err := store.ModelEvents(model)
	if err != nil {
		return Evidence{}, false, err
	}
	if len(gradedPasses(events, last.Events)) < MinEvidence {
		return e, false, nil
	}
	return e, e.Digest() != last.Evidence, nil
}
