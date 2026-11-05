//go:build linux

package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The child holds nothing of the extension's: an empty environment (a secret and yaegi's dot
// command set here are absent), cwd /, stdin that reads EOF, no descriptor the extension had
// open, and on Linux an oom_score_adj of 1000.
func TestChildHoldsNothingOfTheExtensions(t *testing.T) {
	t.Setenv("OM_TEST_SECRET", "sk-secret")
	t.Setenv("YAEGI_DOT_CMD", "touch /tmp/om-yaegi-dot")
	t.Setenv("PIG_EXT_SOCKET", "/tmp/pig.sock")
	held, err := os.Create(filepath.Join(t.TempDir(), "extension-file"))
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	s := newSup(t, Config{})
	r := runCode(t, context.Background(), s, "var line string\nn, err := fmt.Scanln(&line)\nfmt.Sprint(n, err)", nil)
	if ok(t, r); r.Val != "0 EOF" {
		t.Fatalf("stdin read %q", r.Val)
	}
	pid := livePID(s)
	environ, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		t.Fatal(err)
	}
	if len(environ) != 0 {
		t.Fatalf("the child's environment is %q", environ)
	}
	if cwd, err := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid)); err != nil || cwd != "/" {
		t.Fatalf("cwd %q, %v", cwd, err)
	}
	fds, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pid))
	if err != nil {
		t.Fatal(err)
	}
	for _, fd := range fds {
		target, _ := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pid, fd.Name()))
		if target == held.Name() || strings.Contains(target, "extension-file") {
			t.Fatalf("the child inherited fd %s -> %s", fd.Name(), target)
		}
	}
	adj, err := os.ReadFile(fmt.Sprintf("/proc/%d/oom_score_adj", pid))
	if err != nil || strings.TrimSpace(string(adj)) != "1000" {
		t.Fatalf("oom_score_adj %q, %v", adj, err)
	}
	if st := s.Status(); st.OOM != oomSet {
		t.Fatalf("status %+v", st)
	}
	if _, err := os.Stat("/tmp/om-yaegi-dot"); err == nil {
		t.Fatal("yaegi ran the dot command")
	}
}
