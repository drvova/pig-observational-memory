package jsonschema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func set(t *testing.T, docs map[string]string) (*Set, error) {
	t.Helper()
	dir := t.TempDir()
	for name, text := range docs {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return Load(dir)
}

// Each keyword the contracts use is checked, references resolve across documents, and a
// keyword the validator does not implement is refused at load rather than ignored.
func TestKeywords(t *testing.T) {
	s, err := set(t, map[string]string{
		"defs.schema.json": `{"$defs":{"id":{"type":"string","pattern":"^[a-f0-9]{4}$"}}}`,
		"doc.schema.json": `{"type":"object","required":["id","kind"],"additionalProperties":false,"properties":{
			"id":{"$ref":"defs.schema.json#/$defs/id"},
			"kind":{"enum":["a","b"]},
			"n":{"type":"integer","minimum":1},
			"list":{"type":"array","minItems":1,"items":{"type":"string","minLength":1}},
			"either":{"anyOf":[{"type":"string"},{"type":"null"}]},
			"fixed":{"const":1}}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if errs := s.ValidateJSON("doc.schema.json", []byte(`{"id":"ab12","kind":"a","n":2,"list":["x"],"either":null,"fixed":1}`)); len(errs) != 0 {
		t.Fatalf("valid document refused: %v", errs)
	}
	for doc, want := range map[string]string{
		`{"kind":"a"}`:                          `missing required member "id"`,
		`{"id":"zz","kind":"a"}`:                "does not match",
		`{"id":"ab12","kind":"c"}`:              "is not one of",
		`{"id":"ab12","kind":"a","n":1.5}`:      "type number, want integer",
		`{"id":"ab12","kind":"a","n":0}`:        "below",
		`{"id":"ab12","kind":"a","list":[]}`:    "at least 1",
		`{"id":"ab12","kind":"a","list":[""]}`:  "shorter",
		`{"id":"ab12","kind":"a","either":1}`:   "anyOf",
		`{"id":"ab12","kind":"a","fixed":2}`:    "want 1",
		`{"id":"ab12","kind":"a","extra":true}`: "not allowed",
	} {
		errs := s.ValidateJSON("doc.schema.json", []byte(doc))
		if len(errs) == 0 || !strings.Contains(errs[0].Error(), want) {
			t.Errorf("%s: %v, want %q", doc, errs, want)
		}
	}
	if _, err := set(t, map[string]string{"x.schema.json": `{"oneOf":[{"type":"string"}]}`}); err == nil || !strings.Contains(err.Error(), "unsupported keyword") {
		t.Fatalf("unsupported keyword: %v", err)
	}
	if _, err := set(t, map[string]string{"x.schema.json": `{"$ref":"#/$defs/missing"}`}); err == nil {
		t.Fatal("a dangling $ref loaded")
	}
}
