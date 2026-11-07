package memeval

import (
	"context"
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/ledger"
)

// Tier 0b part (c) with decay on (docs/brain-memory.md, decision 6; contracts/truth.md, "Decay"):
// the Tier 0 runs again with the decay setting on a compaction clock and on a turn clock at the
// same budget (the turn horizon is the compaction horizon times the runs' mean turns per
// compaction), scored for the protected classes and for every Tier 0 class.

// DecayCounts are the compaction counts the decay comparison reports.
var DecayCounts = []int{1, 4, 8, 16, 20}

// decayArm is one decay setting's runs.
type decayArm struct {
	name             string
	clock            string
	horizon          int
	protected, tier0 map[Class]map[int]Tally
}

// decayRows runs the decay arms beside off (the Tier 0 runs without decay) and adds their rows.
func decayRows(ctx context.Context, s *Suite, cfg SuiteConfig, off []Run, lister Lister) error {
	turns, compactions := 0, 0
	for _, run := range off {
		if n := len(run.Snapshots); n > 0 {
			turns += run.Snapshots[n-1].Turn + 1
			compactions += n
		}
	}
	perCompaction := 1
	if compactions > 0 {
		perCompaction = max(1, (turns+compactions/2)/compactions)
	}
	horizon := ledger.DefaultDecayHorizon
	arms := []*decayArm{
		{name: "off", horizon: horizon},
		{name: fmt.Sprintf("compactions, horizon %d", horizon), clock: ledger.DecayCompactions, horizon: horizon},
		{name: fmt.Sprintf("turns, horizon %d", horizon*perCompaction), clock: ledger.DecayTurns, horizon: horizon * perCompaction},
	}
	t0 := DefaultConfig(0)
	if cfg.Tier0Turns > 0 {
		t0.Turns, t0.MaxCompactions = cfg.Tier0Turns, cfg.Tier0Compactions
	}
	for _, arm := range arms {
		runs := off
		if arm.clock != "" {
			runs = nil
			for _, run := range off {
				w := t0.Window
				w.Decay, w.DecayHorizon = arm.clock, arm.horizon
				r, err := Simulate(ctx, run.Script, w, t0.MaxCompactions)
				if err != nil {
					return err
				}
				runs = append(runs, r)
			}
		}
		var err error
		if arm.protected, err = protectedTallies(runs, lister); err != nil {
			return err
		}
		var results []Result
		for _, run := range runs {
			results = append(results, Evaluate(run, run.Script.Probes, lister))
		}
		arm.tier0 = Curves(results)
	}
	const part = "c. protected"
	base := arms[0]
	for _, arm := range arms[1:] {
		below, lost, cells := 0, 0, 0
		for _, class := range ProtectedClasses {
			for _, n := range ProtectedCounts {
				t, b := arm.protected[class][n], base.protected[class][n]
				cells += t.Total()
				lost += t.Lost
				if t.Shown < b.Shown {
					below++
				}
			}
		}
		s.add(part, "decay on ("+arm.clock+" clock): no protected class below its floor, none lost", verdict(below == 0 && lost == 0 && cells > 0), "",
			"%d cells, %d shown less than without decay, %d lost", cells, below, lost)
		more := 0
		for class, byN := range arm.tier0 {
			for n, t := range byN {
				if t.Lost > base.tier0[class][n].Lost {
					more += t.Lost - base.tier0[class][n].Lost
				}
			}
		}
		s.add(part, "decay on ("+arm.clock+" clock) lowers rank only: nothing lost that is kept without it", verdict(more == 0), "", "%d more cells lost than without decay", more)
	}
	s.Extra = append(s.Extra, DecayTable(arms))
	return nil
}

// DecayTable compares what each decay arm keeps: shown / reachable / lost, in percent, by class
// and compaction count.
func DecayTable(arms []*decayArm) string {
	var b strings.Builder
	b.WriteString("| class | decay |")
	for _, n := range DecayCounts {
		fmt.Fprintf(&b, " %d |", n)
	}
	b.WriteString("\n|---|---|")
	for range DecayCounts {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	row := func(class Class, pick func(*decayArm) map[Class]map[int]Tally) {
		for _, arm := range arms {
			fmt.Fprintf(&b, "| %s | %s |", class, arm.name)
			for _, n := range DecayCounts {
				t := pick(arm)[class][n]
				if t.Total() == 0 {
					b.WriteString(" n/a |")
					continue
				}
				fmt.Fprintf(&b, " %s / %s / %s |", pct(t.Shown, t.Total()), pct(t.Reachable, t.Total()), pct(t.Lost, t.Total()))
			}
			b.WriteString("\n")
		}
	}
	for _, class := range Classes {
		row(class, func(a *decayArm) map[Class]map[int]Tally { return a.tier0 })
	}
	for _, class := range ProtectedClasses {
		row(class, func(a *decayArm) map[Class]map[int]Tally { return a.protected })
	}
	return b.String()
}
