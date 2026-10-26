// Package sandbox runs the memory tool's Go code in a child process (docs/code-tool.md, "The
// child process"): the extension's own binary re-executed with a marker, holding one yaegi
// interpreter whose symbol table is exactly the allow-listed packages, the om API and the
// seeded functions. om calls come back to the extension over a pipe, answered through the
// Handler the caller supplies.
//
// The child is recognized in this package's init, before the runner's main (which would look
// for extension sockets) can run; sibling extensions' inits run before it in the child, with
// an empty environment, cwd /, stdin and stdout on the null device and none of PiG's
// descriptors. The child holds no path, key or descriptor of the extension's: it receives
// source text and JSON values only. It is not an OS sandbox: the symbol table is the wall.
//
// The supervisor (Supervisor) starts the child lazily, replays the saved definitions in
// component order, runs one call at a time, answers its om calls, kills it when the call's
// context ends (a true stop), reports crashes with the child's fatal-error lines, isolates and
// quarantines a definition that crashes the child at replay, and restarts it on the next use.
//
// The symbol tables are generated (go generate ./internal/sandbox, see symgen); this package
// never imports yaegi's stdlib, which would link every package it wraps. The generator runs as
// symgen's test with -write, since PiG refuses an extension module holding a main package.
package sandbox

//go:generate go test ./symgen -run ^TestTablesAreUpToDate$ -args -write

import (
	"fmt"
	"os"
)

// ChildMarker is the re-executed binary's first argument; the next is the mode.
const ChildMarker = "__om_sandbox_child__"

// The child's modes.
const (
	// ModeNamespace is the namespace child: the agent's namespace for one conversation.
	ModeNamespace = "namespace"
	// ModeDetector is a detector child: one detector run (docs/code-tool.md, "Detectors").
	ModeDetector = "detector"
)

// childMode runs a child of one mode with the arguments after the mode and returns its exit
// status. The tests register fixture modes beside the real ones.
type childMode func(args []string) int

// childModes are the modes a child can run. It is filled by variable initialization, which
// runs before any init function, so a fixture mode a test file registers the same way is
// present when init reads the marker.
var childModes = map[string]childMode{
	ModeNamespace: func(args []string) int { return namespaceMain(args, nil) },
	ModeDetector:  func(args []string) int { return detectorMain(args, nil) },
}

func init() {
	if len(os.Args) < 3 || os.Args[1] != ChildMarker {
		return
	}
	run, ok := childModes[os.Args[2]]
	if !ok {
		fmt.Fprintf(os.Stderr, "om sandbox: unknown child mode %q\n", os.Args[2])
		os.Exit(2)
	}
	os.Exit(run(os.Args[3:]))
}
