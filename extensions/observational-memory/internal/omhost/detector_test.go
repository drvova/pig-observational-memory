package omhost

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// A detector run reads its pass alone (the removed entries and their records, the folded ledger
// as of the compaction for NoteByID) and answers nothing but the detector's four reads.
func TestDetectorCallReadsThePassOnly(t *testing.T) {
	plan := tf.Observation("aaaaaaaaaaa1", func(o *ledger.Observation) {
		o.Kind, o.Subject, o.SourceEntryIDs, o.Timestamp = "plan", "deploy", []string{"u1"}, "2026-05-02 10:00"
	})
	older := tf.Observation("bbbbbbbbbbb1", func(o *ledger.Observation) {
		o.Kind, o.SourceEntryIDs, o.Timestamp = "fact", []string{"u0"}, "2026-05-01 10:00"
	})
	covered := []ledger.Entry{tf.RawMessage("u1", "plan the deploy"), tf.RawMessage("a1", "will do")}
	all := append([]ledger.Entry{tf.RawMessage("u0", "old"), tf.ObservationsRecordedEntry("r0", []ledger.Observation{older}, "u0")}, covered...)
	all = append(all, tf.ObservationsRecordedEntry("r1", []ledger.Observation{plan}, "a1"))
	folded := ledger.FoldLedger(all, "")
	c := DetectorCall(&DetectorPass{Covered: covered, Notes: []ledger.Observation{plan}, Known: folded.ActiveObservations, Aliases: folded.Aliases, Folded: folded})
	ask := func(method string, args ...any) (json.RawMessage, error) {
		raw, _ := json.Marshal(args)
		return c.Call(context.Background(), method, raw)
	}
	raw, err := ask(omwire.MNotes, omwire.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var notes om.NoteList
	if err := json.Unmarshal(raw, &notes); err != nil || notes.Matched != 1 || notes.Rows[0].ID != plan.ID {
		t.Fatalf("notes %+v %v", notes, err)
	}
	raw, err = ask(omwire.MHistory, omwire.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var history om.EntryList
	if err := json.Unmarshal(raw, &history); err != nil || history.Matched != 2 {
		t.Fatalf("history %+v %v", history, err)
	}
	// NoteByID reads the folded ledger as of the compaction, outside the pass's own records.
	raw, err = ask(omwire.MNoteByID, older.ID)
	if err != nil {
		t.Fatal(err)
	}
	var n om.Note
	if err := json.Unmarshal(raw, &n); err != nil || n.ID != older.ID {
		t.Fatalf("note %+v %v", n, err)
	}
	if raw, err = ask(omwire.MSubjects); err != nil || string(raw) != `{"deploy":1}` {
		t.Fatalf("subjects %s %v", raw, err)
	}
	for _, m := range []string{omwire.MSave, omwire.MRecall, omwire.MSaved, omwire.MLayerForget, omwire.MInfo, omwire.MKept, omwire.MCitedBy} {
		if _, err := ask(m, "x"); !errors.Is(err, omwire.ErrBadArgs) {
			t.Fatalf("%s answered for a detector: %v", m, err)
		}
	}
}
