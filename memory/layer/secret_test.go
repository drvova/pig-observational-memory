package layer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testKey is a value the secret rules redact, built at run time so no file holds one whole.
func testKey() string { return "AKIA" + strings.Repeat("Q7", 4) + "ZX3V" + "LMN5" }

// Decision 10 at the layer: a prose definition is redacted as it is written, its variable name
// and line kept, in its file and its blob; a code or query definition holding a secret is refused
// with where the secret is.
func TestLayerWriteRedactsProseAndRefusesCode(t *testing.T) {
	s := newStore(t, nil)
	value := testKey()
	_, err := s.Put(Definition{Kind: KindInstructions, Name: "consolidation", Model: model, Text: "Keep the deploy line; AWS_ACCESS_KEY_ID=" + value + " stays out."})
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	_ = filepath.WalkDir(s.Dir, func(path string, e os.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return err
		}
		data, _ := os.ReadFile(path)
		if strings.Contains(string(data), value) {
			t.Errorf("%s holds the value", path)
		}
		if strings.Contains(string(data), "AWS_ACCESS_KEY_ID=[redacted:") {
			found++
		}
		return nil
	})
	if found < 2 {
		t.Fatalf("the redacted assignment is in %d files, want the definition's file and its blob", found)
	}
	got, err := s.Get(model, KindInstructions, "consolidation")
	if err != nil || strings.Contains(got.Text, value) || !strings.Contains(got.Text, "AWS_ACCESS_KEY_ID=[redacted:") {
		t.Fatalf("read back %q (%v)", got.Text, err)
	}
	// Writing the same text again changes nothing: the stored text is the redacted one.
	again, err := s.Put(Definition{Kind: KindInstructions, Name: "consolidation", Model: model, Text: "Keep the deploy line; AWS_ACCESS_KEY_ID=" + value + " stays out."})
	if err != nil || again.Text != got.Text {
		t.Fatalf("again %q (%v)", again.Text, err)
	}
	err = s.Check(Definition{Kind: KindDetector, Name: "leaky", Model: model, Text: "func Count() int {\n\tconst k = \"AWS_ACCESS_KEY_ID=" + value + "\"\n\treturn len(k)\n}"})
	if err == nil || !strings.Contains(err.Error(), "holds a secret") || !strings.Contains(err.Error(), "line 2") || strings.Contains(err.Error(), value) {
		t.Fatalf("a detector holding a secret: %v", err)
	}
}
