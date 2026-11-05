// Command om is observational memory for coding agents: one binary that Claude Code, Codex,
// OpenCode, Pi and any other harness drive through hooks, MCP or a plugin (docs/universal.md).
package main

import (
	"os"

	"github.com/drvova/pig-observational-memory/memory/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}
