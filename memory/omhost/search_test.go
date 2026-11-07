package omhost_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/om"
	"github.com/drvova/pig-observational-memory/memory/om/omwire"
	"github.com/drvova/pig-observational-memory/memory/omhost"
	"github.com/drvova/pig-observational-memory/memory/omtool"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/snippettest"
	tf "github.com/drvova/pig-observational-memory/memory/testfix"
)

// searchBranch is a branch of user turns, each with one record, then the records' batch.
func searchBranch(records ...ledger.Observation) []session.Entry {
	var entries []session.Entry
	for i := range records {
		id := fmt.Sprintf("u%d", i+1)
		entries = append(entries, tf.RawMessage(id, records[i].Content, at("2026-05-02")))
		if len(records[i].SourceEntryIDs) == 0 {
			records[i].SourceEntryIDs = []string{id}
		}
		if records[i].Timestamp == "" {
			records[i].Timestamp = "2026-05-02 12:00"
		}
	}
	entries = append(entries, tf.ObservationsRecordedEntry("rec", records, entries[len(entries)-1].ID, at("2026-05-02")))
	return append(entries, tf.RawMessage("now", "go on", at("2026-05-03")))
}

func searchRecord(id, kind, content string, f ...func(*ledger.Observation)) ledger.Observation {
	return tf.Observation(id, func(o *ledger.Observation) {
		o.Kind, o.Content, o.Origin, o.SourceEntryIDs = ledger.ObservationKind(kind), content, ledger.OriginCode, nil
		for _, g := range f {
			g(o)
		}
	})
}

func searchCall(entries []session.Entry) *omhost.Call {
	return &omhost.Call{Entries: entries, Skip: []string{omtool.ToolName}}
}

// Scenario 35: a superseded record never crowds out its update; status filters apply before
// the bound; the current record carries its tag.
func TestSearchStatusBeforeTopK(t *testing.T) {
	var records []ledger.Observation
	var olds []string
	for i := 0; i < 6; i++ {
		id := fmt.Sprintf("00000000a%03d", i)
		olds = append(olds, id)
		records = append(records, searchRecord(id, "fact", fmt.Sprintf("The billing port is %d.", 7000+i), func(o *ledger.Observation) { o.Subject = "billing" }))
	}
	update := "00000000b000"
	records = append(records, searchRecord(update, "correction", "Actually the billing port is 7100, not 7005.", func(o *ledger.Observation) {
		o.Subject, o.Supersedes = "billing", slices.Clone(olds)
	}))
	c := searchCall(searchBranch(records...))
	l := must[om.NoteList](t, c, omwire.MNotes, omwire.Options{Search: []string{"billing port"}, Limit: limit(2)})
	if len(l.Rows) != 2 || l.Rows[0].ID != update || l.Rows[0].Tag != "believed" || l.Rows[1].Tag != "superseded" {
		t.Fatalf("rows %v", l.Rows)
	}
	if !strings.Contains(l.String(), "| tag") && !strings.Contains(l.String(), "tag") {
		t.Fatalf("the table shows no tag:\n%s", l)
	}
	if l.Matched != 7 || l.Next == "" {
		t.Fatalf("matched %d next %q", l.Matched, l.Next)
	}
	believed := must[om.NoteList](t, c, omwire.MNotes, omwire.Options{Search: []string{"billing port"}, Status: "believed"})
	if !slices.Equal(believed.IDs(), []string{update}) || believed.Matched != 1 {
		t.Fatalf("believed %v", believed.IDs())
	}
	old := must[om.NoteList](t, c, omwire.MNotes, omwire.Options{Search: []string{"billing"}, Status: "superseded"})
	if old.Matched != 6 || slices.Contains(old.IDs(), update) {
		t.Fatalf("superseded %v", old.IDs())
	}
	// The next page is a bigger one, and its snippet runs.
	next := snippettest.RunWith(t, c, l.Next)
	if next.Matched != 7 || len(next.IDs) != 7 || next.IDs[0] != update {
		t.Fatalf("next page %+v", next)
	}
	// Scenario 9: reading never changes the rank.
	again := must[om.NoteList](t, c, omwire.MNotes, omwire.Options{Search: []string{"billing port"}, Limit: limit(2)})
	if !slices.Equal(again.IDs(), l.IDs()) {
		t.Fatalf("a second read ranks %v, the first %v", again.IDs(), l.IDs())
	}
}

// Scenario 36: a version, a SHA and an error line are found byte for byte through the exact
// index, never through split numerals.
func TestSearchExactKeys(t *testing.T) {
	version := searchRecord("00000000c001", "fact", "Pinned SBCL to 2.6.10 in CI.")
	numbers := searchRecord("00000000c002", "fact", "Field 2 of 6 moved, and 10 others.")
	commit := searchRecord("00000000c003", "commit", "Committed 28f8fe2 with the baseline.")
	failing := searchRecord("00000000c004", "evidence", "--- FAIL: TestInsertUsers (0.02s)\n    users_test.go:42: pq: duplicate key value violates unique constraint \"users_pkey\"", func(o *ledger.Observation) {
		o.Keys.Add(ledger.KeyTests, "TestInsertUsers")
	})
	unrelated := searchRecord("00000000c005", "fact", "The users table has a unique constraint on email.")
	c := searchCall(searchBranch(version, numbers, commit, failing, unrelated))
	for _, tc := range []struct {
		search string
		want   []string
	}{
		{"2.6.10", []string{version.ID}},
		{"28f8fe2", []string{commit.ID}},
		{"pq: duplicate key value violates unique constraint \"users_pkey\"", []string{failing.ID}},
		{"TestInsertUsers", []string{failing.ID}},
		{"insert_users", []string{failing.ID}},
	} {
		l := must[om.NoteList](t, c, omwire.MNotes, omwire.Options{Search: []string{tc.search}})
		if !slices.Equal(l.IDs(), tc.want) {
			t.Errorf("%q: %v, want %v", tc.search, l.IDs(), tc.want)
		}
	}
	// A partial version never matches split numerals.
	if l := must[om.NoteList](t, c, omwire.MNotes, omwire.Options{Search: []string{"2.6"}}); slices.Contains(l.IDs(), numbers.ID) {
		t.Fatalf("2.6 found split numerals: %v", l.IDs())
	}
}

// Contiguous records of one source entry are one result, never extra rows.
func TestSearchMergesNeighbours(t *testing.T) {
	path := searchRecord("00000000d001", "value", "path internal/store/users.go", func(o *ledger.Observation) {
		o.Target, o.SourceEntryIDs = "path:internal/store/users.go", []string{"u1"}
	})
	attempt := searchRecord("00000000d002", "attempt", "go test ./internal/store failed.", func(o *ledger.Observation) {
		o.SourceEntryIDs = []string{"u1"}
		o.Keys.Add(ledger.KeyPaths, "internal/store")
	})
	other := searchRecord("00000000d003", "fact", "The store package owns the schema.")
	c := searchCall(searchBranch(path, attempt, other))
	l := must[om.NoteList](t, c, omwire.MNotes, omwire.Options{Search: []string{"internal/store"}})
	if !slices.Equal(l.IDs(), []string{attempt.ID}) || l.Matched != 1 || len(l.Rows[0].With) != 1 || l.Rows[0].With[0].ID != path.ID {
		t.Fatalf("rows %+v", l.Rows)
	}
	if !strings.Contains(l.Table(), path.ID+" [value] path internal/store/users.go") {
		t.Fatalf("the merged record is not in its row:\n%s", l.Table())
	}
	// A list read without a search keeps its columns.
	if plain := must[om.NoteList](t, c, omwire.MNotes, omwire.Options{}); strings.Contains(plain.Table(), "tag") {
		t.Fatalf("a plain read shows search columns:\n%s", plain.Table())
	}
}
