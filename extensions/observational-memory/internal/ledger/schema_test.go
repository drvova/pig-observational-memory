package ledger_test

import (
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
