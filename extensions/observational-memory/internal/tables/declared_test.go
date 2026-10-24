package tables_test

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

var rowsDeclaration = tables.Format{Name: "rows", Header: tables.HeaderCounted, Delimiter: ',', Quote: '"', Escape: '\\', Escapes: "nrt", Special: ":|", Indent: 2}

// A declaration survives its own text, and the rows codec declared as data is interpreted
// exactly as the native codec: the same text for every kernel test table, and each reads the
// other's output.
func TestDeclaredRowsMatchesTheNativeCodec(t *testing.T) {
	parsed, err := tables.ParseFormat("rows", rowsDeclaration.Declaration())
	if err != nil || parsed != rowsDeclaration {
		t.Fatalf("parsed %+v err %v", parsed, err)
	}
	c, err := tables.Declared(parsed)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range kernel.TestTables() {
		native := tables.Rows.Encode(group...)
		if got := c.Encode(group...); got != native {
			t.Fatalf("declared rows encodes\n%s\nnative\n%s", got, native)
		}
		if _, err := c.Decode(native); err != nil {
			t.Fatalf("declared rows cannot read native rows: %v", err)
		}
		if _, err := tables.Rows.Decode(c.Encode(group...)); err != nil {
			t.Fatalf("native rows cannot read declared rows: %v", err)
		}
	}
	if err := kernel.AcceptCodec(c); err != nil {
		t.Fatal(err)
	}
}

// Other declarations are interpreted by the same generic codec and pass the kernel's round
// trip; the jsonl form is JSON lines.
func TestDeclaredFormatsRoundTrip(t *testing.T) {
	for _, f := range []tables.Format{
		{Name: "semi", Header: tables.HeaderCounted, Delimiter: ';', Quote: '\'', Escape: '^', Escapes: "nr", Indent: 4},
		{Name: "pipes", Header: tables.HeaderCounted, Delimiter: '|', Quote: '`', Escape: '~', Escapes: "ntr", Special: "#", Indent: 1},
		{Name: "lines", Header: tables.HeaderJSONLines, Delimiter: ',', Quote: '"', Escape: '\\'},
	} {
		c, err := tables.Declared(f)
		if err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		if err := kernel.AcceptCodec(c); err != nil {
			t.Fatalf("%s: %v", f.Name, err)
		}
		if c.Name() != f.Name || c.Spec() == "" || tables.LongSpec(c) == c.Spec() {
			t.Fatalf("%s: name %q spec %q", f.Name, c.Name(), c.Spec())
		}
	}
	semi, _ := tables.Declared(tables.Format{Name: "semi", Header: tables.HeaderCounted, Delimiter: ';', Quote: '\'', Escape: '^', Escapes: "nr", Indent: 4})
	text := semi.Encode(tables.Table{Name: "notes", Fields: []string{"id", "content"}, Rows: [][]string{{"a1", "it's; here"}}})
	if text != "notes[1]{id;content}:\n    a1;'it^'s; here'" {
		t.Fatalf("encoded %q", text)
	}
}

// A declaration the interpreter cannot read strictly is refused, naming why.
func TestDeclaredFormatRefusals(t *testing.T) {
	base := rowsDeclaration
	for name, tc := range map[string]struct {
		edit func(*tables.Format)
		want string
	}{
		"form":       {func(f *tables.Format) { f.Header = "markdown" }, "not a header form"},
		"letter":     {func(f *tables.Format) { f.Delimiter = 'x' }, "punctuation"},
		"structural": {func(f *tables.Format) { f.Delimiter = '{' }, "punctuation"},
		"same":       {func(f *tables.Format) { f.Quote = ',' }, "three different"},
		"newline":    {func(f *tables.Format) { f.Escapes = "t" }, "must include n and r"},
		"escapes":    {func(f *tables.Format) { f.Escapes = "nrx" }, "only n, r and t"},
		"indent":     {func(f *tables.Format) { f.Indent = 0 }, "indent"},
		"special":    {func(f *tables.Format) { f.Special = "a" }, "punctuation and symbols"},
		"json":       {func(f *tables.Format) { f.Header = tables.HeaderJSONLines; f.Delimiter = ';' }, "fixes JSON"},
	} {
		f := base
		tc.edit(&f)
		if _, err := tables.Declared(f); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err %v", name, err)
		}
	}
	for name, text := range map[string]string{
		"not a table": "delimiter: ,",
		"two rows":    "format[2]{header,delimiter,quote,escape,escapes,special,indent}:\n  counted,\",\",\"\\\"\",\"\\\\\",nrt,,2\n  counted,\",\",\"\\\"\",\"\\\\\",nrt,,2",
		"unknown":     "format[1]{header,delimiter,quote,escape,escapes,indent,colour}:\n  counted,\",\",\"\\\"\",\"\\\\\",nrt,2,red",
		"wide":        "format[1]{header,delimiter,quote,escape,escapes,indent}:\n  counted,\";;\",\"\\\"\",\"\\\\\",nrt,2",
	} {
		if _, err := tables.ParseFormat("x", text); err == nil {
			t.Fatalf("%s: parsed", name)
		}
	}
}

// The declared codec decodes strictly: every malformed text fails with a line and a fix.
func TestDeclaredDecodeIsStrict(t *testing.T) {
	c, _ := tables.Declared(tables.Format{Name: "semi", Header: tables.HeaderCounted, Delimiter: ';', Quote: '\'', Escape: '^', Escapes: "nr", Indent: 2})
	for name, text := range map[string]string{
		"few rows":     "t[2]{a;b}:\n  1;2",
		"more rows":    "t[1]{a;b}:\n  1;2\n  3;4",
		"width":        "t[1]{a;b}:\n  1;2;3",
		"unterminated": "t[1]{a;b}:\n  1;'open",
		"escape":       "t[1]{a;b}:\n  1;'bad ^x'",
		"indent":       "t[1]{a;b}:\n   1;2",
		"stray quote":  "t[1]{a;b}:\n  1;it's",
		"space":        "t[1]{a;b}:\n  1; 2",
		"after quote":  "t[1]{a;b}:\n  1;'x'y",
		"orphan row":   "  1;2",
		"header":       "t{a;b}:",
		"duplicate":    "t[0]{a;a}:",
		"comma fields": "t[0]{a,b}:",
	} {
		_, err := c.Decode(text)
		e, ok := tables.AsError(err)
		if !ok || e.Line == 0 || e.Hint == "" {
			t.Fatalf("%s: err %v", name, err)
		}
	}
	got, err := c.Decode("t[2]{a;b}:\n  1;\n  '';'x^ny'")
	if err != nil || len(got) != 1 || !got[0].IsAbsent(0, 1) || got[0].IsAbsent(1, 0) || got[0].Rows[1][1] != "x\ny" {
		t.Fatalf("got %+v err %v", got, err)
	}
}
