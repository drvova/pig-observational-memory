package memeval

// Phase 4's project-knowledge scenarios (docs/brain-memory.md, decision 20): real git
// repositories in temporary directories, read by the adapter's gitrepo as the PiG adapter and
// the om command read them, the engine asking its HEAD structure view.

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/gitrepo"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// gitRepo is a repository in a temporary directory with files committed once.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "main")
	commitFiles(t, dir, "first", files)
	return dir
}

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// commitFiles writes files ("" removes one) and commits them.
func commitFiles(t *testing.T, dir, message string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		p := filepath.Join(dir, name)
		if text == "" {
			git(t, dir, "rm", "-q", name)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", message)
}

// inRepo sets a host up as the adapter does: the repository read with gitrepo, and the view
// answered by gitrepo.Structure. It returns the repository read.
func inRepo(t *testing.T, dir string) (host.Repo, func(*testfix.FakeHost)) {
	t.Helper()
	repo, ok, err := gitrepo.Read(dir)
	if err != nil || !ok {
		t.Fatalf("gitrepo.Read: %v %v", ok, err)
	}
	return repo, func(h *testfix.FakeHost) {
		h.RepoInfo = &repo
		h.StructureFunc = func(q host.StructureQuery) (host.StructureAnswer, bool, error) {
			return gitrepo.Structure(dir, repo, q)
		}
	}
}

// laterSession is entries continued by more turns and a last message to keep, for a second
// compaction.
func laterSession(entries []session.Entry, turns ...Turn) ([]session.Entry, string) {
	out := append([]session.Entry{}, entries...)
	turns = append(append([]Turn{}, turns...), Filler(11, 8)...)
	for i, turn := range turns {
		out = append(out, TurnEntries(turn, 100+i)...)
	}
	cut := UserID(100 + len(turns))
	return append(out, testfix.RawMessage(cut, "Carry on.")), cut
}

// rowWith is the record row whose text holds needle (the intent rows quote the requests).
func rowWith(summary, needle string) (Row, bool) {
	for _, r := range SummaryRows(summary) {
		if r.Table != "intent" && r.Table != "request" && strings.Contains(r.Text, needle) {
			return r, true
		}
	}
	return Row{}, false
}

// Scenario 7: session 1 records parseConfig; the repository renames it; session 2 prints the
// record "gone at <sha>" at the commit that renamed it, and never deletes it.
func TestScenario07RenameMarksStale(t *testing.T) {
	dir := gitRepo(t, map[string]string{"config/load.go": "package config\n\nfunc parseConfig() {}\n", "README.md": "readme\n"})
	_, setup := inRepo(t, dir)
	entries, cut := turnsBranch(say("`parseConfig` in `config/load.go` reads the YAML."))
	first, after := compactOnceWith(t, entries, cut, setup)
	row, ok := rowWith(first.Summary, "parseConfig")
	if !ok || strings.Contains(row.Text, "gone at") || strings.Contains(row.Text, "changed since") {
		t.Fatalf("session 1: %+v\n%s", row, first.Summary)
	}

	commitFiles(t, dir, "rename", map[string]string{"config/load.go": "", "config/config.go": "package config\n\nfunc LoadConfig() {}\n"})
	repo2, setup2 := inRepo(t, dir)
	later, cut2 := laterSession(after, say("Let's look at the logging next."))
	second, final := compactOnceWith(t, later, cut2, setup2)
	row, ok = rowWith(second.Summary, "parseConfig")
	if !ok || !strings.Contains(row.Text, "(gone at "+repo2.Head[:7]+")") {
		t.Fatalf("session 2: %+v\n%s", row, second.Summary)
	}
	if !strings.Contains(second.Summary, "- fresh: ") {
		t.Fatalf("no freshness instructions:\n%s", second.Summary)
	}
	// Never deleted: the record stands in the ledger and its text is unchanged.
	standing := false
	for _, o := range ledger.FoldLedger(final, "").ActiveObservations {
		standing = standing || strings.Contains(o.Content, "parseConfig") && !strings.Contains(o.Content, "gone at")
	}
	if !standing {
		t.Fatal("the record left the ledger")
	}
}

// Scenario 26: a project record whose file changed at HEAD prints "changed since <sha>,
// unverified", one whose path is gone "gone at <sha>", and no line of the structure view is
// stored in the ledger.
func TestScenario26ProjectRecordStaleAgainstHEAD(t *testing.T) {
	dir := gitRepo(t, map[string]string{
		"src/net/retry.go": "package net\n\nfunc retry() {}\n",
		"docs/old.md":      "old\n",
		"Makefile":         "test:\n\tgo test ./...\n",
	})
	repo1, setup := inRepo(t, dir)
	entries, cut := turnsBranch(
		say("We decided to keep the retries in `src/net/retry.go` because the client must stay small."),
		say("We decided to write the design notes in `docs/old.md` because the wiki is read-only."),
	)
	_, after := compactOnceWith(t, entries, cut, setup)
	stamped := 0
	for _, o := range ledger.FoldLedger(after, "").ActiveObservations {
		if o.Frame.BodyHash != "" {
			stamped++
		}
	}
	if stamped != 2 {
		t.Fatalf("%d records carry a body hash, want both", stamped)
	}

	commitFiles(t, dir, "edit", map[string]string{"src/net/retry.go": "package net\n\nfunc retry(n int) {}\n", "docs/old.md": ""})
	repo2, setup2 := inRepo(t, dir)
	later, cut2 := laterSession(after, say("Now the logging."))
	second, final := compactOnceWith(t, later, cut2, setup2)
	if row, ok := rowWith(second.Summary, "src/net/retry.go"); !ok || !strings.Contains(row.Text, "(changed since "+repo1.Head[:7]+", unverified)") {
		t.Fatalf("changed: %+v\n%s", row, second.Summary)
	}
	if row, ok := rowWith(second.Summary, "docs/old.md"); !ok || !strings.Contains(row.Text, "(gone at "+repo2.Head[:7]+")") {
		t.Fatalf("gone: %+v\n%s", row, second.Summary)
	}
	// Level 1 re-checks the changed claim against its diff inside its one request: confirmed,
	// the record stands checked at HEAD and is no longer marked.
	var retry string
	for _, o := range ledger.FoldLedger(final, "").ActiveObservations {
		if strings.Contains(o.Content, "src/net/retry.go") {
			retry = o.ID
		}
	}
	var requests *[]map[string]any
	rechecked, checked := compactWithLevel(t, later, cut2, func(h *testfix.FakeHost) {
		setup2(h)
		h.ModelsFake.Streams.Replies = []map[string]any{testfix.AssistantToolCall("call-1", "consolidate_memory", map[string]any{
			"stale": []any{map[string]any{"id": retry, "verdict": "confirm"}},
		})}
		requests = &h.ModelsFake.Streams.Requests
	}, true)
	if requests == nil || len(*requests) != 1 {
		t.Fatalf("level 1 made %v requests", requests)
	}
	prompt, _ := json.Marshal((*requests)[0])
	if !strings.Contains(string(prompt), "POSSIBLY STALE") || !strings.Contains(string(prompt), "+func retry(n int) {}") {
		t.Fatalf("the request shows no possibly stale record with its diff: %.2000s", prompt)
	}
	if r := rechecked.Details.Consolidation; r == nil || !r.Applied || r.Rechecked != 1 {
		t.Fatalf("report %+v", rechecked.Details.Consolidation)
	}
	if row, ok := rowWith(rechecked.Summary, "src/net/retry.go"); !ok || strings.Contains(row.Text, "changed since") {
		t.Fatalf("still marked after the re-check: %+v\n%s", row, rechecked.Summary)
	}
	final = append(final, checked[len(later):]...)

	// No structure line: no stored entry names the view's file list, a blob of HEAD, or a verdict.
	blob := ""
	if a, ok, _ := gitrepo.Structure(dir, repo2, host.StructureQuery{Paths: []string{"src/net/retry.go"}}); ok {
		blob = a.Paths[0].Blob
	}
	for _, e := range final {
		if e.Type != "custom" {
			continue
		}
		data, _ := json.Marshal(e.Data)
		// A record's own frame names its path's blob (its body hash); nothing else may.
		data = []byte(strings.ReplaceAll(string(data), `"bodyHash":"`+blob+`"`, ""))
		for _, bad := range []string{"Makefile", "gone at", "changed since", blob} {
			if bad != "" && strings.Contains(string(data), bad) {
				t.Fatalf("a %s entry stores %q: %s", e.CustomType, bad, data)
			}
		}
	}
}

// Scenario 37: the root-commit lookup is slow on a large repository. A compaction never waits on
// it, no record takes another id meanwhile, and once it lands every later read gives the id it
// computed, from the cache under the engine's own store.
func TestScenario37RepoIDNeverWaitsOnRootLookup(t *testing.T) {
	dir := gitRepo(t, map[string]string{"a.go": "package a\n"})
	store := t.TempDir()
	defer func(f func(string, time.Duration) (string, error), w time.Duration) {
		gitrepo.RootLookup, gitrepo.RootWait = f, w
	}(gitrepo.RootLookup, gitrepo.RootWait)
	real := gitrepo.RootLookup
	release := make(chan struct{})
	var calls atomic.Int32
	gitrepo.RootLookup = func(d string, timeout time.Duration) (string, error) {
		calls.Add(1)
		select {
		case <-release:
		case <-time.After(timeout):
			return "", errors.New("timed out")
		}
		return real(d, timeout)
	}
	gitrepo.RootWait = 20 * time.Millisecond
	reader := gitrepo.Reader{Store: store}
	setup := func(h *testfix.FakeHost) {
		repo, ok, err := reader.Read(dir)
		if err != nil || !ok {
			t.Fatalf("read: %v %v", ok, err)
		}
		h.RepoInfo = &repo
	}
	entries, cut := turnsBranch(say("We decided to keep `a.go` tiny because it is the entry point."))
	start := time.Now()
	_, after := compactOnceWith(t, entries, cut, setup)
	if waited := time.Since(start); waited > 5*time.Second {
		t.Fatalf("the compaction waited %v", waited)
	}
	for _, o := range ledger.FoldLedger(after, "").ActiveObservations {
		if o.Frame.RepoID != "" {
			t.Fatalf("a record took an id before the lookup landed: %+v", o.Frame)
		}
	}
	close(release)
	deadline := time.Now().Add(10 * time.Second)
	var id string
	for id == "" {
		if time.Now().After(deadline) {
			t.Fatal("the id was never cached")
		}
		time.Sleep(10 * time.Millisecond)
		repo, _, _ := reader.Read(dir)
		id = repo.ID
	}
	later, cut2 := laterSession(after, say("We decided to keep `a.go` free of imports because it is the entry point."))
	_, final := compactOnceWith(t, later, cut2, setup)
	ids := map[string]bool{}
	for _, o := range ledger.FoldLedger(final, "").ActiveObservations {
		if o.Frame.RepoID != "" {
			ids[o.Frame.RepoID] = true
		}
	}
	if len(ids) != 1 || !ids[id] || calls.Load() != 1 {
		t.Fatalf("ids %v, want only %s; %d lookups", ids, id, calls.Load())
	}
	repo, _, _ := reader.Read(dir)
	if _, err := os.Stat(gitrepo.IDPath(store, repo.CommonDir)); err != nil {
		t.Fatalf("no cache file in the engine's store: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "om")); err == nil {
		t.Fatal("the cache was written inside the repository")
	}
}
