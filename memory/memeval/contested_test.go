package memeval

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/selection"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// The contested invariant holds on a summary with a contested pair and a value history, and
// breaks on a pair after another row, a pair against authority order and a second older value.
func TestCheckContested(t *testing.T) {
	var entries []ledger.Entry
	var records []ledger.Observation
	add := func(i int, content, authority string, f func(*ledger.Observation)) {
		id := "m" + string(rune('a'+i))
		entries = append(entries, testfix.RawMessage(id, content, map[string]any{"timestamp": "2026-05-01T10:00:00.000Z"}))
		o := ledger.Observation{ID: "a0000000000" + string(rune('a'+i)), Content: content, Kind: "fact", Subject: "api", Authority: authority,
			Timestamp: "2026-05-01 10:00", Relevance: "medium", SourceEntryIDs: []string{id}, TokenCount: 10, Origin: ledger.OriginCode}
		if f != nil {
			f(&o)
		}
		records = append(records, o)
	}
	add(0, "the api logs to stderr", ledger.AuthorityUser, nil)
	add(1, "the api does not use TLS", ledger.AuthorityTool, nil)
	add(2, "the api port is 8080", ledger.AuthorityUser, nil)
	add(3, "the api uses TLS", ledger.AuthorityUser, func(o *ledger.Observation) { o.Conflicts = []string{"a0000000000b"} })
	add(4, "the api port is 9090", ledger.AuthorityUser, func(o *ledger.Observation) { o.PossiblyUpdates = []string{"a0000000000c"} })
	entries = append(entries, testfix.ObservationsRecordedEntry("rec", records, entries[len(entries)-1].ID))
	folded := ledger.FoldLedger(entries, "")
	r := selection.Render(selection.Input{Observations: folded.ActiveObservations, Folded: folded, Branch: entries, Budget: 10_000})
	snap := Snapshot{N: 1, Branch: entries}
	rows := SummaryRows(r.Text)
	if c := checkContested(snap, folded, rows); !c.OK {
		t.Fatalf("broken on the engine's summary: %s\n%s", c.Detail, r.Text)
	}
	var notes []int
	for i, row := range rows {
		if row.Table == "notes.api" {
			notes = append(notes, i)
		}
	}
	swap := func(a, b int) []Row {
		out := append([]Row{}, rows...)
		out[notes[a]], out[notes[b]] = out[notes[b]], out[notes[a]]
		return out
	}
	if c := checkContested(snap, folded, swap(0, 1)); c.OK || !strings.Contains(c.Detail, "against authority") {
		t.Fatalf("a pair against authority order: %+v", c)
	}
	if c := checkContested(snap, folded, swap(1, 2)); c.OK || !strings.Contains(c.Detail, "after an uncontested") {
		t.Fatalf("a pair member after another row: %+v", c)
	}
	twice := append([]Row{}, rows...)
	for _, i := range notes {
		if !strings.Contains(twice[i].Cells["content"], "(conflicts ") {
			cells := map[string]string{}
			for k, v := range twice[i].Cells {
				cells[k] = v
			}
			cells["content"] += " (updated by a0000000000e)"
			twice[i].Cells = cells
		}
	}
	if c := checkContested(snap, folded, twice); c.OK || !strings.Contains(c.Detail, "older values") {
		t.Fatalf("two older values: %+v", c)
	}
}
