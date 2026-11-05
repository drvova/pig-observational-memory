package cli

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
)

var writeDoc = flag.Bool("write", false, "rewrite docs/harnesses.md")

// docs/harnesses.md is the registry rendered; the unsupported section is kept from the file.
func TestHarnessDocIsCurrent(t *testing.T) {
	path := filepath.Join("..", "..", "docs", "harnesses.md")
	unsupported, err := os.ReadFile(filepath.Join("assets", "unsupported.md"))
	if err != nil {
		t.Fatal(err)
	}
	want := HarnessDoc(string(unsupported))
	if *writeDoc {
		if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("docs/harnesses.md is stale: run go test ./cli -run HarnessDoc -args -write")
	}
}
