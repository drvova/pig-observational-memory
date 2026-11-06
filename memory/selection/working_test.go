package selection_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/selection"
	"github.com/drvova/pig-observational-memory/memory/tokens"
)

// The constraints registry shows the user's rules in force after the notes, newest first up to
// each slot's cap; what a cap leaves out falls back to the notes tables, and what the summary
// shows nowhere is named on the also: line, whose snippet lists it.
func TestRenderConstraintsRegistryCaps(t *testing.T) {
	entries := branch(10, func(i int, o *ledger.Observation) bool {
		o.Kind = "preference"
		if i%2 == 0 {
			o.Content = fmt.Sprintf("Don't touch `dir%d/` while you work on this.", i)
		} else {
			o.Content = fmt.Sprintf("Always run `make check%d` before you push.", i)
		}
		return true
	})
	in := input(entries, 60_000)
	in.Slots = selection.Slots{Constraints: 2, Scopes: 3}
	r := selection.Render(in)
	registry := 0
	for _, line := range strings.Split(r.Text, "\n") {
		if strings.HasPrefix(line, "  ") && strings.Contains(line, ",m0") && !strings.Contains(line, "preference") {
			registry++
		}
	}
	if !strings.Contains(r.Text, "constraints[5]{content,source}:") || registry != 5 || !strings.Contains(r.Text, selection.ConstraintsLine) {
		t.Fatalf("registry rows %d:\n%s", registry, r.Text)
	}
	// The newest of each kind are in the registry; the rest are notes rows, so nothing is left
	// out and no also: line is printed.
	if !strings.Contains(r.Text, "Always run `make check9` before you push.,m09") || strings.Contains(r.Text, "also: ") {
		t.Fatalf("summary:\n%s", r.Text)
	}
	// Under a budget too small for every rule, the also: line names the rest, and its snippet
	// lists them.
	tight := input(entries, 0)
	tight.Budget = tokens.EstimateStringTokens(selection.TablesInstructions) + 1 + noticeRoom() + 120
	tight.Slots = in.Slots
	r = selection.Render(tight)
	if !strings.Contains(r.Text, "\nalso: +") || !strings.Contains(r.Text, selection.AlsoLine) {
		t.Fatalf("no also: line:\n%s", r.Text)
	}
	everyOmittedIsListed(t, tight, r, runQueries(t, entries, tight, r))
}

// A plan a receipt closed shows done with its receipt, in next, and is never pending; an open
// plan a newer request bears on is marked may be stale.
func TestRenderDoneAndStalePlans(t *testing.T) {
	plan := func(n int, content string) ledger.Observation {
		return ledger.Observation{ID: recordID(n), Content: content, Timestamp: day(n) + " 10:00", Relevance: "medium", SourceEntryIDs: []string{fmt.Sprintf("m%02d", n)}, TokenCount: 10, Kind: "fact", Origin: ledger.OriginCode, Status: ledger.StatusProposed, DerivedFrom: fmt.Sprintf("m%02d", n)}
	}
	entries := branch(4, func(i int, o *ledger.Observation) bool {
		switch i {
		case 0:
			*o = plan(0, "Next we will run the 3 migrations with `make migrate-up`.")
		case 1:
			o.Kind, o.Content, o.Tool, o.Target, o.Outcome = ledger.KindAttempt, `bash "make migrate-up" worked`, "bash", "make migrate-up", ledger.OutcomeWorked
			o.Closes = []ledger.Closure{{ID: recordID(0), ClosedBy: recordID(1), Receipt: ledger.ReceiptExit0}}
		case 2:
			*o = plan(2, "Next we will add rate limiting to `api/limit.go` with a burst of 20.")
		default:
			return false
		}
		return true
	})
	entries = append(entries[:3], append([]ledger.Entry{{Type: "message", ID: "m03", Message: map[string]any{"role": "user", "content": "The rate limit must be per tenant now."}}}, entries[3:]...)...)
	r := selection.Render(input(entries, 60_000))
	for _, want := range []string{
		"next[1]{", recordID(0) + ",2026-05-01,fact,done,,\"Next we will run the 3 migrations with `make migrate-up`. (closed by " + recordID(1) + ": exit 0)\",,m00",
		"Next we will add rate limiting to `api/limit.go` with a burst of 20. (may be stale m03)", selection.ClosedLine, selection.StaleLine,
	} {
		if !strings.Contains(r.Text, want) {
			t.Fatalf("missing %q:\n%s", want, r.Text)
		}
	}
}
