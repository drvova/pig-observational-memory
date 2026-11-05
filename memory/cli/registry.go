package cli

import (
	"encoding/json"
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
	// NoStdin says the harness passes everything in the environment, so om does not read stdin
	// (which it may leave open: Nanocoder).
	NoStdin bool
	// Key is the object path the events live under ("hooks" when empty; Nanocoder's
	// nanocoder.hooks).
	Key []string
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
		MCP: &MCPSpec{Path: map[string]string{"darwin": "~/.cursor/mcp.json", "linux": "~/.cursor/mcp.json"}, Key: []string{"mcpServers"}, Entry: stdioEntry},
		Hooks: &HookSpec{Path: map[string]string{"darwin": "~/.cursor/hooks.json", "linux": "~/.cursor/hooks.json"}, Transcript: "cursor", Flat: true, TimeoutKey: "timeout", Version: 1,
			PlainContext: true, ContextField: "additional_context", SummaryOnStart: true,
			Events: map[string]string{evSessionStart: "sessionStart", evTurnEnd: "stop", evPreCompact: "preCompact"}},
		Note:   "IDE and agent CLI hooks; its transcript is lossy (no tool results, ids or timestamps), so level 0 records the requests and replies; the summary comes back at the next session start and through the tool",
		Source: "https://cursor.com/docs/hooks.md",
	})
	register(Harness{
		Name: "devin", Title: "Devin Desktop (Windsurf)",
		MCP: &MCPSpec{Path: map[string]string{"darwin": "${XDG_CONFIG_HOME:-~/.config}/devin/mcp_config.json", "linux": "${XDG_CONFIG_HOME:-~/.config}/devin/mcp_config.json", "windows": "${APPDATA}/devin/mcp_config.json"}, Key: []string{"mcpServers"}},
		Hooks: &HookSpec{Path: map[string]string{"darwin": "~/.codeium/windsurf/hooks.json", "linux": "~/.codeium/windsurf/hooks.json", "windows": "${USERPROFILE}/.codeium/windsurf/hooks.json"}, Transcript: "generic",
			Flat: true, NoTimeout: true, NoContext: true, SessionFrom: rawString("trajectory_id"),
			Record: recordFields(map[string]string{"tool_info.user_prompt": "user", "tool_info.response": "assistant"}),
			Events: map[string]string{evPrompt: "pre_user_prompt", evTurnEnd: "post_cascade_response"}},
		Note:   "Cascade hooks: om records each prompt and Cascade's response summary itself (no transcript it reads); no compaction hook and no hook output reaches the model, so memory is the tool's",
		Source: "https://docs.devin.ai/zh/desktop/cascade/hooks.md",
	})
	register(Harness{
		Name: "cline", Title: "Cline",
		MCP:    &MCPSpec{Path: map[string]string{"darwin": "${CLINE_MCP_SETTINGS_PATH:-~/.cline/data/settings/cline_mcp_settings.json}", "linux": "${CLINE_MCP_SETTINGS_PATH:-~/.cline/data/settings/cline_mcp_settings.json}"}, Key: []string{"mcpServers"}},
		Custom: installClineHook,
		Hooks: &HookSpec{Transcript: "cline", NoContext: true, SessionFrom: rawString("sessionContext.rootSessionId"), TranscriptFrom: transcript.ClineMessagesPath,
			Events: map[string]string{evTurnEnd: "TaskComplete"}},
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
		MCP:    &MCPSpec{Path: map[string]string{"darwin": "~/.kiro/settings/mcp.json", "linux": "~/.kiro/settings/mcp.json"}, Key: []string{"mcpServers"}},
		Custom: installKiroHooks,
		Hooks: &HookSpec{Transcript: "generic", TextContext: true, Record: recordFields(map[string]string{"prompt": "user"}),
			Events: map[string]string{evSessionStart: "SessionStart", evPrompt: "UserPromptSubmit", evTurnEnd: "Stop"}},
		Note:   "hooks in ~/.kiro/hooks/om.json: its payloads carry the prompt but not the reply and name no transcript om reads, so om records each request itself; no compaction hook, so memory is the tool's",
		Source: "https://kiro.dev/docs/hooks.md",
	})
	register(Harness{
		Name: "amazon-q", Title: "Amazon Q Developer CLI",
		MCP:    &MCPSpec{Path: map[string]string{"darwin": "~/.aws/amazonq/mcp.json", "linux": "~/.aws/amazonq/mcp.json"}, Key: []string{"mcpServers"}},
		Custom: printAmazonQHooks,
		Hooks: &HookSpec{Transcript: "amazon-q", TranscriptFrom: transcript.AmazonQDBPath, NoContext: true,
			SessionFrom: func(raw map[string]any) string {
				cwd, _ := raw["cwd"].(string)
				return transcript.AmazonQConversation(cwd)
			},
			Events: map[string]string{evTurnEnd: "stop"}},
		Note:   "read by an agent only with useLegacyMcpJson: true; its stop hook goes in your agent file (om install prints it) and om reads the directory's conversation row (sqlite3 command or python3); Q CLI is being replaced by Kiro",
		Source: "https://github.com/aws/amazon-q-developer-cli/blob/main/docs/hooks.md",
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
		MCP: &MCPSpec{Path: map[string]string{"darwin": "~/.augment/settings.json", "linux": "~/.augment/settings.json"}, Key: []string{"mcpServers"}, Entry: stdioEntry},
		Hooks: &HookSpec{Path: map[string]string{"darwin": "~/.augment/settings.json", "linux": "~/.augment/settings.json"}, Transcript: "auggie", TimeoutMS: true, PlainContext: true,
			TranscriptFrom: transcript.AuggieSessionPath, Events: map[string]string{evSessionStart: "SessionStart", evTurnEnd: "Stop"}},
		Note:   "om reads the session file Auggie keeps for the conversation (~/.augment/sessions); no compaction hook, so memory is the tool's and the session-start context",
		Source: "https://docs.augmentcode.com/cli/hooks",
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
		MCP:    &MCPSpec{Path: map[string]string{"darwin": "~/.config/amp/settings.json", "linux": "~/.config/amp/settings.json", "windows": "${USERPROFILE}/.config/amp/settings.json"}, Key: []string{"amp.mcpServers"}},
		Custom: assetInstaller("amp", "${XDG_CONFIG_HOME:-~/.config}/amp/plugins", "assets/amp/om.ts"),
		Hooks:  &HookSpec{Transcript: "generic", NoContext: true, Record: ampRecord, Events: map[string]string{evTurnEnd: "agent.end"}},
		Note:   "plugin: Amp keeps threads on its servers, so om records each turn's messages from agent.end itself; no compaction event, so memory is the tool's (plugin not yet run inside Amp)",
		Source: "https://ampcode.com/docs/plugin-api",
	})
	register(Harness{
		Name: "goose", Title: "Goose",
		YAML: &YAMLSpec{Path: map[string]string{"darwin": "${XDG_CONFIG_HOME:-~/.config}/goose/config.yaml", "linux": "${XDG_CONFIG_HOME:-~/.config}/goose/config.yaml", "windows": "${APPDATA}/Block/goose/config/config.yaml"}, Key: "extensions",
			Entry: func(exe, h string) map[string]any {
				return map[string]any{"type": "stdio", "name": "om", "enabled": true, "cmd": exe, "args": []any{"mcp", "--harness", h}, "envs": map[string]any{}, "env_keys": []any{}, "timeout": 300}
			}},
		Custom: installGoosePlugin,
		Hooks:  &HookSpec{Transcript: "goose", TranscriptFrom: transcript.GooseDBPath, NoContext: true, Events: map[string]string{evTurnEnd: "Stop"}},
		Note:   "a plugin's Stop hook: om reads its SQLite sessions (with the sqlite3 command or python3; Linux path confirmed); no compaction hook and no hook output reaches the model, so memory is the tool's",
		Source: "https://github.com/block/goose/blob/main/documentation/docs/guides/context-engineering/hooks.md",
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
		Hooks: &HookSpec{Path: map[string]string{"darwin": "${CRUSH_GLOBAL_CONFIG:-~/.config/crush}/crush.json", "linux": "${CRUSH_GLOBAL_CONFIG:-~/.config/crush}/crush.json"}, Transcript: "crush", TranscriptFrom: transcript.CrushDBPath,
			Flat: true, TimeoutKey: "timeout", NoType: true, NoContext: true, Events: map[string]string{evTurnEnd: "TurnEnd", evPreCompact: "PreCompact"}},
		Note:   "TurnEnd and PreCompact hooks over its SQLite sessions (read with the sqlite3 command or python3); its turn hooks cannot hand the model context, so the summary is the tool's; crush run fires no hooks",
		Source: "https://github.com/charmbracelet/crush/blob/main/docs/hooks/README.md",
	})
	register(Harness{
		Name: "qoder-cli", Title: "Qoder CLI",
		MCP:    &MCPSpec{Path: map[string]string{"darwin": "${QODER_CONFIG_DIR:-~/.qoder}/settings.json", "linux": "${QODER_CONFIG_DIR:-~/.qoder}/settings.json"}, Key: []string{"mcpServers"}},
		Hooks:  &HookSpec{Path: map[string]string{"darwin": "${QODER_CONFIG_DIR:-~/.qoder}/settings.json", "linux": "${QODER_CONFIG_DIR:-~/.qoder}/settings.json"}, Events: claudeEventNames, Transcript: "qoder"},
		Note:   "Claude Code-compatible hooks over its Claude-shaped transcript (Qwen Code lineage); restart Qoder CLI after installing",
		Source: "https://docs.qoder.com/cli/hooks",
	})
	register(Harness{
		Name: "neovate-code", Title: "Neovate Code",
		MCP:    &MCPSpec{Path: map[string]string{"darwin": "~/.neovate/config.json", "linux": "~/.neovate/config.json"}, Key: []string{"mcpServers"}, Entry: plainEntry},
		Custom: assetInstaller("neovate-code", "~/.neovate/plugins", "assets/neovate/om.js"),
		Note:   "plugin: its stop hook hands om each turn over Neovate's own session log (level 0); no compaction hook, so memory is the tool's (plugin not yet run inside Neovate)",
		Source: "https://github.com/neovateai/neovate-code/blob/master/src/plugin.ts",
	})
	register(Harness{
		Name: "iflow-cli", Title: "iFlow CLI",
		MCP: &MCPSpec{Path: map[string]string{"darwin": "${IFLOW_HOME:-~/.iflow}/settings.json", "linux": "${IFLOW_HOME:-~/.iflow}/settings.json"}, Key: []string{"mcpServers"}},
		Hooks: &HookSpec{Path: map[string]string{"darwin": "${IFLOW_HOME:-~/.iflow}/settings.json", "linux": "${IFLOW_HOME:-~/.iflow}/settings.json"}, Transcript: "iflow",
			EnvFields: map[string]string{"session_id": "IFLOW_SESSION_ID", "transcript_path": "IFLOW_TRANSCRIPT_PATH", "cwd": "IFLOW_CWD"},
			Events:    map[string]string{evSessionStart: "SessionStart", evTurnEnd: "Stop"}},
		Note:   "Claude-style hooks over its Claude-style transcript; no compaction hook, so memory is the tool's; iFlow checks hook commands against an allowlist (if om is refused, allow it there)",
		Source: "https://github.com/iflow-ai/iflow-cli/blob/main/docs_en/examples/hooks.md",
	})
	register(Harness{
		Name: "cortex-code", Title: "Cortex Code CLI (Snowflake CoCo)",
		MCP: &MCPSpec{Path: map[string]string{"darwin": "${SNOWFLAKE_HOME:-~/.snowflake}/cortex/mcp.json", "linux": "${SNOWFLAKE_HOME:-~/.snowflake}/cortex/mcp.json"}, Key: []string{"mcpServers"}, Entry: stdioEntry},
		Hooks: &HookSpec{Path: map[string]string{"darwin": "${SNOWFLAKE_HOME:-~/.snowflake}/cortex/hooks.json", "linux": "${SNOWFLAKE_HOME:-~/.snowflake}/cortex/hooks.json"}, Events: claudeEventNames,
			Transcript: "cortex", TranscriptFrom: transcript.CortexHistoryPath, SummaryOnPrompt: true},
		Note:   "Claude-shaped hooks over its <session>.history.jsonl; its SessionStart after a compaction drops context, so om's summary comes back with the next prompt; user MCP servers need the account administrator's allowance",
		Source: "https://docs.snowflake.com/en/user-guide/cortex-code/extensibility",
	})
	register(Harness{
		Name: "codebuddy", Title: "CodeBuddy Code (CLI)",
		MCP: &MCPSpec{Path: map[string]string{"darwin": "~/.codebuddy/.mcp.json", "linux": "~/.codebuddy/.mcp.json"}, Key: []string{"mcpServers"}, Entry: stdioEntry},
		Hooks: &HookSpec{Path: map[string]string{"darwin": "~/.codebuddy/settings.json", "linux": "~/.codebuddy/settings.json"}, Events: claudeEventNames, Transcript: "generic", SummaryAtPreCompact: true,
			Record: recordFields(map[string]string{"prompt": "user", "last_assistant_message": "assistant"})},
		Note:   "Claude Code-compatible hooks; its transcript's fields are unconfirmed, so om records each prompt and reply itself; at PreCompact om's summary joins CodeBuddy's compaction instructions",
		Source: "https://www.codebuddy.cn/docs/cli/hooks",
	})
	register(Harness{
		Name: "junie", Title: "JetBrains Junie (IDE and CLI)",
		MCP: &MCPSpec{Path: map[string]string{"darwin": "${JUNIE_HOME:-~/.junie}/mcp/mcp.json", "linux": "${JUNIE_HOME:-~/.junie}/mcp/mcp.json"}, Key: []string{"mcpServers"}, Entry: plainEntry},
		Hooks: &HookSpec{Path: map[string]string{"darwin": "${JUNIE_HOME:-~/.junie}/config.json", "linux": "${JUNIE_HOME:-~/.junie}/config.json"}, Transcript: "generic", SessionFromCurrent: true,
			Record: recordFields(map[string]string{"prompt": "user", "last_assistant_message": "assistant"}),
			Events: map[string]string{evSessionStart: "SessionStart", evPrompt: "UserPromptSubmit", evTurnEnd: "Stop"}},
		Note:   "Junie CLI hooks (Early Access): om records each prompt and task reply itself (Stop fires for tasks, not chat, and names no session: om takes the one the prompt hook recorded); no compaction hook",
		Source: "https://junie.jetbrains.com/docs/junie-cli-hooks.html",
	})
	register(Harness{
		Name: "nanocoder", Title: "Nanocoder",
		MCP: &MCPSpec{Path: map[string]string{"darwin": "${NANOCODER_CONFIG_DIR:-~/Library/Preferences/nanocoder}/.mcp.json", "linux": "${NANOCODER_CONFIG_DIR:-~/.config/nanocoder}/.mcp.json"}, Key: []string{"mcpServers"}, Entry: func(exe, h string) map[string]any {
			return map[string]any{"transport": "stdio", "command": exe, "args": []any{"mcp", "--harness", h}}
		}},
		Hooks: &HookSpec{Path: map[string]string{"darwin": "${NANOCODER_CONFIG_DIR:-~/Library/Preferences/nanocoder}/agents.config.json", "linux": "${NANOCODER_CONFIG_DIR:-~/.config/nanocoder}/agents.config.json"}, Key: []string{"nanocoder", "hooks"},
			Transcript: "generic", Flat: true, TimeoutKey: "timeout", TimeoutMS: true, NoType: true, TextContext: true, SummaryOnPrompt: true, NoStdin: true,
			EnvFields: map[string]string{"session_id": "NANOCODER_SESSION_ID", "cwd": "NANOCODER_CWD", "prompt": "NANOCODER_PROMPT"},
			Record:    recordFields(map[string]string{"prompt": "user"}),
			Events:    map[string]string{evSessionStart: "session-start", evPrompt: "user-prompt-submit", evPreCompact: "pre-compact"}},
		Note:   "hooks pass the prompt in the environment and there is no turn-end hook or readable transcript, so om records each request itself (level 0 runs at the next prompt and at pre-compact); the summary comes back with the next prompt; MCP tools reach the model only with the full /tune profile",
		Source: "https://github.com/Nano-Collective/nanocoder/blob/main/docs/features/hooks.md",
	})
	register(Harness{
		Name: "trae", Title: "Trae", Manual: "Settings > MCP > Add > Add Manually, and paste it under mcpServers",
		Hooks: &HookSpec{Path: map[string]string{"darwin": "~/.trae/hooks.json", "linux": "~/.trae/hooks.json", "windows": "${USERPROFILE}/.trae/hooks.json"}, Transcript: "generic",
			Record: recordFields(map[string]string{"prompt": "user", "last_assistant_message": "assistant"}),
			Events: map[string]string{evSessionStart: "SessionStart", evPrompt: "UserPromptSubmit", evTurnEnd: "Stop"}},
		Note:   "Claude-style hooks: om records each prompt and reply itself (no transcript it reads); no compaction hook, so memory is the tool's and the session-start context",
		Source: "https://docs.trae.ai/ide/hook-configuration-reference",
	})
	register(Harness{
		Name: "letta-code", Title: "Letta Code", Manual: "Letta Code app: connect an MCP server to the agent (/mcp)",
		Hooks: &HookSpec{Path: map[string]string{"darwin": "~/.letta/settings.json", "linux": "~/.letta/settings.json"}, Transcript: "generic", TimeoutMS: true,
			TextContext: true, SummaryOnPrompt: true, SessionFromCurrent: true, SessionFrom: lettaSession,
			Record: recordFields(map[string]string{"user_message": "user", "assistant_message": "assistant"}),
			Events: claudeEventNames},
		Note:   "om records each turn from Stop (which names no session: om takes the one the prompt hook recorded for the directory) and compacts at PreCompact; the summary comes back with the next prompt",
		Source: "https://github.com/letta-ai/letta-code/blob/main/src/hooks/types.ts",
	})
	register(Harness{
		Name: "open-interpreter", Title: "Open Interpreter (terminal agent)",
		TOML:   &TOMLSpec{Path: map[string]string{"darwin": "${INTERPRETER_HOME:-~/.openinterpreter}/config.toml", "linux": "${INTERPRETER_HOME:-~/.openinterpreter}/config.toml"}, Table: "mcp_servers", Entry: plainEntry},
		Hooks:  &HookSpec{Path: map[string]string{"darwin": "${INTERPRETER_HOME:-~/.openinterpreter}/hooks.json", "linux": "${INTERPRETER_HOME:-~/.openinterpreter}/hooks.json"}, Events: claudeEventNames, Transcript: "codex"},
		Note:   "a Codex fork: its rollout and Claude-style hooks; approve om's hooks once in /hooks (new hooks are untrusted); the summary comes back at the SessionStart after its compaction (source compact)",
		Source: "https://github.com/openinterpreter/open-interpreter/blob/main/docs/hooks.md",
	})
	register(Harness{
		Name: "openclaude", Title: "OpenClaude",
		MCP:    &MCPSpec{Path: map[string]string{"darwin": "${OPENCLAUDE_CONFIG_DIR:-~}/.openclaude.json", "linux": "${OPENCLAUDE_CONFIG_DIR:-~}/.openclaude.json"}, Key: []string{"mcpServers"}, Entry: stdioEntry},
		Hooks:  &HookSpec{Path: map[string]string{"darwin": "${OPENCLAUDE_CONFIG_DIR:-~/.openclaude}/settings.json", "linux": "${OPENCLAUDE_CONFIG_DIR:-~/.openclaude}/settings.json"}, Events: claudeEventNames, Transcript: "claude-code"},
		Note:   "Claude Code's source: its hooks and transcript, so om's summary is handed back at the SessionStart after its compaction (source compact)",
		Source: "https://raw.githubusercontent.com/Gitlawb/openclaude/main/src/services/mcp/config.ts",
	})
	register(Harness{
		Name: "deepagents-code", Title: "Deep Agents Code (LangChain dcode)",
		MCP:    &MCPSpec{Path: map[string]string{"darwin": "${DEEPAGENTS_HOME:-~/.deepagents}/.mcp.json", "linux": "${DEEPAGENTS_HOME:-~/.deepagents}/.mcp.json"}, Key: []string{"mcpServers"}},
		Hooks:  &HookSpec{Path: map[string]string{"darwin": "${DEEPAGENTS_HOME:-~/.deepagents}/hooks.json", "linux": "${DEEPAGENTS_HOME:-~/.deepagents}/hooks.json"}, Events: claudeEventNames, Transcript: "deepagents"},
		Note:   "its hook transcript records no tool call ids (results pair with calls by tool name); om's summary is handed back at the SessionStart after its compaction (source compact)",
		Source: "https://github.com/langchain-ai/deepagents/blob/master/libs/code/HOOKS.md",
	})
	register(Harness{
		Name: "kode", Title: "Kode (shareAI-lab)",
		MCP: &MCPSpec{Path: map[string]string{"darwin": "~/.kode.json", "linux": "~/.kode.json"}, Key: []string{"mcpServers"}, Entry: stdioEntry},
		Hooks: &HookSpec{Path: map[string]string{"darwin": "${KODE_CONFIG_DIR:-~/.kode}/settings.json", "linux": "${KODE_CONFIG_DIR:-~/.kode}/settings.json"}, Transcript: "claude-code", TranscriptFrom: transcript.KodeSessionPath,
			Events: map[string]string{evTurnEnd: "Stop", evPreCompact: "PreCompact"}},
		Note:   "Stop and PreCompact only (Kode runs SessionStart hooks from plugins alone): om reads its Claude-format session log, and the summary is reached through the tool",
		Source: "https://github.com/shareAI-lab/Kode-cli/blob/main/packages/protocol/src/utils/kodeAgentSessionLog.ts",
	})
	register(Harness{
		Name: "oh-my-pi", Title: "Oh My Pi", Custom: piExtensionInstaller("oh-my-pi", "${PI_CONFIG_DIR:-~/.omp}/agent/extensions"),
		Note:   "Pi's extension, which Oh My Pi loads from its own agent directory: om compacts on its cut; the default profile only",
		Source: "https://github.com/can1357/oh-my-pi/blob/main/docs/extension-loading.md",
	})
	register(Harness{
		Name: "gentle-shell", Title: "Gentle Shell", Custom: piExtensionInstaller("gentle-shell", "${GENTLE_SHELL_HOME:-~/.gentle-shell/agent}/extensions"),
		Note:   "Pi's extension in Gentle Shell's own home (with --link it uses Pi's: om install pi)",
		Source: "https://github.com/Gentleman-Programming/gentle-shell",
	})
	register(Harness{
		Name: "pig", Title: "PiG", Custom: installPiG,
		Note: "the engine runs inside PiG as its extension, with level 1 on the session model: om install runs pig install",
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

// plainEntry is the common server entry, {"command", "args"}.
func plainEntry(exe, harness string) map[string]any {
	return map[string]any{"command": exe, "args": []any{"mcp", "--harness", harness}}
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

// lettaSession is Letta Code's conversation key: conversation:<id>, or default:<agent> for an
// agent's default conversation (src/backend/local/local-conversation-key.ts).
func lettaSession(raw map[string]any) string {
	conv, _ := raw["conversation_id"].(string)
	agent, _ := raw["agent_id"].(string)
	switch {
	case conv != "" && conv != "default":
		return "conversation:" + conv
	case agent != "":
		return "default:" + agent
	}
	return ""
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
