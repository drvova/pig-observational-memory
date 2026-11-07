package memeval

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/extract"
	"github.com/drvova/pig-observational-memory/memory/ledger"
)

// Tier 0b part (e): forget records and tombstones (contracts/truth.md, "Forget records";
// decision 1). Level 1 merges two plans; the user then asks the memory to forget one. The forget
// record's tombstone should take that plan and the merge holding its text out of the fold for good,
// bring the other plan back as itself, carry no value, and leave nothing visible when the same
// source is extracted again.

// forgetOutcome tallies part (e) over the seeds.
type forgetOutcome struct {
	merged, forgets, gone, others, valueFree, reextracted, shown int
}

// forgetRows runs part (e) and adds its rows.
func forgetRows(ctx context.Context, s *Suite, cfg SuiteConfig) error {
	var o forgetOutcome
	for _, seed := range cfg.Seeds {
		out, run, err := ForgottenMerge(ctx, seed)
		if err != nil {
			return fmt.Errorf("seed %d: %w", seed, err)
		}
		if !out.Merged {
			continue
		}
		o.merged++
		last := run.Snapshots[len(run.Snapshots)-1]
		folded := ledger.FoldLedger(last.Branch, "")
		var forget *ledger.Observation
		holds := false
		for i, r := range folded.Observations {
			if r.Kind == ledger.KindForget {
				forget = &folded.Observations[i]
			} else if strings.Contains(r.Content, "rate limiting") {
				holds = true
			}
		}
		if forget == nil {
			continue
		}
		o.forgets++
		// The merge left the fold, so only its entry in Truth.Out names it.
		if !holds && len(folded.Truth.Out) > 0 && !out.MergeStands {
			o.gone++
		}
		if out.OtherStands {
			o.others++
		}
		if line, err := json.Marshal(forget); err == nil && !strings.Contains(string(line), "rate limiting") && !strings.Contains(string(line), "api/limit.go") {
			o.valueFree++
		}
		// The same source read again: level 0 over the whole branch writes nothing the tombstone
		// names, so nothing holding the forgotten plan's text becomes visible.
		again := extract.Extract(extract.Input{Entries: last.Branch, Start: 0, End: len(last.Branch), Ledger: folded})
		visible := false
		for _, r := range again.Observations() {
			visible = visible || strings.Contains(r.Content, "rate limiting")
		}
		if !visible {
			o.reextracted++
		}
		for _, r := range SummaryRows(last.Summary) {
			if r.Table != "omitted" && !strings.HasPrefix(r.Table, "intent") && strings.Contains(r.Text, "rate limiting") {
				o.shown++
				break
			}
		}
	}
	const part = "e. forget records"
	n := len(cfg.Seeds)
	s.add(part, "a user's forget request writes a forget record (scripted merge first)", verdict(o.merged == n && o.forgets == o.merged), "", "%d of %d merges, %d forget records", o.merged, n, o.forgets)
	s.add(part, "the forgotten plan and the merge holding it leave the fold", verdict(o.gone == o.forgets), "", "%d of %d", o.gone, o.forgets)
	s.add(part, "the merge's other source reappears as itself", verdict(o.others == o.forgets), "", "%d of %d", o.others, o.forgets)
	s.add(part, "a tombstone carries no forgotten value", verdict(o.valueFree == o.forgets), "", "%d of %d", o.valueFree, o.forgets)
	s.add(part, "a re-extraction of the forgotten source makes no visible record", verdict(o.reextracted == o.forgets), "", "%d of %d", o.reextracted, o.forgets)
	s.add(part, "no summary row holds the forgotten plan", verdict(o.shown == 0), "", "%d summaries hold it", o.shown)
	return nil
}
