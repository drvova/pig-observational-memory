package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Every harness with an MCP spec installs om's server at its key path beside what the file
// already held, and uninstall takes out exactly that, leaving the rest as it was.
func TestRegistryMCPRoundTrip(t *testing.T) {
	for name, h := range registry {
		if h.MCP == nil || pathFor(h.MCP.Path) == "" {
			continue
		}
		t.Run(name, func(t *testing.T) {
			env(t)
			path := pathFor(h.MCP.Path)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(path, []byte(`{"theirs": {"keep": true}}`), 0o600)
			w := writer{out: &strings.Builder{}}
			exe := "/opt/om/bin/om"
			if err := installMCP(w, exe, name, h.MCP, false); err != nil {
				t.Fatal(err)
			}
			cfg, _ := readJSONObject(path)
			if h.MCP.List {
				data, _ := json.Marshal(cfg)
				id := h.MCP.ID
				if id == "" {
					id = "name"
				}
				if !strings.Contains(string(data), `"`+id+`":"om"`) || !strings.Contains(string(data), exe) || cfg["theirs"] == nil {
					t.Fatalf("list config %s", data)
				}
				if err := installMCP(w, exe, name, h.MCP, true); err != nil {
					t.Fatal(err)
				}
				if data, _ := os.ReadFile(path); strings.Contains(string(data), exe) {
					t.Fatalf("after uninstall %s", data)
				}
				return
			}
			obj := cfg
			for _, k := range h.MCP.Key {
				next, ok := obj[k].(map[string]any)
				if !ok {
					t.Fatalf("no %v in %v", h.MCP.Key, cfg)
				}
				obj = next
			}
			entry, _ := json.Marshal(obj["om"])
			if !strings.Contains(string(entry), exe) || !strings.Contains(string(entry), `"--harness"`) || !strings.Contains(string(entry), `"`+name+`"`) {
				t.Fatalf("entry %s", entry)
			}
			if cfg["theirs"] == nil {
				t.Fatal("the file's own keys were lost")
			}
			if err := installMCP(w, exe, name, h.MCP, true); err != nil {
				t.Fatal(err)
			}
			if data, _ := os.ReadFile(path); strings.TrimSpace(string(data)) != "{\n  \"theirs\": {\n    \"keep\": true\n  }\n}" {
				t.Fatalf("after uninstall %s", data)
			}
		})
	}
}

// A config with comments is refused, never rewritten.
func TestJSONCRefused(t *testing.T) {
	env(t)
	h := registry["zed"]
	path := pathFor(h.MCP.Path)
	if path == "" {
		t.Skip("no zed path on " + runtime.GOOS)
	}
	os.MkdirAll(filepath.Dir(path), 0o700)
	orig := "// my settings\n{\"theme\": \"x\"}\n"
	os.WriteFile(path, []byte(orig), 0o600)
	if err := installMCP(writer{out: &strings.Builder{}}, "/om", "zed", h.MCP, false); err == nil || !strings.Contains(err.Error(), "by hand") {
		t.Fatalf("err %v", err)
	}
	if data, _ := os.ReadFile(path); string(data) != orig {
		t.Fatal("a JSONC file was rewritten")
	}
}

// Every registered name installs through om install (dry run), and every path expands.
func TestRegistryDryRun(t *testing.T) {
	env(t)
	for name, h := range registry {
		var out strings.Builder
		if err := install(name, false, true, &out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// A manual harness's steps may name a path for the user to open; only paths om writes
		// must be expanded.
		if h.Manual == "" && (strings.Contains(out.String(), "$") || strings.Contains(out.String(), "~")) {
			t.Fatalf("%s: unexpanded path:\n%s", name, out.String())
		}
	}
}
