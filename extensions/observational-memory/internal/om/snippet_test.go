package om

import (
	"go/ast"
	"go/parser"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ids"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
)

func intp(n int) *int { return &n }

// The trusted formatter writes ids as raw strings and chains the options as methods, with no
// comma, double quote or | between them.
func TestFormatRead(t *testing.T) {
	cases := []struct {
		fn, id string
		o      omwire.Options
		want   string
	}{
		{"Notes", "", omwire.Options{From: "e1", Through: "e9"}, "om.Notes(om.From(`e1`).Through(`e9`))"},
		{"Notes", "", omwire.Options{From: "e1", Through: "e9", Limit: intp(50)}, "om.Notes(om.From(`e1`).Through(`e9`).Limit(50))"},
		{"Notes", "", omwire.Options{Since: "2026-01-01", Until: "2026-01-02"}, "om.Notes(om.Since(`2026-01-01`).Until(`2026-01-02`))"},
		{"History", "", omwire.Options{Search: []string{"oven"}, Through: "e5 (off-path)"}, "om.History(om.Search(`oven`).Through(`e5 (off-path)`))"},
		{"Recall", "e42", omwire.Options{Offset: intp(10000)}, "om.Recall(`e42`, om.Offset(10000))"},
		{"Files", "", omwire.Options{}, "om.Files()"},
		{"History", "", omwire.Options{Search: []string{"a`b"}}, "om.History(om.Search(\"a`b\"))"},
	}
	for _, c := range cases {
		if got := FormatRead(c.fn, c.id, c.o); got != c.want {
			t.Errorf("FormatRead(%s, %+v) = %s, want %s", c.fn, c.o, got, c.want)
		}
		if err := CheckSnippet(c.want); err != nil {
			t.Errorf("%s: %v", c.want, err)
		}
	}
	for _, s := range []string{SnippetRecall, SnippetHistorySearch, SnippetSavedSearch, SnippetNotesSpan} {
		if err := CheckSnippet(s); err != nil {
			t.Errorf("%s: %v", s, err)
		}
	}
}

// The strict formatter takes words with backticks, quotes, newlines and ")" and still yields
// one read call with literal options; ids, dates and limits outside their formats yield no
// snippet.
func TestFormatReadStrict(t *testing.T) {
	for _, w := range []string{"a`b", `"); os.Exit(1); ("`, "x\ny", "`)\n.Save(`", "\r"} {
		s, err := FormatReadStrict("Notes", "", omwire.Options{Search: []string{w}, Where: []omwire.Pair{{Field: "kind", Value: w}}}, "")
		if err != nil {
			t.Fatalf("%q: %v", w, err)
		}
		expr, err := parser.ParseExpr(s)
		if err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		call := expr.(*ast.CallExpr)
		chain := call.Args[0].(*ast.CallExpr)
		lit := chain.Args[1].(*ast.BasicLit)
		if v, _ := strconv.Unquote(lit.Value); v != w {
			t.Fatalf("%q came back as %q in %s", w, v, s)
		}
	}
	good := "0123456789ab"
	if s, err := FormatReadStrict("Notes", "", omwire.Options{From: good, Through: "deadbeef"}, "Matched"); err != nil || s != "om.Notes(om.From(`0123456789ab`).Through(`deadbeef`)).Matched" {
		t.Fatalf("%s %v", s, err)
	}
	for _, o := range []omwire.Options{
		{From: "e1`)"},
		{Through: "e1"},
		{Since: "yesterday"},
		{Limit: intp(201)},
		{Limit: intp(0)},
		{Where: []omwire.Pair{{Field: "Kind\"", Value: "x"}}},
		{Offset: intp(3)},
	} {
		if s, err := FormatReadStrict("Notes", "", o, ""); err == nil {
			t.Errorf("%+v gave %s", o, s)
		}
	}
	if _, err := FormatReadStrict("Recall", "my-memory", omwire.Options{}, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := FormatReadStrict("Recall", "x`y", omwire.Options{}, ""); err == nil {
		t.Fatal("a bad recall id was written")
	}
	if _, err := FormatReadStrict("Save", "", omwire.Options{}, ""); err == nil {
		t.Fatal("a write was written")
	}
}

// CheckSnippet accepts only read calls with literal option chains.
func TestCheckSnippetRefuses(t *testing.T) {
	for _, s := range []string{
		"om.Save(`a`, `b`, `c`)",
		"om.Notes(om.Search(x))",
		"om.Notes(om.Search(`a`), om.Limit(2))",
		"om.Notes(om.Search(`a`).Save(`b`))",
		"om.Notes(om.Search(f()))",
		"om.Notes().Rows",
		"fmt.Println(1)",
		"om.Notes(om.Search(`a`)); om.Save(`x`)",
	} {
		if CheckSnippet(s) == nil {
			t.Errorf("%s was accepted", s)
		}
	}
}

// No id format can hold a backtick, so a snippet always writes an id as a raw string.
func TestIDFormatsHaveNoBacktick(t *testing.T) {
	for _, p := range []string{ids.RecordIDPattern.String(), ids.EntryIDPattern.String()} {
		if strings.Contains(p, "`") || strings.Contains(p, ".") {
			t.Errorf("%s may match a backtick", p)
		}
	}
	if !ids.Valid("0123456789ab") || !ids.Valid("deadbeef (off-path)") || !ids.Valid("deadbeef-0000-4000-8000-0123456789ab") || ids.Valid("e1") {
		t.Fatal("id formats")
	}
}

// The manual lists every entry point with its doc, the bounds and the kept definitions, and
// differs only in the store's part.
func TestManual(t *testing.T) {
	m := Manual("")
	for _, g := range ManualGroups {
		for _, n := range g.Names {
			if !strings.Contains(m, "om."+n) {
				t.Errorf("the manual lacks om.%s", n)
			}
			if _, doc, ok := Doc(n); !ok || doc == "" {
				t.Errorf("%s has no doc", n)
			}
		}
	}
	for _, want := range []string{"describe(x any) string", "kept(name ...string) string", "10000 characters", "min, max", "headless", "── Kept definitions ──\nnone"} {
		if !strings.Contains(m, want) {
			t.Errorf("the manual lacks %q", want)
		}
	}
	if Manual("") != m || !strings.HasSuffix(Manual("f\n"), "── Kept definitions ──\nf") {
		t.Fatal("the store part")
	}
	if n := utf8.RuneCountInString(m); n > RecallPageChars {
		t.Fatalf("the manual is %d characters, over one result", n)
	}
	big := strings.Repeat("unit-with-a-long-name (func) loaded\n", 1000)
	if b := Manual(big); utf8.RuneCountInString(b) > RecallPageChars || !strings.HasSuffix(b, "kept() lists them all") || !strings.Contains(b, "unit-with-a-long-name") {
		t.Fatalf("a long store part: %d characters", utf8.RuneCountInString(b))
	}
	if s := ManualSection("Note"); !strings.Contains(s, "── Values") {
		t.Fatal(s)
	}
	if s := ManualSection("Recall"); !strings.Contains(s, "── Reads ──") {
		t.Fatal(s)
	}
	if s := ManualSection("max"); !strings.Contains(s, "Language level") {
		t.Fatal(s)
	}
	if s := ManualSection("Opt"); !strings.Contains(s, "── Options") {
		t.Fatal(s)
	}
	if ManualSection("nope") != "" {
		t.Fatal("nope")
	}
}
