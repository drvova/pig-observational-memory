package transcript

import (
	"path/filepath"
	"strings"
	"testing"
)

// Claude Code's projects folder name (its sanitizePath): every UTF-16 code unit that is not an
// ASCII letter or digit as -, and a name over 200 characters cut with its hash appended. Each
// want is what Claude Code's own function returns for the path.
func TestClaudeProjectDir(t *testing.T) {
	long := "/home/user/" + strings.Repeat("very_long-directory.name/", 9) + "end"
	longUnicode := "/srv/" + strings.Repeat("ünïcødé_dir/", 20) + "😀"
	for _, c := range []struct{ cwd, want string }{
		{"/home/u/my_proj v2/café", "-home-u-my-proj-v2-caf-"},
		{"/w/日本語/😀x", "-w-------x"},
		{`C:\Users\a.b\x`, "C--Users-a-b-x"},
		{long, "-home-user-very-long-directory-name-very-long-directory-name-very-long-directory-name-very-long-directory-name-very-long-directory-name-very-long-directory-name-very-long-directory-name-very-long-dire-iekqsj"},
		{longUnicode, "-srv--n-c-d--dir--n-c-d--dir--n-c-d--dir--n-c-d--dir--n-c-d--dir--n-c-d--dir--n-c-d--dir--n-c-d--dir--n-c-d--dir--n-c-d--dir--n-c-d--dir--n-c-d--dir--n-c-d--dir--n-c-d--dir--n-c-d--dir--n-c-d--dir--n--6bcgy6"},
	} {
		if got := ClaudeProjectDir(c.cwd); got != c.want {
			t.Errorf("ClaudeProjectDir(%q) = %q, want %q", c.cwd, got, c.want)
		}
	}
	// Kode keeps the whole slug, however long.
	if got := JSSlug(long); len(got) != len(long) || strings.Contains(got, "_") {
		t.Errorf("JSSlug(long) = %q", got)
	}
	t.Setenv("KODE_CONFIG_DIR", "/k")
	if got := KodeSessionPath("/w/my_app é", "s1"); got != filepath.Join("/k", "projects", "-w-my-app--", "s1.jsonl") {
		t.Fatalf("kode path %s", got)
	}
}
