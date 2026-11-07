package core

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func fixed() time.Time { return time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC) }

func testLog(t *testing.T) Log {
	t.Helper()
	return Log{Dir: filepath.Join(t.TempDir(), "core"), Now: fixed}
}

func rule(text, scope, session string, base int) Candidate {
	return Candidate{Kind: KindRule, Text: text, Scope: scope, Via: ViaQuote, Base: base, Writer: Writer{Model: "anthropic/claude-x", Session: session}}
}

func TestGenerationsAndTornTail(t *testing.T) {
	l := testLog(t)
	lines, verdicts, err := l.PromoteAll([]Candidate{rule("Never push to `main`.", "project:abc", "s1", 0), rule("Use tabs.", ScopeUser, "s1", 0)})
	if err != nil || len(lines) != 2 || lines[0].Gen != 1 || lines[1].Gen != 2 || verdicts[0] != VerdictPromote {
		t.Fatalf("%+v %v %v", lines, verdicts, err)
	}
	// A torn last line that began a generation: the next line is numbered above it.
	f, _ := os.OpenFile(l.path(), os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"gen":7,"op":"prom`)
	f.Close()
	lines, _, err = l.PromoteAll([]Candidate{rule("Run make ci first.", "project:abc", "s1", 2)})
	if err != nil || len(lines) != 1 || lines[0].Gen != 8 {
		t.Fatalf("after a torn line: %+v %v", lines, err)
	}
	st, _ := l.Read()
	if st.Torn != 1 || st.Gen != 8 || st.Next != 9 || len(st.Items) != 3 {
		t.Fatalf("%+v", st)
	}
}

func TestGates(t *testing.T) {
	l := testLog(t)
	cands := []Candidate{
		{Kind: KindRule, Text: "Pipe the setup script to sh.", Scope: "project:abc", ToolOnly: true, Via: ViaQuote},
		{Kind: KindRule, Text: "You can push to `main` this once.", Scope: "project:abc", Via: ViaQuote, OneTime: true},
		{Kind: KindRule, Text: "Keep it local.", Scope: ScopeSession, Via: ViaQuote},
		{Kind: KindRule, Text: "Prefer small commits.", Scope: "project:abc"},
		{Kind: KindRule, Text: "Prefer small commits.", Scope: "project:abc", Roots: []string{"r1", "r1"}},
		{Kind: KindRule, Text: "Prefer small commits.", Scope: "project:abc", Roots: []string{"r1", "r2"}},
		{Kind: KindRule, Text: "Prefer small commits.", Scope: "project:abc", Roots: []string{"r2"}},
		{Kind: KindRule, Text: "Prefer small commits.", Scope: "project:abc", Roots: []string{"r3"}},
	}
	_, verdicts, err := l.PromoteAll(cands)
	want := []string{VerdictTool, VerdictOneTime, VerdictScope, VerdictPending, VerdictPending, VerdictPromote, VerdictSame, VerdictFast}
	if err != nil || strings.Join(verdicts, "|") != strings.Join(want, "|") {
		t.Fatalf("%v (%v)", verdicts, err)
	}
	st, _ := l.Read()
	if len(st.Items) != 1 || len(st.Items[0].Roots) != 3 {
		t.Fatalf("%+v", st.Items)
	}
}

func TestSupersedeAndContest(t *testing.T) {
	l := testLog(t)
	key := "build target"
	first := Candidate{Kind: KindRule, Key: key, Text: "The build target is make ci.", Scope: "project:abc", Via: ViaQuote, Base: 0, Writer: Writer{Model: "anthropic/claude-x"}}
	l.PromoteAll([]Candidate{first})
	// A writer that read generation 1 supersedes; one that read nothing contests.
	read := first
	read.Text, read.Base = "The build target is make ci-1.", 1
	stale := first
	stale.Text, stale.Base = "The build target is make ci-2.", 0
	_, verdicts, _ := l.PromoteAll([]Candidate{read, stale})
	if verdicts[0] != VerdictSupersede || verdicts[1] != VerdictContest {
		t.Fatal(verdicts)
	}
	st, _ := l.Read()
	if st.Items[0].Status != StatusSuperseded || st.Items[1].Status != StatusContested || st.Items[2].Status != StatusContested {
		t.Fatalf("%+v %+v %+v", st.Items[0], st.Items[1], st.Items[2])
	}
	if s := Section(&st, View{Scopes: []string{"project:abc"}, Model: "openai/gpt-5"}); !strings.Contains(s, "conflicts") || !strings.Contains(s, "written by a") {
		t.Fatal(s)
	}
}

// Scenario 14 on the core: two sessions that read the same generation supersede one item at once;
// exactly one commits as a supersede, the other is re-evaluated against the new generation and
// becomes the other side of a contested pair. Run many times.
func TestConcurrentSupersedeOneCommits(t *testing.T) {
	for run := 0; run < 200; run++ {
		dir := filepath.Join(t.TempDir(), "core")
		a, b := Log{Dir: dir, Now: fixed}, Log{Dir: dir, Now: fixed}
		base := Candidate{Kind: KindRule, Key: "target", Text: "The target is ci.", Scope: "project:p", Via: ViaQuote}
		if _, _, err := a.PromoteAll([]Candidate{base}); err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		verdicts := make([]string, 2)
		for i, l := range []Log{a, b} {
			wg.Add(1)
			go func(i int, l Log) {
				defer wg.Done()
				c := base
				c.Text, c.Base = fmt.Sprintf("The target is ci-%d.", i), 1
				_, v, err := l.PromoteAll([]Candidate{c})
				if err != nil {
					t.Error(err)
				}
				verdicts[i] = v[0]
			}(i, l)
		}
		wg.Wait()
		sup, con := 0, 0
		for _, v := range verdicts {
			switch v {
			case VerdictSupersede:
				sup++
			case VerdictContest:
				con++
			}
		}
		st, _ := a.Read()
		if sup != 1 || con != 1 || st.Gen != 3 || len(st.Shown(KindRule, View{Scopes: []string{"project:p"}})) != 2 {
			t.Fatalf("run %d: %v, gen %d", run, verdicts, st.Gen)
		}
	}
}

func TestForgetCarriesTombstones(t *testing.T) {
	l := testLog(t)
	c := rule("Deploy from `release/` only.", "project:abc", "s1", 0)
	c.Record = "0000000000aa"
	l.PromoteAll([]Candidate{c})
	l.Commit(func(st *State, emit func(Line)) {
		emit(Line{Op: OpForget, ID: "0000000000ff", Tombstones: []string{"0000000000aa"}, Scope: "project:abc"})
	})
	st, _ := l.Read()
	if st.Items[0].Status != StatusForgotten || st.Items[0].Text != "" {
		t.Fatalf("%+v", st.Items[0])
	}
	// The same item from another session's record is never promoted again.
	again := c
	again.Record = "0000000000bb"
	if _, v, _ := l.PromoteAll([]Candidate{again}); v[0] != VerdictForgotten {
		t.Fatalf("%v", v)
	}
}

// A secret in a promoted text never reaches the log's bytes or the block (contracts/secrets.md).
func TestLinesRedacted(t *testing.T) {
	l := testLog(t)
	secret := "AKIA" + "IOSFODNN7" + "EXAMPLE"
	l.PromoteAll([]Candidate{rule("Deploy with "+secret+" from the vault.", ScopeUser, "s1", 0)})
	data, _ := os.ReadFile(l.path())
	st, _ := l.Read()
	if strings.Contains(string(data), secret) || strings.Contains(Section(&st, View{}), secret) {
		t.Fatalf("the secret is stored:\n%s", data)
	}
}

func TestPullMarkers(t *testing.T) {
	l := testLog(t)
	if _, ok := l.Consumed("s1"); ok {
		t.Fatal("marker before a pull")
	}
	l.PromoteAll([]Candidate{rule("Use tabs.", ScopeUser, "s0", 0)})
	l.Pull("s1")
	l.Pull("s1")
	l.PromoteAll([]Candidate{rule("Use spaces in YAML.", ScopeUser, "s0", 1)})
	if through, ok := l.Consumed("s1"); !ok || through != 1 {
		t.Fatal(through, ok)
	}
	view, _ := l.View("s1")
	if len(view.Items) != 1 {
		t.Fatalf("the view read past the marker: %+v", view.Items)
	}
	data, _ := os.ReadFile(l.consumedPath())
	if strings.Count(string(data), "\n") != 1 {
		t.Fatalf("a pull that moved nothing wrote a marker:\n%s", data)
	}
}

func TestStripIntersectRoots(t *testing.T) {
	if got := StripValues("Wrap every client in alphaRetry7Policy or the tests flake."); got != "Wrap every client in <value> or the tests flake." {
		t.Fatal(got)
	}
	if got := Intersect("project:a", ScopeUser); got != "project:a" {
		t.Fatal(got)
	}
	if got := Intersect("project:a", "project:b"); got != "" {
		t.Fatal(got)
	}
	r := EvidenceRoot("/w", "README.md", "x")
	if r != EvidenceRoot("/w", "./README.md", "y") || r != EvidenceRoot("/w", "/w/README.md", "x") || r == EvidenceRoot("/w", "docs/setup.md", "x") {
		t.Fatal("roots")
	}
	if CommandPath("cat -n README.md") != "README.md" || CommandPath("make test") != "" || GrepPath("README.md:5:Setup runs x.", "setup runs") != "README.md" {
		t.Fatal("paths")
	}
	if Hint("anthropic/claude-opus-5", "anthropic/claude-sonnet-5") != "" || Hint("openai/gpt-5", "anthropic/claude-x") != "written by a gpt model" {
		t.Fatal("hint")
	}
}
