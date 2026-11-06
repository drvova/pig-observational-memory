package gitrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNormalizeRemote(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/Owner/Repo.git":           "github.com/Owner/Repo",
		"https://user:pw@GitHub.com/owner/repo/":      "github.com/owner/repo",
		"git@github.com:owner/repo.git":               "github.com/owner/repo",
		"ssh://git@example.org:2222/team/project.git": "example.org:2222/team/project",
		"/srv/git/project.git/":                       "/srv/git/project.git",
		"":                                            "",
	} {
		if got := NormalizeRemote(in); got != want {
			t.Errorf("NormalizeRemote(%q) = %q, want %q", in, got, want)
		}
	}
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestRead(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	if _, ok, _ := Read(dir); ok {
		t.Fatal("a directory outside a repository read as one")
	}
	run(t, dir, "init", "-q", "-b", "main")
	repo, ok, err := Read(dir)
	if err != nil || !ok || repo.Head != "" || repo.RootCommit != "" || repo.ID == "" {
		t.Fatalf("empty repository: %+v %v %v", repo, ok, err)
	}
	empty := repo.ID
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", "a.txt")
	run(t, dir, "commit", "-q", "-m", "first")
	run(t, dir, "remote", "add", "origin", "git@github.com:owner/repo.git")
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	repo, ok, err = Read(sub)
	if err != nil || !ok {
		t.Fatalf("Read: %v %v", ok, err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if repo.Root != real || repo.Branch != "main" || len(repo.Head) != 40 || repo.RootCommit != repo.Head || repo.Remote != "github.com/owner/repo" {
		t.Fatalf("repo: %+v", repo)
	}
	if repo.ID == empty || repo.ID != ID("github.com/owner/repo", repo.Head, repo.CommonDir) {
		t.Fatalf("id %q", repo.ID)
	}
	// A worktree of the clone shares its identity and common directory.
	wt := filepath.Join(t.TempDir(), "wt")
	run(t, dir, "worktree", "add", "-q", "-b", "side", wt)
	other, ok, _ := Read(wt)
	if !ok || other.ID != repo.ID || other.CommonDir != repo.CommonDir || other.Branch != "side" || other.Root == repo.Root {
		t.Fatalf("worktree: %+v", other)
	}
}
