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
//go:embed assets/opencode/om.js assets/pi/om.ts assets/harnesses.json
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
	h, ok := registry[harnessName]
	if !ok {
		return fmt.Errorf("om install: unknown harness %q (om harnesses lists them)", harnessName)
	}
	if h.Manual != "" {
		return printManual(w, exe, h, remove)
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
	return installMCP(writer{dry: dry, out: out}, exe, name, spec, remove)
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
		return nil, fmt.Errorf("%s is not plain JSON (comments or trailing commas?): %w; om rewrites only plain JSON, so add its entry by hand (om install --dry-run shows where)", path, err)
	}
	return obj, nil
}

func encodeJSON(v any) ([]byte, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	return append(data, '\n'), err
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

// pluginInstaller writes the om plugin into an OpenCode-style plugin directory (OpenCode and its
// forks share the plugin API); the registry adds the MCP server.
func pluginInstaller(harness, dirTemplate string) func(w writer, exe string, remove bool) error {
	return func(w writer, exe string, remove bool) error {
		plugin := filepath.Join(pathTemplate(dirTemplate), "om.js")
		if remove {
			return w.remove(plugin)
		}
		src, err := assets.ReadFile("assets/opencode/om.js")
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
		if line != "" && !strings.HasSuffix(line, managedMark) {
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
