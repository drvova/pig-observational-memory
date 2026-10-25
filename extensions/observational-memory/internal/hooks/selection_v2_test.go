package hooks

import (
	"fmt"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/memquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/selection"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// A cut inside a turn: the summary opens with the turn's request, ending with its entry id.
func TestV2SplitTurnRequestLeadsTheSummary(t *testing.T) {
	counter := testfix.CountRequests(t)
	turn := codingTurn("x", "Fix the loader for config version 3.")
	rt, h := v2Setup(t, turn)
	event := v2Event(turn, "xa3", 20_000, "threshold")
	event.Preparation.IsSplitTurn = true
	for _, entry := range turn[:5] {
		event.Preparation.TurnPrefixMessages = append(event.Preparation.TurnPrefixMessages, entry.Message)
	}

	c := expectV2Compaction(t, runV2Hook(rt, h, event), "xa3", h)

	body, ok := ledger.TablesSummaryBody(c.Summary)
	if !ok || !strings.HasPrefix(body, "request[1]{content,source}:\n  Fix the loader for config version 3.,xu\n") {
		t.Fatalf("summary:\n%s", c.Summary)
	}
	if counter.Total() != 0 {
		t.Fatalf("requests %v", counter.ByStage())
	}
}

// printedQueries are the memory queries a v2 summary prints: its omitted table's and the
// capped lists' tails.
func printedQueries(t *testing.T, summary string) []string {
	t.Helper()
	out := []string{}
	body, _ := ledger.TablesSummaryBody(summary)
	decoded, err := tables.Rows.Decode(body)
	if err != nil {
		t.Fatalf("summary body does not decode: %v\n%s", err, body)
	}
	for _, table := range decoded {
		if table.Name != "omitted" {
			continue
		}
		for r := range table.Rows {
			q, _ := table.Cell(r, "query")
			out = append(out, q)
		}
	}
	_, lists, _ := strings.Cut(summary, "\n\n---\n\n")
	for _, line := range strings.Split(lists, "\n") {
		if _, tail, ok := strings.Cut(line, " more: "); ok {
			out = append(out, tail)
		}
	}
	return out
}

// Under a budget too small for every record, the hook's summary lists the rest as omitted
// spans and capped lists whose queries run on the branch after the compaction, and still run
// and list the same records on a fork made after it.
func TestV2SummaryQueriesRunAfterTheCompactionAndAFork(t *testing.T) {
	counter := testfix.CountRequests(t)
	entries := []ledger.Entry{}
	for i := range 6 {
		entries = append(entries, codingTurn(fmt.Sprintf("t%d", i), fmt.Sprintf("Fix loader bug number %d in package p%d.", i, i))...)
	}
	entries = append(entries, userMessage("last", "Thanks, now the docs."))
	rt, h := v2Setup(t, entries)
	OnTurnEnd(rt, h)
	// The budget: room for the instructions and a few rows (the threshold bound, which the
	// hook derives from tokensBefore against PiG's window and reserve).
	cut := v2SourceEntries(v2CutRange(h.Entries, v2Event(h.Entries, "last", 0, "threshold")))
	removed := v2SourceTokens(cut)
	room := tokens.EstimateStringTokens(selection.TablesInstructions) + 120
	event := v2Event(h.Entries, "last", 200_000-16_384+removed-room, "threshold")

	c := expectV2Compaction(t, runV2Hook(rt, h, event), "last", h)

	queries := printedQueries(t, c.Summary)
	if len(queries) == 0 {
		t.Fatalf("no omitted records under a tight budget:\n%s", c.Summary)
	}
	after := append(append([]ledger.Entry{}, h.Entries...), testfix.CompactionEntry("cmp", testfix.CompactionArgs{FirstKeptEntryID: "last", Summary: c.Summary, Details: testfix.ToJSONValue(c.Details)}))
	fork := append(append([]ledger.Entry{}, after...), userMessage("forked", "a different direction"))
	shown := map[string]bool{}
	for _, o := range c.Details.Observations {
		if strings.Contains(c.Summary, o.ID) {
			shown[o.ID] = true
		}
	}
	listed := ""
	for _, q := range queries {
		res := memquery.Run(memquery.Env{Entries: after}, q)
		again := memquery.Run(memquery.Env{Entries: fork}, q)
		if res.IsError || again.IsError || res.Text != again.Text {
			t.Fatalf("query %q: %s / after the fork: %s", q, res.Text, again.Text)
		}
		listed += res.Text
	}
	for _, o := range c.Details.Observations {
		if !shown[o.ID] && !strings.Contains(listed, o.ID) {
			t.Errorf("omitted record %s (%q) is listed by no query:\n%s", o.ID, o.Content, c.Summary)
		}
	}
	if counter.Total() != 0 {
		t.Fatalf("requests %v", counter.ByStage())
	}
}
