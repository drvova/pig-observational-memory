package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A key the user adds under om's Codex table stays in om's table across a reinstall (it used to
// move into the table before), and goes with om's table on uninstall.
func TestTOMLUserKeyInOmTable(t *testing.T) {
	_, home := env(t)
	cfg := filepath.Join(home, ".codex", "config.toml")
	writeLines(t, cfg, []string{"model = \"x\"\n\n[profiles.fast]\nmodel = \"y\""})
	om(t, "", "install", "codex")
	data, _ := os.ReadFile(cfg)
	os.WriteFile(cfg, []byte(strings.Replace(string(data), "[mcp_servers.om] # om:managed\n", "[mcp_servers.om] # om:managed\nstartup_timeout_sec = 30\n", 1)), 0o600)
	om(t, "", "install", "codex")
	data, _ = os.ReadFile(cfg)
	om := strings.Index(string(data), "[mcp_servers.om]")
	if key := strings.Index(string(data), "startup_timeout_sec = 30"); key < om || strings.Count(string(data), "startup_timeout_sec") != 1 {
		t.Fatalf("reinstall moved the user's key:\n%s", data)
	}
	runOm(t, "uninstall", "codex")
	if data, _ = os.ReadFile(cfg); strings.Contains(string(data), "startup_timeout_sec") || !strings.Contains(string(data), "[profiles.fast]\nmodel = \"y\"") {
		t.Fatalf("after uninstall:\n%s", data)
	}
}

func runOm(t *testing.T, args ...string) string { t.Helper(); return om(t, "", args...) }

// Uninstalling from a harness that was never installed writes nothing.
func TestUninstallCreatesNothing(t *testing.T) {
	_, home := env(t)
	for _, h := range []string{"gemini-cli", "codex", "kimi-cli", "claude-desktop", "goose", "crush", "mistral-vibe"} {
		om(t, "", "uninstall", h)
	}
	var made []string
	filepath.Walk(home, func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() {
			made = append(made, p)
		}
		return nil
	})
	if len(made) > 0 {
		t.Fatalf("uninstall created %v", made)
	}
}

// A YAML config with several documents is refused, not cut to its first; one holding only
// comments keeps them.
func TestYAMLDocuments(t *testing.T) {
	_, home := env(t)
	cfg := filepath.Join(home, ".config", "goose", "config.yaml")
	writeLines(t, cfg, []string{"a: 1\n---\nb: 2"})
	var out, errOut strings.Builder
	if code := Run([]string{"install", "goose"}, strings.NewReader(""), &out, &errOut); code == 0 || !strings.Contains(errOut.String(), "2 YAML documents") {
		t.Fatalf("multi-document install: %d %s", code, errOut.String())
	}
	if data, _ := os.ReadFile(cfg); string(data) != "a: 1\n---\nb: 2\n" {
		t.Fatalf("file changed: %q", data)
	}
	writeLines(t, cfg, []string{"# my goose settings"})
	om(t, "", "install", "goose")
	if data, _ := os.ReadFile(cfg); !strings.HasPrefix(string(data), "# my goose settings\n") || !strings.Contains(string(data), "name: om") {
		t.Fatalf("comment-only file:\n%s", data)
	}
}

// crushrc keeps its blank lines through install and uninstall.
func TestCrushrcBlankLines(t *testing.T) {
	_, home := env(t)
	rc := filepath.Join(home, ".config", "crush", "crushrc")
	writeLines(t, rc, []string{"a\n\nb"})
	om(t, "", "install", "crush")
	om(t, "", "uninstall", "crush")
	if data, _ := os.ReadFile(rc); string(data) != "a\n\nb\n" {
		t.Fatalf("crushrc %q", data)
	}
}

// A hook command quotes om's path for sh: $ and backticks in it are not expanded.
func TestHookCommandQuoting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows hooks quote with double quotes")
	}
	if got := hookCommand("/opt/$HOME `x`/om", "claude-code", "Stop"); got != "'/opt/$HOME `x`/om' hook claude-code Stop" {
		t.Fatalf("command %s", got)
	}
}
