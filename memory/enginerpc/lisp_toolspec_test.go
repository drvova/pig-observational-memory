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

// TestLispToolSpec keeps the Lisp image's copy of the memory tool's spec (what its initialize
// answers, docs/lisp-port-peer.md) equal to MemoryToolSpec, byte for byte; -args -write
// regenerates it.
func TestLispToolSpec(t *testing.T) {
	encoded, err := json.Marshal(MemoryToolSpec())
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
