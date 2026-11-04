package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Harness is one coding agent om works with, described as data (docs/universal.md, "Harness
// registry"): where its MCP client reads servers from, which hook family it speaks and where
// those hooks live, and what its transcripts are. A harness that needs more than data (Codex's
// TOML and notify program, OpenCode's plugin, Pi's extension, Claude Code's MCP command) has a
// Custom step. Adding a harness that speaks a known family is one entry.
type Harness struct {
	Name, Title string
	// MCP registers the memory tool's server; nil for a harness without an MCP client.
	MCP *MCPSpec
	// Hooks registers om's lifecycle hooks; nil for a harness without hooks.
	Hooks *HookSpec
	// Custom installs (or removes) what data cannot say; it runs after MCP and Hooks.
	Custom func(w writer, exe string, remove bool) error
	// Note is shown by om harnesses: what the harness gets beyond its columns, or what it lacks.
	Note string
}

// MCPSpec is where a harness's MCP client reads stdio servers: a JSON config file and the
// object path its servers live under, and the entry one server takes there.
type MCPSpec struct {
	// Path is the config file by GOOS ("" for every other), in pathTemplate syntax.
	Path map[string]string
	// Key is the object path the servers live under ("mcpServers"; "mcp" for OpenCode).
	Key []string
	// Entry is om's server entry, nil for the common {"command","args"}.
	Entry func(exe, harness string) map[string]any
}

// HookSpec is a harness's hooks in Claude Code's schema, the family most harnesses copied:
// {"hooks": {"<Event>": [{"matcher": "", "hooks": [{"type": "command", "command": …, "timeout": …}]}]}}
// in a JSON settings file, with a payload on stdin carrying session_id, transcript_path and cwd.
type HookSpec struct {
	Path map[string]string
	// Events maps om's events (session-start, prompt, turn-end, pre-compact) to the harness's
	// event names; an event the harness lacks is absent.
	Events map[string]string
	// Transcript is the format of the payload's transcript_path (transcript.Formats).
	Transcript string
	// Matcher is set on every hook group when the harness requires one.
	Matcher *string
}

// The om events a hook family can carry.
const (
	evSessionStart = "session-start"
	evPrompt       = "prompt"
	evTurnEnd      = "turn-end"
	evPreCompact   = "pre-compact"
)

// hookTimeouts are each om event's hook timeout in seconds: a compaction reads the whole
// transcript.
var hookTimeouts = map[string]int{evSessionStart: 60, evPrompt: 30, evTurnEnd: 120, evPreCompact: 600}

// registry is every harness om installs into.
var registry = map[string]Harness{}

func register(h Harness) { registry[h.Name] = h }

// claudeEventNames are Claude Code's hook events for om's.
var claudeEventNames = map[string]string{evSessionStart: "SessionStart", evPrompt: "UserPromptSubmit", evTurnEnd: "Stop", evPreCompact: "PreCompact"}

func init() {
	register(Harness{
		Name: "claude-code", Title: "Claude Code",
		Hooks:  &HookSpec{Path: map[string]string{"": "${CLAUDE_CONFIG_DIR:-~/.claude}/settings.json"}, Events: claudeEventNames, Transcript: "claude-code"},
		Custom: claudeMCP,
		Note:   "compaction: om's summary is handed back after Claude's own (SessionStart, source compact)",
	})
	register(Harness{
		Name: "codex", Title: "Codex CLI", Custom: installCodex,
		Note: "no compaction hook: memory is reached through the tool",
	})
	register(Harness{
		Name: "opencode", Title: "OpenCode", Custom: pluginInstaller("opencode", "${XDG_CONFIG_HOME:-~/.config}/opencode/plugin"),
		MCP: &MCPSpec{Path: map[string]string{"": "${XDG_CONFIG_HOME:-~/.config}/opencode/opencode.json"}, Key: []string{"mcp"}, Entry: func(exe, h string) map[string]any {
			return map[string]any{"type": "local", "command": []any{exe, "mcp", "--harness", h}, "enabled": true}
		}},
		Note: "plugin: level 0 on session.idle; om's summary joins OpenCode's compaction context",
	})
	// The harnesses below were checked against their own documentation (docs/universal.md,
	// "Harness registry", lists the sources); each takes what its docs confirm.
	register(Harness{
		Name: "kilo-code", Title: "Kilo Code", Custom: pluginInstaller("kilo-code", "${XDG_CONFIG_HOME:-~/.config}/kilo/plugin"),
		MCP: &MCPSpec{Path: map[string]string{"darwin": "${XDG_CONFIG_HOME:-~/.config}/kilo/kilo.json", "linux": "${XDG_CONFIG_HOME:-~/.config}/kilo/kilo.json"}, Key: []string{"mcp"}, Entry: func(exe, h string) map[string]any {
			return map[string]any{"type": "local", "command": []any{exe, "mcp", "--harness", h}, "enabled": true}
		}},
		Note: "OpenCode's plugin API: level 0 on session.idle; om's summary joins Kilo's compaction context (plugin not yet run inside Kilo)",
	})
	register(Harness{
		Name: "cursor", Title: "Cursor",
		MCP:  &MCPSpec{Path: map[string]string{"darwin": "~/.cursor/mcp.json", "linux": "~/.cursor/mcp.json"}, Key: []string{"mcpServers"}, Entry: stdioEntry},
		Note: "Cursor's hooks are not wired yet: its transcript format is undocumented",
	})
	register(Harness{
		Name: "devin", Title: "Devin Desktop (Windsurf)",
		MCP:  &MCPSpec{Path: map[string]string{"darwin": "${XDG_CONFIG_HOME:-~/.config}/devin/mcp_config.json", "linux": "${XDG_CONFIG_HOME:-~/.config}/devin/mcp_config.json", "windows": "${APPDATA}/devin/mcp_config.json"}, Key: []string{"mcpServers"}},
		Note: "its hooks carry no transcript path, so they are not wired",
	})
	register(Harness{
		Name: "cline", Title: "Cline",
		MCP: &MCPSpec{Path: map[string]string{"darwin": "${CLINE_MCP_SETTINGS_PATH:-~/.cline/data/settings/cline_mcp_settings.json}", "linux": "${CLINE_MCP_SETTINGS_PATH:-~/.cline/data/settings/cline_mcp_settings.json}"}, Key: []string{"mcpServers"}},
	})
	register(Harness{
		Name: "zed", Title: "Zed",
		MCP: &MCPSpec{Path: map[string]string{"darwin": "~/.config/zed/settings.json", "linux": "~/.config/zed/settings.json"}, Key: []string{"context_servers"}, Entry: func(exe, h string) map[string]any {
			return map[string]any{"command": exe, "args": []any{"mcp", "--harness", h}, "env": map[string]any{}}
		}},
	})
	register(Harness{
		Name: "copilot", Title: "GitHub Copilot (CLI, VS Code)",
		MCP: &MCPSpec{Path: map[string]string{"darwin": "${COPILOT_HOME:-~/.copilot}/mcp-config.json", "linux": "${COPILOT_HOME:-~/.copilot}/mcp-config.json"}, Key: []string{"mcpServers"}, Entry: func(exe, h string) map[string]any {
			return map[string]any{"type": "local", "command": exe, "args": []any{"mcp", "--harness", h}, "tools": []any{"*"}}
		}},
		Note: "Copilot's hooks are not wired yet: their transcript format is undocumented",
	})
	register(Harness{
		Name: "claude-desktop", Title: "Claude Desktop",
		MCP: &MCPSpec{Path: map[string]string{"darwin": "~/Library/Application Support/Claude/claude_desktop_config.json", "linux": "${XDG_CONFIG_HOME:-~/.config}/Claude/claude_desktop_config.json", "windows": "${APPDATA}/Claude/claude_desktop_config.json"}, Key: []string{"mcpServers"}},
	})
	register(Harness{
		Name: "gemini-cli", Title: "Gemini CLI",
		MCP:  &MCPSpec{Path: map[string]string{"darwin": "~/.gemini/settings.json", "linux": "~/.gemini/settings.json"}, Key: []string{"mcpServers"}},
		Note: "its hooks are not wired yet: its transcript format is undocumented",
	})
	register(Harness{
		Name: "qwen-code", Title: "Qwen Code",
		MCP:  &MCPSpec{Path: map[string]string{"darwin": "~/.qwen/settings.json", "linux": "~/.qwen/settings.json"}, Key: []string{"mcpServers"}},
		Note: "its hooks are not wired yet: its transcript format is undocumented",
	})
	register(Harness{
		Name: "kiro", Title: "Kiro",
		MCP:  &MCPSpec{Path: map[string]string{"darwin": "~/.kiro/settings/mcp.json", "linux": "~/.kiro/settings/mcp.json"}, Key: []string{"mcpServers"}},
		Note: "its hooks are not wired yet: no transcript is documented",
	})
	register(Harness{
		Name: "amazon-q", Title: "Amazon Q Developer CLI",
		MCP:  &MCPSpec{Path: map[string]string{"darwin": "~/.aws/amazonq/mcp.json", "linux": "~/.aws/amazonq/mcp.json"}, Key: []string{"mcpServers"}},
		Note: "read by an agent only with useLegacyMcpJson: true (Q CLI is being replaced by Kiro)",
	})
	register(Harness{
		Name: "factory-droid", Title: "Factory Droid",
		MCP:  &MCPSpec{Path: map[string]string{"darwin": "~/.factory/mcp.json", "linux": "~/.factory/mcp.json"}, Key: []string{"mcpServers"}, Entry: stdioEntry},
		Note: "its hooks are not wired yet: its transcript format is undocumented",
	})
	register(Harness{
		Name: "warp", Title: "Warp",
		MCP: &MCPSpec{Path: map[string]string{"darwin": "~/.warp/.mcp.json", "linux": "~/.warp/.mcp.json", "windows": "${USERPROFILE}/.warp/.mcp.json"}, Key: []string{"mcpServers"}},
	})
	register(Harness{
		Name: "augment", Title: "Augment (Auggie CLI)",
		MCP:  &MCPSpec{Path: map[string]string{"darwin": "~/.augment/settings.json", "linux": "~/.augment/settings.json"}, Key: []string{"mcpServers"}, Entry: stdioEntry},
		Note: "its hooks cannot hand the model context and carry no transcript",
	})
	register(Harness{
		Name: "kimi-cli", Title: "Kimi CLI",
		MCP:  &MCPSpec{Path: map[string]string{"darwin": "~/.kimi/mcp.json", "linux": "~/.kimi/mcp.json"}, Key: []string{"mcpServers"}},
		Note: "its hooks are not wired yet: its transcript format is undocumented",
	})
	register(Harness{
		Name: "amp", Title: "Amp",
		MCP:  &MCPSpec{Path: map[string]string{"darwin": "~/.config/amp/settings.json", "linux": "~/.config/amp/settings.json", "windows": "${USERPROFILE}/.config/amp/settings.json"}, Key: []string{"amp.mcpServers"}},
		Note: "its hooks are TypeScript plugins, not wired yet",
	})
	register(Harness{
		Name: "pi", Title: "Pi", Custom: installPi,
		Note: "extension: om compacts on Pi's own cut and Pi stores it; the tool runs through om call",
	})
}

// stdioEntry is the server entry for a client that wants the transport named.
func stdioEntry(exe, h string) map[string]any {
	return map[string]any{"type": "stdio", "command": exe, "args": []any{"mcp", "--harness", h}}
}

// pathTemplate expands a config path: a leading ~ is the home directory, ${VAR:-default} is
// $VAR when set and default otherwise, $VAR is $VAR.
func pathTemplate(t string) string {
	home, _ := os.UserHomeDir()
	out := os.Expand(t, func(v string) string {
		name, def, hasDef := strings.Cut(v, ":-")
		if val := os.Getenv(name); val != "" {
			return val
		}
		if hasDef {
			return def
		}
		return ""
	})
	if out == "~" || strings.HasPrefix(out, "~/") {
		out = filepath.Join(home, strings.TrimPrefix(out, "~"))
	}
	return filepath.FromSlash(out)
}

// pathFor is a spec's path on this GOOS, "" when the harness has none here.
func pathFor(paths map[string]string) string {
	if p, ok := paths[runtime.GOOS]; ok {
		return pathTemplate(p)
	}
	if p, ok := paths[""]; ok {
		return pathTemplate(p)
	}
	return ""
}

// installMCP merges om's server into a harness's JSON MCP config (or takes it out).
func installMCP(w writer, exe, harness string, spec *MCPSpec, remove bool) error {
	path := pathFor(spec.Path)
	if path == "" {
		fmt.Fprintf(w.out, "%s: no MCP config path on %s\n", harness, runtime.GOOS)
		return nil
	}
	cfg, err := readJSONObject(path)
	if err != nil {
		return err
	}
	// Walk (creating) to the servers object.
	parents := []map[string]any{cfg}
	obj := cfg
	for _, k := range spec.Key {
		next, _ := obj[k].(map[string]any)
		if next == nil {
			next = map[string]any{}
		}
		obj[k] = next
		obj = next
		parents = append(parents, next)
	}
	if remove {
		delete(obj, "om")
	} else {
		entry := map[string]any{"command": exe, "args": []any{"mcp", "--harness", harness}}
		if spec.Entry != nil {
			entry = spec.Entry(exe, harness)
		}
		obj["om"] = entry
	}
	// Drop the objects left empty, innermost first.
	for i := len(spec.Key) - 1; i >= 0; i-- {
		if len(parents[i+1]) == 0 {
			delete(parents[i], spec.Key[i])
		}
	}
	data, err := encodeJSON(cfg)
	if err != nil {
		return err
	}
	return w.write(path, data)
}

// isOmHook reports a hook group om wrote for harness.
func isOmHook(group any, harness string) bool {
	g, _ := group.(map[string]any)
	list, _ := g["hooks"].([]any)
	for _, raw := range list {
		h, _ := raw.(map[string]any)
		if cmd, _ := h["command"].(string); strings.Contains(cmd, " hook "+harness+" ") {
			return true
		}
	}
	return false
}

// installHooks merges om's hooks, in Claude Code's schema, into a harness's settings beside any
// others (or takes om's out).
func installHooks(w writer, exe, harness string, spec *HookSpec, remove bool) error {
	path := pathFor(spec.Path)
	if path == "" {
		return nil
	}
	settings, err := readJSONObject(path)
	if err != nil {
		return err
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	events := make([]string, 0, len(spec.Events))
	for ev := range spec.Events {
		events = append(events, ev)
	}
	sort.Strings(events)
	for _, ev := range events {
		name := spec.Events[ev]
		groups, _ := hooks[name].([]any)
		kept := []any{}
		for _, g := range groups {
			if !isOmHook(g, harness) {
				kept = append(kept, g)
			}
		}
		if !remove {
			group := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": strconv.Quote(exe) + " hook " + harness + " " + name, "timeout": hookTimeouts[ev]}}}
			if spec.Matcher != nil {
				group["matcher"] = *spec.Matcher
			}
			kept = append(kept, group)
		}
		if len(kept) == 0 {
			delete(hooks, name)
		} else {
			hooks[name] = kept
		}
	}
	if len(hooks) == 0 {
		delete(settings, "hooks")
	} else {
		settings["hooks"] = hooks
	}
	data, err := encodeJSON(settings)
	if err != nil {
		return err
	}
	return w.write(path, data)
}

// claudeMCP registers the MCP server with Claude Code's own command, at user scope.
func claudeMCP(w writer, exe string, remove bool) error {
	if remove {
		w.run("claude", "mcp", "remove", "--scope", "user", "om")
		return nil
	}
	w.run("claude", "mcp", "add", "--scope", "user", "om", "--", exe, "mcp", "--harness", "claude-code")
	return nil
}

// listHarnesses prints the registry: each harness and what om gives it.
func listHarnesses(out io.Writer) error {
	names := make([]string, 0, len(registry))
	for n := range registry {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		h := registry[n]
		var parts []string
		switch {
		case h.MCP != nil || h.Name == "claude-code" || h.Name == "codex":
			parts = append(parts, "memory tool (MCP)")
		case h.Name == "pi":
			parts = append(parts, "memory tool")
		}
		if h.Hooks != nil {
			for _, ev := range []string{evTurnEnd, evPreCompact, evSessionStart} {
				if _, ok := h.Hooks.Events[ev]; ok {
					parts = append(parts, ev)
				}
			}
		}
		if h.Note != "" {
			parts = append(parts, h.Note)
		}
		fmt.Fprintf(out, "%-16s %-30s %s\n", n, h.Title, strings.Join(parts, "; "))
	}
	return nil
}
