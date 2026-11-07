package memeval

import (
	"context"
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/consolidate"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// Tier 0b part (d), scenario 17 and decision 1: a level-1 merge of two plans whose source is
// then retracted should go out, its other source reappearing as itself. Level 1 is a scripted
// reply the fake host serves.

// RetractOutcome is one seed's merge-then-retract run.
type RetractOutcome struct {
	// Merged is whether level 1's merge was accepted at the first compaction.
	Merged bool
	// MergeStands is whether the merge still stands after the retraction's compaction;
	// OtherStands whether the other source stands as itself; RetractedShown whether a summary
	// row still shows the retracted plan as open.
	MergeStands, OtherStands, RetractedShown bool
	// Retraction is the retraction record's targets; MergeID the merge's id.
	Retraction []string
	MergeID    string
}

// The two plans and the retraction.
const (
	planA      = "Next we will add rate limiting to `api/limit.go` with a burst of 20."
	planB      = "Next we will add request tracing to `api/trace.go` with a sample rate of 5."
	planMerged = "Next we will add rate limiting to `api/limit.go` with a burst of 20 and request tracing to `api/trace.go` with a sample rate of 5."
	retraction = "Scratch the rate limiting plan."
)

// RetractedMerge runs seed's session: the two plans, filler, level 1 merging them at the first
// compaction, the retraction a few turns after it, and a second compaction.
func RetractedMerge(ctx context.Context, seed int64) (RetractOutcome, error) {
	o, _, err := withdrawnMerge(ctx, seed, retraction)
	return o, err
}

// ForgottenMerge is RetractedMerge with the user's forget request (forgetPlan) in the
// retraction's place, and the last run.
func ForgottenMerge(ctx context.Context, seed int64) (RetractOutcome, Run, error) {
	return withdrawnMerge(ctx, seed, forgetPlan)
}

// forgetPlan asks the memory to forget plan A (contracts/truth.md, "Forget records").
const forgetPlan = "Please forget the rate limiting plan from memory."

// withdrawnMerge runs RetractedMerge's session with withdraw said after the merge.
func withdrawnMerge(ctx context.Context, seed int64, withdraw string) (RetractOutcome, Run, error) {
	var out RetractOutcome
	ids := map[string]string{}
	reply := func(n int, branch []session.Entry) map[string]any {
		if n != 1 {
			return nil
		}
		for _, o := range ledger.FoldLedger(branch, "").ActiveObservations {
			if len(o.SourceEntryIDs) == 0 {
				continue
			}
			switch o.SourceEntryIDs[0] {
			case UserID(0):
				ids["a"] = o.ID
			case UserID(1):
				ids["b"] = o.ID
			}
		}
		if ids["a"] == "" || ids["b"] == "" {
			return nil
		}
		return testfix.AssistantToolCall("call-1", consolidate.ToolName, map[string]any{
			"merges": []any{map[string]any{"ids": []any{ids["a"], ids["b"]}, "content": planMerged, "subject": "api"}},
		})
	}
	script := func(at int) Script {
		turns := append([]Turn{say(planA), say(planB)}, Filler(seed, 300)...)
		if at > 0 {
			turns[at] = say(withdraw)
		}
		return Script{Seed: seed, Turns: turns}
	}
	first, err := SimulateWith(ctx, script(0), level1Window, 1, Sim{Level1: reply})
	if err != nil || len(first.Snapshots) != 1 {
		return out, Run{}, fmt.Errorf("first compaction: %v", err)
	}
	at := first.Snapshots[0].Turn + 3
	run, err := SimulateWith(ctx, script(at), level1Window, 2, Sim{Level1: reply})
	if err != nil || len(run.Snapshots) != 2 {
		return out, Run{}, fmt.Errorf("second compaction: %v (%d)", err, len(run.Snapshots))
	}
	c := run.Snapshots[0].Result.Details.Consolidation
	out.Merged = c != nil && c.Applied && c.Merges == 1
	last := run.Snapshots[1]
	folded := ledger.FoldLedger(last.Branch, "")
	standing := map[string]bool{}
	for _, o := range folded.ActiveObservations {
		if ledger.Standing(o.ID, folded.ReplacedBy, nil) == o.ID && !isSuperseded(folded, o.ID) {
			standing[o.ID] = true
		}
	}
	for _, o := range folded.Observations {
		if strings.Contains(o.Content, "rate limiting") && strings.Contains(o.Content, "request tracing") && o.Origin != ledger.OriginCode {
			out.MergeStands = out.MergeStands || standing[o.ID]
			out.MergeID = o.ID
		}
		if o.ID == ids["b"] {
			out.OtherStands = standing[o.ID]
		}
		if o.Status == ledger.StatusRetracted && len(o.SourceEntryIDs) > 0 && o.SourceEntryIDs[0] == UserID(at) {
			out.Retraction = append(out.Retraction, o.Supersedes...)
		}
		for _, t := range o.Forgets {
			out.Retraction = append(out.Retraction, t.ID)
		}
	}
	for _, r := range SummaryRows(last.Summary) {
		if r.Table != "omitted" && strings.Contains(r.Text, "rate limiting") && r.Cells["status"] == ledger.StatusProposed {
			out.RetractedShown = true
		}
	}
	return out, run, nil
}
