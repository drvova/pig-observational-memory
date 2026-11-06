package cli

import (
	"io"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/engine"
	"github.com/drvova/pig-observational-memory/memory/harness"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/omtool"
	"github.com/drvova/pig-observational-memory/memory/runtime"
)

// app is one om process: the engine's runtime and the memory tool's manager. A hook runs in a
// process of its own; the MCP server keeps one for the harness session's life, so its
// namespace child carries definitions between calls as it does in PiG.
type app struct {
	root    string
	log     io.Writer
	rt      *runtime.Runtime
	manager *harness.Manager
	pin     curated.Pin
	// lisp is the engine image's client with OM_ENGINE=lisp, else nil (lisp.go).
	lisp *lispClient
}

func newApp(root string, log io.Writer) *app {
	useRoot(root)
	a := &app{root: root, log: log, rt: runtime.New(), lisp: newLispClient(root)}
	a.manager = engine.NewManager(a.rt, &a.pin)
	return a
}

// startContext is what a harness is given when a session starts or resumes, and after it
// compacted: the latest summary when the engine compacted this session (withSummary), how to
// reach the memory tool, and the system-prompt section (the tool-note line, the curated index).
func (a *app) startContext(h host.ToolHost, summary string) string {
	var parts []string
	if summary != "" {
		parts = append(parts, "Observational memory of this session, from before the compaction:\n\n"+summary)
	}
	guide := []string{omtool.PromptSnippet}
	guide = append(guide, omtool.PromptGuidelines...)
	parts = append(parts, "The memory tool (MCP server om, tool memory):\n- "+strings.Join(guide, "\n- "))
	index, on := engine.Index(h, &a.pin)
	parts = append(parts, engine.SectionText(index, on))
	return strings.Join(parts, "\n\n")
}
