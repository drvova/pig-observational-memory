package sandbox

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// Fixture children: the test binary re-executed in a mode only the tests register, to act as a
// child that misbehaves in ways the real one cannot be made to. They are registered by variable
// initialization, before init reads the marker.
var _ = registerFixtures()

func registerFixtures() bool {
	childModes["fixture-forge-gen"] = fixture(func(w io.Writer, run msg) {
		// A call from another generation: the host must refuse it and kill the child.
		writeLine(w, msg{T: tCall, Gen: run.Gen + 1, Call: run.Call, Seq: 1, M: "Notes", Args: []byte(`[{}]`)})
		select {}
	})
	childModes["fixture-forge-save"] = fixture(func(w io.Writer, run msg) {
		writeLine(w, msg{T: tCall, Gen: run.Gen, Call: run.Call, Seq: 1, M: "Save", Args: []byte(`["n","lesson","d",{}]`)})
		select {}
	})
	childModes["fixture-endless-line"] = fixture(func(w io.Writer, run msg) {
		// A line that never ends: 100 MB without a newline, then nothing.
		chunk := []byte(strings.Repeat("x", 1<<20))
		for range 100 {
			if _, err := w.Write(chunk); err != nil {
				os.Exit(1)
			}
		}
		select {}
	})
	childModes["fixture-big-args"] = fixture(func(w io.Writer, run msg) {
		// A call within the line bound whose arguments exceed the argument bound.
		arg := strings.Repeat("y", 2*ArgChars)
		writeLine(w, msg{T: tCall, Gen: run.Gen, Call: run.Call, Seq: 1, M: "Save", Args: []byte(`["` + arg + `","lesson","d",{}]`)})
		select {}
	})
	childModes["fixture-stderr-flood"] = fixture(func(w io.Writer, run msg) {
		chunk := []byte(strings.Repeat("e", 1<<20) + "\n")
		for range 50 {
			os.Stderr.Write(chunk)
		}
		fmt.Fprintln(os.Stderr, "fatal error: fixture flood done")
		os.Exit(2)
	})
	childModes["fixture-crash-on-marker"] = func(args []string) int {
		return namespaceMain(args, &childHooks{beforeEval: func(src string) {
			if strings.Contains(src, "CRASHME") {
				fmt.Fprintln(os.Stderr, "fatal error: fixture crash at replay")
				os.Exit(2)
			}
		}})
	}
	return true
}

// fixture is a child that answers init with ready and then does onRun with the first run.
func fixture(onRun func(w io.Writer, run msg)) childMode {
	return func(args []string) int {
		in, out, err := childPipes(args)
		if err != nil {
			return exitSetup
		}
		r := newLineReader(in, HostLineBytes)
		init, err := r.readMsg()
		if err != nil {
			return exitHostGone
		}
		writeLine(out, msg{T: tReady, Gen: init.Gen})
		for {
			m, err := r.readMsg()
			if err != nil {
				return exitHostGone
			}
			if m.T == tRun {
				onRun(out, m)
			}
		}
	}
}

func writeLine(w io.Writer, m msg) {
	b, err := encodeLine(m, 1<<30)
	if err != nil {
		os.Exit(1)
	}
	if _, err := w.Write(b); err != nil {
		os.Exit(1)
	}
}
