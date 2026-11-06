package extract_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/extract"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/transcript"
)

var writeEncoding = flag.Bool("write", false, "rewrite testdata/encoding-records.jsonl")

// encodingBranch is lisp/tests/encoding.lisp's encoding-test-branch.
func encodingBranch() []ledger.Entry {
	lines := []map[string]any{
		{"id": "u1", "timestamp": "2026-05-01T10:00:00Z", "role": "user", "text": "I think the port is 8080. Pin react@18.3.1 please."},
		{"id": "a1", "timestamp": "2026-05-01T10:01:00Z", "role": "assistant", "text": "Apparently the tests are flaky.",
			"tools": []any{map[string]any{"id": "c1", "name": "bash", "arguments": map[string]any{"command": "make test"}}}},
		{"id": "r1", "timestamp": "2026-05-01T10:02:00Z", "role": "tool", "toolCallId": "c1", "toolName": "bash", "text": "boom: no rule\nCommand exited with code 2"},
		{"id": "a2", "timestamp": "2026-05-01T10:03:00Z", "role": "assistant", "text": "",
			"tools": []any{map[string]any{"id": "c2", "name": "write", "arguments": map[string]any{"path": "src/a.go"}}}},
		{"id": "r2", "timestamp": "2026-05-01T10:04:00Z", "role": "tool", "toolCallId": "c2", "toolName": "write", "text": "ok"},
	}
	return transcript.Generic(lines)
}

// TestEncodeRecordsBytes pins level 0's encoding fields with a writer and a frame: the Lisp port's
// encoding-level0-records reads the same file and must write the same bytes.
func TestEncodeRecordsBytes(t *testing.T) {
	entries := encodingBranch()
	result := extract.Extract(extract.Input{
		Entries: entries, End: len(entries), Ledger: ledger.FoldLedger(nil, ""), Cwd: "/work",
		Writer:   ledger.Writer{Harness: "golden", Session: "s1", Engine: ledger.EngineGo},
		Frame:    ledger.Frame{RepoID: "r1", Branch: "main", Head: "abc"},
		RepoRoot: "/work",
	})
	var b strings.Builder
	byKind := map[string]ledger.Observation{}
	for _, o := range result.Observations() {
		line, err := json.Marshal(o)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
		if _, seen := byKind[string(o.Kind)+":"+o.Source]; !seen {
			byKind[string(o.Kind)+":"+o.Source] = o
		}
	}
	path := filepath.Join("testdata", "encoding-records.jsonl")
	if *writeEncoding {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (go test ./extract -run EncodeRecordsBytes -args -write)", err)
	}
	if string(want) != b.String() {
		t.Fatalf("records differ from %s:\n%s", path, b.String())
	}
	// The rules the file pins, stated: the user's hedged fact, the failed attempt and the file.
	fact := byKind["fact:user-said"]
	if fact.Authority != ledger.AuthorityUser || fact.Hedge != ledger.HedgeHedged || !fact.Keys.IsZero() || fact.Writer.Harness != "golden" || fact.Frame.RepoID != "r1" {
		t.Errorf("user fact: %+v", fact)
	}
	attempt := byKind["attempt:tool-observed"]
	if attempt.Authority != ledger.AuthorityTool || attempt.Hedge != ledger.HedgeUnverified || attempt.Keys.Commands[0] != "make test" || attempt.Keys.Errors[0] != "boom: no rule" {
		t.Errorf("attempt: %+v", attempt)
	}
	file := byKind["file:tool-observed"]
	if file.Keys.Paths[0] != "/work/src/a.go" || file.Frame.Path != "src/a.go" || file.Hedge != ledger.HedgeStated {
		t.Errorf("file: %+v", file)
	}
}

// Without a writer or a repository, records carry neither, and a record's id never depends on
// the encoding fields.
func TestEncodeLeavesIDs(t *testing.T) {
	entries := encodingBranch()
	bare := extract.Extract(extract.Input{Entries: entries, End: len(entries), Ledger: ledger.FoldLedger(nil, ""), Cwd: "/work"}).Observations()
	framed := extract.Extract(extract.Input{Entries: entries, End: len(entries), Ledger: ledger.FoldLedger(nil, ""), Cwd: "/work",
		Writer: ledger.Writer{Engine: ledger.EngineLisp}, Frame: ledger.Frame{RepoID: "x"}}).Observations()
	if len(bare) != len(framed) || len(bare) == 0 {
		t.Fatalf("%d vs %d records", len(bare), len(framed))
	}
	for i := range bare {
		if bare[i].ID != framed[i].ID {
			t.Errorf("record %d: id %s vs %s", i, bare[i].ID, framed[i].ID)
		}
		if bare[i].Writer != (ledger.Writer{}) || bare[i].Frame != (ledger.Frame{}) || bare[i].Source == "" {
			t.Errorf("bare record %d: %+v", i, bare[i])
		}
	}
}
