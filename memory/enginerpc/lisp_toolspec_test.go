package enginerpc

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var writeToolSpec = flag.Bool("write", false, "rewrite lisp/src/server/toolspec.json")

// TestLispToolSpec keeps the Lisp image's copy of the memory tool's specs equal to Go's, byte
// for byte (docs/lisp-port-peer.md): "lisp" is LispMemoryToolSpec, what the image's initialize,
// MCP server and start context answer (docs/lisp-core.md, decision 10), and "go" is
// MemoryToolSpec, the Go engine's text, which the image only names to tell the two apart;
// -args -write regenerates it.
func TestLispToolSpec(t *testing.T) {
	encoded, err := json.Marshal(struct {
		Go   ToolSpec `json:"go"`
		Lisp ToolSpec `json:"lisp"`
	}{MemoryToolSpec(), LispMemoryToolSpec()})
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	path := filepath.Join("..", "..", "lisp", "src", "server", "toolspec.json")
	if *writeToolSpec {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	held, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(held, encoded) {
		t.Fatalf("%s is stale: run go test ./enginerpc -run LispToolSpec -args -write", path)
	}
}
