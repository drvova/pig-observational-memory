package tables

import (
	"reflect"
	"strings"
	"testing"
)

// abs builds an Absent matrix from rows of 0/1 flags.
func abs(rows ...[]bool) [][]bool { return rows }

func TestSpecExample(t *testing.T) {
	text := `reflections[2]{id,dates,content}:
  a1b2c3d4e5f6,2023-05-20..2023-05-24,User bakes at home with a convection oven.
  b2c3d4e5f6a1,2023-05-26,"User hosts Indian dinners: tikka masala, naan, saag paneer."`
	want := Table{
		Name:   "reflections",
		Fields: []string{"id", "dates", "content"},
		Rows: [][]string{
			{"a1b2c3d4e5f6", "2023-05-20..2023-05-24", "User bakes at home with a convection oven."},
			{"b2c3d4e5f6a1", "2023-05-26", "User hosts Indian dinners: tikka masala, naan, saag paneer."},
		},
	}
	got, err := Rows.Decode(text)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []Table{want}) {
		t.Fatalf("decoded %#v, want %#v", got, want)
	}
	if enc := Rows.Encode(want); enc != text {
		t.Fatalf("encoded\n%s\nwant\n%s", enc, text)
	}
}

func TestQuoteValue(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain", "plain"},
		{"two words", "two words"},
		{"", `""`},
		{" lead", `" lead"`},
		{"trail ", `"trail "`},
		{"\ttab", "\"\\ttab\""},
		{"in\ttab", "in\ttab"},
		{"\u00a0nbsp", "\"\u00a0nbsp\""},
		{"a,b", `"a,b"`},
		{"k: v", `"k: v"`},
		{`say "hi"`, `"say \"hi\""`},
		{`C:\path`, `"C:\\path"`},
		{`back\slash`, `"back\\slash"`},
		{"a|b", `"a|b"`},
		{"line\nbreak", `"line\nbreak"`},
		{"cr\rlf", `"cr\rlf"`},
		{"ünïcødé 日本語 🍞", "ünïcødé 日本語 🍞"},
		{"2023-05-20..2023-05-24", "2023-05-20..2023-05-24"},
		{"#[]{}=~", "#[]{}=~"},
	}
	for _, c := range cases {
		if got := quoteValue(c.in); got != c.want {
			t.Errorf("quoteValue(%q) = %q, want %q", c.in, got, c.want)
		}
		if needsQuote(c.in) != (c.in != c.want) {
			t.Errorf("needsQuote(%q) disagrees with quoteValue", c.in)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	cases := []struct {
		name   string
		blocks []block
		text   string
	}{
		{
			name:   "empty table",
			blocks: []block{Table{Name: "notes", Fields: []string{"id", "content"}}},
			text:   "notes[0]{id,content}:",
		},
		{
			name: "special characters",
			blocks: []block{Table{Name: "notes.baking", Fields: []string{"id", "content"}, Rows: [][]string{
				{"a", "commas, here"},
				{"b", "colon: here"},
				{"c", `quote " here`},
				{"d", `back\slash`},
				{"e", "new\nline"},
				{"f", "pipe | here"},
				{"g", "tab\there"},
				{"h", "日本語 🍞 ünï"},
				{"i", " padded "},
				{"j", `\"`},
				{"k", `"`},
				{"l", "verbatim #[]{}=~ text"},
			}}},
			text: `notes.baking[12]{id,content}:
  a,"commas, here"
  b,"colon: here"
  c,"quote \" here"
  d,"back\\slash"
  e,"new\nline"
  f,"pipe | here"
  g,tab	here
  h,日本語 🍞 ünï
  i," padded "
  j,"\\\""
  k,"\""
  l,verbatim #[]{}=~ text`,
		},
		{
			name: "empty and absent",
			blocks: []block{Table{
				Name:   "t",
				Fields: []string{"a", "b", "c"},
				Rows:   [][]string{{"", "", ""}, {"x", "", ""}, {"", "", "z"}},
				Absent: abs([]bool{false, true, false}, []bool{true, true, true}, []bool{true, false, false}),
			}},
			text: "t[3]{a,b,c}:\n  \"\",,\"\"\n  ,,\n  ,\"\",z",
		},
		{
			name: "single field empty row",
			blocks: []block{Table{
				Name:   "t",
				Fields: []string{"a"},
				Rows:   [][]string{{"x"}, {""}, {"y"}},
			}},
			text: "t[3]{a}:\n  x\n  \"\"\n  y",
		},
		{
			name: "scalars and lists",
			blocks: []block{
				scalar{Key: "matched", Value: "12"},
				scalar{Key: "query", Value: `notes | search "x"`},
				scalar{Key: "empty", Value: ""},
				scalar{Key: "none", Absent: true},
				list{Key: "ids", Items: []string{"a1", "b2", "c,3"}},
				list{Key: "nothing"},
				list{Key: "gaps", Items: []string{"a", "", ""}, Absent: []bool{false, true, false}},
				list{Key: "one-absent", Items: []string{""}, Absent: []bool{true}},
			},
			text: `matched: 12
query: "notes | search \"x\""
empty: ""
none:
ids[3]: a1,b2,"c,3"
nothing[0]:
gaps[3]: a,,""
one-absent[1]: `,
		},
		{
			name: "mixed document",
			blocks: []block{
				scalar{Key: "matched", Value: "3"},
				Table{Name: "a", Fields: []string{"x"}, Rows: [][]string{{"1"}}},
				Table{Name: "b", Fields: []string{"y", "z"}, Rows: [][]string{{"2", "3"}}},
				list{Key: "l", Items: []string{"q"}},
			},
			text: "matched: 3\na[1]{x}:\n  1\nb[1]{y,z}:\n  2,3\nl[1]: q",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := encodeBlocks(c.blocks...); got != c.text {
				t.Fatalf("encoded\n%s\nwant\n%s", got, c.text)
			}
			got, err := decodeAll(c.text)
			if err != nil {
				t.Fatal(err)
			}
			if !blocksEqual(got, c.blocks) {
				t.Fatalf("decoded %#v\nwant %#v", got, c.blocks)
			}
		})
	}
}

func TestDecodeLenient(t *testing.T) {
	// Blank lines between blocks, CRLF line endings and a trailing newline are accepted.
	text := "a[1]{x}:\r\n  1\r\n\r\n\r\nk: v\r\n"
	got, err := decodeAll(text)
	if err != nil {
		t.Fatal(err)
	}
	want := []block{Table{Name: "a", Fields: []string{"x"}, Rows: [][]string{{"1"}}}, scalar{Key: "k", Value: "v"}}
	if !blocksEqual(got, want) {
		t.Fatalf("decoded %#v", got)
	}
	// Unquoted values are verbatim, including inner spaces, colons, pipes and (in a scalar)
	// commas.
	got, err = decodeAll("t[1]{a,b}:\n  x: y,z | w\nl[2]: a,b c\ns: a, b: c")
	if err != nil {
		t.Fatal(err)
	}
	want = []block{
		Table{Name: "t", Fields: []string{"a", "b"}, Rows: [][]string{{"x: y", "z | w"}}},
		list{Key: "l", Items: []string{"a", "b c"}},
		scalar{Key: "s", Value: "a, b: c"},
	}
	if !blocksEqual(got, want) {
		t.Fatalf("decoded %#v", got)
	}
	if blocks, err := decodeAll(""); err != nil || len(blocks) != 0 {
		t.Fatalf("decodeAll(\"\") = %v, %v", blocks, err)
	}
}

func TestDecodeErrors(t *testing.T) {
	cases := []struct {
		name      string
		text      string
		line, col int
		msg       string
	}{
		{"too few rows", "notes[3]{id,content}:\n  a,b\n  c,d", 1, 7, "table notes has 2 rows, header says [3]"},
		{"too few rows before blank", "notes[2]{id}:\n  a\n\n  b", 1, 7, "table notes has 1 rows, header says [2]"},
		{"too many rows", "notes[1]{id}:\n  a\n  b\n  c\nk: v", 3, 1, "table notes has 3 rows, header says [1]"},
		{"row too short", "t[1]{id,when,kind,scope,content,supersedes}:\n  a,b,c,d,e", 2, 12, "row 1 has 5 fields, header {id,when,kind,scope,content,supersedes} has 6"},
		{"row too long", "t[2]{a,b}:\n  1,2\n  3,4,5", 3, 7, "row 2 has 3 fields, header {a,b} has 2"},
		{"unquoted comma", "t[1]{a,b}:\n  x,tikka, naan", 2, 11, "row 1 has 3 fields, header {a,b} has 2"},
		{"unterminated quote", "t[1]{a,b}:\n  x,\"open", 2, 5, "unterminated quote"},
		{"unterminated by escape", "s: \"open\\", 1, 4, "unterminated quote"},
		{"unknown escape", "t[1]{a}:\n  \"a\\qb\"", 2, 5, `unknown escape \q`},
		{"text after quote", "t[1]{a,b}:\n  \"x\"y,z", 2, 6, "text after the closing quote"},
		{"quote in unquoted", "t[1]{a,b}:\n  x,y\"z\"", 2, 6, "quote inside an unquoted value"},
		{"space after comma", "t[1]{a,b}:\n  a, b", 2, 5, "space after comma"},
		{"space after comma in observer row", "observations[1]{timestamp,relevance,sourceEntryIds,content}:\n  2026-05-02 10:30, high, entry-a, User bakes.", 2, 20, "space after comma"},
		{"space between quoted values", "t[1]{a,b}:\n  \"a\", \"b\"", 2, 7, "space after comma"},
		{"blank value after comma", "t[1]{a,b}:\n  a, ", 2, 5, "space after comma"},
		{"row starts with nbsp", "t[1]{a}:\n  \u00a0x", 2, 3, "value starts with a space"},
		{"list space after comma", "ids[2]: a, b", 1, 11, "space after comma"},
		{"list two spaces", "ids[2]:  a,b", 1, 9, "value starts with a space"},
		{"scalar two spaces", "k:  v", 1, 4, "value starts with a space"},
		{"scalar text after quote", "k: \"v\" x", 1, 7, "text after the closing quote"},
		{"scalar quote in unquoted", "k: say \"hi\"", 1, 8, "quote inside an unquoted value"},
		{"scalar missing space", "k:v", 1, 3, "expected a space after ':'"},
		{"list missing space", "k[1]:v", 1, 6, "expected a space after ':'"},
		{"list too few", "ids[3]: a,b", 1, 5, "list ids has 2 items, header says [3]"},
		{"list too many", "ids[2]: a,b,c", 1, 13, "list ids has 3 items, header says [2]"},
		{"list zero with items", "ids[0]: a", 1, 5, "list ids has 1 items, header says [0]"},
		{"row indented three", "t[1]{a}:\n   x", 2, 4, "bad indentation"},
		{"row indented one", "t[1]{a}:\n x", 2, 2, "bad indentation"},
		{"row indented tab", "t[1]{a}:\n\tx", 2, 2, "bad indentation"},
		{"row with tab after indent", "t[1]{a}:\n  \tx", 2, 4, "bad indentation"},
		{"row outside table", "  x,y", 1, 1, "row outside a table"},
		{"indented top level", "    k: v", 1, 5, "bad indentation"},
		{"whitespace line", " ", 1, 1, "bad indentation"},
		{"no name", "[2]{a}:", 1, 1, "expected a name"},
		{"bad char in name", "my notes[1]{a}:", 1, 3, `unexpected ' ' after name "my"`},
		{"name only", "notes", 1, 6, `expected ':' or '[' after "notes"`},
		{"missing count", "t[]{a}:", 1, 3, "expected a count in [N]"},
		{"non-digit count", "t[x]{a}:", 1, 3, "expected a count in [N]"},
		{"unclosed count", "t[2{a}:", 1, 4, "expected ']' after the count"},
		{"huge count", "t[99999999999999999999999]{a}:", 1, 3, "count too large"},
		{"nothing after count", "t[2]", 1, 5, "expected ':' or '{' after the count"},
		{"junk after count", "t[2]x", 1, 5, "expected ':' or '{' after the count"},
		{"unterminated fields", "t[1]{a,b:", 1, 10, "unterminated field list"},
		{"empty fields", "t[0]{}:", 1, 6, "empty field name"},
		{"spaced field", "t[0]{a, b}:", 1, 8, `bad field name " b"`},
		{"trailing comma field", "t[0]{a,}:", 1, 8, "empty field name"},
		{"duplicate field", "t[0]{a,b,a}:", 1, 10, `duplicate field "a"`},
		{"no colon after fields", "t[0]{a}", 1, 8, "expected ':' after the field list"},
		{"text after header", "t[1]{a}: x", 1, 9, "unexpected text after the table header"},
		{"unicode column", "t[1]{a,b}:\n  日本,\"語", 2, 6, "unterminated quote"},
		{"error after valid blocks", "a: 1\nb[1]: x\nt[1]{a}:\n  \"x\n", 4, 3, "unterminated quote"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := decodeAll(c.text)
			if err == nil {
				t.Fatal("decoded without error")
			}
			e, ok := AsError(err)
			if !ok {
				t.Fatalf("error %T is not *Error: %v", err, err)
			}
			if e.Line != c.line || e.Column != c.col || e.Msg != c.msg {
				t.Fatalf("got line %d, column %d, %q; want line %d, column %d, %q", e.Line, e.Column, e.Msg, c.line, c.col, c.msg)
			}
			if e.Hint == "" {
				t.Fatal("error has no hint")
			}
			if !strings.HasPrefix(err.Error(), "line ") || !strings.Contains(err.Error(), e.Hint) {
				t.Fatalf("Error() = %q", err.Error())
			}
		})
	}
}

func TestErrorFormat(t *testing.T) {
	_, err := decodeAll("t[1]{id,when,kind,scope,content,supersedes}:\n  a,b,c,d,e")
	want := "line 2, column 12: row 1 has 5 fields, header {id,when,kind,scope,content,supersedes} has 6; quote values that contain commas, and give every field a value or an empty slot"
	if err == nil || err.Error() != want {
		t.Fatalf("got %v\nwant %s", err, want)
	}
	if got := (&Error{Line: 1, Column: 2, Msg: "m"}).Error(); got != "line 1, column 2: m" {
		t.Fatalf("no-hint format %q", got)
	}
}

func TestRowsDecodeTablesOnly(t *testing.T) {
	tables, err := Rows.Decode("a[0]{x}:\n\nb[1]{y}:\n  1")
	if err != nil || len(tables) != 2 || tables[1].Name != "b" {
		t.Fatalf("DecodeTables = %#v, %v", tables, err)
	}
	for _, c := range []struct {
		text string
		line int
		msg  string
	}{
		{"a[0]{x}:\nk: v", 2, "expected a table, found a scalar"},
		{"a[0]{x}:\n\nk[1]: v", 3, "expected a table, found a list"},
	} {
		_, err := Rows.Decode(c.text)
		e, ok := AsError(err)
		if !ok || e.Line != c.line || e.Msg != c.msg {
			t.Errorf("Rows.Decode(%q) error = %v", c.text, err)
		}
	}
	if _, err := Rows.Decode("t[1]{a}:\n  \"x"); err == nil {
		t.Error("DecodeTables passed a syntax error through as success")
	}
}

func TestTableAccessors(t *testing.T) {
	tb := Table{
		Name:   "t",
		Fields: []string{"id", "when"},
		Rows:   [][]string{{"a", "2023"}, {"b", ""}},
		Absent: abs(nil, []bool{false, true}),
	}
	if tb.Index("when") != 1 || tb.Index("nope") != -1 {
		t.Fatal("Index")
	}
	if v, ok := tb.Cell(0, "when"); !ok || v != "2023" {
		t.Fatalf("Cell(0, when) = %q, %v", v, ok)
	}
	for _, c := range []struct {
		r int
		f string
	}{{1, "when"}, {2, "id"}, {-1, "id"}, {0, "nope"}} {
		if _, ok := tb.Cell(c.r, c.f); ok {
			t.Errorf("Cell(%d, %s) present", c.r, c.f)
		}
	}
	if !(list{Absent: []bool{true}}).IsAbsent(0) || (list{}).IsAbsent(0) {
		t.Fatal("list.IsAbsent")
	}
}

func TestValidate(t *testing.T) {
	ok := Table{Name: "notes.baking-2_x", Fields: []string{"id", "when"}, Rows: [][]string{{"a", "b"}}}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []Table{
		{Name: "", Fields: []string{"a"}},
		{Name: "my notes", Fields: []string{"a"}},
		{Name: "t"},
		{Name: "t", Fields: []string{"a b"}},
		{Name: "t", Fields: []string{"a", "a"}},
		{Name: "t", Fields: []string{"a", "b"}, Rows: [][]string{{"1"}}},
	}
	for _, b := range bad {
		if b.Validate() == nil {
			t.Errorf("Validate(%#v) = nil", b)
		}
	}
	for name, want := range map[string]bool{"a": true, "日本": true, "x.y-z_1": true, "": false, "a b": false, "a:b": false, "a[": false, "\xff": false} {
		if ValidName(name) != want {
			t.Errorf("ValidName(%q) != %v", name, want)
		}
	}
}

func TestEncodeTableUnevenRows(t *testing.T) {
	// A short row is padded with absent values and a long one truncated.
	got := encodeTable(Table{Name: "t", Fields: []string{"a", "b"}, Rows: [][]string{{"1"}, {"2", "3", "4"}}})
	if want := "t[2]{a,b}:\n  1,\n  2,3"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// blocksEqual compares decoded blocks with expected ones, treating absent cells as equal
// whatever their text and nil and empty slices as equal.
func blocksEqual(got, want []block) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		switch w := want[i].(type) {
		case Table:
			g, ok := got[i].(Table)
			if !ok || !tablesEqual(g, w) {
				return false
			}
		case scalar:
			g, ok := got[i].(scalar)
			if !ok || g.Key != w.Key || g.Absent != w.Absent || (!w.Absent && g.Value != w.Value) {
				return false
			}
		case list:
			g, ok := got[i].(list)
			if !ok || g.Key != w.Key || len(g.Items) != len(w.Items) {
				return false
			}
			for k := range w.Items {
				if g.IsAbsent(k) != w.IsAbsent(k) || (!w.IsAbsent(k) && g.Items[k] != w.Items[k]) {
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}

func tablesEqual(g, w Table) bool {
	if g.Name != w.Name || !reflect.DeepEqual(g.Fields, w.Fields) || len(g.Rows) != len(w.Rows) {
		return false
	}
	for r := range w.Rows {
		if len(g.Rows[r]) != len(w.Fields) {
			return false
		}
		for c := range w.Fields {
			if g.IsAbsent(r, c) != w.IsAbsent(r, c) || (!w.IsAbsent(r, c) && g.Rows[r][c] != w.Rows[r][c]) {
				return false
			}
		}
	}
	return true
}

func TestEncodeTrimSafe(t *testing.T) {
	// A single-field absent cell is written as "" so a trimmed text still decodes.
	tb := Table{Name: "t", Fields: []string{"a"}, Rows: [][]string{{"x"}, {""}}, Absent: abs(nil, []bool{true})}
	text := Rows.Encode(tb)
	if want := "t[2]{a}:\n  x\n  \"\""; text != want {
		t.Fatalf("encoded %q, want %q", text, want)
	}
	got, err := Rows.Decode(strings.TrimSpace(text))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || len(got[0].Rows) != 2 || got[0].Rows[1][0] != "" || got[0].IsAbsent(1, 0) {
		t.Fatalf("decoded %#v", got)
	}
	if tb.Validate() == nil {
		t.Fatal("Validate accepted an absent cell in a single-field table")
	}
	// Multi-field rows with absent cells keep their commas, so they never end in white space.
	multi := Table{Name: "t", Fields: []string{"a", "b"}, Rows: [][]string{{"", ""}}, Absent: abs([]bool{true, true})}
	if got, err := Rows.Decode(strings.TrimSpace(Rows.Encode(multi))); err != nil || !got[0].IsAbsent(0, 1) {
		t.Fatalf("multi-field absent row = %#v, %v", got, err)
	}
}

func TestCodecs(t *testing.T) {
	if Rows.Name() != "rows" || Codecs()[0] != Rows {
		t.Fatalf("default codec = %s", Codecs()[0].Name())
	}
	if c, ok := Lookup("rows"); !ok || c != Rows {
		t.Fatal("Lookup(rows)")
	}
	if _, ok := Lookup("nope"); ok {
		t.Fatal("Lookup(nope) found a codec")
	}
	spec := Rows.Spec()
	for _, want := range []string{"name[N]{", `\r`, "no space after a comma"} {
		if !strings.Contains(spec, want) {
			t.Errorf("Spec() lacks %q:\n%s", want, spec)
		}
	}
	if strings.Contains(spec, "TOON") {
		t.Error("Spec() names TOON")
	}
}

func TestIsAbsentNegativeIndex(t *testing.T) {
	tb := Table{Name: "t", Fields: []string{"a"}, Rows: [][]string{{""}}, Absent: abs([]bool{true})}
	if tb.IsAbsent(0, tb.Index("kind")) || tb.IsAbsent(-1, 0) {
		t.Fatal("negative index reads as absent")
	}
	if (list{Absent: []bool{true}}).IsAbsent(-1) {
		t.Fatal("list negative index reads as absent")
	}
}

// decodeAll parses a rows text into its blocks.
func decodeAll(text string) ([]block, error) {
	blocks, _, err := decode(text)
	return blocks, err
}
