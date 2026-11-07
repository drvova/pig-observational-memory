package contracts

import (
	"os"
	"os/exec"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/gitrepo"
)

// The repository id cache file the adapter writes (gitrepo.Reader) validates against its schema,
// under both stores' layouts.
func TestRepoIDCacheFile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	dir := t.TempDir()
	env := append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "first"}, {"remote", "add", "origin", "https://example.org/team/project.git"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	root := t.TempDir()
	if repo, ok, err := (gitrepo.Reader{Store: root}).Read(dir); err != nil || !ok || repo.ID == "" {
		t.Fatalf("read: %+v %v %v", repo, ok, err)
	}
	if counts := validateTree(t, loadSchemas(t), root); counts["repo-id.schema.json"] != 1 {
		t.Fatalf("validated %v", counts)
	}
}
