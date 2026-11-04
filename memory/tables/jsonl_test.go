package tables

import (
	"strings"
	"testing"
)

func TestJSONLinesRoundTrip(t *testing.T) {
	in := []Table{
		{Name: "reflections", Fields: []string{"id", "dates", "content"}, Rows: [][]string{
			{"a1b2c3d4e5f6", "2023-05-20..2023-05-24", "User bakes: \"sourdough\", rye\nand <focaccia>."},
			{"b2c3d4e5f6a1", "", "x"},
		}, Absent: abs(nil, []bool{false, true, false})},
		{Name: "empty", Fields: []string{"a"}, Rows: [][]string{}},
	}
	text := JSONLines.Encode(in...)
	if !strings.Contains(text, "<focaccia>") || !strings.Contains(text, "null") {
		t.Fatalf("encoded:\n%s", text)
	}
	got, err := JSONLines.Decode(text)
	if err != nil {
		t.Fatalf("decode: %v\n%s", err, text)
	}
	if len(got) != 2 || !tablesEqual(got[0], in[0]) || got[1].Name != "empty" || len(got[1].Rows) != 0 {
		t.Fatalf("round trip = %#v", got)
	}
	if v, ok := got[0].Cell(1, "dates"); ok || v != "" {
		t.Fatalf("absent cell = %q, %v", v, ok)
	}
}

func TestJSONLinesDecodeErrors(t *testing.T) {
	cases := []struct {
		text, want string
		line       int
	}{
		{`["a"]`, "row outside a table", 1},
		{"{\"table\":\"t\",\"fields\":[\"a\",\"b\"]}\n[\"x\"]", "row has 1 values, header has 2 fields", 2},
		{`{"table":"t","fields":["a"],"extra":1}`, "invalid table header", 1},
		{`{"table":"bad name","fields":["a"]}`, "invalid table name", 1},
		{`{"table":"t","fields":[]}`, "has no fields", 1},
		{`{"table":"t","fields":["a","a"]}`, "duplicate field", 1},
		{"{\"table\":\"t\",\"fields\":[\"a\"]}\nx", "expected a table header or a row", 2},
		{"{\"table\":\"t\",\"fields\":[\"a\"]}\n[1]", "invalid row", 2},
	}
	for _, c := range cases {
		_, err := JSONLines.Decode(c.text)
		e, ok := AsError(err)
		if !ok || !strings.Contains(e.Msg, c.want) || e.Line != c.line || e.Hint == "" {
			t.Errorf("Decode(%q) = %v, want line %d %q", c.text, err, c.line, c.want)
		}
	}
}

func TestLongSpec(t *testing.T) {
	for _, c := range Codecs() {
		long := LongSpec(c)
		if !strings.HasPrefix(long, c.Spec()) || len(long) <= len(c.Spec()) || !strings.Contains(long, "Common mistakes") {
			t.Errorf("%s: LongSpec does not extend Spec:\n%s", c.Name(), long)
		}
	}
	if c, ok := Lookup("jsonl"); !ok || c != JSONLines || Codecs()[1] != JSONLines {
		t.Fatal("jsonl is not the second codec")
	}
}
