package cli

import (
	"errors"
	"fmt"
	"github.com/drvova/pig-observational-memory/memory/transcript"
	"io"
	"io/fs"
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
	// YAML registers it in a YAML config instead.
	YAML *YAMLSpec
	// TOML registers it as a TOML table instead.
	TOML *TOMLSpec
	// Own writes a file of om's own, for a harness that loads every file of a pattern.
	Own *OwnSpec
	// Hooks registers om's lifecycle hooks; nil for a harness without hooks.
	Hooks *HookSpec
	// Custom installs (or removes) what data cannot say; it runs after MCP and Hooks.
	Custom func(w writer, exe string, remove bool) error
	// Note is shown by om harnesses: what the harness gets beyond its columns, or what it lacks.
	Note string
	// Manual is set for a harness whose MCP servers are added only in its own interface (or in a
	// file om does not rewrite): om install prints these steps with the server's command and
	// writes nothing.
	Manual string
	// Source is where its configuration is documented.
	Source string
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
	// List says the servers are a list of entries each carrying its "name", not a map by name.
	List bool
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
	// TimeoutMS says the harness reads hook timeouts in milliseconds (Gemini CLI).
	TimeoutMS bool
	// TopLevel says the hooks file is keyed by event name itself, not under "hooks" (Factory
	// Droid's hooks.json).
	TopLevel bool
	// Flat says each event lists commands directly, not matcher groups of them, with the timeout
	// as timeoutSec (Copilot).
	Flat bool
	// PlainContext says context goes back as a top-level additionalContext, not under
	// hookSpecificOutput (Copilot).
	PlainContext bool
	// TOML says the hooks are [[hooks]] tables in a TOML config (Kimi CLI), not Claude's JSON.
	TOML bool
	// TranscriptFrom finds the session's transcript from the payload's cwd and session id, and is
	// read over any path the payload names (Kimi CLI names none; Copilot's documented session log
	// is its events.jsonl).
	TranscriptFrom func(cwd, sessionID string) string
	// SummaryOnPrompt hands a compaction's summary to the model at the next prompt, for a harness
	// whose session-start does not run again after it compacts (Gemini CLI).
	SummaryOnPrompt bool
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
		Note: "Cursor's hooks are not wired: its transcript keeps no entry ids, timestamps or tool results, and folds reasoning into the reply",
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
		Hooks: &HookSpec{Path: map[string]string{"darwin": "${COPILOT_HOME:-~/.copilot}/settings.json", "linux": "${COPILOT_HOME:-~/.copilot}/settings.json"}, Transcript: "copilot", Flat: true, PlainContext: true,
			TranscriptFrom: func(_, sessionID string) string { return transcript.CopilotEventsPath(sessionID) },
			Events:         map[string]string{evSessionStart: "sessionStart", evTurnEnd: "agentStop", evPreCompact: "preCompact"}},
		Note: "the CLI's hooks: om compacts at preCompact; Copilot's sessionStart never follows its compaction, so om's summary comes back on resume and through the tool",
	})
	register(Harness{
		Name: "claude-desktop", Title: "Claude Desktop",
		MCP: &MCPSpec{Path: map[string]string{"darwin": "~/Library/Application Support/Claude/claude_desktop_config.json", "linux": "${XDG_CONFIG_HOME:-~/.config}/Claude/claude_desktop_config.json", "windows": "${APPDATA}/Claude/claude_desktop_config.json"}, Key: []string{"mcpServers"}},
	})
	register(Harness{
		Name: "gemini-cli", Title: "Gemini CLI",
		MCP: &MCPSpec{Path: map[string]string{"darwin": "~/.gemini/settings.json", "linux": "~/.gemini/settings.json"}, Key: []string{"mcpServers"}},
		Hooks: &HookSpec{Path: map[string]string{"darwin": "~/.gemini/settings.json", "linux": "~/.gemini/settings.json"}, Transcript: "gemini", TimeoutMS: true, SummaryOnPrompt: true,
			Events: map[string]string{evSessionStart: "SessionStart", evPrompt: "BeforeAgent", evTurnEnd: "AfterAgent", evPreCompact: "PreCompress"}},
		Note: "compaction: om compacts at PreCompress and hands its summary to the model at the next prompt",
	})
	register(Harness{
		Name: "qwen-code", Title: "Qwen Code",
		MCP:   &MCPSpec{Path: map[string]string{"darwin": "~/.qwen/settings.json", "linux": "~/.qwen/settings.json"}, Key: []string{"mcpServers"}},
		Hooks: &HookSpec{Path: map[string]string{"darwin": "~/.qwen/settings.json", "linux": "~/.qwen/settings.json"}, Events: claudeEventNames, Transcript: "qwen"},
		Note:  "compaction: om's summary is handed back at the SessionStart after Qwen's (source compact)",
	})
	register(Harness{
		Name: "kiro", Title: "Kiro",
		MCP:  &MCPSpec{Path: map[string]string{"darwin": "~/.kiro/settings/mcp.json", "linux": "~/.kiro/settings/mcp.json"}, Key: []string{"mcpServers"}},
		Note: "its hooks carry no transcript path (sessions are SQLite, schema unpublished) and it has no compaction hook, so they are not wired",
	})
	register(Harness{
		Name: "amazon-q", Title: "Amazon Q Developer CLI",
		MCP:  &MCPSpec{Path: map[string]string{"darwin": "~/.aws/amazonq/mcp.json", "linux": "~/.aws/amazonq/mcp.json"}, Key: []string{"mcpServers"}},
		Note: "read by an agent only with useLegacyMcpJson: true (Q CLI is being replaced by Kiro)",
	})
	register(Harness{
		Name: "factory-droid", Title: "Factory Droid",
		MCP:   &MCPSpec{Path: map[string]string{"darwin": "~/.factory/mcp.json", "linux": "~/.factory/mcp.json"}, Key: []string{"mcpServers"}, Entry: stdioEntry},
		Hooks: &HookSpec{Path: map[string]string{"darwin": "~/.factory/hooks.json", "linux": "~/.factory/hooks.json"}, Events: claudeEventNames, Transcript: "droid", TopLevel: true},
		Note:  "compaction: om's summary is handed back at the SessionStart after Droid's (source compact)",
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
		MCP: &MCPSpec{Path: map[string]string{"darwin": "~/.kimi/mcp.json", "linux": "~/.kimi/mcp.json"}, Key: []string{"mcpServers"}},
		Hooks: &HookSpec{Path: map[string]string{"darwin": "~/.kimi/config.toml", "linux": "~/.kimi/config.toml"}, Events: claudeEventNames, Transcript: "kimi", TOML: true,
			TranscriptFrom: transcript.KimiWirePath},
		Note: "Kimi's hooks cannot hand the model context: om records each turn and compacts with Kimi, and memory is read through the tool",
	})
	register(Harness{
		Name: "amp", Title: "Amp",
		MCP:  &MCPSpec{Path: map[string]string{"darwin": "~/.config/amp/settings.json", "linux": "~/.config/amp/settings.json", "windows": "${USERPROFILE}/.config/amp/settings.json"}, Key: []string{"amp.mcpServers"}},
		Note: "its hooks are TypeScript plugins, not wired yet",
	})
	register(Harness{
		Name: "goose", Title: "Goose",
		YAML: &YAMLSpec{Path: map[string]string{"darwin": "${XDG_CONFIG_HOME:-~/.config}/goose/config.yaml", "linux": "${XDG_CONFIG_HOME:-~/.config}/goose/config.yaml", "windows": "${APPDATA}/Block/goose/config/config.yaml"}, Key: "extensions",
			Entry: func(exe, h string) map[string]any {
				return map[string]any{"type": "stdio", "name": "om", "enabled": true, "cmd": exe, "args": []any{"mcp", "--harness", h}, "envs": map[string]any{}, "env_keys": []any{}, "timeout": 300}
			}},
		Note: "its hooks carry no transcript om reads (sessions are SQLite) and cannot hand the model context",
	})
	register(Harness{
		Name: "continue", Title: "Continue",
		YAML: &YAMLSpec{Path: map[string]string{"darwin": "~/.continue/config.yaml", "linux": "~/.continue/config.yaml", "windows": "${USERPROFILE}/.continue/config.yaml"}, Key: "mcpServers", List: true,
			Entry: func(exe, h string) map[string]any {
				return map[string]any{"command": exe, "args": []any{"mcp", "--harness", h}}
			},
			Skeleton: map[string]any{"name": "Local config", "version": "1.0.0", "schema": "v1"}},
	})
	register(Harness{
		Name: "openhands", Title: "OpenHands",
		Manual: "add it under Settings, MCP (OpenHands stores servers with encrypted credentials and says not to edit the file)",
	})
	register(Harness{
		Name: "roo-code", Title: "Roo Code",
		Manual: "open the MCP panel, Edit Global MCP, and add it under mcpServers (the global file's path is not documented)",
	})
	register(Harness{
		Name: "crush", Title: "Crush", Custom: installCrush,
		Note: "its session-start and turn-start hooks cannot hand the model context, so they are not wired",
	})
	register(Harness{
		Name: "pi", Title: "Pi", Custom: installPi,
		Note: "extension: om compacts on Pi's own cut and Pi stores it; the tool runs through om call",
	})
	// The data harnesses join after the ones declared here, so a name in both is caught.
	data, err := assets.ReadFile("assets/harnesses.json")
	if err == nil {
		err = loadData(data)
	}
	if err != nil {
		panic(err)
	}
}

// OwnSpec is a file om owns whole: install writes it, uninstall removes it, and nothing of the
// user's is ever edited.
type OwnSpec struct {
	Path    map[string]string
	Content func(exe, harness string) string
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
	if spec.List {
		return installMCPList(w, exe, harness, spec, cfg, path, remove)
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

// installMCPList is installMCP for servers kept as a list of named entries.
func installMCPList(w writer, exe, harness string, spec *MCPSpec, cfg map[string]any, path string, remove bool) error {
	obj := cfg
	last := len(spec.Key) - 1
	for _, k := range spec.Key[:last] {
		next, _ := obj[k].(map[string]any)
		if next == nil {
			next = map[string]any{}
		}
		obj[k] = next
		obj = next
	}
	list, _ := obj[spec.Key[last]].([]any)
	kept := []any{}
	for _, item := range list {
		if m, _ := item.(map[string]any); m == nil || m["name"] != "om" {
			kept = append(kept, item)
		}
	}
	if !remove {
		entry := map[string]any{"command": exe, "args": []any{"mcp", "--harness", harness}}
		if spec.Entry != nil {
			entry = spec.Entry(exe, harness)
		}
		entry["name"] = "om"
		kept = append(kept, entry)
	}
	if len(kept) == 0 {
		delete(obj, spec.Key[last])
	} else {
		obj[spec.Key[last]] = kept
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
	for _, raw := range append([]any{g}, list...) {
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
	if spec.TOML {
		return installTOMLHooks(w, exe, harness, spec, path, remove)
	}
	settings, err := readJSONObject(path)
	if err != nil {
		return err
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if spec.TopLevel {
		hooks = settings
	}
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
			timeout := hookTimeouts[ev]
			if spec.TimeoutMS {
				timeout *= 1000
			}
			command := map[string]any{"type": "command", "command": strconv.Quote(exe) + " hook " + harness + " " + name, "timeout": timeout}
			group := map[string]any{"hooks": []any{command}}
			if spec.Flat {
				command["timeoutSec"] = command["timeout"]
				delete(command, "timeout")
				group = command
			}
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
	switch {
	case spec.TopLevel:
	case len(hooks) == 0:
		delete(settings, "hooks")
	default:
		settings["hooks"] = hooks
	}
	data, err := encodeJSON(settings)
	if err != nil {
		return err
	}
	return w.write(path, data)
}

// installTOMLHooks writes om's hooks as [[hooks]] tables (event, command, timeout in seconds) at
// the end of a TOML config, every line marked as om's, and takes out exactly those lines.
func installTOMLHooks(w writer, exe, harness string, spec *HookSpec, path string, remove bool) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var kept []string
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if !strings.HasSuffix(line, managedMark) && (line != "" || len(kept) > 0) {
			kept = append(kept, line)
		}
	}
	body := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if remove {
		if body == "" {
			return w.write(path, nil)
		}
		return w.write(path, []byte(body+"\n"))
	}
	events := make([]string, 0, len(spec.Events))
	for ev := range spec.Events {
		events = append(events, ev)
	}
	sort.Strings(events)
	var tables []string
	for _, ev := range events {
		name := spec.Events[ev]
		tables = append(tables,
			"[[hooks]] "+managedMark,
			"event = "+strconv.Quote(name)+" "+managedMark,
			"command = "+strconv.Quote(shellQuote(exe)+" hook "+harness+" "+name)+" "+managedMark,
			"timeout = "+strconv.Itoa(hookTimeouts[ev])+" "+managedMark)
	}
	out := ""
	if body != "" {
		out = body + "\n\n"
	}
	return w.write(path, []byte(out+strings.Join(tables, "\n")+"\n"))
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
		case h.Manual != "":
			parts = append(parts, "memory tool (MCP, added by hand: om install prints the steps)")
		case h.MCP != nil || h.YAML != nil || h.TOML != nil || h.Own != nil || h.Name == "claude-code" || h.Name == "codex":
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
