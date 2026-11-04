package cli

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// assets are the adapters om writes for harnesses that load a plugin or an extension file.
//
//go:embed assets/opencode/om.js assets/pi/om.ts
var assets embed.FS

// managedMark marks every line om writes into a harness's TOML config, so uninstall removes
// exactly those.
const managedMark = "# om:managed"

// install wires om into a harness (or, with remove, takes out what install wrote), printing
// each change; with dry nothing is written.
func install(harnessName string, remove, dry bool, out io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	w := writer{dry: dry, out: out}
	switch harnessName {
	case "claude-code":
		return installClaude(w, exe, remove)
	case "codex":
		return installCodex(w, exe, remove)
	case "opencode":
		return installOpenCode(w, exe, remove)
	case "pi":
		return installPi(w, exe, remove)
	}
	return fmt.Errorf("om install: unknown harness %q (claude-code, codex, opencode, pi)", harnessName)
}

// writer writes config files, keeping a one-time backup of what was there.
type writer struct {
	dry bool
	out io.Writer
}

func (w writer) write(path string, data []byte) error {
	old, err := os.ReadFile(path)
	if err == nil && bytes.Equal(old, data) {
		fmt.Fprintf(w.out, "unchanged %s\n", path)
		return nil
	}
	fmt.Fprintf(w.out, "write %s\n", path)
	if w.dry {
		return nil
	}
	if err == nil {
		if _, statErr := os.Stat(path + ".om-backup"); errors.Is(statErr, fs.ErrNotExist) {
			if err := os.WriteFile(path+".om-backup", old, 0o600); err != nil {
				return err
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (w writer) remove(path string) error {
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	fmt.Fprintf(w.out, "remove %s\n", path)
	if w.dry {
		return nil
	}
	return os.Remove(path)
}

func (w writer) run(name string, args ...string) {
	line := name + " " + strings.Join(args, " ")
	if _, err := exec.LookPath(name); err != nil {
		fmt.Fprintf(w.out, "%s is not on PATH; run: %s\n", name, line)
		return
	}
	fmt.Fprintf(w.out, "run %s\n", line)
	if w.dry {
		return
	}
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		fmt.Fprintf(w.out, "  %v: %s\n", err, strings.TrimSpace(string(out)))
	}
}

func homeDir(env string, parts ...string) string {
	if dir := os.Getenv(env); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(append([]string{home}, parts...)...)
}

func readJSONObject(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	obj := map[string]any{}
	if len(bytes.TrimSpace(data)) == 0 {
		return obj, nil
	}
	if err := json.Unmarshal(data, &obj); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object: %w", path, err)
	}
	return obj, nil
}

func encodeJSON(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	return append(data, '\n'), err
}

// claudeEvents are the Claude Code hooks om answers, with each one's timeout in seconds: the
// compaction reads the whole transcript.
var claudeEvents = []struct {
	name    string
	timeout int
}{{"SessionStart", 60}, {"UserPromptSubmit", 30}, {"Stop", 120}, {"PreCompact", 600}}

// installClaude merges om's hooks into Claude Code's user settings (~/.claude/settings.json,
// or $CLAUDE_CONFIG_DIR) beside any others, and registers the MCP server at user scope with
// claude mcp add.
func installClaude(w writer, exe string, remove bool) error {
	path := filepath.Join(homeDir("CLAUDE_CONFIG_DIR", ".claude"), "settings.json")
	settings, err := readJSONObject(path)
	if err != nil {
		return err
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	isOurs := func(group any) bool {
		g, _ := group.(map[string]any)
		list, _ := g["hooks"].([]any)
		for _, raw := range list {
			h, _ := raw.(map[string]any)
			if cmd, _ := h["command"].(string); strings.Contains(cmd, " hook claude-code ") {
				return true
			}
		}
		return false
	}
	for _, ev := range claudeEvents {
		groups, _ := hooks[ev.name].([]any)
		kept := []any{}
		for _, g := range groups {
			if !isOurs(g) {
				kept = append(kept, g)
			}
		}
		if !remove {
			kept = append(kept, map[string]any{"hooks": []any{map[string]any{
				"type": "command", "command": strconv.Quote(exe) + " hook claude-code " + ev.name, "timeout": ev.timeout,
			}}})
		}
		if len(kept) == 0 {
			delete(hooks, ev.name)
		} else {
			hooks[ev.name] = kept
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
	if err := w.write(path, data); err != nil {
		return err
	}
	if remove {
		w.run("claude", "mcp", "remove", "--scope", "user", "om")
		return nil
	}
	w.run("claude", "mcp", "add", "--scope", "user", "om", "--", exe, "mcp", "--harness", "claude-code")
	return nil
}

// installCodex adds the MCP server and the notify program to Codex's config
// ($CODEX_HOME/config.toml, ~/.codex), each line marked as om's. An existing notify is left as
// it is: Codex runs one notify program, and om does not replace another.
func installCodex(w writer, exe string, remove bool) error {
	path := filepath.Join(homeDir("CODEX_HOME", ".codex"), "config.toml")
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var kept []string
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		if !strings.HasSuffix(line, managedMark) && line != "" || line == "" && len(kept) > 0 {
			kept = append(kept, line)
		}
	}
	body := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if remove {
		return w.write(path, []byte(body+"\n"))
	}
	q := func(s string) string { return strconv.Quote(s) }
	var top []string
	hasNotify := false
	for _, line := range kept {
		if strings.HasPrefix(strings.TrimSpace(line), "notify") {
			hasNotify = true
		}
	}
	if hasNotify {
		fmt.Fprintf(w.out, "%s already sets notify, which Codex runs alone: om's turn-end hook is not added, so nothing records Codex turns until your notify program also runs: %s hook codex notify '<payload>'\n", path, exe)
	} else {
		top = append(top, "notify = ["+q(exe)+", "+q("hook")+", "+q("codex")+", "+q("notify")+"] "+managedMark)
	}
	table := []string{
		"[mcp_servers.om] " + managedMark,
		"command = " + q(exe) + " " + managedMark,
		"args = [" + q("mcp") + ", " + q("--harness") + ", " + q("codex") + "] " + managedMark,
	}
	// A top-level key must come before the first table.
	out := strings.Join(top, "\n")
	if out != "" {
		out += "\n"
	}
	if body != "" {
		out += body + "\n\n"
	}
	out += strings.Join(table, "\n") + "\n"
	return w.write(path, []byte(out))
}

// installOpenCode writes the om plugin into OpenCode's plugin directory and adds the MCP server
// to its config ($XDG_CONFIG_HOME/opencode, ~/.config/opencode).
func installOpenCode(w writer, exe string, remove bool) error {
	dir := filepath.Join(homeDir("XDG_CONFIG_HOME", ".config"), "opencode")
	plugin := filepath.Join(dir, "plugin", "om.js")
	configPath := filepath.Join(dir, "opencode.json")
	cfg, err := readJSONObject(configPath)
	if err != nil {
		return err
	}
	mcp, _ := cfg["mcp"].(map[string]any)
	if mcp == nil {
		mcp = map[string]any{}
	}
	if remove {
		delete(mcp, "om")
	} else {
		mcp["om"] = map[string]any{"type": "local", "command": []any{exe, "mcp", "--harness", "opencode"}, "enabled": true}
	}
	if len(mcp) == 0 {
		delete(cfg, "mcp")
	} else {
		cfg["mcp"] = mcp
	}
	data, err := encodeJSON(cfg)
	if err != nil {
		return err
	}
	if err := w.write(configPath, data); err != nil {
		return err
	}
	if remove {
		return w.remove(plugin)
	}
	src, err := assets.ReadFile("assets/opencode/om.js")
	if err != nil {
		return err
	}
	return w.write(plugin, bytes.ReplaceAll(src, []byte("__OM_BIN__"), []byte(strconv.Quote(exe))))
}

// installPi writes the om extension into Pi's global extensions directory
// ($PI_CODING_AGENT_DIR/extensions, ~/.pi/agent/extensions), which Pi loads at start.
func installPi(w writer, exe string, remove bool) error {
	path := filepath.Join(homeDir("PI_CODING_AGENT_DIR", ".pi", "agent"), "extensions", "om.ts")
	if remove {
		return w.remove(path)
	}
	src, err := assets.ReadFile("assets/pi/om.ts")
	if err != nil {
		return err
	}
	return w.write(path, bytes.ReplaceAll(src, []byte("__OM_BIN__"), []byte(strconv.Quote(exe))))
}
