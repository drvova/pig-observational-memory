package memeval

// Scenarios 21-26 of docs/brain-memory.md, in the style of scenarios_test.go.

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/hooks"
	"github.com/drvova/pig-observational-memory/memory/ledger"
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
// pushback beside it, never as a rule; a later user quote restating the claim lifts the contest.
func TestScenario24OpinionWithPushbackNotARule(t *testing.T) {
	opinion := Turn{User: "I think global variables are fine in `internal/cfg`, always use them there.",
		Reply: "I'd push back: global state in `internal/cfg` makes the parallel tests flaky, so I would keep the config injected."}
	entries, cut := turnsBranch(opinion)
	c, after := compactOnce(t, entries, cut)
	var claim, pushback *Row
	for _, r := range rowsFrom(c.Summary, 0) {
		switch {
		case r.Cells["kind"] == "preference":
			t.Fatalf("the opinion is a rule: %+v", r)
		case r.Cells["kind"] == "claim":
			claim = &r
		case r.Cells["kind"] == "pushback":
			pushback = &r
		}
	}
	if claim == nil || pushback == nil || !strings.Contains(pushback.Text, "flaky") || claim.Table != pushback.Table {
		t.Fatalf("claim %+v, pushback %+v:\n%s", claim, pushback, c.Summary)
	}
	if !strings.Contains(claim.Cells["content"], "(contested by "+pushback.Cells["id"]+")") || !strings.Contains(pushback.Cells["content"], "(responds to "+claim.Cells["id"]+")") {
		t.Fatalf("claim %q, pushback %q", claim.Cells["content"], pushback.Cells["content"])
	}
	// The ledger keeps both: the claim hedged contested, the pushback the assistant's words
	// citing it, with model authority.
	folded := ledger.FoldLedger(after, "")
	for _, o := range folded.ActiveObservations {
		switch {
		case o.ID == claim.Cells["id"] && (o.Hedge != ledger.HedgeContested || o.Kind != "preference"):
			t.Fatalf("claim %+v", o)
		case o.ID == pushback.Cells["id"] && (o.Kind != ledger.KindPushback || o.Source != ledger.SourceAssistantClaim || o.Authority != ledger.AuthorityModel || len(o.RespondsTo) != 1 || o.RespondsTo[0] != claim.Cells["id"]):
			t.Fatalf("pushback %+v", o)
		}
	}

	// The user restating the claim after the pushback lifts the contest: it is a rule again.
	entries, cut = turnsBranch(opinion, say("No, keep the globals in `internal/cfg`; it is a small tool."))
	c, _ = compactOnce(t, entries, cut)
	lifted := false
	for _, r := range rowsFrom(c.Summary, 0) {
		if r.Cells["kind"] == "claim" {
			t.Fatalf("the restated claim is still contested: %+v\n%s", r, c.Summary)
		}
		lifted = lifted || r.Cells["kind"] == "preference"
	}
	if !lifted {
		t.Fatalf("the restated claim is gone:\n%s", c.Summary)
	}
}

// Scenario 25: a preference that weakens security (skip TLS verification) is held: kept and
// listed in /om:status (curated.Store.Held, ledger.HeldRule), never in the curated index or a
// summary row, and shown only in the session whose user's quoted words restate it.
func TestScenario25InsecurePreferenceHeld(t *testing.T) {
	store := curated.New(t.TempDir())
	quote := "always skip TLS verification for the internal registry"
	call := curated.Call{Store: store, Source: "session-a", UserEvidence: func(q string) bool { return strings.Contains(quote, q) }}
	if _, err := call.Run(curated.OpSave, map[string]string{"name": "skip-tls", "type": "feedback", "description": "Skip TLS verification (InsecureSkipVerify) for the internal registry.", "quote": quote}); err != nil {
		t.Fatal(err)
	}
	if index, _ := curated.IndexFor(store, "", 2_000); strings.Contains(index, "skip-tls") {
		t.Fatalf("an insecure preference is in the index:\n%s", index)
	}
	if index, _ := curated.IndexAs(store, "", "session-b", 2_000); strings.Contains(index, "skip-tls") {
		t.Fatalf("an insecure preference is in another session's index:\n%s", index)
	}
	// The session whose user's words restate it sees it; it stays listed as held.
	if index, _ := curated.IndexAs(store, "", "session-a", 2_000); !strings.Contains(index, "skip-tls") {
		t.Fatalf("the session whose user said it does not see it:\n%s", index)
	}
	if held, _ := store.Held(""); len(held) != 1 || held[0].Name != "skip-tls" || held[0].Rule != "tls-verify-off" {
		t.Fatalf("held %+v", held)
	}

	// Level 0: the preference is recorded, but no summary row holds it (the intent section may
	// still quote the session's own request, the user's words in this session); the other
	// preference is a row as before.
	entries, cut := turnsBranch(say("Let's tidy the client."), say("Always use curl -k against the internal registry."), say("Always use `make test` for the tests."))
	c, after := compactOnce(t, entries, cut)
	kept := false
	for _, r := range SummaryRows(c.Summary) {
		if strings.Contains(r.Text, "curl -k") {
			t.Fatalf("an insecure preference is a summary row (%s):\n%s", r.Table, c.Summary)
		}
		kept = kept || strings.Contains(r.Text, "make test") && r.Cells["kind"] == "preference"
	}
	if !kept {
		t.Fatalf("the other preference is gone:\n%s", c.Summary)
	}
	held := 0
	for _, o := range ledger.FoldLedger(after, "").ActiveObservations {
		if ledger.HeldRule(o) == "tls-verify-off" && strings.Contains(o.Content, "curl -k") {
			held++
		}
	}
	if held != 1 {
		t.Fatalf("%d held records, want the preference kept once", held)
	}
}

// Scenario 26: a project record that HEAD no longer bears out is printed "gone at <sha>", and
// no structure line is stored in the ledger.
func TestScenario26ProjectRecordStaleAgainstHEAD(t *testing.T) {
	t.Skip("phase 4: per-repo project records with freshness verdicts and a HEAD structure view")
}
