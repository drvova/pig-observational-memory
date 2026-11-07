package memeval

import (
	"strconv"

	"github.com/drvova/pig-observational-memory/memory/tokens"
)

// The summary-of-summary arm (docs/brain-memory.md, phase 0's erosion curve; decision m of the
// measuring contract): what a compaction that re-summarizes its previous summary keeps, the
// engine's own design never does (the summary is selection over the ledger, never
// summary-of-summary). It is code only and generous: at compaction n the arm's summary is the
// rows the engine's summary n picked from the turns since compaction n-1, then the arm's summary
// n-1's rows verbatim, cut to the room summary n's rows take. A re-summarizing model can at best keep that
// much, so the curve is an upper bound on what summary-of-summary keeps. Probes are scored as
// Tier 0 scores them, shown or lost: the arm prints no snippet, so nothing is reachable.

// rowTurn is the turn of a row's source entry (TurnEntries' ids: a letter, three digits), -1
// for none.
func rowTurn(source string) int {
	if len(source) < 4 {
		return -1
	}
	n, err := strconv.Atoi(source[1:4])
	if err != nil {
		return -1
	}
	return n
}

// SummaryOfSummary scores run under the summary-of-summary arm.
func SummaryOfSummary(run Run, gold []Probe) Result {
	res := Result{Seed: run.Script.Seed}
	ages := map[string]int{}
	var carried []Row
	prevTurn := -1
	for _, snap := range run.Snapshots {
		// The room is what summary n's own rows take, its header and omitted spans left out.
		budget := 0
		var next []Row
		for _, r := range SummaryRows(snap.Summary) {
			if r.Table == "omitted" {
				continue
			}
			budget += tokens.EstimateStringTokens(r.Text) + 1
			if t := rowTurn(r.Source); t > prevTurn {
				next = append(next, r)
			}
		}
		next = append(next, carried...)
		used := 0
		kept := next[:0]
		for _, r := range next {
			cost := tokens.EstimateStringTokens(r.Text) + 1
			if used+cost > budget {
				break
			}
			used += cost
			kept = append(kept, r)
		}
		carried = append([]Row{}, kept...)
		prevTurn = snap.Turn - 1
		for _, p := range gold {
			if p.Turn >= snap.Turn {
				continue
			}
			ages[p.Name]++
			cell := Cell{Probe: p.Name, Class: p.Class, N: ages[p.Name], Outcome: Lost}
			if anyRowHolds(carried, p) {
				cell.Outcome = Shown
			}
			res.Cells = append(res.Cells, cell)
		}
	}
	return res
}
