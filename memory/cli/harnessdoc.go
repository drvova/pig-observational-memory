package cli

import (
	"fmt"
	"sort"
	"strings"
)

// HarnessDoc is docs/harnesses.md: every registered harness, what om gives it and where its
// configuration lives, rendered from the registry (TestHarnessDocIsCurrent keeps the file in
// step, and -write regenerates it).
func HarnessDoc(unsupported string) string {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.ToLower(registry[names[i]].Title) < strings.ToLower(registry[names[j]].Title)
	})
	var full, file, manual []string
	for _, n := range names {
		h := registry[n]
		row := fmt.Sprintf("| %s | `%s` | %s | %s |", h.Title, n, docWhere(h), docNote(h))
		switch {
		case h.Hooks != nil || h.Custom != nil && h.Name != "crush":
			full = append(full, row)
		case h.Manual != "":
			manual = append(manual, row)
		default:
			file = append(file, row)
		}
	}
	var b strings.Builder
	b.WriteString("# Harnesses\n\n")
	b.WriteString("<!-- Generated from memory/cli's registry by TestHarnessDocIsCurrent (go test ./cli -run HarnessDoc -args -write); do not edit. -->\n\n")
	fmt.Fprintf(&b, "`om install <name>` wires om into %d harnesses. Each entry takes only what the harness's own documentation or source confirms, linked in its last column; `om install --mcp-config <file>` covers any harness with a JSON MCP config not listed here, and [om](universal.md) has the protocol for any other.\n\n", len(names))
	section := func(title, intro string, rows []string) {
		fmt.Fprintf(&b, "## %s (%d)\n\n%s\n\n| Harness | Name | Where | Notes |\n|---|---|---|---|\n%s\n\n", title, len(rows), intro, strings.Join(rows, "\n"))
	}
	section("Recording, compaction and the memory tool", "Hooks, a plugin or an extension hand om the conversation, and the memory tool reads it.", full)
	section("The memory tool, installed by om", "om writes its MCP server into the harness's config, beside what is there, and uninstall takes out exactly that. The tool serves the curated memory and kept definitions, shared across harnesses.", file)
	section("The memory tool, added in the harness", "These keep their MCP servers in their own interface (or a file om does not rewrite): `om install <name>` prints the steps, the command and its arguments, and writes nothing.", manual)
	b.WriteString(unsupported)
	return b.String()
}

func docWhere(h Harness) string {
	path := func(paths map[string]string) string {
		for _, goos := range []string{"linux", "darwin", "windows"} {
			if p, ok := paths[goos]; ok {
				if goos != "linux" {
					return "`" + p + "` (" + goos + ")"
				}
				return "`" + p + "`"
			}
		}
		return "`" + paths[""] + "`"
	}
	switch {
	case h.Manual != "":
		return strings.ReplaceAll(h.Manual, "|", "\\|")
	case h.MCP != nil:
		return path(h.MCP.Path) + " `" + strings.Join(h.MCP.Key, ".") + "`"
	case h.YAML != nil:
		return path(h.YAML.Path) + " `" + h.YAML.Key + "`"
	case h.TOML != nil:
		return path(h.TOML.Path) + " `[" + h.TOML.Table + ".om]`"
	case h.Own != nil:
		return path(h.Own.Path) + " (a file of om's own)"
	case h.Hooks != nil:
		return path(h.Hooks.Path) + " hooks"
	}
	return "its own config (`om install " + h.Name + " --dry-run` shows the files)"
}

func docNote(h Harness) string {
	parts := []string{}
	if h.Note != "" {
		parts = append(parts, h.Note)
	}
	if h.Source != "" {
		parts = append(parts, "[docs]("+strings.SplitN(h.Source, ",", 2)[0]+")")
	}
	return strings.ReplaceAll(strings.Join(parts, "; "), "|", "\\|")
}
