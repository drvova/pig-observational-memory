package cli

import (
	"io"
	"os"

	"github.com/drvova/pig-observational-memory/memory/enginerpc"
)

// serve runs the engine behind the host RPC (om serve, docs/engine-rpc.md) until stdin
// ends. The connection owns stdout: anything else the process would print there goes to
// stderr instead, so a stray line cannot corrupt the protocol. An adapter that names no
// directories at initialize gets om's root, as every other om command does.
func serve(root string, stdin io.Reader, stdout io.Writer) error {
	if stdout == io.Writer(os.Stdout) {
		os.Stdout = os.Stderr
	}
	server := enginerpc.NewServer()
	server.Version = Version
	server.DefaultDirs = func() { useRoot(root) }
	return server.Serve(stdin, stdout)
}
