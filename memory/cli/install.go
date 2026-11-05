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
	"runtime"
	"strconv"
	"strings"
)

// assets are the adapters om writes for harnesses that load a plugin or an extension file.
//
//go:embed assets/opencode/om.js assets/pi/om.ts assets/neovate/om.js assets/amp/om.ts assets/harnesses.json
var assets embed.FS

// managedMark marks every line om writes into a harness's TOML config, so uninstall removes
// exactly those.
const managedMark = "# om:managed"

// stripManaged takes om's marked lines out of a config (and the blank lines before the first
// kept one). A line the user added inside one of om's tables (an unmarked line after om's
// marked table header, before the next table) belongs to that table: it is left out of kept
// so it never lands in the table before, and returned by the header (without the mark) so an
// install puts it back after om's table.
func stripManaged(data []byte) (kept []string, extras map[string][]string) {
	extras = map[string][]string{}
	inOm := ""
	for _, line := range strings.Split(strings.TrimRight(string(data), "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasSuffix(line, managedMark) {
			if head := strings.TrimSpace(strings.TrimSuffix(trimmed, managedMark)); strings.HasPrefix(head, "[") {
				inOm = head
			}
			continue
		}
		if strings.HasPrefix(trimmed, "[") {
			inOm = ""
		}
		if inOm != "" {
			if trimmed != "" {
				extras[inOm] = append(extras[inOm], line)
			}
			continue
		}
		if line != "" || len(kept) > 0 {
			kept = append(kept, line)
		}
	}
	return kept, extras
}

// withExtras is om's table lines with the user's own lines of each of om's tables put back
// after its header's table (the first table under that header).
func withExtras(table []string, extras map[string][]string) []string {
	var out []string
	pending := []string(nil)
	for _, line := range table {
		if head := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(line), managedMark)); strings.HasPrefix(head, "[") {
			out = append(out, pending...)
			pending = extras[head]
			delete(extras, head)
		}
		out = append(out, line)
	}
	return append(out, pending...)
}

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
	w := writer{dry: dry, out: out, removing: remove}
	h, ok := registry[harnessName]
	if !ok {
		return fmt.Errorf("om install: unknown harness %q (om harnesses lists them)", harnessName)
	}
	if h.Manual != "" {
		if err := printManual(w, exe, h, remove); err != nil || h.Hooks == nil {
			return err
		}
	}
	if h.MCP != nil {
		if err := installMCP(w, exe, h.Name, h.MCP, remove); err != nil {
			return err
		}
	}
	if h.YAML != nil {
		if err := installYAML(w, exe, h.Name, h.YAML, remove); err != nil {
			return err
		}
	}
	if h.TOML != nil {
		if err := installTOML(w, exe, h.Name, h.TOML, remove); err != nil {
			return err
		}
	}
	if h.Own != nil {
		if path := pathFor(h.Own.Path); path == "" {
			fmt.Fprintf(w.out, "%s: no config path on %s\n", h.Name, runtime.GOOS)
		} else if remove {
			if err := w.remove(path); err != nil {
				return err
			}
		} else if err := w.write(path, []byte(h.Own.Content(exe, h.Name))); err != nil {
			return err
		}
	}
	if h.Hooks != nil {
		if err := installHooks(w, exe, h.Name, h.Hooks, remove); err != nil {
			return err
		}
	}
	if h.Custom != nil {
		return h.Custom(w, exe, remove)
	}
	return nil
}

// printManual prints how to add (or remove) om's MCP server in a harness that keeps its servers
// in its own interface.
func printManual(w writer, exe string, h Harness, remove bool) error {
	if remove {
		fmt.Fprintf(w.out, "%s: remove the MCP server named om (%s)\n", h.Title, h.Manual)
		return nil
	}
	fmt.Fprintf(w.out, "%s keeps its MCP servers in its own settings: %s\n", h.Title, h.Manual)
	fmt.Fprintf(w.out, "  name:      om\n  transport: stdio\n  command:   %s\n  args:      mcp --harness %s\n", exe, h.Name)
	return nil
}

// installAny merges om's server into any JSON MCP config, for a harness not in the registry:
// path is the file, key the "/"-separated object path its servers live under, name the
// harness name the server reports.
func installAny(path, key, name string, remove, dry bool, out io.Writer) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	if name == "" {
		name = "generic"
	}
	spec := &MCPSpec{Path: map[string]string{"": path}, Key: strings.Split(key, "/")}
	return installMCP(writer{dry: dry, out: out, removing: remove}, exe, name, spec, remove)
}

// writer writes config files, keeping a one-time backup of what was there.
type writer struct {
	dry bool
	out io.Writer
	// removing is set by uninstall, which never creates a file that is not there.
	removing bool
}

func (w writer) write(path string, data []byte) error {
	return w.writeMode(path, data, 0o644)
}

// writeMode is write with the file's permission bits (an executable hook).
func (w writer) writeMode(path string, data []byte, mode os.FileMode) error {
	old, err := os.ReadFile(path)
	if w.removing && errors.Is(err, fs.ErrNotExist) {
		return nil
	}
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
	if err := os.WriteFile(path, data, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
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
		return nil, fmt.Errorf("%s is not plain JSON (comments or trailing commas?): %w; om rewrites only plain JSON, so add its entry by hand (om install --dry-run shows where)", path, err)
	}
	return obj, nil
}

// encodeJSON writes a config back with two-space indents, leaving &, < and > as they are (the
// standard encoder escapes them for HTML, which would rewrite a user's "cd x && make").
func encodeJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	err := enc.Encode(v)
	return buf.Bytes(), err
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
	kept, extras := stripManaged(data)
	body := strings.TrimRight(strings.Join(kept, "\n"), "\n")
	if remove {
		if body == "" {
			return w.write(path, nil)
		}
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
	out += strings.Join(withExtras(table, extras), "\n") + "\n"
	return w.write(path, []byte(out))
}

// pluginInstaller writes the om plugin into an OpenCode-style plugin directory (OpenCode and its
// forks share the plugin API); the registry adds the MCP server.
func pluginInstaller(harness, dirTemplate string) func(w writer, exe string, remove bool) error {
	return assetInstaller(harness, dirTemplate, "assets/opencode/om.js")
}

// assetInstaller writes the plugin asset (with om's path and the harness name) into the
// harness's plugin directory under the asset's own file name.
func assetInstaller(harness, dirTemplate, asset string) func(w writer, exe string, remove bool) error {
	return func(w writer, exe string, remove bool) error {
		plugin := filepath.Join(pathTemplate(dirTemplate), filepath.Base(asset))
		if remove {
			return w.remove(plugin)
		}
		src, err := assets.ReadFile(asset)
		if err != nil {
			return err
		}
		src = bytes.ReplaceAll(src, []byte("__OM_BIN__"), []byte(strconv.Quote(exe)))
		return w.write(plugin, bytes.ReplaceAll(src, []byte("__OM_HARNESS__"), []byte(strconv.Quote(harness))))
	}
}

// installCrush adds om's MCP server to Crush's crushrc ($XDG_CONFIG_HOME/crush/crushrc,
// ~/.config/crush), a shell file of commands, as one line marked as om's.
func installCrush(w writer, exe string, remove bool) error {
	path := pathTemplate("${XDG_CONFIG_HOME:-~/.config}/crush/crushrc")
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
	if !remove {
		kept = append(kept, "mcp add om --command "+shellQuote(exe)+" --args mcp --args --harness --args crush "+managedMark)
	}
	out := strings.Join(kept, "\n")
	if out != "" {
		out += "\n"
	}
	return w.write(path, []byte(out))
}

// shellQuote is s single-quoted for a POSIX shell.
// hookCommand is the shell command a JSON hook runs: om's path quoted for the shell the
// harness runs hooks in (sh on Unix, where a double-quoted path would still expand $ and `).
func hookCommand(exe, harness, event string) string {
	q := shellQuote(exe)
	if runtime.GOOS == "windows" {
		q = strconv.Quote(exe)
	}
	return q + " hook " + harness + " " + event
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// installPi writes the om extension into Pi's global extensions directory
// ($PI_CODING_AGENT_DIR/extensions, ~/.pi/agent/extensions), which Pi loads at start.
func installPi(w writer, exe string, remove bool) error {
	return installPiExtension(w, exe, "pi", filepath.Join(homeDir("PI_CODING_AGENT_DIR", ".pi", "agent"), "extensions"), remove)
}

// piExtensionInstaller installs Pi's extension into a harness that loads Pi extensions from its
// own agent directory (dir, a path template), under its own harness name.
func piExtensionInstaller(harness, dir string) func(w writer, exe string, remove bool) error {
	return func(w writer, exe string, remove bool) error {
		return installPiExtension(w, exe, harness, pathTemplate(dir), remove)
	}
}

func installPiExtension(w writer, exe, harness, dir string, remove bool) error {
	path := filepath.Join(dir, "om.ts")
	if remove {
		return w.remove(path)
	}
	src, err := assets.ReadFile("assets/pi/om.ts")
	if err != nil {
		return err
	}
	src = bytes.ReplaceAll(src, []byte("__OM_BIN__"), []byte(strconv.Quote(exe)))
	return w.write(path, bytes.ReplaceAll(src, []byte("__OM_HARNESS__"), []byte(strconv.Quote(harness))))
}

// pigSource is the PiG package this repository publishes (README.md).
const pigSource = "git:github.com/drvova/pig-observational-memory"

// installPiG adds the engine to PiG the way PiG installs any package: PiG runs it natively, so
// nothing of om's binary is written.
func installPiG(w writer, exe string, remove bool) error {
	if remove {
		w.run("pig", "remove", pigSource)
	} else {
		w.run("pig", "install", pigSource)
	}
	return nil
}

// installClineHook writes Cline's TaskComplete hook, an executable file in its hooks directory
// (${CLINE_DIR:-~/.cline}/hooks) that hands the payload to om.
func installClineHook(w writer, exe string, remove bool) error {
	path := filepath.Join(pathTemplate("${CLINE_DIR:-~/.cline}/hooks"), "TaskComplete")
	if remove {
		return w.remove(path)
	}
	return w.writeMode(path, []byte("#!/bin/sh\n# om: observational memory (written by om install cline)\nexec "+shellQuote(exe)+" hook cline TaskComplete\n"), 0o755)
}

// installGoosePlugin writes om as a Goose plugin (~/.agents/plugins/om): its plugin.json and the
// Stop hook in hooks/hooks.json, the only way Goose loads hooks.
func installGoosePlugin(w writer, exe string, remove bool) error {
	dir := pathTemplate("~/.agents/plugins/om")
	manifest, hooksFile := filepath.Join(dir, "plugin.json"), filepath.Join(dir, "hooks", "hooks.json")
	if remove {
		if err := w.remove(hooksFile); err != nil {
			return err
		}
		return w.remove(manifest)
	}
	if err := w.write(manifest, []byte("{\"name\": \"om\", \"version\": \"1.0.0\", \"description\": \"observational memory\"}\n")); err != nil {
		return err
	}
	hooks := map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": shellQuote(exe) + " hook goose Stop", "timeout": hookTimeouts[evTurnEnd]}}}}}}
	data, err := encodeJSON(hooks)
	if err != nil {
		return err
	}
	return w.write(hooksFile, data)
}

// printAmazonQHooks prints the stop hook to add to an Amazon Q CLI agent file: hooks live in
// each agent's JSON (~/.aws/amazonq/cli-agents/<name>.json), and which agent is yours is yours.
func printAmazonQHooks(w writer, exe string, remove bool) error {
	if remove {
		fmt.Fprintf(w.out, "Amazon Q Developer CLI: remove om's stop hook from your agent file in %s\n", pathTemplate("~/.aws/amazonq/cli-agents"))
		return nil
	}
	fmt.Fprintf(w.out, "Amazon Q Developer CLI: add to your agent file in %s, under \"hooks\":\n  \"stop\": [{\"command\": %q, \"timeout_ms\": %d}]\n", pathTemplate("~/.aws/amazonq/cli-agents"), shellQuote(exe)+" hook amazon-q stop", hookTimeouts[evTurnEnd]*1000)
	return nil
}

// installKiroHooks writes om's Kiro hooks, a file of om's own in its global hooks directory
// (~/.kiro/hooks/om.json, Kiro's v1 hook format).
func installKiroHooks(w writer, exe string, remove bool) error {
	path := pathTemplate("~/.kiro/hooks/om.json")
	if remove {
		return w.remove(path)
	}
	var hooks []any
	for _, ev := range []string{"SessionStart", "UserPromptSubmit", "Stop"} {
		hooks = append(hooks, map[string]any{"name": "om " + ev, "trigger": ev, "action": map[string]any{"type": "command", "command": shellQuote(exe) + " hook kiro " + ev}, "timeout": 60})
	}
	data, err := encodeJSON(map[string]any{"version": "v1", "hooks": hooks})
	if err != nil {
		return err
	}
	return w.write(path, data)
}
