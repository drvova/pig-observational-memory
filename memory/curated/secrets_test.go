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

// A quote holding a secret (a feedback's, a forget's, a release's) is kept redacted in the usage
// ledger, and a forget's quote still counts against MaxForgetsPerQuote (the Tier 0b residue
// suite, docs/brain-memory-baseline.md, found the ledger holding them).
func TestUsageLedgerRedactsQuotes(t *testing.T) {
	value := strings.Join([]string{"Hb1Jc5Fd0Ge8", "QaT2Zq8Vt2"}, "")
	dir := t.TempDir()
	s := New(dir)
	quote := "forget the deploy notes, api_key=" + value + " is revoked"
	c := Call{Store: s, Source: "s1", UserEvidence: func(string) bool { return true }}
	for i := 0; i < MaxForgetsPerQuote+1; i++ {
		name := "note-" + string(rune('a'+i))
		if _, err := c.Run(OpSave, map[string]string{"name": name, "type": TypeProject, "description": "A deploy note.", "quote": "remember the deploy notes"}); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			if _, err := c.Run(OpFeedback, map[string]string{"name": name, "outcome": EventHelped, "quote": "yes, token=" + value + " was the one"}); err != nil {
				t.Fatal(err)
			}
		}
		text, err := Call{Store: s, Source: "s2", UserEvidence: func(string) bool { return true }}.Run(OpForget, map[string]string{"name": name, "quote": quote})
		if err != nil {
			t.Fatal(err)
		}
		if retired := strings.HasPrefix(text, "Forgot memory"); retired != (i < MaxForgetsPerQuote) {
			t.Fatalf("forget %d: %s", i, text)
		}
	}
	raw, err := os.ReadFile(filepath.Join(dir, "usage.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), value) || !strings.Contains(string(raw), "[redacted:") {
		t.Fatalf("usage ledger:\n%s", raw)
	}
}
