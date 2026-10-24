package memquery

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
)

// Select returns tables with the record ids of their rows, reads records given directly, is
// not cut by the default limit, honours an explicit one, and refuses writes, recall and
// sources the caller does not allow.
func TestSelectReadsGivenRecords(t *testing.T) {
	observations := []ledger.Observation{}
	for i := 0; i < DefaultLimit+5; i++ {
		subject := "baking"
		if i%2 == 1 {
			subject = "travel"
		}
		observations = append(observations, ledger.Observation{ID: fmt.Sprintf("%012x", i+1), Timestamp: "2026-05-02 10:30", Relevance: "low", Subject: subject, Content: fmt.Sprintf("note %d", i)})
	}
	env := Env{Records: &Records{Observations: observations}}
	sel, err := Select(env, "notes | group by subject", SourceNotes)
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.Tables) != 2 || sel.Tables[0].Name != "notes.baking" || sel.Count != len(observations) || len(sel.IDs[0]) != len(sel.Tables[0].Rows) || sel.IDs[0][0] != observations[0].ID {
		t.Fatalf("selection %+v", sel)
	}
	if sel, _ := Select(env, "notes | limit 3"); sel.Count != 3 {
		t.Fatalf("limit: %d", sel.Count)
	}
	if sel, _ := Select(env, "notes | where subject=travel | count"); sel.Count != 12 || sel.IDs[0][0] != "" {
		t.Fatalf("count: %+v", sel)
	}
	for _, q := range []string{"recall aaaaaaaaaaaa", "save type=user name=x description=y", "history | limit 1"} {
		if _, err := Select(env, q, SourceNotes, SourceReflections); err == nil || !strings.Contains(err.Error(), "notes") {
			t.Fatalf("%s: err %v", q, err)
		}
	}
}

// The notes source reads the canonical subject (ledger.CanonicalSubjects over the notes
// present), keeps the stored scope, puts what a note's own subject adds in subject_scope, and
// keeps what the observer wrote in raw_subject: a query by subject finds every related note,
// a query by raw_subject the exact one, and a query by the stored scope still matches.
func TestNotesReadCanonicalSubjects(t *testing.T) {
	subjects := []string{"running", "running training", "running gear", "running shoes", "half marathon training", "fitness routine", "health metrics"}
	observations := []ledger.Observation{}
	for i, subject := range subjects {
		o := ledger.Observation{ID: fmt.Sprintf("%012x", i+1), Timestamp: "2026-05-02 10:30", Relevance: "low", Subject: subject, Content: "note about " + subject}
		if subject == "running shoes" {
			o.Scope = "trail"
		}
		observations = append(observations, o)
	}
	env := Env{Records: &Records{Observations: observations}}
	sel, err := Select(env, "notes | where subject=running", SourceNotes)
	if err != nil {
		t.Fatal(err)
	}
	if sel.Count != 4 {
		t.Fatalf("where subject=running matched %d notes: %+v", sel.Count, sel.Tables)
	}
	table := sel.Tables[0]
	got := []string{}
	for r := range table.Rows {
		subject, _ := table.Cell(r, "subject")
		scope, _ := table.Cell(r, "scope")
		extra, _ := table.Cell(r, ledger.SubjectScopeField)
		raw, _ := table.Cell(r, ledger.RawSubjectField)
		got = append(got, subject+"/"+scope+"/"+extra+"/"+raw)
	}
	want := "running///running|running//training/running training|running//gear/running gear|running/trail/shoes/running shoes"
	if strings.Join(got, "|") != want {
		t.Fatalf("rows %q\nwant %q", strings.Join(got, "|"), want)
	}
	if sel, _ := Select(env, `notes | where raw_subject="running shoes"`, SourceNotes); sel.Count != 1 {
		t.Fatalf("raw_subject matched %d", sel.Count)
	}
	if sel, _ := Select(env, "notes | where scope=trail", SourceNotes); sel.Count != 1 {
		t.Fatalf("where scope=trail matched %d", sel.Count)
	}
	sel, _ = Select(env, "notes | group by subject", SourceNotes)
	names := []string{}
	for _, table := range sel.Tables {
		names = append(names, table.Name)
	}
	if strings.Join(names, " ") != "notes.running notes.half_marathon_training notes.fitness_routine notes.health_metrics" {
		t.Fatalf("groups %q", names)
	}
	if observations[3].Subject != "running shoes" || observations[3].Scope != "trail" {
		t.Fatal("the query rewrote a record")
	}
}

// A stored definition filtering on a written subject keeps its rows when a shorter subject
// containing it first appears: where subject= also matches raw_subject.
func TestSubjectFilterSurvivesShorterSubject(t *testing.T) {
	note := func(id int, subject string) ledger.Observation {
		return ledger.Observation{ID: fmt.Sprintf("%012x", id), Timestamp: "2026-05-02 10:30", Relevance: "low", Subject: subject, Content: "note about " + subject}
	}
	before := []ledger.Observation{note(1, "running shoes"), note(2, "running shoes"), note(3, "running shoes")}
	after := append(append([]ledger.Observation{}, before...), note(4, "running"))
	for _, query := range []string{`notes | where subject="running shoes"`, `notes | where subject~"running shoes"`} {
		for _, set := range [][]ledger.Observation{before, after} {
			sel, err := Select(Env{Records: &Records{Observations: set}}, query, SourceNotes)
			if err != nil {
				t.Fatal(err)
			}
			if sel.Count != 3 {
				t.Fatalf("%s over %d notes matched %d, want 3", query, len(set), sel.Count)
			}
		}
	}
	if sel, _ := Select(Env{Records: &Records{Observations: after}}, "notes | where subject=running", SourceNotes); sel.Count != 4 {
		t.Fatalf("where subject=running matched %d, want 4", sel.Count)
	}
}

// A field a schema added under a name the kernel later took for a subject field stays
// readable as schema_<name>.
func TestShadowedSchemaFieldStaysReadable(t *testing.T) {
	o := ledger.Observation{ID: fmt.Sprintf("%012x", 1), Timestamp: "2026-05-02 10:30", Relevance: "low", Subject: "running shoes", Content: "note", Fields: map[string]string{ledger.RawSubjectField: "what the user called it"}}
	sel, err := Select(Env{Records: &Records{Observations: []ledger.Observation{o}}}, "notes", SourceNotes)
	if err != nil {
		t.Fatal(err)
	}
	table := sel.Tables[0]
	raw, _ := table.Cell(0, ledger.RawSubjectField)
	schema, ok := table.Cell(0, "schema_"+ledger.RawSubjectField)
	if raw != "running shoes" || !ok || schema != "what the user called it" {
		t.Fatalf("raw_subject %q, schema_raw_subject %q (%v)", raw, schema, ok)
	}
}

// The notes source reads subjects under the recorded aliases, from the records given (a
// model summary's projection) or folded from the branch: the aliased note queries as running,
// with what it wrote in subject_scope and raw_subject.
func TestNotesReadSubjectAliases(t *testing.T) {
	observations := []ledger.Observation{
		{ID: "000000000001", Timestamp: "2026-05-02 10:30", Relevance: "low", Subject: "running", Content: "note about running", SourceEntryIDs: []string{"raw-1"}},
		{ID: "000000000002", Timestamp: "2026-05-02 10:31", Relevance: "low", Subject: "half marathon training", Content: "note about the race", SourceEntryIDs: []string{"raw-1"}},
		{ID: "000000000003", Timestamp: "2026-05-02 10:32", Relevance: "low", Subject: "running/pace", Content: "note about pace", SourceEntryIDs: []string{"raw-1"}},
	}
	aliases := []ledger.SubjectAlias{{From: "half marathon training", To: "running"}}
	branch := []ledger.Entry{
		{Type: "message", ID: "raw-1"},
		{Type: "custom", ID: "om-obs", CustomType: ledger.OMObservationsRecorded, Data: toJSON(map[string]any{"observations": observations, "coversUpToId": "raw-1"})},
		{Type: "custom", ID: "om-alias", CustomType: ledger.OMSubjectsAliased, Data: toJSON(map[string]any{"aliases": aliases})},
	}
	for name, env := range map[string]Env{"records": {Records: &Records{Observations: observations, Aliases: aliases}}, "branch": {Entries: branch}} {
		sel, err := Select(env, "notes | where subject=running", SourceNotes)
		if err != nil || sel.Count != 3 {
			t.Fatalf("%s: matched %d (%v)", name, sel.Count, err)
		}
		got := []string{}
		for r := range sel.Tables[0].Rows {
			extra, _ := sel.Tables[0].Cell(r, ledger.SubjectScopeField)
			raw, _ := sel.Tables[0].Cell(r, ledger.RawSubjectField)
			got = append(got, extra+"="+raw)
		}
		if want := "=running|half marathon training=half marathon training|pace=running/pace"; strings.Join(got, "|") != want {
			t.Fatalf("%s: rows %q, want %q", name, strings.Join(got, "|"), want)
		}
	}
}

// toJSON round-trips v through JSON, as a session entry's data reads.
func toJSON(v any) any {
	data, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(data, &out)
	return out
}
