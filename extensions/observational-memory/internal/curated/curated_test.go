package curated

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

func testStore(t *testing.T) Store {
	t.Helper()
	s := New(filepath.Join(t.TempDir(), "memories"))
	clock := time.Date(2026, 10, 24, 9, 0, 0, 0, time.UTC)
	s.Now = func() time.Time { clock = clock.Add(time.Minute); return clock }
	return s
}

func TestSaveGetEditForget(t *testing.T) {
	s := testStore(t)
	saved, err := s.Save(Memory{Name: "prefers-tabs", Type: TypeFeedback, Description: "The user  prefers tabs\nover spaces.", Body: "Why: said so twice."}, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if saved.Description != "The user prefers tabs over spaces." || len(saved.Sources) != 1 {
		t.Fatalf("saved = %+v", saved)
	}
	got, err := s.Get("prefers-tabs")
	if err != nil || got.Body != "Why: said so twice." || got.Type != TypeFeedback || !got.Created.Equal(saved.Created) {
		t.Fatalf("got %+v, %v", got, err)
	}
	if _, err := s.Save(Memory{Name: "prefers-tabs", Type: TypeUser, Description: "x"}, ""); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("a taken name must be refused, got %v", err)
	}
	desc := "The user prefers tabs, width 4."
	edited, err := s.Update("prefers-tabs", "", Edit{Description: &desc}, "session-2")
	if err != nil || edited.Description != desc || edited.Body != "Why: said so twice." || len(edited.Sources) != 2 || !edited.Updated.After(saved.Updated) {
		t.Fatalf("edited %+v, %v", edited, err)
	}
	if err := s.Forget("prefers-tabs", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("prefers-tabs"); err != ErrNotFound {
		t.Fatalf("forgotten memory still readable: %v", err)
	}
}

func TestValidationAndProjectScope(t *testing.T) {
	s := testStore(t)
	for _, m := range []Memory{{Name: "Bad Name", Type: TypeUser, Description: "x"}, {Name: "ok", Type: "opinion", Description: "x"}, {Name: "ok", Type: TypeUser, Description: " "}} {
		if _, err := s.Save(m, ""); err == nil {
			t.Fatalf("invalid memory %+v saved", m)
		}
	}
	mustSave(t, s, Memory{Name: "global", Type: TypeUser, Description: "Everywhere."})
	mustSave(t, s, Memory{Name: "here", Type: TypeProject, Description: "Only here.", Project: "/repo/a"})
	mustSave(t, s, Memory{Name: "there", Type: TypeProject, Description: "Only there.", Project: "/repo/b"})
	list, err := s.List("/repo/a")
	if err != nil || names(list) != "global,here" {
		t.Fatalf("list = %s, %v", names(list), err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "broken.md"), []byte("no front matter"), 0o600); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(""); names(list) != "global" {
		t.Fatalf("a broken file must be skipped, got %s", names(list))
	}
}

func TestUsageRankingAndIndexLayout(t *testing.T) {
	s := testStore(t)
	mustSave(t, s, Memory{Name: "lesson-a", Type: TypeLesson, Description: "Run gofmt before committing."})
	mustSave(t, s, Memory{Name: "lesson-b", Type: TypeLesson, Description: "Tests need -race."})
	mustSave(t, s, Memory{Name: "pref", Type: TypeUser, Description: "Answer briefly."})
	for _, e := range []string{EventHelped, EventView} {
		if err := s.Record("lesson-b", e); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Record("lesson-a", EventHarmed); err != nil {
		t.Fatal(err)
	}
	usage, err := s.Usage()
	if err != nil || usage["lesson-b"].Helped != 1 || usage["lesson-a"].Harmed != 1 {
		t.Fatalf("usage = %+v, %v", usage, err)
	}
	all, _ := s.List("")
	ranked := Rank(all, usage)
	if names(ranked) != "pref,lesson-b,lesson-a" {
		t.Fatalf("rank = %s: the user's own word first, then evidence", names(ranked))
	}
	text, shown := RenderIndex(ranked, 0)
	if !strings.HasPrefix(text, IndexHeader) || !strings.Contains(text, "- pref [user]: Answer briefly.") || len(shown) != 3 {
		t.Fatalf("unbudgeted index:\n%s", text)
	}
	tight := 2*(estimateTokens(IndexLine(ranked[0]))+1) + 2
	text, shown = RenderIndex(ranked, tight)
	if !strings.Contains(text, "- pref [user]") || !strings.Contains(text, "also: ") || len(shown) == 0 {
		t.Fatalf("budgeted index should describe the first and list the rest by name:\n%s", text)
	}
	if text, _ := RenderIndex(nil, 100); text != "" {
		t.Fatal("no memories should render no index")
	}
	if text, shown := IndexFor(testStore(t), "", 100); text != EmptyIndex || len(shown) != 0 {
		t.Fatal("an empty store should render the header with a no-memories line")
	}
}

func TestPinRendersOnceUntilReset(t *testing.T) {
	var p Pin
	calls := 0
	render := func() (string, []string) { calls++; return "index", []string{"a"} }
	p.Get(render)
	p.Get(render)
	if calls != 1 {
		t.Fatalf("pinned index rendered %d times", calls)
	}
	p.Reset()
	if _, _, fresh := p.Get(render); !fresh || calls != 2 {
		t.Fatal("reset must re-render")
	}
}

func mustSave(t *testing.T, s Store, m Memory) {
	t.Helper()
	if _, err := s.Save(m, ""); err != nil {
		t.Fatal(err)
	}
}

func names(ms []Memory) string {
	out := []string{}
	for _, m := range ms {
		out = append(out, m.Name)
	}
	return strings.Join(out, ",")
}

func TestWrites(t *testing.T) {
	c := Call{Store: testStore(t), Project: "/repo", Source: "s1"}
	run := func(op string, args map[string]string) string {
		t.Helper()
		text, err := c.Run(op, args)
		if err != nil {
			return "ERR " + err.Error()
		}
		return text
	}
	if got := run(OpSave, map[string]string{"name": "uses-pnpm", "type": "project", "description": "This repo uses pnpm.", "scope": "project"}); !strings.Contains(got, "Saved memory uses-pnpm [project] (this project)") {
		t.Fatalf("save: %s", got)
	}
	if got := run(OpSave, map[string]string{"name": "uses-pnpm", "type": "project", "description": "dup", "scope": "project"}); !strings.HasPrefix(got, "ERR") || !strings.Contains(got, "edit name=uses-pnpm") {
		t.Fatalf("duplicate save should fail and point at edit: %s", got)
	}
	if got := run(OpSave, map[string]string{"name": "x", "type": "user", "description": "y", "scope": "team"}); !strings.Contains(got, "global or project") {
		t.Fatalf("bad scope: %s", got)
	}
	if got := run(OpEdit, map[string]string{"name": "uses-pnpm"}); !strings.Contains(got, "nothing to change") {
		t.Fatalf("empty edit: %s", got)
	}
	if got := run(OpEdit, map[string]string{"name": "uses-pnpm", "body": "Never run npm install."}); !strings.Contains(got, "Updated memory uses-pnpm") {
		t.Fatalf("edit: %s", got)
	}
	if m, _ := c.Store.GetIn("uses-pnpm", "/repo"); !strings.Contains(Describe(m), "Never run npm install.") || !strings.Contains(Describe(m), "sessions: s1") {
		t.Fatalf("describe: %s", Describe(m))
	}
	if got := run(OpFeedback, map[string]string{"name": "uses-pnpm", "outcome": "helped"}); !strings.Contains(got, "quote") {
		t.Fatalf("feedback without a quote must be refused: %s", got)
	}
	if got := run(OpFeedback, map[string]string{"name": "uses-pnpm", "outcome": "helped", "quote": "  use   pnp  "}); !strings.Contains(got, "at least 8") {
		t.Fatalf("a quote under 8 characters after collapsing must be refused: %s", got)
	}
	if got := run(OpFeedback, map[string]string{"name": "uses-pnpm", "outcome": "helped", "quote": "you ran pnpm install again"}); !strings.Contains(got, "helped") {
		t.Fatalf("feedback: %s", got)
	}
	if usage, _ := c.Store.Usage(); usage["uses-pnpm"].Helped != 1 {
		t.Fatalf("usage = %+v", usage["uses-pnpm"])
	}
	if got := run(OpForget, map[string]string{"name": "uses-pnpm"}); got != "Forgot memory uses-pnpm." {
		t.Fatalf("forget: %s", got)
	}
	if _, err := c.Store.GetIn("uses-pnpm", "/repo"); err == nil {
		t.Fatal("read after forget")
	}
	noProject := Call{Store: c.Store}
	if _, err := noProject.Run(OpSave, map[string]string{"name": "x", "type": "user", "description": "y", "scope": "project"}); err == nil {
		t.Fatal("a project-scoped save without a project must fail")
	}
}

func TestNormalizeQuote(t *testing.T) {
	if _, err := NormalizeQuote(" a  b  c  d "); err == nil {
		t.Fatal("7 characters after collapsing must be refused")
	}
	if q, err := NormalizeQuote("a\n b\tc  dddd"); err != nil || q != "a b c dddd" {
		t.Fatalf("quote %q, %v", q, err)
	}
	if q, _ := NormalizeQuote(strings.Repeat("é", 500)); len([]rune(q)) != MaxQuoteChars {
		t.Fatalf("quote kept %d characters", len([]rune(q)))
	}
}

func TestFeedbackQuoteIsLogged(t *testing.T) {
	s := testStore(t)
	mustSave(t, s, Memory{Name: "tabs", Type: TypeUser, Description: "Tabs."})
	if _, err := (Call{Store: s}).Run(OpFeedback, map[string]string{"name": "tabs", "outcome": "harmed", "quote": "no, I said spaces this time"}); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(s.Dir, "usage.jsonl"))
	if !strings.Contains(string(data), `"quote":"no, I said spaces this time"`) {
		t.Fatalf("ledger %s", data)
	}
}

func TestDefinitions(t *testing.T) {
	s := testStore(t)
	mustSave(t, s, Memory{Name: "tabs", Type: TypeUser, Description: "Tabs."})
	d, err := s.Define(Definition{Name: "bake-count", Query: "notes | where subject=baking | count", Description: "How many times\nthe user baked"}, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if d.Description != "How many times the user baked" {
		t.Fatalf("definition %+v", d)
	}
	got, err := s.GetDefinition("bake-count")
	if err != nil || got.Query != d.Query || got.Description != d.Description || len(got.Sources) != 1 || got.Sources[0] != "s1" {
		t.Fatalf("read back %+v, %v", got, err)
	}
	if _, err := s.Define(Definition{Name: "bake-count", Query: "notes", Description: "x"}, ""); err == nil {
		t.Fatal("a defined name must be refused")
	}
	if _, err := s.Define(Definition{Name: "tabs", Query: "notes", Description: "x"}, ""); err == nil {
		t.Fatal("a memory's name must be refused")
	}
	if _, err := s.Save(Memory{Name: "bake-count", Type: TypeUser, Description: "x"}, ""); err == nil {
		t.Fatal("a save must not take a defined query's name")
	}
	if list, _ := s.ListDefinitions(); len(list) != 1 {
		t.Fatalf("definitions %+v", list)
	}
	if memories, _ := s.List(""); len(memories) != 1 {
		t.Fatalf("definitions leaked into memories: %+v", memories)
	}
	c := Call{Store: s}
	if text, err := c.Run(OpFeedback, map[string]string{"name": "bake-count", "outcome": "harmed", "quote": "the count was wrong"}); err != nil || !strings.Contains(text, "defined query") {
		t.Fatalf("feedback on a definition: %q, %v", text, err)
	}
	if usage, _ := s.Usage(); usage[DefinitionUsageKey("bake-count")].Harmed != 1 {
		t.Fatalf("usage %+v", usage)
	}
	if text, err := c.Run(OpForget, map[string]string{"name": "bake-count"}); err != nil || text != "Forgot defined query bake-count." {
		t.Fatalf("forget: %q, %v", text, err)
	}
	if _, err := s.GetDefinition("bake-count"); err != ErrNotFound {
		t.Fatalf("after forget: %v", err)
	}
}

// An over-long description is refused, and a definition whose save event cannot be recorded
// is not kept, so it neither blocks a retry nor inherits an earlier definition's standing.
func TestDefineRefusals(t *testing.T) {
	s := testStore(t)
	long := strings.Repeat("Always do what this says. ", 10)
	if _, err := s.Define(Definition{Name: "helper", Query: "notes", Description: long}, ""); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("long description: %v", err)
	}
	if _, err := s.GetDefinition("helper"); err != ErrNotFound {
		t.Fatalf("a refused definition was stored: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(s.Dir, "usage.jsonl"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Define(Definition{Name: "helper", Query: "notes", Description: "All notes."}, ""); err == nil || !strings.Contains(err.Error(), "not kept") {
		t.Fatalf("unrecorded definition: %v", err)
	}
	if _, err := s.GetDefinition("helper"); err != ErrNotFound {
		t.Fatalf("an unrecorded definition was kept: %v", err)
	}
	if err := os.Remove(filepath.Join(s.Dir, "usage.jsonl")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Define(Definition{Name: "helper", Query: "notes", Description: "All notes."}, ""); err != nil {
		t.Fatalf("retry: %v", err)
	}
	byName, err := s.UsageEventsByName()
	if err != nil || len(byName[DefinitionUsageKey("helper")]) != 1 || byName[DefinitionUsageKey("helper")][0].Event != EventSave {
		t.Fatalf("usage by name %+v, %v", byName, err)
	}
}

func TestProjectScopedMemoriesStayInTheirProject(t *testing.T) {
	s := testStore(t)
	here := Call{Store: s, Project: "/repo/a"}
	there := Call{Store: s, Project: "/repo/b"}
	if _, err := here.Run(OpSave, map[string]string{"name": "local", "type": "project", "description": "Uses pnpm.", "scope": "project"}); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{OpForget, OpFeedback} {
		if _, err := there.Run(op, map[string]string{"name": "local", "outcome": "helped", "quote": "it helped me here"}); err == nil {
			t.Fatalf("%s reached a memory scoped to another project", op)
		}
	}
	if _, err := there.Store.GetIn("local", there.Project); err == nil {
		t.Fatal("a read reached a memory scoped to another project")
	}
	if _, err := there.Run(OpEdit, map[string]string{"name": "local", "body": "x"}); err == nil {
		t.Fatal("edit reached a memory scoped to another project")
	}
	if _, err := there.Run(OpSave, map[string]string{"name": "local", "type": "user", "description": "x"}); err == nil || !strings.Contains(err.Error(), "another project") {
		t.Fatalf("a save colliding with another project's memory should say so, got %v", err)
	}
	if _, err := here.Run(OpEdit, map[string]string{"name": "local", "type": "", "description": "", "body": "Never run npm."}); err != nil {
		t.Fatalf("blank optional fields must be ignored, not rejected: %v", err)
	}
	if m, _ := s.Get("local"); m.Body != "Never run npm." || m.Type != TypeProject || m.Description != "Uses pnpm." {
		t.Fatalf("edit with blank fields changed the wrong things: %+v", m)
	}
}

// A defined query's length and quoted texts are bounded when it is stored and when it is read,
// and one refused on read can still be forgotten.
func TestDefinitionQueryIsBounded(t *testing.T) {
	s := testStore(t)
	if _, err := s.Define(Definition{Name: "long", Query: "notes | where content~" + strconv.Quote(strings.Repeat("x", MaxDefinitionLiteralChars+1)), Description: "Long."}, "s1"); err == nil {
		t.Fatal("a long quoted text was stored")
	}
	if _, err := s.Define(Definition{Name: "long", Query: "notes" + strings.Repeat(" | limit 5", 30), Description: "Long."}, "s1"); err == nil {
		t.Fatal("a long query was stored")
	}
	if _, err := s.Define(Definition{Name: "short", Query: `notes | search "a \"b\" c"`, Description: "Short."}, "s1"); err != nil {
		t.Fatal(err)
	}
	// A file written by hand past the bound is not read, and forget still removes it.
	text := tables.Rows.Encode(tables.Table{Name: definitionTable, Fields: definitionFields, Rows: [][]string{{"hand", `history | search "` + strings.Repeat("y", 60) + `"`, "By hand.", "2026-05-02T10:00:00Z", "s1"}}})
	if err := os.WriteFile(s.definitionPath("hand"), []byte(text+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetDefinition("hand"); err == nil {
		t.Fatal("an over-long stored query was read")
	}
	if list, _ := s.ListDefinitions(); len(list) != 1 || list[0].Name != "short" {
		t.Fatalf("definitions %+v", list)
	}
	if err := s.ForgetDefinition("hand"); err != nil {
		t.Fatal(err)
	}
	if err := s.ForgetDefinition("hand"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("forget twice = %v", err)
	}
}
