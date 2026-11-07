package memeval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/om"
	"github.com/drvova/pig-observational-memory/memory/om/omwire"
	"github.com/drvova/pig-observational-memory/memory/omhost"
	"github.com/drvova/pig-observational-memory/memory/omtool"
)

// The retrieval check (docs/brain-memory-baseline.md, "Phase 4: retrieval"): for each probe of
// a retrieval class, does om.Notes(om.Search(<the probe's key>)) list the probe's target record
// in its first RetrievalTopK rows, over the session's last branch? It runs the read in process
// (omhost.Call, no child, no model request), as the memory tool's reads answer it.

// RetrievalTopK is how many rows of the search count.
const RetrievalTopK = 5

// The retrieval classes, in report order: the Tier 0 classes a key can name, a tool-output
// value, and the superseded value (whose target must also rank above the value it replaced).
const (
	RetrievalIdentifier = "identifier"
	RetrievalIdentParts = "identifier parts"
	RetrievalError      = "error text"
	RetrievalConstraint = "constraint"
	RetrievalDecision   = "decision"
	RetrievalValue      = "value"
	RetrievalSuperseded = "superseded"
)

// RetrievalClasses are the classes in report order.
var RetrievalClasses = []string{RetrievalIdentifier, RetrievalIdentParts, RetrievalError, RetrievalConstraint, RetrievalDecision, RetrievalValue, RetrievalSuperseded}

// RetrievalTally is one class's probes: how many have a target record in the ledger, how many
// of those the search lists in its top rows, and how many it lists first.
type RetrievalTally struct {
	Probes, Present, Top, First int
}

// retrievalProbe is one probe's search: the key searched, and the test a row's record passes to
// be the target; old, for a superseded value, finds the record it replaced.
type retrievalProbe struct {
	class  string
	key    string
	target func(ledger.Observation) bool
	old    func(ledger.Observation) bool
}

// retrievalProbes are the searches of a run's probes.
func retrievalProbes(run Run) []retrievalProbe {
	var out []retrievalProbe
	holds := func(needles ...string) func(ledger.Observation) bool {
		return func(o ledger.Observation) bool { return holdsAll(o.Content, needles) }
	}
	for _, p := range run.Script.Probes {
		switch p.Class {
		case ClassIdentifier:
			ident := p.Needles[0]
			name, _, _ := strings.Cut(ident, "(")
			out = append(out, retrievalProbe{class: RetrievalIdentifier, key: name, target: holds(ident)})
			// The same name as a person types it apart: retryWithCache42 as retry_with_cache42.
			out = append(out, retrievalProbe{class: RetrievalIdentParts, key: snakeCase(name), target: holds(ident)})
		case ClassError:
			out = append(out, retrievalProbe{class: RetrievalError, key: p.Needles[0], target: holds(p.Needles[0])})
			if p.Turn < len(run.Script.Turns) && len(run.Script.Turns[p.Turn].Calls) > 0 {
				result := run.Script.Turns[p.Turn].Calls[0].Result
				if rest, ok := strings.CutPrefix(result, "--- FAIL: "); ok {
					test, _, _ := strings.Cut(rest, " ")
					out = append(out, retrievalProbe{class: RetrievalValue, key: test, target: func(o ledger.Observation) bool {
						return o.Kind == ledger.KindValue && o.Target == "test:"+test
					}})
				}
			}
		case ClassConstraint:
			needles := p.Needles
			out = append(out, retrievalProbe{class: RetrievalConstraint, key: p.Needles[0], target: func(o ledger.Observation) bool {
				return (o.Kind == "preference" || o.Kind == "correction") && holdsAll(o.Content, needles)
			}})
		case ClassDecision:
			needles := p.Needles
			out = append(out, retrievalProbe{class: RetrievalDecision, key: p.Needles[0], target: func(o ledger.Observation) bool {
				return o.Kind == ledger.KindDecision && holdsAll(o.Content, needles)
			}})
		case ClassSuperseded:
			newer, older := p.Needles[0], p.Old
			out = append(out, retrievalProbe{class: RetrievalSuperseded, key: p.Subject + " port", target: holds(newer, p.Subject), old: func(o ledger.Observation) bool {
				return holdsAll(o.Content, []string{older}) && !holdsAll(o.Content, []string{newer})
			}})
		}
	}
	return out
}

// RetrievalCheck runs every probe's search over each run's last branch and tallies the classes.
func RetrievalCheck(ctx context.Context, runs []Run) (map[string]RetrievalTally, error) {
	out := map[string]RetrievalTally{}
	for _, run := range runs {
		if len(run.Snapshots) == 0 {
			continue
		}
		branch := run.Snapshots[len(run.Snapshots)-1].Branch
		folded := ledger.FoldLedger(branch, "")
		call := &omhost.Call{Entries: branch, Skip: []string{omtool.ToolName}}
		for _, p := range retrievalProbes(run) {
			t := out[p.class]
			t.Probes++
			present := false
			for _, o := range folded.ActiveObservations {
				if p.target(o) {
					present = true
					break
				}
			}
			if !present {
				out[p.class] = t
				continue
			}
			t.Present++
			limit := RetrievalTopK
			raw, err := json.Marshal([]any{omwire.Options{Search: []string{p.key}, Limit: &limit}})
			if err != nil {
				return nil, err
			}
			answer, err := call.Call(ctx, omwire.MNotes, raw)
			if err != nil {
				return nil, fmt.Errorf("%s search %q: %w", p.class, p.key, err)
			}
			var list om.NoteList
			if err := json.Unmarshal(answer, &list); err != nil {
				return nil, err
			}
			targetAt, oldAt := -1, -1
			for i, n := range list.Rows {
				o, ok := folded.ObservationsByID[n.ID]
				if !ok {
					continue
				}
				if targetAt < 0 && p.target(o) {
					targetAt = i
				}
				if oldAt < 0 && p.old != nil && p.old(o) {
					oldAt = i
				}
			}
			if targetAt >= 0 && (oldAt < 0 || targetAt < oldAt) {
				t.Top++
				if targetAt == 0 {
					t.First++
				}
			}
			out[p.class] = t
		}
	}
	return out, nil
}

// RetrievalTable is the check as a markdown table.
func RetrievalTable(tallies map[string]RetrievalTally) string {
	var b strings.Builder
	fmt.Fprintf(&b, "| Class | Probes | Target in ledger | In top %d | First |\n|---|---|---|---|---|\n", RetrievalTopK)
	for _, c := range RetrievalClasses {
		t := tallies[c]
		if t.Probes == 0 {
			continue
		}
		top, first := "–", "–"
		if t.Present > 0 {
			top, first = fmt.Sprintf("%d (%s)", t.Top, pct(t.Top, t.Present)), fmt.Sprintf("%d (%s)", t.First, pct(t.First, t.Present))
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %s | %s |\n", c, t.Probes, t.Present, top, first)
	}
	return b.String()
}

// snakeCase is a camelCase name in snake case: retryWithCache42 is retry_with_cache42.
func snakeCase(name string) string {
	var b strings.Builder
	for i, r := range name {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			r += 'a' - 'A'
		}
		b.WriteRune(r)
	}
	return b.String()
}
