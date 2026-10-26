package harness_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/codestore"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omtool"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// The code tool's acceptance tests (docs/code-tool.md, "Tests"), driven through
// Manager.RunMemory with real namespace children (the test binary re-executed). Every store
// path is under t.TempDir() (session's h.Home).

// codeStore is the code store of h's project.
func codeStore(h *tf.FakeHost) codestore.Store {
	return codestore.Store{Dir: codestore.Dir(h.Home, harness.ProjectRoot(h.Cwd()))}
}

// Empty code, and whitespace, return the manual, and man() returns the same text.
func TestEmptyCodeIsTheManual(t *testing.T) {
	h, _, m := session(t)
	empty := codeOK(t, h, m, "")
	blank := codeOK(t, h, m, " \n\t")
	viaMan := codeOK(t, h, m, "fmt.Print(man())")
	if empty.Text != blank.Text || empty.Text != viaMan.Text || !strings.HasPrefix(empty.Text, "── The memory tool ──") || !empty.Details.Manual {
		t.Fatalf("manual %q\nblank %q\nman() %q", empty.Text[:80], blank.Text[:80], viaMan.Text[:80])
	}
	if !strings.Contains(empty.Text, "── Kept definitions ──\n") {
		t.Fatalf("manual store part: %s", empty.Text)
	}
}

// A helper defined in one call is usable in the next, and so is a variable; a session_start
// reset keeps the definition and drops the variable.
func TestDefinitionsAndVariablesAcrossCalls(t *testing.T) {
	h, _, m := session(t)
	r := codeOK(t, h, m, "func shout(s string) string { return strings.ToUpper(s) + \"!\" }\ny := 41")
	if strings.Join(r.Details.Kept, ",") != "shout" || !strings.Contains(r.Text, "kept: shout") {
		t.Fatalf("keep: %s %+v", r.Text, r.Details)
	}
	if r := codeOK(t, h, m, `shout("hi") + fmt.Sprint(y+1)`); r.Text != "HI!42" {
		t.Fatalf("next call: %q", r.Text)
	}
	m.Namespace().Reset()
	if r := codeOK(t, h, m, `shout("again")`); r.Text != "AGAIN!" {
		t.Fatalf("after session_start: %q", r.Text)
	}
	if r := code(t, h, m, "y"); r.Details.Outcome != omtool.OutcomeCompile {
		t.Fatalf("the variable survived the reset: %+v", r)
	}
	// Strings are read by name and never kept or echoed in the details.
	r = m.RunMemory(h, map[string]any{"code": `om.Str("note")`, "strings": map[string]any{"note": "a `raw` \"quoted\"\nline"}})
	if r.IsError || r.Text != "a `raw` \"quoted\"\nline" || strings.Join(r.Details.Strings, ",") != "note" {
		t.Fatalf("strings: %q %+v", r.Text, r.Details)
	}
	if b, _ := json.Marshal(r.Details); strings.Contains(string(b), "quoted") {
		t.Fatalf("details carry a string's text: %s", b)
	}
}

// A kept definition reaches a fresh namespace in another extension process at the same store
// path; forget removes it from the store, and a forgotten function fails at once in the live
// namespace while its variables survive.
func TestDefinitionPersistsAndForgetRemovesIt(t *testing.T) {
	h, store, m := session(t)
	codeOK(t, h, m, "func twice(n int) int { return 2 * n }\ntype Box struct{ N int }\nv := 7")
	other := harness.NewManager(func(host.ToolHost) (curated.Call, bool) { return curated.Call{Store: store, Source: "s2"}, true })
	t.Cleanup(other.Namespace().Reset)
	if r := codeOK(t, h, other, "twice(21)"); r.Text != "42" {
		t.Fatalf("a fresh process: %q", r.Text)
	}
	if r := codeOK(t, h, other, "kept()"); !strings.Contains(r.Text, "twice\tfunc\tkept") || !strings.Contains(r.Text, "Box\ttype\tkept") {
		t.Fatalf("kept(): %s", r.Text)
	}
	if r := codeOK(t, h, other, `kept("twice")`); !strings.Contains(r.Text, "return 2 * n") {
		t.Fatalf("kept(twice): %s", r.Text)
	}
	r := codeOK(t, h, m, `forget("twice")`)
	if !strings.Contains(r.Text, "forgot twice") {
		t.Fatalf("forget: %s", r.Text)
	}
	if r := code(t, h, m, "twice(1)"); r.Details.Outcome != omtool.OutcomeRuntime || !strings.Contains(r.Text, "was forgotten") {
		t.Fatalf("the forgotten function answered: %+v", r)
	}
	if r := codeOK(t, h, m, "v"); r.Text != "7" {
		t.Fatalf("variables lost: %q", r.Text)
	}
	l, err := codeStore(h).Load()
	if err != nil || len(l.Units) != 1 || l.Units[0].Name != "Box" {
		t.Fatalf("store %+v %v", l.Units, err)
	}
	// A forgotten type restarts the namespace when the call returns.
	if r := codeOK(t, h, m, `forget("Box")`); !strings.Contains(r.Text, "restarted from the kept definitions") {
		t.Fatalf("forget a type: %s", r.Text)
	}
	if r := code(t, h, m, "v"); r.Details.Outcome != omtool.OutcomeCompile {
		t.Fatalf("variables survived a restart: %+v", r)
	}
}

// Reload is isolated: a stored unit that does not compile is a load error, leaves no callable
// symbol of its own or of a dependent, and the others load; a unit the load-time check refuses
// (a go statement) is listed as refused.
func TestReloadIsIsolated(t *testing.T) {
	h, _, m := session(t)
	units := []codestore.Unit{
		{Name: "good", Src: "func good() int { return 1 }"},
		{Name: "bad", Src: `func bad() int { return "not an int" }`},
		{Name: "usesBad", Src: "func usesBad() int { return bad() + 1 }"},
		{Name: "spawn", Src: "func spawn() { go func() {}() }"},
	}
	for i := range units {
		units[i].Digest, units[i].Written = codestore.Digest(units[i].Src), "2026-10-01T00:00:00Z"
	}
	dir := codeStore(h).Dir
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(units)
	if err := os.WriteFile(filepath.Join(dir, "units.json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if r := codeOK(t, h, m, "good()"); r.Text != "1" {
		t.Fatalf("good: %q", r.Text)
	}
	for _, name := range []string{"bad", "usesBad", "spawn"} {
		if r := code(t, h, m, name+"()"); r.Details.Outcome != omtool.OutcomeCompile {
			t.Errorf("%s is callable: %+v", name, r)
		}
	}
	listing := codeOK(t, h, m, "kept()").Text
	for _, want := range []string{"bad", "depends on bad", "go statement"} {
		if !strings.Contains(listing, want) {
			t.Fatalf("kept() lacks %q:\n%s", want, listing)
		}
	}
	if st := m.Namespace().Status(); len(st.LoadErrors) != 2 {
		t.Fatalf("load errors %+v", st.LoadErrors)
	}
}

// Old-grammar text is never executed: a legacy read gets the removal notice and its snippet,
// and no read runs; a legacy write runs nothing; a hint built from words with backticks,
// quotes and newlines parses to the read-call shape only.
func TestLegacyQueriesNeverRun(t *testing.T) {
	h, store, m := session(t)
	for _, q := range []string{`history | search "sourdough"`, "notes | count", "notes"} {
		r := code(t, h, m, q)
		if !r.IsError || !r.Details.Legacy || len(r.Details.Hits) != 0 || len(r.Details.Searches) != 0 || !strings.Contains(r.Text, "query language was removed") {
			t.Fatalf("%s: %s %+v", q, r.Text, r.Details)
		}
	}
	r := code(t, h, m, `history | search "sourdough"`)
	if !strings.Contains(r.Text, "Run this code instead:\nom.History(om.Search(\"sourdough\"))") {
		t.Fatalf("hint: %s", r.Text)
	}
	if r := code(t, h, m, `save name=x type=user description="Tabs."`); !r.Details.Legacy || !strings.Contains(r.Text, "om.Save") {
		t.Fatalf("legacy write: %s", r.Text)
	}
	if _, err := store.Get("x"); err == nil {
		t.Fatal("a legacy write ran")
	}
	r = code(t, h, m, "history | search \"a`b \\\"c\\\" )\"")
	_, snippet, ok := strings.Cut(r.Text, "Run this code instead:\n")
	if !ok || om.CheckSnippet(snippet) != nil {
		t.Fatalf("hint %q: %v", r.Text, om.CheckSnippet(snippet))
	}
	if events, _ := store.ModelEvents("p/m"); len(events) == 0 || events[len(events)-1].Kind != curated.ModelKindCode {
		t.Fatalf("events %+v", events)
	}
}

// describe on an om value lists its methods with docs; a policy refusal names its rule and runs
// nothing; a compile error naming an om symbol comes with its manual section.
func TestDescribeRefusalsAndCompileErrors(t *testing.T) {
	h, _, m := session(t)
	if r := codeOK(t, h, m, "describe(om.Notes())"); !strings.Contains(r.Text, "Len() int") || !strings.Contains(r.Text, "//") {
		t.Fatalf("describe: %s", r.Text)
	}
	if r := code(t, h, m, "go func() {}()"); r.Details.Outcome != omtool.OutcomeCompile || !strings.Contains(r.Text, "go statement") {
		t.Fatalf("policy: %s", r.Text)
	}
	if r := code(t, h, m, "om.Notes(om.Bogus())"); r.Details.Outcome != omtool.OutcomeCompile {
		t.Fatalf("compile: %s", r.Text)
	}
	if r := code(t, h, m, "om.Recall()"); r.Details.Outcome != omtool.OutcomeCompile || !strings.Contains(r.Text, "── Reads ──") {
		t.Fatalf("compile error without the manual section: %s", r.Text)
	}
}

// Curated writes go through staging: a save without a user's quote is staged, one with it is
// live, a quote only in the call's own code confirms nothing; every write lands in the details
// and the user is told of saves and edits. The old pure-read defined query runs as its snippet.
func TestCuratedWritesFromCode(t *testing.T) {
	h, store, m := session(t, tf.RawMessage("u2", "I indent with tabs, always."), tf.RawMessage("u3", "ok"))
	r := codeOK(t, h, m, `fmt.Println(om.Save("tabs", "user", "The user indents with tabs.", om.Quoting("I indent with tabs")))
fmt.Println(om.Save("spaces", "user", "The user likes spaces.", om.Quoting("I like spaces, always")))
fmt.Println(om.Save("staged", "user", "The user may like spaces."))
fmt.Println(om.Edit("tabs", om.SetBody("Said twice.")))`)
	if len(r.Details.Writes) != 3 || r.Details.Writes[0].Op != omtool.WriteSave || r.Details.Writes[1].Name != "staged" || r.Details.Writes[2].Op != omtool.WriteEdit || !strings.Contains(r.Text, "not a user's words") {
		t.Fatalf("writes %+v\n%s", r.Details.Writes, r.Text)
	}
	tabs, err := store.Get("tabs")
	if err != nil || tabs.Staged || tabs.Body != "Said twice." {
		t.Fatalf("tabs %+v %v", tabs, err)
	}
	if _, err := store.Get("spaces"); err == nil {
		t.Fatal("a quote only in the code confirmed a save")
	}
	if staged, err := store.Get("staged"); err != nil || !staged.Staged {
		t.Fatalf("a save without a quote is not staged: %+v %v", staged, err)
	}
	if !hasMessage(h, "tabs") {
		t.Fatalf("notifications %v", h.Messages())
	}
	r = codeOK(t, h, m, `fmt.Println(om.Feedback("tabs", "helped", "I indent with tabs"))
fmt.Println(om.ForgetMemory("staged"))`)
	if len(r.Details.Writes) != 2 {
		t.Fatalf("writes %+v\n%s", r.Details.Writes, r.Text)
	}
	if m, err := store.Get("staged"); err == nil && m.Retired.IsZero() {
		t.Fatal("forgotten memory still live")
	}
	// A retired defined query's snippet, read from om.Retired, runs as code.
	if _, err := store.Define(curated.Definition{Name: "bakes", Query: `notes | search "sourdough"`, Description: "Bakes."}, "s"); err != nil {
		t.Fatal(err)
	}
	r = codeOK(t, h, m, `q := om.Retired(om.Where("name", "bakes")).Rows[0]
fmt.Println(q.Snippet)`)
	snippet := strings.TrimSpace(r.Text)
	if got := codeOK(t, h, m, "strings.Join("+snippet+".IDs(), \" \")").Text; got != obsBake {
		t.Fatalf("snippet %s listed %q", snippet, got)
	}
}

// A save that lands before a runtime panic or a cancel is in the details and the result; the
// cancelled call kills the child, settles at once, says unsaved variables are gone, and the next
// call works with the kept definitions. No code call makes a model request.
func TestWritesBeforeAPanicOrACancelAreReported(t *testing.T) {
	counter := tf.CountRequests(t)
	h, store, m := session(t)
	codeOK(t, h, m, "func keepMe() int { return 5 }\nw := 1")
	r := code(t, h, m, `om.Save("before-panic", "lesson", "Saved before a panic.")
var mp map[string]int
mp["x"] = 1`)
	if r.Details.Outcome != omtool.OutcomeRuntime || len(r.Details.Writes) != 1 || r.Details.Writes[0].Name != "before-panic" {
		t.Fatalf("panic: %+v\n%s", r.Details, r.Text)
	}
	codeEvents := func() int {
		events, err := store.ModelEvents("p/m")
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, e := range events {
			if e.Kind == curated.ModelKindCode {
				n++
			}
		}
		return n
	}
	beforeCancel := codeEvents()
	ctx, cancel := context.WithCancel(context.Background())
	h.Ctx = ctx
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()
	r = code(t, h, m, `om.Save("before-cancel", "lesson", "Saved before a cancel.")
for {}`)
	if took := time.Since(start); took > 3*time.Second {
		t.Fatalf("the cancelled call took %s", took)
	}
	h.Ctx = nil
	// A cancelled call records no usage event.
	if n := codeEvents(); n != beforeCancel {
		t.Fatalf("the cancelled call recorded %d code events", n-beforeCancel)
	}
	if r.Details.Outcome != omtool.OutcomeCancelled || len(r.Details.Writes) != 1 || !strings.Contains(r.Text, "landed before the cancel: save before-cancel") || !strings.Contains(r.Text, "unsaved variables are gone") {
		t.Fatalf("cancel: %+v\n%s", r.Details, r.Text)
	}
	for _, name := range []string{"before-panic", "before-cancel"} {
		if _, err := store.Get(name); err != nil {
			t.Fatalf("%s not saved: %v", name, err)
		}
	}
	if r := codeOK(t, h, m, "keepMe()"); r.Text != "5" {
		t.Fatalf("after the cancel: %q", r.Text)
	}
	if r := code(t, h, m, "w"); r.Details.Outcome != omtool.OutcomeCompile {
		t.Fatalf("a variable survived the cancel: %+v", r)
	}
	if counter.Total() != 0 {
		t.Fatalf("code calls made %d requests", counter.Total())
	}
}

// The results of a code call render in the TUI, and level 0 reads its writes back.
func TestCodeDetailsRoundTrip(t *testing.T) {
	h, _, m := session(t)
	r := codeOK(t, h, m, `om.Save("prefers-tabs", "user", "The user indents with tabs.")`)
	var decoded any
	b, _ := json.Marshal(r.Details)
	_ = json.Unmarshal(b, &decoded)
	d := omtool.DetailsFrom(decoded)
	if d == nil || len(d.Writes) != 1 || d.Writes[0].Description != "The user indents with tabs." {
		t.Fatalf("details %+v", d)
	}
	if got := omtool.FormatRenderedResultForTui(decoded, r.Text, false); !strings.HasPrefix(got, "\n✓ ok · 1 write") {
		t.Fatalf("render %q", got)
	}
}

// /om:status's code lines read the store and the supervisor's record only: they list kept units
// and the namespace's state, and start, kill or replay no child.
func TestCodeStatusStartsNoChild(t *testing.T) {
	h, _, m := session(t)
	lines := strings.Join(m.CodeStatusLines(h), "\n")
	if !strings.Contains(lines, "Kept definitions: 0") || !strings.Contains(lines, "Namespace: not started") || m.Namespace().Status().Starts != 0 {
		t.Fatalf("before any call:\n%s", lines)
	}
	codeOK(t, h, m, "func one() int { return 1 }")
	starts := m.Namespace().Status().Starts
	lines = strings.Join(m.CodeStatusLines(h), "\n")
	if !strings.Contains(lines, "Kept definitions: 1") || !strings.Contains(lines, "Namespace: generation") || m.Namespace().Status().Starts != starts {
		t.Fatalf("after a call:\n%s", lines)
	}
	if !m.Namespace().Status().Live {
		t.Fatal("status killed the child")
	}
}

// forget refuses a name the same call declares, in either order, so the store and the live
// namespace never disagree: a declaration that compiled before the refusal is kept and answers
// in the next call, and one after it never ran.
func TestForgetRefusesANameTheCallDeclares(t *testing.T) {
	h, _, m := session(t)
	codeOK(t, h, m, "func twice(n int) int { return 2 * n }\ntype Box struct{ N int }")
	for _, src := range []string{
		"fmt.Println(forget(\"twice\"))\nfunc twice(n int) int { return 3 * n }",
		"func twice(n int) int { return 3 * n }\nfmt.Println(forget(\"twice\"))",
		"fmt.Println(forget(\"Box\"))\nfunc (b Box) Twice() int { return 2 * b.N }",
	} {
		r := code(t, h, m, src)
		if !strings.Contains(r.Text, "is not forgotten: this call declares") {
			t.Fatalf("%q: %s", src, r.Text)
		}
		if strings.Contains(r.Text, "forgot twice") || strings.Contains(r.Text, "forgot Box") {
			t.Fatalf("%q forgot: %s", src, r.Text)
		}
	}
	if r := codeOK(t, h, m, "twice(2)"); r.Text != "6" {
		t.Fatalf("the live namespace: %q", r.Text)
	}
	l, err := codeStore(h).Load()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, u := range l.Units {
		names = append(names, u.Name)
		if u.Name == "twice" && !strings.Contains(u.Src, "3 * n") {
			t.Fatalf("the store holds %s", u.Src)
		}
	}
	// The refused forget does not stop the code (its failure is its "error: ..." text), so
	// the method the third call declares after it is kept.
	if strings.Join(names, ",") != "Box,Box.Twice,twice" {
		t.Fatalf("store %v", names)
	}
	// Forgotten in a call of its own, twice fails at once and is gone from the store.
	if r := codeOK(t, h, m, `forget("twice")`); !strings.Contains(r.Text, "forgot twice") {
		t.Fatalf("forget: %s", r.Text)
	}
	if r := code(t, h, m, "twice(2)"); !strings.Contains(r.Text, "was forgotten") {
		t.Fatalf("after forget: %s", r.Text)
	}
}

// A type forgotten (the namespace restarts), or gone from the store before a session_start
// reset, can be defined again with another source at once: the policy checks the call against
// the namespace the next child loads, not the one that was killed.
func TestATypeIsRedefinedAfterForgetOrReset(t *testing.T) {
	h, _, m := session(t)
	codeOK(t, h, m, "type Box struct{ N int }")
	if r := codeOK(t, h, m, `forget("Box")`); !strings.Contains(r.Text, "restarted") {
		t.Fatalf("forget: %s", r.Text)
	}
	if r := codeOK(t, h, m, "type Box struct{ N, M int }"); strings.Join(r.Details.Kept, ",") != "Box" {
		t.Fatalf("redefined after forget: %s %+v", r.Text, r.Details)
	}
	// The type the killed namespace defined is not the next one's: Box leaves the store
	// outside this namespace (another process's forget), and after a session_start reset it is
	// defined again differently.
	if _, err := codeStore(h).Forget("Box"); err != nil {
		t.Fatal(err)
	}
	m.Namespace().Reset()
	if r := codeOK(t, h, m, "type Box struct{ S string }\nBox{S: `x`}.S"); !strings.HasPrefix(r.Text, "x\n") || strings.Join(r.Details.Kept, ",") != "Box" {
		t.Fatalf("redefined after reset: %s %+v", r.Text, r.Details)
	}
}

// A store I/O error reaches the code without the store's path (the child never learns it), and
// /om:status, which runs in the extension, shows the whole error.
func TestStoreErrorsNameNoPath(t *testing.T) {
	h, _, m := session(t)
	codeOK(t, h, m, "func one() int { return 1 }")
	dir := codeStore(h).Dir
	// units.json as a directory: reading it fails with an *fs.PathError naming it.
	if err := os.Remove(filepath.Join(dir, "units.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "units.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, src := range []string{"kept()", `forget("one")`, "fmt.Print(man())"} {
		r := code(t, h, m, src)
		if strings.Contains(r.Text, dir) || !strings.Contains(r.Text, "the code store is unreadable") {
			t.Fatalf("%s: %s", src, r.Text)
		}
	}
	if lines := strings.Join(m.CodeStatusLines(h), "\n"); !strings.Contains(lines, dir) {
		t.Fatalf("status leaves the path out:\n%s", lines)
	}
}

// A last (T, error) result shows T alone when the error is nil and "error: <message>" when it
// is not, for the curated and layer writes and om.Recall alike; kept, forget and man return
// their text alone, or "error: <message>"; a fmt printer last shows no byte count.
func TestResultsRenderTheValueOrTheError(t *testing.T) {
	h, _, m := session(t, tf.RawMessage("u2", "I indent with tabs, always."))
	cases := []struct{ src, want string }{
		{`om.Save("tabs", "user", "The user indents with tabs.", om.Quoting("I indent with tabs"))`, "Saved memory tabs [user]: The user indents with tabs."},
		{`om.Edit("tabs", om.SetDescription("The user indents with tabs, always."))`, "Updated memory tabs [user]: The user indents with tabs, always."},
		{`om.Edit("nope", om.SetBody("x"))`, "error: om.Edit: no memory by that name"},
		{"om.Recall(`" + obsBake + "zz`)", "error: om.Recall: nothing called \"" + obsBake + "zz\": no record or reflection id, entry id or saved name on this branch; find ids with " + om.SnippetHistorySearch + " and names with " + om.SnippetSavedSearch},
		{`fmt.Println("printed")`, "printed\n"},
		{"func one() int { return 1 }", "kept: one"},
		{`forget("nope")`, "error: forget: no kept unit is named nope: kept are one"},
	}
	for _, c := range cases {
		r := code(t, h, m, c.src)
		if r.Text != c.want {
			t.Errorf("%s:\n got %q\nwant %q", c.src, r.Text, c.want)
		}
		if strings.HasPrefix(r.Text, ",") || strings.Contains(r.Text, ", nil") {
			t.Errorf("%s: %q", c.src, r.Text)
		}
	}
	if r := code(t, h, m, `kept("one")`); !strings.HasSuffix(r.Text, "\nfunc one() int { return 1 }") {
		t.Errorf("kept(one): %q", r.Text)
	}
	if r := code(t, h, m, "man()"); !strings.HasPrefix(r.Text, "── The memory tool ──") || strings.Contains(r.Text, "error: man") {
		t.Errorf("man(): %.80q", r.Text)
	}
}

// Several top-level expression statements: the last value is shown and one line says how many
// values were discarded and what to do; printers and a single expression add no line.
func TestDiscardedValuesAreNamed(t *testing.T) {
	h, _, m := session(t)
	r := codeOK(t, h, m, "om.Notes(om.Search(`sourdough`)).Len()\nom.Notes(om.Search(`boots`)).Len()\nom.Notes().Len()")
	if r.Text != "2\n(2 expression values were discarded: only the last expression's value is shown; print the others with fmt.Println, or put the wanted value last)" {
		t.Fatalf("%q", r.Text)
	}
	r = codeOK(t, h, m, "om.Notes().Len()\nx := 1\n_ = x")
	if r.Text != "(1 expression value was discarded: only the last expression's value is shown; print the others with fmt.Println, or put the wanted value last)" {
		t.Fatalf("%q", r.Text)
	}
	// A refused seeded call before the last expression is reported, not only counted.
	r = codeOK(t, h, m, "forget(`never-kept`)\nom.Notes().Len()")
	if !strings.Contains(r.Text, "\nA discarded value was an error: forget: ") {
		t.Fatalf("%q", r.Text)
	}
	for _, src := range []string{"fmt.Println(om.Notes().Len())\nom.Notes().Len()", "om.Notes().Len()"} {
		if r := codeOK(t, h, m, src); strings.Contains(r.Text, "discarded") {
			t.Fatalf("%s: %q", src, r.Text)
		}
	}
}

// A fixed snippet form run as it stands is refused before anything runs, naming the
// placeholder and what goes in its place, with no manual section; a placeholder that reaches
// an om call another way (a named string) is refused by the extension too.
func TestPlaceholdersAreRefused(t *testing.T) {
	h, _, m := session(t)
	for _, fixed := range []string{om.SnippetRecall, om.SnippetRecallEntry, om.SnippetRecallName, om.SnippetHistorySearch, om.SnippetSavedSearch, om.SnippetNotesSpan, om.SnippetSave, om.SnippetFeedback, "fmt.Println(" + om.SnippetHistorySearch + ".Len())"} {
		r := code(t, h, m, fixed)
		if !r.IsError || !strings.HasPrefix(r.Text, "Error: nothing ran: `<") || !om.NamesPlaceholder(r.Text) || strings.Contains(r.Text, "──") || len(r.Details.Writes) > 0 || len(r.Details.Searches) > 0 || len(r.Details.Recalled) > 0 {
			t.Errorf("%s: %q %+v", fixed, r.Text, r.Details)
		}
	}
	r := m.RunMemory(h, map[string]any{"code": "om.History(om.Search(om.Str(`w`)))", "strings": map[string]any{"w": om.PlaceholderWords}})
	if !r.IsError || !om.NamesPlaceholder(r.Text) || strings.Contains(r.Text, "──") {
		t.Fatalf("%q", r.Text)
	}
	// Real values in the same forms run.
	if r := codeOK(t, h, m, "om.Notes(om.Search(`sourdough`)).Len()"); r.Text != "1" {
		t.Fatalf("%q", r.Text)
	}
	if r := codeOK(t, h, m, "om.Recall(`"+obsBake+"`)"); !strings.Contains(r.Text, "sourdough") {
		t.Fatalf("%q", r.Text)
	}
}
