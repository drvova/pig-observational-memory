package ledger_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
)

const schemaHeader = "schema[1]{field,type,values,max,description}:\n  "

// A schema declares only fields beyond the kernel's, with fixed types for the structured
// fields, and refuses everything else, naming why.
func TestNoteSchemaRefusals(t *testing.T) {
	for name, tc := range map[string]struct{ row, want string }{
		"kernel field":   {"relevance,text,,,", "kernel note field"},
		"kernel field 2": {"sourceEntryIds,text,,,", "lowercase"},
		"retype":         {"subject,enum,a b,,", "fixed type text"},
		"kind value":     {"kind,enum,fact rumor,,", "not a kind the kernel knows"},
		"kind empty":     {"kind,enum,,,", "needs its values"},
		"builtin max":    {"scope,text,,10,", "takes no max"},
		"ids":            {"links,ids,,,", "type"},
		"type":           {"mood,colour,,,", "type"},
		"text max":       {"mood,text,,500,", "max 500"},
		"enum values":    {"mood,enum,,,", "1 to 32 values"},
		"enum value":     {"mood,enum,Happy,,", "lowercase"},
		"number max":     {"n,number,,3,", "only text takes max"},
		"text values":    {"mood,text,a b,,", "only enum takes values"},
		"description":    {"mood,text,,," + strings.Repeat("x", 201), "description"},
	} {
		if _, err := ledger.ParseNoteSchema(schemaHeader + tc.row); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err %v", name, err)
		}
	}
	if _, err := ledger.ParseNoteSchema("schema[2]{field,type}:\n  mood,text\n  mood,text"); err == nil {
		t.Fatal("a field declared twice was accepted")
	}
	if _, err := ledger.ParseNoteSchema("fields[1]{field,type}:\n  mood,text"); err == nil {
		t.Fatal("a table of another name was accepted")
	}
	many := "schema[13]{field,type}:"
	for i := 0; i < 13; i++ {
		many += "\n  f" + string(rune('a'+i)) + ",number"
	}
	if _, err := ledger.ParseNoteSchema(many); err == nil {
		t.Fatal("too many fields were accepted")
	}
}

// Values of the fields a schema adds are normalized or refused by their declaration, and a
// note carries them through its stored JSON.
func TestNoteSchemaFieldValues(t *testing.T) {
	s, err := ledger.ParseNoteSchema("schema[4]{field,type,values,max}:\n  mood,enum,happy sad,\n  note,text,,5\n  amount,number,,\n  due,date,,")
	if err != nil {
		t.Fatal(err)
	}
	check := func(field, in, want string, ok bool) {
		t.Helper()
		f, _ := s.Field(field)
		got, gotOK := f.Normalize(in)
		if got != want || gotOK != ok {
			t.Fatalf("%s %q: %q %v", field, in, got, gotOK)
		}
	}
	check("mood", "HAPPY", "happy", true)
	check("mood", "angry", "angry", false)
	check("note", "  one   two three ", "one t", true)
	check("amount", "-12.5", "-12.5", true)
	check("amount", "12,5", "12,5", false)
	check("due", "2026-02-30", "", false)
	check("due", "2026-02-28", "2026-02-28", true)
	o := ledger.Observation{ID: "aaaaaaaaaaaa", Content: "x", Timestamp: "2026-05-02 10:30", Relevance: "low", SourceEntryIDs: []string{"e"}, Fields: map[string]string{"mood": "happy", "Bad Name": "x"}}
	raw, _ := json.Marshal(o)
	var decoded any
	_ = json.Unmarshal(raw, &decoded)
	back, ok := ledger.ObservationFrom(decoded)
	if !ok || len(back.Fields) != 1 || back.Fields["mood"] != "happy" {
		t.Fatalf("back %+v", back)
	}
	plain, _ := json.Marshal(ledger.Observation{ID: "aaaaaaaaaaaa"})
	if strings.Contains(string(plain), "fields") {
		t.Fatalf("a note without schema fields stores them: %s", plain)
	}
}
