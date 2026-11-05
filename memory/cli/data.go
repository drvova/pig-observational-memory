package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// harnessData is one harness of assets/harnesses.json: a client om supports through its MCP
// configuration alone, as the research of its own documentation recorded it (docs/universal.md,
// "Harness registry"). Harnesses that need code (hooks, plugins, Codex's TOML and notify) are
// registered in registry.go instead.
type harnessData struct {
	Name   string            `json:"name"`
	Title  string            `json:"title"`
	Class  string            `json:"class"` // file | own | manual
	Path   map[string]string `json:"path"`
	Format string            `json:"format"` // json | yaml | toml
	Key    []string          `json:"key"`
	List   bool              `json:"list"`
	// ID is the field a list entry is identified by, when not "name".
	ID string `json:"id"`
	// Entry is the server entry, with $OM for om's path and $HARNESS for the harness name in any
	// string.
	Entry  map[string]any `json:"entry"`
	Manual string         `json:"manual"`
	// Content is the whole file of an "own" harness (one that loads every file of a pattern
	// from a directory), with $OM and $HARNESS.
	Content string `json:"content"`
	Note    string `json:"note"`
	Source  string `json:"source"`
}

// loadData registers every harness of the data file; a malformed entry is an error (the tests
// load the file, so a bad entry never ships).
func loadData(data []byte) error {
	var list []harnessData
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("harnesses.json: %w", err)
	}
	for _, d := range list {
		h, err := d.harness()
		if err != nil {
			return fmt.Errorf("harnesses.json: %s: %w", d.Name, err)
		}
		if _, dup := registry[h.Name]; dup {
			return fmt.Errorf("harnesses.json: %s is registered twice", h.Name)
		}
		register(h)
	}
	return nil
}

func (d harnessData) harness() (Harness, error) {
	if d.Name == "" || d.Title == "" {
		return Harness{}, fmt.Errorf("needs a name and a title")
	}
	h := Harness{Name: d.Name, Title: d.Title, Note: d.Note, Source: d.Source}
	switch d.Class {
	case "manual":
		if d.Manual == "" {
			return h, fmt.Errorf("a manual harness needs its steps")
		}
		h.Manual = d.Manual
		return h, nil
	case "own":
		if len(d.Path) == 0 || d.Content == "" {
			return h, fmt.Errorf("an own-file harness needs a path and its content")
		}
		content := d.Content
		h.Own = &OwnSpec{Path: d.Path, Content: func(exe, harness string) string {
			return strings.NewReplacer("$OM", exe, "$HARNESS", harness).Replace(content)
		}}
		return h, nil
	case "file":
	default:
		return h, fmt.Errorf("unknown class %q", d.Class)
	}
	if len(d.Path) == 0 || len(d.Key) == 0 || d.Entry == nil {
		return h, fmt.Errorf("a file harness needs a path, a key and an entry")
	}
	for goos := range d.Path {
		if goos != "darwin" && goos != "linux" && goos != "windows" {
			return h, fmt.Errorf("unknown OS %q in path", goos)
		}
	}
	entry := func(exe, harness string) map[string]any {
		return expandEntry(d.Entry, exe, harness).(map[string]any)
	}
	switch d.Format {
	case "json":
		h.MCP = &MCPSpec{Path: d.Path, Key: d.Key, Entry: entry, List: d.List, ID: d.ID}
	case "yaml":
		if len(d.Key) != 1 {
			return h, fmt.Errorf("a YAML key is one top-level key")
		}
		h.YAML = &YAMLSpec{Path: d.Path, Key: d.Key[0], List: d.List, Entry: entry}
	case "toml":
		h.TOML = &TOMLSpec{Path: d.Path, Table: strings.Join(d.Key, "."), Entry: entry, List: d.List}
	default:
		return h, fmt.Errorf("unknown format %q", d.Format)
	}
	return h, nil
}

// expandEntry replaces $OM and $HARNESS in every string of v.
func expandEntry(v any, exe, harness string) any {
	switch x := v.(type) {
	case string:
		return strings.NewReplacer("$OM", exe, "$HARNESS", harness).Replace(x)
	case []any:
		out := make([]any, len(x))
		for i, e := range x {
			out[i] = expandEntry(e, exe, harness)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			out[k] = expandEntry(x[k], exe, harness)
		}
		return out
	}
	return v
}
