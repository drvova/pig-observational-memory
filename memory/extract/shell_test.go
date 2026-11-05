package extract

import (
	"fmt"
	"testing"
)

// Only an unambiguous command changes a path: literal words, no other sequencing, no change of
// directory.
func TestShellPathEvents(t *testing.T) {
	for _, c := range []struct {
		command string
		want    string
	}{
		{"rm a.go", "[{a.go deleted }]"},
		{"rm -rf build/ 'my file.txt'", "[{build/ deleted } {my file.txt deleted }]"},
		{`rm "$HOME/x"`, "[]"},
		{"rm ~/x *.o", "[]"},
		{"rm a.go | tee log", "[]"},
		{"rm a.go || true", "[]"},
		{"rm a.go &", "[]"},
		{"rm $(cat list)", "[]"},
		{"FOO=1 rm a.go", "[]"},
		{"pushd x && rm a.go", "[]"},
		{"git -C sub rm a.go", "[]"},
		{"git rm --cached a.go", "[]"},
		{"git rm -r old/", "[{old/ deleted }]"},
		{"git mv a.go b/ c.go", "[]"},
		{"git checkout main", "[]"},
		{"git checkout main -- a.go", "[]"},
		{"git checkout -- a.go b.go", "[{a.go reverted } {b.go reverted }]"},
		{"git restore --staged a.go", "[]"},
		{"git restore --staged --worktree a.go", "[{a.go reverted }]"},
		{"git restore --source=HEAD~1 a.go", "[]"},
		{"echo 'unclosed", "[]"},
		{"rm -i main.go", "[]"},
		{"rm -rI build", "[]"},
		{"rm --interactive=always a.go", "[]"},
		{"rm --interactive=never a.go", "[{a.go deleted }]"},
		{"git restore -p main.go", "[]"},
		{"git restore --patch main.go", "[]"},
		{"git checkout -p -- main.go", "[]"},
	} {
		if got := fmt.Sprint(shellPathEvents(c.command)); got != c.want {
			t.Errorf("%q: %s, want %s", c.command, got, c.want)
		}
	}
}

func TestCommitFrom(t *testing.T) {
	if hash, subject, ok := commitFrom(`git commit -am "x"`, "[feature/a-b 0123abcd] Add the loader\n 2 files changed"); !ok || hash != "0123abcd" || subject != "Add the loader" {
		t.Fatalf("%q %q %v", hash, subject, ok)
	}
	if _, _, ok := commitFrom("git log", "[main 0123abc] x"); ok {
		t.Fatal("not a commit")
	}
}
