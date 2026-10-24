package memquery

import (
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
)

func mustParse(t *testing.T, query string) *Query {
	t.Helper()
	q, err := Parse(query)
	if err != nil {
		t.Fatalf("Parse(%q): %v", query, err)
	}
	return q
}

// strip clears the positions, which the construct tests do not compare.
func strip(stages []Stage) []Stage {
	out := []Stage{}
	for _, s := range stages {
		s.pos, s.fieldPos = 0, 0
		out = append(out, s)
	}
	return out
}

func TestParseReads(t *testing.T) {
	cases := []struct {
		query  string
		source string
		stages []Stage
	}{
		{"notes", SourceNotes, []Stage{}},
		{"reflections", SourceReflections, []Stage{}},
		{"saved", SourceSaved, []Stage{}},
		{`history | search "india online store" | limit 5`, SourceHistory, []Stage{{Kind: StageSearch, Words: "india online store"}, {Kind: StageLimit, N: 5}}},
		{`notes | search sourdough`, SourceNotes, []Stage{{Kind: StageSearch, Words: "sourdough"}}},
		{`notes | where kind=event | where content~"two weeks"`, SourceNotes, []Stage{{Kind: StageWhere, Field: "kind", Op: '=', Value: "event"}, {Kind: StageWhere, Field: "content", Op: '~', Value: "two weeks"}}},
		{`notes | where scope=""`, SourceNotes, []Stage{{Kind: StageWhere, Field: "scope", Op: '=', Value: ""}}},
		{`notes | since 2023-05-01 | until 2023-05-31`, SourceNotes, []Stage{{Kind: StageSince, Value: "2023-05-01"}, {Kind: StageUntil, Value: "2023-05-31"}}},
		{`notes | sort when desc | sort id asc | sort kind`, SourceNotes, []Stage{{Kind: StageSort, Field: "when", Desc: true}, {Kind: StageSort, Field: "id"}, {Kind: StageSort, Field: "kind"}}},
		{`notes | count`, SourceNotes, []Stage{{Kind: StageCount}}},
		{`notes | search "sourdough" | where kind=event | count by subject`, SourceNotes, []Stage{{Kind: StageSearch, Words: "sourdough"}, {Kind: StageWhere, Field: "kind", Op: '=', Value: "event"}, {Kind: StageCount, Field: "subject"}}},
		{`notes | group by subject`, SourceNotes, []Stage{{Kind: StageGroup, Field: "subject"}}},
		{"notes|limit 200", SourceNotes, []Stage{{Kind: StageLimit, N: 200}}},
	}
	for _, c := range cases {
		q := mustParse(t, c.query)
		if q.Source != c.source || q.Write != "" || !reflect.DeepEqual(strip(q.Stages), c.stages) {
			t.Errorf("Parse(%q) = %s %+v", c.query, q.Source, q.Stages)
		}
	}
}

func TestParseRecall(t *testing.T) {
	for query, id := range map[string]string{"recall 3f9a2c1d7b4e": "3f9a2c1d7b4e", "recall prefers-tabs": "prefers-tabs", `recall "entry-1"`: "entry-1"} {
		if q := mustParse(t, query); q.Source != SourceRecall || q.ID != id {
			t.Errorf("Parse(%q) = %+v", query, q)
		}
	}
}

func TestParseStringEscapes(t *testing.T) {
	q := mustParse(t, `notes | search "say \"hi\"\n\tnow \\ \x"`)
	if got := q.Stages[0].Words; got != "say \"hi\"\n\tnow \\ \\x" {
		t.Fatalf("words %q", got)
	}
}

func TestParseWrites(t *testing.T) {
	q := mustParse(t, `save type=user name=prefers-tabs description="The user indents with tabs." scope=project`)
	if q.Write != WriteSave || !reflect.DeepEqual(q.Args, map[string]string{"type": "user", "name": "prefers-tabs", "description": "The user indents with tabs.", "scope": "project"}) {
		t.Fatalf("save %+v", q)
	}
	q = mustParse(t, `edit name=prefers-tabs body=""`)
	if q.Write != WriteEdit || q.Args["body"] != "" || len(q.Args) != 2 {
		t.Fatalf("edit %+v", q)
	}
	if q := mustParse(t, "forget name=prefers-tabs"); q.Write != WriteForget || q.Args["name"] != "prefers-tabs" {
		t.Fatalf("forget %+v", q)
	}
	q = mustParse(t, `feedback name=prefers-tabs outcome=helped quote="you asked for tabs again"`)
	if q.Write != WriteFeedback || q.Args["outcome"] != "helped" || q.Args["quote"] != "you asked for tabs again" {
		t.Fatalf("feedback %+v", q)
	}
}

func TestParseDefine(t *testing.T) {
	q := mustParse(t, `define bake-count = notes | where subject=baking | count "How many times the user baked"`)
	want := &Definition{Name: "bake-count", Query: "notes | where subject=baking | count", Description: "How many times the user baked"}
	if q.Write != WriteDefine || !reflect.DeepEqual(q.Define, want) {
		t.Fatalf("define %+v", q.Define)
	}
	q = mustParse(t, `define find =  history | search "$input"   "Raw history mentioning the input"`)
	if q.Define.Query != `history | search "$input"` {
		t.Fatalf("query kept as written: %q", q.Define.Query)
	}
	if q := mustParse(t, `define show = recall $input "One record"`); q.Define.Query != "recall $input" {
		t.Fatalf("recall body %q", q.Define.Query)
	}
}

// Every error names the position and the fix.
func TestParseErrors(t *testing.T) {
	cases := []struct {
		query string
		pos   int
		msg   string
		hint  string
	}{
		{"", 0, "empty query", "start with a source"},
		{"   ", 0, "empty query", "start with a source"},
		{"memories", 1, `unknown source "memories"`, "notes, reflections, history, saved, recall"},
		{"search \"x\"", 1, `unknown source "search"`, "a stage needs a source first"},
		{"notes | filter x", 9, `unknown stage "filter"`, "stages are: search, where"},
		{"notes | history", 9, `unknown stage "history"`, "one source"},
		{"notes search \"x\"", 7, `unexpected "search"`, "separate stages with |"},
		{"notes |", 8, "unknown stage the end of the query", "stages are"},
		{"notes | search", 15, "search needs words", `search "words"`},
		{`notes | search ""`, 16, "search needs words", `search "words"`},
		{`notes | search "unterminated`, 16, "unterminated quote", `close the text with "`},
		{"notes | where kind", 19, "where needs = or ~ after kind", "where field=value"},
		{"notes | where =x", 15, "where needs a field", "name a field"},
		{"notes | where kind=", 20, "where kind= needs a value", "quote a value"},
		{"notes | since May", 15, `since needs a date, got "May"`, "YYYY-MM-DD"},
		{"notes | until 2023-13-01", 15, "until needs a date", "YYYY-MM-DD"},
		{"notes | sort", 13, "sort needs a field", "name a field"},
		{"notes | sort when sideways", 19, `unexpected "sideways"`, "separate stages"},
		{"notes | limit 0", 15, "limit needs a whole number from 1 to 200", "narrow with search"},
		{"notes | limit 201", 15, "limit needs a whole number", "1 to 200"},
		{"notes | limit many", 15, "limit needs a whole number", "1 to 200"},
		{"notes | count by", 17, "count by needs a field", "name a field"},
		{"notes | count kind", 15, `unexpected "kind"`, "separate stages"},
		{"notes | group subject", 15, `group needs by, got "subject"`, "group by field"},
		{"recall", 7, "recall needs an id", "recall <id>"},
		{"recall abc | limit 1", 12, "recall takes no stages", "run recall on its own"},
		{`save name=x type=user`, 1, "save needs description=", "save name=<name>"},
		{`save name=x type=user description="d" colour=red`, 39, `unknown key "colour"`, "save takes name, type, description, body, scope"},
		{`save name=x name=y`, 13, "name given twice", "each key once"},
		{`save name x`, 11, "name needs = and a value", "name=value"},
		{`edit name=`, 11, "name= needs a value", "quote a value"},
		{`edit "name"=x`, 6, "expected key=value", "edit name=<name>"},
		{`forget`, 1, "forget needs name=", "forget name=<name>"},
		{`feedback name=x outcome=helped`, 1, "feedback needs quote=", `quote="words`},
		{`define = notes "d"`, 8, "define needs a name", "define <name> = <query>"},
		{`define x notes "d"`, 10, "define x needs = and a query", "define <name> = <query>"},
		{`define x = save name=y "d"`, 12, "a defined query cannot write", "define a read query"},
		{`define x = notes | count`, 25, "define needs a description", "what it returns"},
		{`define x = notes "d" extra`, 22, `unexpected "extra"`, "the description ends the define"},
		{`define x = bogus "d"`, 12, `unknown source "bogus"`, "start with a source"},
	}
	for _, c := range cases {
		_, err := Parse(c.query)
		e, ok := err.(*Error)
		if !ok {
			t.Errorf("Parse(%q) = %v, want an *Error", c.query, err)
			continue
		}
		if e.Pos != c.pos || !strings.Contains(e.Msg, c.msg) || !strings.Contains(e.Hint, c.hint) {
			t.Errorf("Parse(%q) = pos %d msg %q hint %q; want pos %d, %q, %q", c.query, e.Pos, e.Msg, e.Hint, c.pos, c.msg, c.hint)
		}
	}
}

func TestErrorFormat(t *testing.T) {
	_, err := Parse("notes | limit 0")
	if got := err.Error(); !strings.HasPrefix(got, "at character 15: limit needs") || !strings.Contains(got, "; write limit N") {
		t.Fatalf("error %q", got)
	}
	if got := (&Error{Msg: "m", Hint: "h"}).Error(); got != "m; h" {
		t.Fatalf("unpositioned error %q", got)
	}
}

// Positions count characters, not bytes.
func TestErrorPositionCountsCharacters(t *testing.T) {
	_, err := Parse(`notes | search "café" | bogus`)
	if e := err.(*Error); e.Pos != 25 {
		t.Fatalf("pos %d", e.Pos)
	}
}

// propose is a key=value write; without a proposer (no per-model harness) it is refused.
func TestParseProposeAndRefuseWithoutProposer(t *testing.T) {
	q, err := Parse(`propose detector=thin-chunk remedy=prompt-line text="Keep the figures."`)
	if err != nil || q.Write != WritePropose || q.Args["detector"] != "thin-chunk" || q.Args["remedy"] != "prompt-line" || q.Args["text"] != "Keep the figures." {
		t.Fatalf("parsed %+v, %v", q, err)
	}
	if _, err := Parse(`propose detector=thin-chunk`); err == nil || !strings.Contains(err.Error(), "remedy=") {
		t.Fatalf("missing remedy: %v", err)
	}
	if _, err := Parse(`propose detector=thin-chunk remedy=split-chunk model=other`); err == nil {
		t.Fatal("an unknown key parsed")
	}
	saved := curated.Call{Store: curated.New(t.TempDir())}
	r := Run(Env{Saved: &saved}, `propose detector=thin-chunk remedy=split-chunk`)
	if !r.IsError || !strings.Contains(r.Text, "cannot be proposed") {
		t.Fatalf("result %+v", r)
	}
}
