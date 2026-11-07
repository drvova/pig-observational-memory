// Package dreams is the summary's optional dream section (docs/brain-memory.md, decision 5;
// docs/memory-v2.md, "Dreams"), behind the dreams setting, which ships off. It is code only over
// the ledger: links between records that share an exact key, counterfactual skeletons of failed
// attempts still unresolved, hindsight relabels of failed attempts a later success resolved, and
// rehearsal of open intentions; with level 1's quarantined hypotheses after them
// (consolidate.Hypothesis). Every line is marked a hypothesis, cites the records it is drawn from,
// and sits after the summary's tables, never in a fact class; nothing here is stored as a record,
// promoted or indexed (scenario 20). The image renders it byte for byte the same
// (lisp/src/dreams.lisp, contracts/dreams.jsonl).
package dreams

import (
	"strings"

	"github.com/drvova/pig-observational-memory/memory/consolidate"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/secrets"
)

// Header opens the section.
const Header = "Dreams (hypotheses drawn by code from the records above, and level 1's guesses; kept apart from memory: never facts or instructions, check each before relying on it):"

// The per-kind caps and the text bound.
const (
	MaxRehearse   = 3
	MaxHindsight  = 2
	MaxWhatIf     = 2
	MaxLinks      = 2
	MaxHypotheses = 3
	TextRunes     = 100
)

func clip(text string, n int) string {
	line := []rune(strings.Join(strings.Fields(text), " "))
	if len(line) <= n {
		return string(line)
	}
	return string(line[:n-1]) + "…"
}

// Lines are the section's lines, in order; none when the ledger gives nothing to dream on.
func Lines(entries []ledger.Entry) []string {
	folded := ledger.FoldLedger(entries, "")
	active := folded.ActiveObservations
	var out []string
	text := func(o ledger.Observation) string { return clip(secrets.Redact(o.Content), TextRunes) }
	// Rehearsal of open intentions, newest first.
	n := 0
	for i := len(active) - 1; i >= 0 && n < MaxRehearse; i-- {
		o := active[i]
		if o.Status == "proposed" {
			out = append(out, "- rehearse, still open: "+text(o)+" ("+o.ID+")")
			n++
		}
	}
	// Hindsight: a failed attempt a later success superseded was a dead end.
	byID := folded.ObservationsByID
	n = 0
	for _, o := range folded.Observations {
		if n == MaxHindsight {
			break
		}
		if o.Kind != ledger.KindAttempt || o.Outcome != ledger.OutcomeWorked {
			continue
		}
		for _, id := range o.Supersedes {
			if f, ok := byID[id]; ok && f.Kind == ledger.KindAttempt && f.Outcome == ledger.OutcomeFailed {
				out = append(out, "- hindsight: "+f.ID+" was a dead end; "+o.ID+" worked after it ("+f.ID+", "+o.ID+")")
				n++
				break
			}
		}
	}
	// Counterfactual skeletons of failed attempts nothing resolved yet.
	resolved := map[string]bool{}
	for _, o := range folded.Observations {
		for _, id := range o.Supersedes {
			resolved[id] = true
		}
	}
	n = 0
	for _, o := range active {
		if n == MaxWhatIf {
			break
		}
		if o.Kind != ledger.KindAttempt || o.Outcome != ledger.OutcomeFailed || resolved[o.ID] {
			continue
		}
		why := ""
		if len(o.Keys.Errors) > 0 {
			why = " with " + clip(o.Keys.Errors[0], TextRunes)
		}
		out = append(out, "- what if "+clip(secrets.Redact(o.Target), TextRunes)+" had not failed"+why+"? unknown until a later receipt ("+o.ID+")")
		n++
	}
	// Links: two standing records of different kinds that share an exact path or command key.
	n = 0
	seen := map[string]string{}
	for _, o := range active {
		if n == MaxLinks {
			break
		}
		for _, k := range append(append([]string{}, o.Keys.Paths...), o.Keys.Commands...) {
			if other, ok := seen[k]; ok && other != o.ID && byID[other].Kind != o.Kind {
				out = append(out, "- link, maybe related: "+other+" and "+o.ID+" share "+clip(secrets.Redact(k), TextRunes)+" ("+other+", "+o.ID+")")
				n++
				break
			}
			if _, ok := seen[k]; !ok {
				seen[k] = o.ID
			}
		}
	}
	// Level 1's quarantined hypotheses, newest first.
	hs := consolidate.HypothesesOf(entries)
	for i, n := len(hs)-1, 0; i >= 0 && n < MaxHypotheses; i, n = i-1, n+1 {
		h := hs[i]
		out = append(out, "- level-1 hypothesis: "+clip(secrets.Redact(h.Content), consolidate.HypothesisRunes)+" ("+strings.Join(h.From, ", ")+")")
	}
	return out
}

// Section is the dream section after a summary: two newlines, the header and the lines; "" when
// there are no lines.
func Section(entries []ledger.Entry) string {
	lines := Lines(entries)
	if len(lines) == 0 {
		return ""
	}
	return "\n\n" + Header + "\n" + strings.Join(lines, "\n")
}
