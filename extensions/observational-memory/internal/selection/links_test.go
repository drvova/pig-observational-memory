package selection_test

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/selection"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// linkedBranch has a plan, a condition, a retraction withdrawing a second plan, a correction
// replacing a fact, a possible update and a negation conflict, among plain records.
func linkedBranch(n int) []ledger.Entry {
	return branch(n, func(i int, o *ledger.Observation) bool {
		o.Content = plain(i)
		switch i {
		case 0:
			o.Content, o.Status = "we will migrate the orders table to Postgres", ledger.StatusProposed
		case 1:
			o.Content, o.Status = "if we shard we need four shards", ledger.StatusHypothetical
		case 2:
			o.Content, o.Status = "we will ship build 42 on Friday", ledger.StatusProposed
		case 3:
			o.Kind, o.Content, o.Status, o.Supersedes = "correction", "scrap that", ledger.StatusRetracted, []string{recordID(2)}
		case 4:
			o.Content = "the pool size is 8"
		case 5:
			o.Kind, o.Content, o.Supersedes = "correction", "actually the pool size is 10", []string{recordID(4)}
		case 6:
			o.Content = "the cache runs on Redis"
		case 7:
			o.Content, o.PossiblyUpdates = "the cache holds 64 keys", []string{recordID(8)}
		case 8:
			o.Content = "plain line about the cache size"
		case 9:
			o.Content, o.Conflicts = "the cache does not run on Redis", []string{recordID(6)}
		}
		return true
	})
}

// noteRows decodes the summary's notes table into rows by record id, as field-to-value maps.
func noteRows(t *testing.T, summary string) map[string]map[string]string {
	t.Helper()
	body, ok := ledger.TablesSummaryBody(summary)
	if !ok {
		t.Fatalf("no body:\n%s", summary)
	}
	decoded, err := tables.Rows.Decode(body)
	if err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	out := map[string]map[string]string{}
	for _, table := range decoded {
		if !strings.HasPrefix(table.Name, "notes") {
			continue
		}
		for r := range table.Rows {
			row := map[string]string{}
			for _, field := range selection.NoteFields {
				row[field], _ = table.Cell(r, field)
			}
			out[row["id"]] = row
		}
	}
	return out
}

// Every shown status is in its own field, never in the text; a record a retraction withdrew is
// shown retracted; a survivor ends with the recall pointer to what it replaced, a possible
// update with the record it may update, a conflict with the record it conflicts with; the text
// itself is the record's, unchanged.
func TestRenderStatusesAndPointers(t *testing.T) {
	entries := linkedBranch(10)
	in := input(entries, 10_000)
	r := selection.Render(in)
	rows := noteRows(t, r.Text)
	check := func(n int, status, content string) {
		t.Helper()
		row, shown := rows[recordID(n)]
		if !shown {
			t.Fatalf("record %d not shown:\n%s", n, r.Text)
		}
		if row["status"] != status || row["content"] != content {
			t.Errorf("record %d: status %q content %q, want %q %q", n, row["status"], row["content"], status, content)
		}
	}
	check(0, ledger.StatusProposed, "we will migrate the orders table to Postgres")
	check(1, ledger.StatusHypothetical, "if we shard we need four shards")
	check(3, ledger.StatusRetracted, "scrap that (was "+recordID(2)+")")
	check(5, "", "actually the pool size is 10 (was "+recordID(4)+")")
	check(7, "", "the cache holds 64 keys (may update "+recordID(8)+")")
	check(8, "", "plain line about the cache size")
	check(9, "", "the cache does not run on Redis (conflicts "+recordID(6)+")")
	check(6, "", "the cache runs on Redis")
	if _, shown := rows[recordID(2)]; shown {
		t.Fatal("the retracted plan is shown beside its retraction")
	}

	// The lines format adds the same status and pointers around the same text.
	in.Format = ledger.FormatLines
	lines := selection.Render(in).Text
	for _, want := range []string{
		"[" + recordID(0) + "] " + day(0) + " fact proposed we will migrate the orders table to Postgres (m00)",
		"[" + recordID(3) + "] " + day(3) + " correction retracted scrap that (was " + recordID(2) + ") (m03)",
		"the cache holds 64 keys (may update " + recordID(8) + ") (m07)",
		"the cache does not run on Redis (conflicts " + recordID(6) + ") (m09)",
	} {
		if !strings.Contains(lines, want) {
			t.Fatalf("lines lack %q:\n%s", want, lines)
		}
	}
}

// Whatever the budget, a plan a retraction withdrew is shown only as retracted and never beside
// the retraction, a possible update is shown with its target whenever both fit beside what the
// classes took first, and every query the summary prints still runs and lists what it left out.
func TestRenderLinkedRecordsUnderEveryBudget(t *testing.T) {
	entries := linkedBranch(30)
	for _, format := range []string{ledger.FormatTables, ledger.FormatLines} {
		intro := selection.TablesInstructions
		if format == ledger.FormatLines {
			intro = selection.LinesInstructions
		}
		fixed := tokens.EstimateStringTokens(intro)
		sawPair, sawOmitted := false, false
		for budget := fixed; budget <= fixed+700; budget += 3 {
			in := input(entries, budget)
			in.Format = format
			r := selection.Render(in)
			if r.Text == "" {
				continue
			}
			if got := tokens.EstimateStringTokens(r.Text); got > budget {
				t.Fatalf("%s budget %d, summary %d tokens", format, budget, got)
			}
			shown := shownIDs(r)
			if shown[recordID(2)] && shown[recordID(3)] {
				t.Fatalf("%s budget %d shows the retracted plan beside its retraction", format, budget)
			}
			if shown[recordID(2)] && format == ledger.FormatTables {
				if row := noteRows(t, r.Text)[recordID(2)]; row["status"] != ledger.StatusRetracted {
					t.Fatalf("budget %d shows the withdrawn plan as %q", budget, row["status"])
				}
			}
			sawPair = sawPair || shown[recordID(7)] && shown[recordID(8)]
			sawOmitted = sawOmitted || r.Omitted > 0
			everyOmittedIsListed(t, in, r, runQueries(t, entries, in, r))
		}
		if !sawPair || !sawOmitted {
			t.Fatalf("%s: the sweep never showed the pair (%v) or omitted (%v)", format, sawPair, sawOmitted)
		}
	}
}
