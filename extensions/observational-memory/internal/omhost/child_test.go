package omhost_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/codepolicy"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omhost"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/sandbox"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// These tests drive the om API from code in a real namespace child (the test binary
// re-executed; it links internal/sandbox, whose init catches the marker), answered by
// omhost.Call.

func runChild(t *testing.T, s *sandbox.Supervisor, c *omhost.Call, code string, strs map[string]string) sandbox.Result {
	t.Helper()
	p, err := codepolicy.Check(codepolicy.Input{Code: code, Strings: strs})
	if err != nil {
		t.Fatalf("policy: %v\n%s", err, code)
	}
	r := s.Run(context.Background(), sandbox.Request{Program: p, Strings: strs}, c)
	if r.Outcome != sandbox.OutcomeOK {
		t.Fatalf("%s: %+v", code, r)
	}
	return r
}

func newChild(t *testing.T) *sandbox.Supervisor {
	s := sandbox.New(sandbox.Config{})
	t.Cleanup(s.Kill)
	return s
}

// Reads return the child's own copies: changing a string field, a slice element and a map entry
// and reading again shows the original. Code reads its strings, the status figures, the manual
// and an om value's docs.
func TestReadsFromCode(t *testing.T) {
	s := newChild(t)
	rec := &omhost.Record{}
	c := &omhost.Call{Entries: fixture(), Skip: []string{"memory"}, Record: rec, Units: fakeUnits{},
		Info: func() om.StatusInfo {
			return om.StatusInfo{Lines: []string{"Records: 3"}, Models: []om.ModelStatus{{Model: "p/m", Lines: []string{"Profile (1 events)"}}}}
		}}
	code := `n := om.Notes()
n.Rows[0].Content = "changed"
n.Rows[0].SourceEntryIDs[0] = "zz"
if n.Rows[0].Fields == nil { n.Rows[0].Fields = map[string]string{} }
n.Rows[0].Fields["k"] = "v"
again := om.Notes().Rows[0]
fmt.Println(again.Content, again.SourceEntryIDs[0], len(again.Fields))
fmt.Println(om.Str("q"))
fmt.Println(om.Info())
fmt.Println(om.Files().IDs(), om.Notes(om.Kind("fact")).Len(), om.History(om.Search("rye")).Len())
describe(again)`
	r := runChild(t, s, c, code, map[string]string{"q": "a \"quoted\"\nline"})
	want := "User bakes sourdough weekly. u1 0\na \"quoted\"\nline\nRecords: 3\nmodel p/m:\n  Profile (1 events)\n[" + obsFileB + "] 1 2\n"
	if r.Out != want {
		t.Fatalf("printed %q, want %q", r.Out, want)
	}
	if !strings.Contains(r.Val, "Line() string // Line is the record as one summary line") || !strings.Contains(r.Val, "Content string // Content is the record's text") {
		t.Fatalf("describe %s", r.Val)
	}
	if r := runChild(t, s, c, "fmt.Print(man())", nil); r.Out != om.Manual("up (func)") {
		t.Fatalf("man() %q", r.Out[:min(80, len(r.Out))])
	}
	if got := rec.Snapshot(); !slices.Contains(got.Hits, obsBake) || !slices.Contains(got.Searches, "rye") {
		t.Fatalf("record %+v", got)
	}
}

// An old pure-read defined query's snippet, read from om.Retired() and run as code, returns the
// rows the old query returned over the same fixture; a write and a $input query get a reason.
func TestRetiredSnippetRunsAsCode(t *testing.T) {
	// The rows each old query returned over fixture(), recorded from memquery.Run before the
	// query language was deleted (docs/code-tool.md, "Order of work", step 10).
	oldRows := map[string]string{
		"file-states": obsFileB + " " + obsFileA,
		"bakes":       obsBake,
	}
	store := curated.New(t.TempDir())
	for _, d := range []curated.Definition{
		{Name: "file-states", Query: "notes | where kind=file | sort id desc", Description: "File records."},
		{Name: "bakes", Query: `notes | search "sourdough"`, Description: "Bakes."},
		{Name: "by-input", Query: "recall $input", Description: "Recall."},
	} {
		if _, err := store.Define(d, "s0"); err != nil {
			t.Fatal(err)
		}
	}
	retired, err := harness.RetiredQueries(store)
	if err != nil || len(retired) != 3 {
		t.Fatalf("%+v %v", retired, err)
	}
	entries := fixture()
	c := &omhost.Call{Entries: entries, Skip: []string{"memory"}, Retired: func() ([]om.RetiredQuery, error) { return harness.RetiredQueries(store) }}
	s := newChild(t)
	for _, q := range retired {
		if q.Name == "by-input" {
			if q.Snippet != "" || !strings.Contains(q.Reason, "$input") {
				t.Fatalf("%+v", q)
			}
			continue
		}
		listed := runChild(t, s, c, `for _, q := range om.Retired(om.Where("name", "`+q.Name+`")).Rows { fmt.Print(q.Snippet) }`, nil).Out
		if listed != q.Snippet || q.Snippet == "" {
			t.Fatalf("%s: listed %q, snippet %q (%s)", q.Name, listed, q.Snippet, q.Reason)
		}
		got := runChild(t, s, c, "fmt.Print(strings.Join("+q.Snippet+".IDs(), \" \"))", nil).Out
		if want := oldRows[q.Name]; got != want {
			t.Fatalf("%s: code %q, the old query returned %q", q.Name, got, want)
		}
	}
}

// The child's om calls make no model request.
func TestCodeMakesNoRequest(t *testing.T) {
	counter := tf.CountRequests(t)
	s := newChild(t)
	c := &omhost.Call{Entries: fixture()}
	runChild(t, s, c, "om.Notes().Len() + om.History().Len() + om.Reflections().Len()", nil)
	if counter.Total() != 0 {
		t.Fatalf("%d requests", counter.Total())
	}
}
