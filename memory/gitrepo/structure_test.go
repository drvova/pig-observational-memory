package gitrepo

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/host"
)

func needGit(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
}

func write(t *testing.T, dir, name, text string) {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commitRepo is a repository with config/load.go defining parseConfig, committed once.
func commitRepo(t *testing.T) string {
	t.Helper()
	needGit(t)
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	write(t, dir, "config/load.go", "package config\n\nfunc parseConfig() {}\n")
	write(t, dir, "README.md", "readme\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "first")
	return dir
}

func TestStructureAtHead(t *testing.T) {
	dir := commitRepo(t)
	repo, ok, err := Read(dir)
	if err != nil || !ok {
		t.Fatalf("Read: %v %v", ok, err)
	}
	first := repo.Head
	a, ok, err := Structure(dir, repo, host.StructureQuery{
		Paths:   []string{"config/load.go", "config", "gone.go", "../outside", filepath.Join(repo.Root, "README.md")},
		Symbols: []host.SymbolQuery{{Path: "config/load.go", Symbol: "parseConfig"}, {Path: "config/load.go", Symbol: "LoadConfig"}, {Symbol: "parseConfig"}, {Path: "gone.go", Symbol: "x"}},
		List:    &host.ListQuery{Prefix: "config/"},
	})
	if err != nil || !ok || a.Head != first || a.RepoID != repo.ID {
		t.Fatalf("Structure: %+v %v %v", a, ok, err)
	}
	if p := a.Paths[0]; !p.Known || !p.Exists || len(p.Blob) != 40 {
		t.Fatalf("file: %+v", p)
	}
	if p := a.Paths[1]; !p.Known || !p.Exists || p.Blob != "" {
		t.Fatalf("directory: %+v", p)
	}
	if p := a.Paths[2]; !p.Known || p.Exists {
		t.Fatalf("missing: %+v", p)
	}
	if p := a.Paths[3]; p.Known {
		t.Fatalf("outside: %+v", p)
	}
	if p := a.Paths[4]; !p.Exists {
		t.Fatalf("absolute: %+v", p)
	}
	want := []bool{true, false, true, false}
	for i, s := range a.Symbols {
		if !s.Known || s.Found != want[i] {
			t.Fatalf("symbol %d: %+v", i, s)
		}
	}
	if len(a.Files) != 1 || a.Files[0] != "config/load.go" || a.Total != 1 {
		t.Fatalf("list: %+v", a)
	}
	blob := a.Paths[0].Blob

	// The rename: a new HEAD, a new view; the old commit's diff is bounded and readable.
	run(t, dir, "mv", "config/load.go", "config/config.go")
	write(t, dir, "config/config.go", "package config\n\nfunc LoadConfig() {}\n")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "rename")
	repo, _, _ = Read(dir)
	reads := treeReads
	a, _, _ = Structure(dir, repo, host.StructureQuery{
		Paths:   []string{"config/load.go", "config/config.go"},
		Symbols: []host.SymbolQuery{{Symbol: "parseConfig"}},
		Blobs:   []host.BlobQuery{{Path: "config/load.go", At: first}, {Path: "config/config.go", At: first}},
		Diffs:   []host.DiffQuery{{Path: "config/config.go", From: first}, {Path: "config/load.go", From: "--output=/tmp/x"}},
	})
	if b := a.Blobs[0]; !b.Known || b.Blob != blob {
		t.Fatalf("blob at the first commit: %+v, want %s", b, blob)
	}
	if b := a.Blobs[1]; b.Known {
		t.Fatalf("a path the commit does not hold: %+v", b)
	}
	if treeReads != reads+1 || a.Head == first || a.Paths[0].Exists || !a.Paths[1].Exists || a.Paths[1].Blob == blob {
		t.Fatalf("after rename: %+v", a)
	}
	if !a.Symbols[0].Known || a.Symbols[0].Found {
		t.Fatalf("symbol after rename: %+v", a.Symbols[0])
	}
	if d := a.Diffs[0]; !d.Known || !strings.Contains(d.Diff, "+func LoadConfig") {
		t.Fatalf("diff: %+v", d)
	}
	if d := a.Diffs[1]; d.Known {
		t.Fatalf("an option as a commit was run: %+v", d)
	}
	// The same HEAD again reads no tree.
	if _, _, err := Structure(dir, repo, host.StructureQuery{Paths: []string{"README.md"}}); err != nil || treeReads != reads+1 {
		t.Fatalf("view not cached: %d reads, %v", treeReads-reads, err)
	}
}

func TestStructureTruncatedIsUnknown(t *testing.T) {
	dir := commitRepo(t)
	defer func(n int) { MaxTreeFiles = n }(MaxTreeFiles)
	MaxTreeFiles = 1
	write(t, dir, "z.txt", "z")
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "more")
	repo, _, _ := Read(dir)
	a, _, err := Structure(dir, repo, host.StructureQuery{Paths: []string{"zz-missing.go"}})
	if err != nil || !a.Truncated || a.Paths[0].Known {
		t.Fatalf("truncated: %+v %v", a, err)
	}
}

// Scenario 37: the root-commit lookup is slow; a read never waits past RootWait for it, the id
// it caches is the one every later read gives, and a read with the cache never looks again.
func TestRepoIDCachedOnce(t *testing.T) {
	dir := commitRepo(t)
	run(t, dir, "remote", "add", "origin", "https://example.org/team/project.git")
	store := t.TempDir()
	defer func(f func(string, time.Duration) (string, error), w time.Duration) { RootLookup, RootWait = f, w }(RootLookup, RootWait)
	real := RootLookup
	var calls atomic.Int32
	release := make(chan struct{})
	RootLookup = func(dir string, timeout time.Duration) (string, error) {
		calls.Add(1)
		select {
		case <-release:
		case <-time.After(timeout):
			return "", errors.New("timed out")
		}
		return real(dir, timeout)
	}
	RootWait = 50 * time.Millisecond
	r := Reader{Store: store}
	start := time.Now()
	repo, ok, err := r.Read(dir)
	if err != nil || !ok || repo.ID != "" || repo.Head == "" {
		t.Fatalf("slow lookup: %+v %v %v", repo, ok, err)
	}
	if waited := time.Since(start); waited > 3*time.Second {
		t.Fatalf("the read waited %v on the lookup", waited)
	}
	// A second read while the lookup runs joins it.
	if again, _, _ := r.Read(dir); again.ID != "" || calls.Load() != 1 {
		t.Fatalf("second read: %+v, %d lookups", again, calls.Load())
	}
	close(release)
	want := ID("example.org/team/project", repo.Head, repo.CommonDir)
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(IDPath(store, repo.CommonDir)); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no id cached")
		}
		time.Sleep(10 * time.Millisecond)
	}
	repo, _, _ = r.Read(dir)
	if repo.ID != want || repo.RootCommit == "" {
		t.Fatalf("cached id %q, want %q", repo.ID, want)
	}
	// A new remote does not move the id; a later lookup that would fail is never run.
	run(t, dir, "remote", "set-url", "origin", "https://example.org/other/name.git")
	RootLookup = func(string, time.Duration) (string, error) {
		t.Error("the lookup ran with the id cached")
		return "", errors.New("no")
	}
	memoMu.Lock()
	memo = map[string]CachedID{}
	memoMu.Unlock()
	if later, _, _ := r.Read(dir); later.ID != want {
		t.Fatalf("id moved to %q", later.ID)
	}
	// A worktree shares the common directory, so the cached id.
	wt := filepath.Join(t.TempDir(), "wt")
	run(t, dir, "worktree", "add", "-q", "-b", "side", wt)
	if other, _, _ := r.Read(wt); other.ID != want {
		t.Fatalf("worktree id %q", other.ID)
	}
}

// A store from before the cache (no file) computes the id once and writes it; a corrupt file is
// computed again.
func TestRepoIDCacheMigrates(t *testing.T) {
	dir := commitRepo(t)
	store := t.TempDir()
	plain, _, _ := Read(dir)
	r := Reader{Store: store}
	repo, _, _ := r.Read(dir)
	if repo.ID != plain.ID || repo.ID == "" {
		t.Fatalf("cached read %q, plain %q", repo.ID, plain.ID)
	}
	p := IDPath(store, repo.CommonDir)
	if err := os.WriteFile(p, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	memoMu.Lock()
	memo = map[string]CachedID{}
	memoMu.Unlock()
	if again, _, _ := r.Read(dir); again.ID != plain.ID {
		t.Fatalf("after a corrupt file: %q", again.ID)
	}
}
