package memeval

// Scenarios 21-26 of docs/brain-memory.md, in the style of scenarios_test.go.

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/hooks"
)

// planBranch is a plan, then turns, compacted once with filler after them.
func planBranch(t *testing.T, turns ...Turn) hooks.CompactionResult {
	t.Helper()
	entries, cut := turnsBranch(append([]Turn{say("Next we will add rate limiting to `api/limit.go` with a burst of 20.")}, turns...)...)
	c, _ := compactOnce(t, entries, cut)
	return c
}

// planRows are the summary's rows of the plan (turn 0) that still read as open: proposed.
func planRows(summary, text string) []Row {
	var out []Row
	for _, r := range rowsFrom(summary, 0) {
		if r.Cells["status"] == "proposed" && strings.Contains(r.Text, text) {
			out = append(out, r)
		}
	}
	return out
}

// Scenario 21: assistant prose saying a plan is done, with no tool call doing it, leaves the
// plan open; only a tool receipt closes it.
func TestScenario21ProseDoesNotClosePlan(t *testing.T) {
	later(t, "phase 3: slot records and closedBy receipts")
	c := planBranch(t, Turn{User: "How is the limiter going?", Reply: "Done: rate limiting in `api/limit.go` is finished and working."})
	if len(planRows(c.Summary, "api/limit.go")) == 0 {
		t.Fatalf("the plan closed on prose alone:\n%s", c.Summary)
	}
}

// Scenario 22: a fresh requirement that bears on a standing plan renders the plan "may be stale".
func TestScenario22FreshRequirementMarksPlanStale(t *testing.T) {
	later(t, "phase 3: derivedFrom staleness of plans")
	c := planBranch(t, say("New requirement: the rate limit must be per tenant, keyed by `X-Tenant-ID`."))
	rows := planRows(c.Summary, "api/limit.go")
	if len(rows) == 0 || !strings.Contains(rows[0].Text, "may be stale") {
		t.Fatalf("plan rows %+v\n%s", rows, c.Summary)
	}
}

// Scenario 23: on a 1M-token window compaction comes late; the quality trigger fires first and
// nothing below the raw floor is consolidated.
func TestScenario23QualityTriggerOnLargeWindow(t *testing.T) {
	t.Skip("phase 3: the raw floor and the quality trigger")
	// Input: a 1,000,000-token window filled past the quality threshold, well below PiG's.
	// Expected: the trigger compacts; the newest raw floor of entries is kept verbatim.
}

// Scenario 24: a user's opinion the assistant pushed back on is kept as the claim with the
// pushback beside it, never as a rule.
func TestScenario24OpinionWithPushbackNotARule(t *testing.T) {
	later(t, "phase 2: assistant pushback kept beside user claims; a hedge for opinions")
	entries, cut := turnsBranch(Turn{User: "I think global variables are fine in `internal/cfg`, always use them there.",
		Reply: "Global state in `internal/cfg` makes the parallel tests flaky; I would keep the config injected."})
	c, _ := compactOnce(t, entries, cut)
	for _, r := range rowsFrom(c.Summary, 0) {
		if r.Cells["kind"] == "preference" {
			t.Fatalf("the opinion is a rule: %+v", r)
		}
	}
	if !strings.Contains(c.Summary, "flaky") {
		t.Fatalf("the pushback is gone:\n%s", c.Summary)
	}
}

// Scenario 25: a preference that weakens security (skip TLS verification) is held, listed in
// /om:status, and never in the curated index.
func TestScenario25InsecurePreferenceHeld(t *testing.T) {
	later(t, "phase 1: holding security-weakening preferences")
	store := curated.New(t.TempDir())
	quote := "always skip TLS verification for the internal registry"
	call := curated.Call{Store: store, Source: "session-a", UserEvidence: func(q string) bool { return strings.Contains(quote, q) }}
	if _, err := call.Run(curated.OpSave, map[string]string{"name": "skip-tls", "type": "feedback", "description": "Skip TLS verification (InsecureSkipVerify) for the internal registry.", "quote": quote}); err != nil {
		t.Fatal(err)
	}
	if index, _ := curated.IndexFor(store, "", 2_000); strings.Contains(index, "skip-tls") {
		t.Fatalf("an insecure preference is in the index:\n%s", index)
	}
}

// Scenario 26: a project record that HEAD no longer bears out is printed "gone at <sha>", and
// no structure line is stored in the ledger.
func TestScenario26ProjectRecordStaleAgainstHEAD(t *testing.T) {
	t.Skip("phase 4: per-repo project records with freshness verdicts and a HEAD structure view")
}
