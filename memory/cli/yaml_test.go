package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Goose: om's extension joins the map beside the user's, comments kept, and uninstall restores
// the file. Continue: a new file gets the top-level keys Continue requires, the server is a list
// item named om, and uninstall takes only that item.
func TestYAMLRoundTrip(t *testing.T) {
	env(t)
	g := registry["goose"]
	path := pathFor(g.YAML.Path)
	os.MkdirAll(filepath.Dir(path), 0o700)
	orig := "# my goose\nGOOSE_PROVIDER: openai\nextensions:\n  dev:\n    type: builtin # keep\n    enabled: true\n"
	os.WriteFile(path, []byte(orig), 0o600)
	w := writer{out: &strings.Builder{}}
	if err := installYAML(w, "/opt/om", "goose", g.YAML, false); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	for _, want := range []string{"# my goose", "# keep", "GOOSE_PROVIDER: openai", "  om:", "cmd: /opt/om", "- --harness", "- goose"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("goose config lacks %q:\n%s", want, data)
		}
	}
	if err := installYAML(w, "/opt/om", "goose", g.YAML, true); err != nil {
		t.Fatal(err)
	}
	if data, _ = os.ReadFile(path); strings.Contains(string(data), "om:") || !strings.Contains(string(data), "dev:") || !strings.Contains(string(data), "# keep") {
		t.Fatalf("goose after uninstall:\n%s", data)
	}

	c := registry["continue"]
	cpath := pathFor(c.YAML.Path)
	if err := installYAML(w, "/opt/om", "continue", c.YAML, false); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(cpath)
	for _, want := range []string{"schema: v1", "version:", "mcpServers:", "- name: om", "command: /opt/om"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("continue config lacks %q:\n%s", want, data)
		}
	}
	// A second server of the user's survives om's removal.
	os.WriteFile(cpath, []byte(strings.Replace(string(data), "mcpServers:\n", "mcpServers:\n  - name: theirs\n    command: x\n", 1)), 0o600)
	if err := installYAML(w, "/opt/om", "continue", c.YAML, true); err != nil {
		t.Fatal(err)
	}
	if data, _ = os.ReadFile(cpath); strings.Contains(string(data), "name: om") || !strings.Contains(string(data), "name: theirs") {
		t.Fatalf("continue after uninstall:\n%s", data)
	}
}
