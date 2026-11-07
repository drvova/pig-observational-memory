package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/commands"
	"github.com/drvova/pig-observational-memory/memory/lispimage"
	"github.com/drvova/pig-observational-memory/memory/neutral"
	"github.com/drvova/pig-observational-memory/memory/omtool"
)

// Version is om's version, set at build time (-ldflags "-X …/cli.Version=v1.2.3").
var Version = "dev"

const usage = `om: observational memory for coding agents

  om harnesses                       the harnesses om installs into, and what each gets
  om install <harness> [--dry-run]   wire om into a harness (om harnesses lists them)
  om install --mcp-config <file> [--key mcpServers] [--name h]   any harness with a JSON MCP config
  om uninstall <harness>             remove what install wrote
  om hook <harness> <event>          run one harness event (installed hooks call this)
  om mcp --harness <harness>         serve the memory tool over MCP (stdio)
  om call --harness <h> [--dir d]    run one memory tool call (its arguments as JSON on stdin)
  om tool-spec                       the memory tool's name, description and JSON schema
  om status [--harness h] [--dir d]  the current session's memory figures
  om view [--harness h] [--dir d] [args]  print memory content (as /om:view)
  om sleep [--harness h --dir d]     run the sleep pass (code only) over every session store,
                                     or over one session with its repository
  om serve                           run the engine as a long-lived process for one harness
                                     process: JSON-RPC 2.0 on stdin and stdout (docs/engine-rpc.md)
  om version

The store lives under $OM_HOME (default ~/.om); its settings.json holds the settings.
hook, mcp and call run on the SBCL engine image over <root>/engine.sock, started from
$OM_LISP_IMAGE, else built on first use with the sbcl on PATH into <root>/engine (until then,
and for anything the image cannot take, the Go engine runs; see <root>/engine.log).
OM_ENGINE=go (or "engine": "go" in settings.json) always runs the Go engine; it is the default
on Windows (docs/universal.md).
`

// Run is the om command over args (without the program name); it returns the exit status.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) (code int) {
	// A panic would exit 2 with a stack trace, which a harness may read as a blocking hook
	// answer and hand to the model: report it as an error instead.
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(stderr, "om: internal error: %v\n", r)
			code = 1
		}
	}()
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage)
		return 0
	}
	root, err := Root()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := run(root, args, stdin, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func run(root string, args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	switch args[0] {
	case "version":
		_, err := fmt.Fprintln(stdout, "om", Version)
		return err
	case "serve":
		return serve(root, stdin, stdout)
	case "hook":
		if len(args) < 3 {
			return fmt.Errorf("om hook <harness> <event>")
		}
		return newApp(root, stderr).hook(args[1], args[2], args[3:], stdin, stdout)
	case "mcp":
		fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
		harnessName := fs.String("harness", "claude-code", "the harness whose sessions the tool reads")
		dir := fs.String("dir", "", "the project directory (default: $CLAUDE_PROJECT_DIR, else the working directory)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *dir == "" {
			*dir = mcpDir()
		}
		return newApp(root, stderr).mcp(*harnessName, *dir, stdin, stdout)
	case "harnesses":
		return listHarnesses(stdout)
	case "tool-spec":
		return json.NewEncoder(stdout).Encode(map[string]any{"name": omtool.ToolName, "label": omtool.Label, "description": toolDescription(), "parameters": omtool.Parameters})
	case "call":
		fs := flag.NewFlagSet("call", flag.ContinueOnError)
		harnessName := fs.String("harness", "generic", "the harness")
		dir := fs.String("dir", "", "the project directory (default: the working directory)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *dir == "" {
			*dir, _ = os.Getwd()
		}
		var params map[string]any
		if err := json.NewDecoder(stdin).Decode(&params); err != nil {
			return fmt.Errorf("om call: the tool's arguments as JSON on stdin: %w", err)
		}
		a := newApp(root, stderr)
		if res, ok, err := a.callViaLisp(*harnessName, *dir, params); ok {
			if err != nil {
				return err
			}
			return json.NewEncoder(stdout).Encode(map[string]any{"text": res["text"], "isError": res["isError"]})
		}
		raw, _ := json.Marshal(map[string]any{"name": omtool.ToolName, "arguments": params})
		res := a.callTool(*harnessName, *dir, raw)
		text := ""
		if content, ok := res["content"].([]any); ok && len(content) > 0 {
			text, _ = content[0].(map[string]any)["text"].(string)
		}
		return json.NewEncoder(stdout).Encode(map[string]any{"text": text, "isError": res["isError"]})
	case "status", "view":
		fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
		harnessName := fs.String("harness", "claude-code", "the harness")
		dir := fs.String("dir", "", "the project directory (default: the working directory)")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *dir == "" {
			*dir, _ = os.Getwd()
		}
		a := newApp(root, stdout)
		s, ok := session(root, *harnessName, *dir)
		if !ok {
			return fmt.Errorf("om %s: no %s session recorded for %s", args[0], *harnessName, *dir)
		}
		h, err := s.Host(root, stdout)
		if err != nil {
			return err
		}
		a.rt.EnsureConfig(h)
		if args[0] == "view" {
			// The image answers the view's text; showing and copying it stay here.
			res, handled, err := a.viaLisp("view", s, map[string]any{"args": strings.Join(fs.Args(), " ")})
			if handled {
				if err == nil {
					commands.ShowView(a.rt, h, res.Text, res.Notice, nil)
				}
				return err
			}
		}
		if args[0] == "status" {
			// The image answers the report's text (lisp/src/server/status.lisp); showing it
			// stays here.
			res, handled, err := a.viaLisp("om-status", s, nil)
			if handled {
				if err == nil {
					h.Notify(res.Text, "info")
				}
			} else {
				err = commands.RunStatus(a.rt, h)
			}
			// The image counts the events it refused; those it never saw (no image built yet,
			// not reachable) only the engine log holds.
			if a.lisp != nil {
				if n := lispimage.FallbackCount(root); n > 0 {
					h.Notify(fmt.Sprintf("Engine fallbacks: %d events ran on the Go engine (reasons in %s; OM_LISP_FALLBACK=0 turns falling back off)", n, lispimage.LogPath(root)), "info")
				}
			}
			return err
		}
		return commands.RunView(a.rt, h, strings.Join(fs.Args(), " "), nil)
	case "sleep":
		fs := flag.NewFlagSet("sleep", flag.ContinueOnError)
		harnessName := fs.String("harness", "claude-code", "with --dir: the harness")
		dir := fs.String("dir", "", "the project directory of the one session to sleep")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		one := false
		fs.Visit(func(*flag.Flag) { one = true })
		if !one {
			return runSleep(root, nil, stdout)
		}
		if *dir == "" {
			*dir, _ = os.Getwd()
		}
		return runSleep(root, func() (*neutral.Host, string, error) {
			s, ok := session(root, *harnessName, *dir)
			if !ok {
				return nil, "", fmt.Errorf("om sleep: no %s session recorded for %s", *harnessName, *dir)
			}
			h, err := s.Host(root, stdout)
			return h, *harnessName + "/" + s.ID, err
		}, stdout)
	case "install", "uninstall":
		fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
		dry := fs.Bool("dry-run", false, "print what would change, change nothing")
		mcpConfig := fs.String("mcp-config", "", "any harness: the JSON file its MCP servers live in")
		key := fs.String("key", "mcpServers", "with --mcp-config: the /-separated object path of its servers")
		name := fs.String("name", "", "with --mcp-config: the harness name the server reports")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *mcpConfig != "" {
			return installAny(*mcpConfig, *key, *name, args[0] == "uninstall", *dry, stdout)
		}
		if fs.NArg() != 1 {
			return fmt.Errorf("om %s <harness> (om harnesses lists them)", args[0])
		}
		return install(fs.Arg(0), args[0] == "uninstall", *dry, stdout)
	}
	return fmt.Errorf("om: unknown command %q\n\n%s", args[0], usage)
}
