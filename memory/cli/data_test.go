package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every data entry builds, and a JSON, YAML or TOML one installs into a fresh home and
// uninstalls exactly, with its placeholders expanded.
func TestDataHarnesses(t *testing.T) {
	data, err := assets.ReadFile("assets/harnesses.json")
	if err != nil {
		t.Fatal(err)
	}
	saved := registry
	registry = map[string]Harness{}
	defer func() { registry = saved }()
	if err := loadData(data); err != nil {
		t.Fatal(err)
	}
	for name, h := range registry {
		if h.Manual != "" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			env(t)
			var path string
			switch {
			case h.MCP != nil:
				path = pathFor(h.MCP.Path)
			case h.YAML != nil:
				path = pathFor(h.YAML.Path)
			case h.TOML != nil:
				path = pathFor(h.TOML.Path)
			case h.Own != nil:
				path = pathFor(h.Own.Path)
			}
			if path == "" {
				t.Skip("no path on this OS")
			}
			if strings.Contains(path, "$") || strings.Contains(path, "~") {
				t.Fatalf("unexpanded path %s", path)
			}
			var out strings.Builder
			if err := install(name, false, false, &out); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("not written: %v\n%s", err, out.String())
			}
			if strings.Contains(string(got), "$OM") || strings.Contains(string(got), "$HARNESS") || !strings.Contains(string(got), name) {
				t.Fatalf("placeholders not expanded:\n%s", got)
			}
			if err := install(name, true, false, &out); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(path); strings.Contains(string(got), "--harness") {
				t.Fatalf("after uninstall:\n%s", got)
			}
			_ = filepath.Dir(path)
		})
	}
}

// The TOML writer appends one marked table and removes exactly it.
func TestTOMLRoundTrip(t *testing.T) {
	env(t)
	path := filepath.Join(t.TempDir(), "c.toml")
	orig := "model = \"x\"\n\n[other]\nk = 1\n"
	os.WriteFile(path, []byte(orig), 0o600)
	spec := &TOMLSpec{Path: map[string]string{"": path}, Table: "mcp_servers", Entry: func(exe, h string) map[string]any {
		return map[string]any{"command": exe, "args": []any{"mcp", "--harness", h}, "enabled": true}
	}}
	w := writer{out: &strings.Builder{}}
	if err := installTOML(w, "/opt/om", "x", spec, false); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), "[mcp_servers.om] # om:managed") || !strings.Contains(string(got), `args = ["mcp", "--harness", "x"]`) || !strings.HasPrefix(string(got), orig) {
		t.Fatalf("toml:\n%s", got)
	}
	if err := installTOML(w, "/opt/om", "x", spec, true); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(path); string(got) != orig {
		t.Fatalf("after uninstall %q", got)
	}
}
