package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/commands"
	"github.com/drvova/pig-observational-memory/memory/omtool"
)

// Version is om's version, set at build time (-ldflags "-X …/cli.Version=v1.2.3").
var Version = "dev"

const usage = `om: observational memory for coding agents

  om install <harness> [--dry-run]   wire om into a harness: claude-code, codex, opencode, pi
  om uninstall <harness>             remove what install wrote
  om hook <harness> <event>          run one harness event (installed hooks call this)
  om mcp --harness <harness>         serve the memory tool over MCP (stdio)
  om call --harness <h> [--dir d]    run one memory tool call (its arguments as JSON on stdin)
  om tool-spec                       the memory tool's name, description and JSON schema
  om status [--harness h] [--dir d]  the current session's memory figures
  om view [--harness h] [--dir d] [args]  print memory content (as /om:view)
  om version

The store lives under $OM_HOME (default ~/.om); its settings.json holds the settings.
`

// Run is the om command over args (without the program name); it returns the exit status.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
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
		raw, _ := json.Marshal(map[string]any{"name": omtool.ToolName, "arguments": params})
		res := newApp(root, stderr).callTool(*harnessName, *dir, raw)
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
		if args[0] == "status" {
			return commands.RunStatus(a.rt, h)
		}
		return commands.RunView(a.rt, h, strings.Join(fs.Args(), " "), nil)
	case "install", "uninstall":
		fs := flag.NewFlagSet(args[0], flag.ContinueOnError)
		dry := fs.Bool("dry-run", false, "print what would change, change nothing")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return fmt.Errorf("om %s <harness>: claude-code, codex, opencode, pi", args[0])
		}
		return install(fs.Arg(0), args[0] == "uninstall", *dry, stdout)
	}
	return fmt.Errorf("om: unknown command %q\n\n%s", args[0], usage)
}
