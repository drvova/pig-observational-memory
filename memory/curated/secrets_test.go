package curated

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A secret in a save, a staging or an edit is redacted before the memory is stored, and the
// index never shows one, a memory written before the scanner included (scenario 13 of
// docs/brain-memory.md).
func TestCuratedWritesRedactSecrets(t *testing.T) {
	value := strings.Join([]string{"Hb1Jc5Fd0Ge8", "QaT2Zq8Vt2"}, "")
	dir := t.TempDir()
	s := New(dir)
	saved, err := s.Save(Memory{Name: "deploy-key", Type: TypeProject, Description: "deploy uses api_key=" + value, Body: "export API_KEY=" + value}, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(saved.Description+saved.Body, value) {
		t.Fatalf("saved %+v", saved)
	}
	if _, err := s.Save(Memory{Name: "staged-key", Type: TypeProject, Description: "the token is token=" + value, Staged: true}, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Save(Memory{Name: "staged-key", Type: TypeProject, Description: "the token is token=" + value, Staged: true}, "s2"); err != nil {
		t.Fatal(err)
	}
	staged, err := s.Get("staged-key")
	if err != nil {
		t.Fatal(err)
	}
	if len(staged.StagedBy) != 2 {
		t.Fatalf("the same staging did not join: %+v", staged)
	}
	body := "password=" + value
	if _, err := s.Update("deploy-key", "", Edit{Body: &body}, "s1"); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err != nil || len(files) != 2 {
		t.Fatalf("files %v %v", files, err)
	}
	for _, f := range files {
		text, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(text), value) {
			t.Errorf("%s holds the secret:\n%s", f, text)
		}
		if !strings.Contains(string(text), "[redacted:") {
			t.Errorf("%s lost the redaction's place:\n%s", f, text)
		}
	}
	if line := IndexLine(Memory{Name: "old", Type: TypeProject, Description: "written before: secret=" + value}); strings.Contains(line, value) {
		t.Errorf("index line %q", line)
	}
}
