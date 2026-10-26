package omhost

import (
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// om is a leaf and spells the ledger's subject fields itself; they must stay the ledger's, and
// the notes' columns must name them where the rows put their values.
func TestOmFieldsAreTheLedgers(t *testing.T) {
	if om.FieldRawSubject != ledger.RawSubjectField || om.FieldSubjectScope != ledger.SubjectScopeField {
		t.Fatalf("om spells %q, %q; the ledger %q, %q", om.FieldRawSubject, om.FieldSubjectScope, ledger.RawSubjectField, ledger.SubjectScopeField)
	}
	for _, name := range append([]string{om.FieldRawSubject, om.FieldSubjectScope}, om.LevelZeroNoteFields...) {
		if !slices.Contains(om.NoteFields, name) {
			t.Errorf("NoteFields lacks %q", name)
		}
	}
	if om.RecallPageChars != RecallPageChars {
		t.Fatalf("om's recall page is %d characters, serialize's %d", om.RecallPageChars, RecallPageChars)
	}
	if om.HistoryClipChars <= 0 || om.DefaultLimit > om.MaxLimit {
		t.Fatalf("bounds %d %d %d", om.HistoryClipChars, om.DefaultLimit, om.MaxLimit)
	}
}

// The notes' rows carry every column NoteFields names, in order, with each record's day.
func TestNotesRowsMatchTheirFields(t *testing.T) {
	obs := tf.Observation("3f9a2c1d7b4e", func(o *ledger.Observation) {
		o.Subject, o.Timestamp, o.SourceEntryIDs = "baking", "2023-05-20 10:00", []string{"u1"}
	})
	entries := []session.Entry{
		tf.RawMessage("u1", "I baked sourdough", map[string]any{"timestamp": "2023-05-20T10:00:00Z"}),
		tf.ObservationsRecordedEntry("r1", []ledger.Observation{obs}, "u1"),
	}
	rs := Reader{Entries: entries}.Notes(nil)
	if !slices.Equal(rs.Fields, om.NoteFields) || len(rs.Rows) != 1 || len(rs.Rows[0]) != len(om.NoteFields) {
		t.Fatalf("rows %+v", rs)
	}
	if rs.Rows[0][0] != obs.ID || rs.From[0] != "2023-05-20" || rs.Also["subject"] != ledger.RawSubjectField {
		t.Fatalf("row %v from %v also %v", rs.Rows[0], rs.From, rs.Also)
	}
	source := slices.Index(om.NoteFields, om.FieldSource)
	if rs.Rows[0][source] != "u1" {
		t.Fatalf("source column %q", rs.Rows[0][source])
	}
}

// A tool call's history leaves out the current turn and lists newest first; the fallback lists
// only what is off the branch, labelled.
func TestHistoryAndFallback(t *testing.T) {
	branch := []session.Entry{tf.RawMessage("u1", "first"), tf.RawMessage("u2", "second"), tf.RawMessage("u3", "now")}
	r := Reader{Entries: branch, ToolCall: true}
	if ids := column(r.History(), 0); strings.Join(ids, " ") != "u2 u1" {
		t.Fatalf("tool-call history %v", ids)
	}
	if ids := column(Reader{Entries: branch}.History(), 0); strings.Join(ids, " ") != "u1 u2 u3" {
		t.Fatalf("plain history %v", ids)
	}
	file := append(append([]session.Entry{}, branch...), tf.RawMessage("x1", "elsewhere"))
	r.SessionEntries = file
	off := r.Fallback()
	if off.SessionEntries != nil || off.Off == nil {
		t.Fatalf("fallback %+v", off)
	}
	if ids := column(off.History(), 0); strings.Join(ids, "|") != "x1"+OffPathLabel {
		t.Fatalf("fallback history %v", ids)
	}
	var none *OffPath
	if id, ok := none.Row("u1"); !ok || id != "u1" {
		t.Fatalf("on-branch row %q %v", id, ok)
	}
}

func column(rs Rows, c int) []string {
	out := []string{}
	for _, row := range rs.Rows {
		out = append(out, row[c])
	}
	return out
}

// A page ends on a line boundary inside the page, a short text is whole, and an offset past the
// end is refused.
func TestPageOf(t *testing.T) {
	if p, ok := PageOf("short", 0); !ok || !p.Whole || p.Text != "short" {
		t.Fatalf("short %+v", p)
	}
	line := strings.Repeat("x", 99) + "\n"
	text := strings.Repeat(line, RecallPageChars/100+5)
	p, ok := PageOf(text, 0)
	if !ok || p.Whole || p.To >= p.Total || !strings.HasSuffix(p.Text, "\n") || len([]rune(p.Text)) > RecallPageChars {
		t.Fatalf("first page to %d of %d", p.To, p.Total)
	}
	next, ok := PageOf(text, p.To)
	if !ok || next.From != p.To || next.To != next.Total {
		t.Fatalf("next page %+v", next)
	}
	if _, ok := PageOf(text, len([]rune(text))); ok {
		t.Fatal("an offset at the end was accepted")
	}
}

// ClipAround keeps a window around the first searched word and marks the cut ends.
func TestClipAround(t *testing.T) {
	s := strings.Repeat("a", 1000) + "needle" + strings.Repeat("b", 1000)
	got := ClipAround(s, []string{"needle"}, om.HistoryClipChars)
	if !strings.HasPrefix(got, "…") || !strings.HasSuffix(got, "…") || !strings.Contains(got, "needle") {
		t.Fatalf("clip %q", got)
	}
	if ClipAround("short", nil, om.HistoryClipChars) != "short" {
		t.Fatal("a short text was clipped")
	}
}
