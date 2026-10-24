package curated

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	edited, err := s.Update("prefers-tabs", Edit{Description: &desc}, "session-2")
	if err != nil || edited.Description != desc || edited.Body != "Why: said so twice." || len(edited.Sources) != 2 || !edited.Updated.After(saved.Updated) {
		t.Fatalf("edited %+v, %v", edited, err)
	}
	if err := s.Forget("prefers-tabs"); err != nil {
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
	found, _ := s.Search("/repo/a", "ONLY here")
	if names(found) != "here" {
		t.Fatalf("search = %s", names(found))
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

func TestToolCalls(t *testing.T) {
	c := Call{Store: testStore(t), Project: "/repo", Source: "s1"}
	run := func(tool string, params map[string]any) string {
		t.Helper()
		text, err := c.Run(tool, params)
		if err != nil {
			return "ERR " + err.Error()
		}
		return text
	}
	if got := run(ToolSave, map[string]any{"name": "uses-pnpm", "type": "project", "description": "This repo uses pnpm.", "scope": "project"}); !strings.Contains(got, "Saved memory uses-pnpm [project] (this project)") {
		t.Fatalf("save: %s", got)
	}
	if got := run(ToolSave, map[string]any{"name": "uses-pnpm", "type": "project", "description": "dup"}); !strings.HasPrefix(got, "ERR") {
		t.Fatalf("duplicate save should fail: %s", got)
	}
	if got := run(ToolEdit, map[string]any{"name": "uses-pnpm"}); !strings.Contains(got, "nothing to change") {
		t.Fatalf("empty edit: %s", got)
	}
	if got := run(ToolEdit, map[string]any{"name": "uses-pnpm", "body": "Never run npm install."}); !strings.Contains(got, "Updated memory uses-pnpm") {
		t.Fatalf("edit: %s", got)
	}
	if got := run(ToolRead, map[string]any{"name": "uses-pnpm"}); !strings.Contains(got, "Never run npm install.") || !strings.Contains(got, "sessions: s1") {
		t.Fatalf("read: %s", got)
	}
	if got := run(ToolSearch, map[string]any{"query": "npm"}); !strings.Contains(got, "uses-pnpm") {
		t.Fatalf("search: %s", got)
	}
	if got := run(ToolFeedback, map[string]any{"name": "uses-pnpm", "outcome": "helped"}); !strings.Contains(got, "helped") {
		t.Fatalf("feedback: %s", got)
	}
	if usage, _ := c.Store.Usage(); usage["uses-pnpm"].Helped != 1 || usage["uses-pnpm"].Views != 1 {
		t.Fatalf("usage = %+v", usage["uses-pnpm"])
	}
	if got := run(ToolForget, map[string]any{"name": "uses-pnpm"}); got != "Forgot memory uses-pnpm." {
		t.Fatalf("forget: %s", got)
	}
	if got := run(ToolRead, map[string]any{"name": "uses-pnpm"}); !strings.HasPrefix(got, "ERR") {
		t.Fatalf("read after forget: %s", got)
	}
	noProject := Call{Store: c.Store}
	if _, err := noProject.Run(ToolSave, map[string]any{"name": "x", "type": "user", "description": "y", "scope": "project"}); err == nil {
		t.Fatal("a project-scoped save without a project must fail")
	}
}
