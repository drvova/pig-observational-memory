package observationalmemory

import (
	"os"
	"path/filepath"
	"testing"
)

func TestProjectRootIsTheEnclosingRepository(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := projectRoot(sub); got != root {
		t.Fatalf("projectRoot(%s) = %s, want the repository root %s", sub, got, root)
	}
	plain, _ := filepath.EvalSymlinks(t.TempDir())
	if got := projectRoot(plain); got != plain {
		t.Fatalf("outside a repository the project is cwd itself, got %s", got)
	}
}
