package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
)

// Harness is one coding agent om works with, built from its entry in assets/harnesses.json
// (docs/universal.md, "Harness registry"): where its MCP client reads servers from, which hook
// family it speaks and where those hooks live, and what its transcripts are. What a config
// file cannot say (Codex's TOML and notify program, OpenCode's plugin, Pi's extension, Claude
// Code's MCP command) is the entry's install steps, run as Custom. Adding a harness is one
// entry.
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
	// Custom runs the entry's install steps (installSteps); it runs after MCP and Hooks.
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
	// ID is the field a list entry is identified by, when not "name".
	ID string
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
	// TimeoutKey is a flat command's timeout field ("timeoutSec" by default; Cursor's "timeout");
	// NoTimeout leaves it out where the harness has none (Windsurf).
	TimeoutKey string
	NoTimeout  bool
	// Enable is a settings key path om sets to true on install, for a harness that runs hooks
	// only when they are switched on (Tabnine's hooksConfig.enabled).
	Enable []string
	// NoStdin says the harness passes everything in the environment, so om does not read stdin
	// (which it may leave open: Nanocoder).
	NoStdin bool
	// Key is the object path the events live under ("hooks" when empty; Nanocoder's
	// nanocoder.hooks).
	Key []string
	// RecordUnlessFound records the turn (Record) only when TranscriptFrom finds no transcript,
	// for a harness whose transcript om reads where it can find it (Kiro).
	RecordUnlessFound bool
	// SummaryAtPreCompact prints the engine's summary as plain text at the pre-compaction hook,
	// for a harness that appends that hook's output to its own compaction instructions
	// (CodeBuddy).
	SummaryAtPreCompact bool
	// NoType leaves out the command's "type" field, for a harness whose flat entries have none
	// (Crush).
	NoType bool
	// Version is written as the hooks file's top-level "version" when set (Cursor).
	Version int
	// ContextField names the top-level field context goes back in, for PlainContext
	// ("additionalContext" by default; Cursor's "additional_context"). TextContext writes it as
	// plain stdout instead (Letta Code).
	ContextField string
	TextContext  bool
	// NoContext says no hook output reaches the model, so om writes none (Windsurf).
	NoContext bool
	// SummaryOnStart hands the latest summary at every session start, for a harness whose
	// session start names no source (Cursor).
	SummaryOnStart bool
	// SessionFromCurrent takes a payload with no session id as the session the harness's last
	// hook recorded for its directory (Letta Code's Stop).
	SessionFromCurrent bool
	// EnvFields names environment variables the harness passes payload fields in (field ->
	// variable), read when stdin lacks the field (iFlow CLI).
	EnvFields map[string]string
	// SessionFrom finds the session id in a payload that carries it under its own field.
	SessionFrom func(raw map[string]any) string
	// Record turns a hook payload into the generic transcript lines of the turn it carries, for
	// a harness that keeps no transcript om reads but hands its hooks the turn's text: om keeps
	// that transcript itself (~/.om/transcripts) and reads it in the generic format.
	Record func(raw map[string]any) []map[string]any
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

func init() {
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
	id := spec.ID
	if id == "" {
		id = "name"
	}
	kept := []any{}
	for _, item := range list {
		if m, _ := item.(map[string]any); m == nil || m[id] != "om" {
			kept = append(kept, item)
		}
	}
	if !remove {
		entry := map[string]any{"command": exe, "args": []any{"mcp", "--harness", harness}}
		if spec.Entry != nil {
			entry = spec.Entry(exe, harness)
		}
		entry[id] = "om"
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
	key := spec.Key
	if len(key) == 0 {
		key = []string{"hooks"}
	}
	parent := settings
	for _, k := range key[:len(key)-1] {
		next, _ := parent[k].(map[string]any)
		if next == nil {
			next = map[string]any{}
		}
		parent[k] = next
		parent = next
	}
	last := key[len(key)-1]
	hooks, _ := parent[last].(map[string]any)
	if spec.TopLevel {
		hooks = settings
	}
	if len(spec.Enable) > 0 && !remove {
		obj := settings
		for _, k := range spec.Enable[:len(spec.Enable)-1] {
			next, _ := obj[k].(map[string]any)
			if next == nil {
				next = map[string]any{}
			}
			obj[k] = next
			obj = next
		}
		obj[spec.Enable[len(spec.Enable)-1]] = true
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
			command := map[string]any{"type": "command", "command": hookCommand(exe, harness, name), "timeout": timeout}
			group := map[string]any{"hooks": []any{command}}
			if spec.Flat {
				key := spec.TimeoutKey
				if key == "" {
					key = "timeoutSec"
				}
				command[key] = timeout
				if key != "timeout" {
					delete(command, "timeout")
				}
				if spec.NoType {
					delete(command, "type")
				}
				if spec.NoTimeout {
					delete(command, key)
					delete(command, "type")
				}
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
		delete(parent, last)
		if len(key) > 1 && len(parent) == 0 {
			delete(settings, key[0])
		}
		if spec.Version != 0 && len(settings) == 1 && settings["version"] != nil {
			delete(settings, "version")
		}
	default:
		parent[last] = hooks
		if spec.Version != 0 && settings["version"] == nil {
			settings["version"] = spec.Version
		}
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
	kept, extras := stripManaged(data)
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
	return w.write(path, []byte(out+strings.Join(withExtras(tables, extras), "\n")+"\n"))
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

// rawString reads a payload field, "a.b" naming a nested one.
func rawString(field string) func(raw map[string]any) string {
	return func(raw map[string]any) string {
		var v any = raw
		for _, k := range strings.Split(field, ".") {
			m, _ := v.(map[string]any)
			v = m[k]
		}
		s, _ := v.(string)
		return s
	}
}

// recordFields records a turn from payload fields: each field present (rawString's syntax)
// becomes a generic line of its role, users first.
func recordFields(fields map[string]string) func(raw map[string]any) []map[string]any {
	names := make([]string, 0, len(fields))
	for f := range fields {
		names = append(names, f)
	}
	sort.Slice(names, func(i, j int) bool {
		if fields[names[i]] != fields[names[j]] {
			return fields[names[i]] == "user"
		}
		return names[i] < names[j]
	})
	return func(raw map[string]any) []map[string]any {
		var out []map[string]any
		for _, f := range names {
			if text := strings.TrimSpace(rawString(f)(raw)); text != "" {
				out = append(out, map[string]any{"role": fields[f], "text": text})
			}
		}
		return out
	}
}

// ampRecord records an Amp turn from its agent.end messages: user text, assistant text with its
// tool calls, and each tool result (Amp's plugin message shape: content blocks text, thinking,
// tool_use {id, name, input} and tool_result {toolUseID, output, status}).
func ampRecord(raw map[string]any) []map[string]any {
	msgs, _ := raw["messages"].([]any)
	var out []map[string]any
	for _, m := range msgs {
		msg, _ := m.(map[string]any)
		role, _ := msg["role"].(string)
		blocks, _ := msg["content"].([]any)
		var texts []string
		var tools []any
		for _, b := range blocks {
			block, _ := b.(map[string]any)
			switch block["type"] {
			case "text":
				if t, _ := block["text"].(string); t != "" {
					texts = append(texts, t)
				}
			case "tool_use":
				tools = append(tools, map[string]any{"id": block["id"], "name": block["name"], "arguments": block["input"]})
			case "tool_result":
				outText, ok := block["output"].(string)
				if !ok && block["output"] != nil {
					b, _ := json.Marshal(block["output"])
					outText = string(b)
				}
				out = append(out, map[string]any{"role": "tool", "toolCallId": block["toolUseID"], "text": outText, "isError": block["status"] == "error"})
			}
		}
		text := strings.Join(texts, "\n")
		switch {
		case role == "user" && text != "":
			out = append(out, map[string]any{"role": "user", "text": text})
		case role == "assistant" && (text != "" || len(tools) > 0):
			out = append(out, map[string]any{"role": "assistant", "text": text, "tools": tools})
		}
	}
	return out
}
