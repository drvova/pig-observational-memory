package transcript

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/extract"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
)

// fileRecords are level 0's file records over a whole transcript.
func fileRecords(es []session.Entry, cwd string) []string {
	r := extract.Extract(extract.Input{Entries: es, Start: 0, End: len(es), Ledger: ledger.FoldLedger(es, ""), Cwd: cwd})
	out := []string{}
	for _, o := range r.Observations() {
		if o.Kind == ledger.KindFile {
			out = append(out, o.Content)
		}
	}
	return out
}

// Level 0 reads the file state of a Codex rollout's apply_patch, as the harness wrote it.
func TestCodexRolloutFileState(t *testing.T) {
	es, err := File{Path: filepath.Join("testdata", "real", "codex-rollout.jsonl"), Format: Codex}.Entries()
	if err != nil {
		t.Fatal(err)
	}
	got := fileRecords(es, "/workspace")
	want := []string{"package.json written", "index.html written", "src/main.jsx written", "src/styles.css written"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("files %q, want %q", got, want)
	}
}

// Level 0 reads Claude Code's Read, Write, Edit, MultiEdit and NotebookEdit through its reader,
// from lines in the shape Claude Code writes them, and only those whose result did not fail.
func TestClaudeCodeFileState(t *testing.T) {
	lines := []string{
		`{"parentUuid":null,"isSidechain":false,"type":"user","message":{"role":"user","content":"Tidy the handlers"},"uuid":"u1","timestamp":"2026-09-24T03:01:00.000Z","cwd":"/w/app","sessionId":"s1"}`,
		`{"parentUuid":"u1","isSidechain":false,"type":"assistant","message":{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Read","input":{"file_path":"/w/app/a.go"}}],"stop_reason":"tool_use"},"uuid":"a1","timestamp":"2026-09-24T03:01:01.000Z","cwd":"/w/app","sessionId":"s1"}`,
		`{"parentUuid":"a1","isSidechain":false,"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_1","type":"tool_result","content":"     1\tpackage a\n"}]},"uuid":"r1","timestamp":"2026-09-24T03:01:02.000Z","cwd":"/w/app","sessionId":"s1"}`,
		`{"parentUuid":"r1","isSidechain":false,"type":"assistant","message":{"id":"msg_2","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_2","name":"Edit","input":{"file_path":"/w/app/b.go","old_string":"a","new_string":"b","replace_all":false}}],"stop_reason":"tool_use"},"uuid":"a2","timestamp":"2026-09-24T03:01:03.000Z","cwd":"/w/app","sessionId":"s1"}`,
		`{"parentUuid":"a2","isSidechain":false,"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_2","type":"tool_result","content":"The file /w/app/b.go has been updated."}]},"uuid":"r2","timestamp":"2026-09-24T03:01:04.000Z","cwd":"/w/app","sessionId":"s1"}`,
		`{"parentUuid":"r2","isSidechain":false,"type":"assistant","message":{"id":"msg_3","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_3","name":"Write","input":{"file_path":"/w/app/c.go","content":"package c\n"}},{"type":"tool_use","id":"toolu_4","name":"MultiEdit","input":{"file_path":"/w/app/d.go","edits":[{"old_string":"a","new_string":"b"}]}},{"type":"tool_use","id":"toolu_5","name":"NotebookEdit","input":{"notebook_path":"/w/app/e.ipynb","new_source":"x = 1","cell_id":"c1"}},{"type":"tool_use","id":"toolu_6","name":"Edit","input":{"file_path":"/w/app/f.go","old_string":"zz","new_string":"b"}}],"stop_reason":"tool_use"},"uuid":"a3","timestamp":"2026-09-24T03:01:05.000Z","cwd":"/w/app","sessionId":"s1"}`,
		`{"parentUuid":"a3","isSidechain":false,"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_3","type":"tool_result","content":"File created successfully at: /w/app/c.go"},{"tool_use_id":"toolu_4","type":"tool_result","content":"Applied 1 edit to /w/app/d.go"},{"tool_use_id":"toolu_5","type":"tool_result","content":"Updated cell c1"},{"tool_use_id":"toolu_6","type":"tool_result","content":"<tool_use_error>String to replace not found in file.</tool_use_error>","is_error":true}]},"uuid":"r3","timestamp":"2026-09-24T03:01:06.000Z","cwd":"/w/app","sessionId":"s1"}`,
	}
	path := filepath.Join(t.TempDir(), "s1.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	es, err := File{Path: path, Format: ClaudeCode}.Entries()
	if err != nil {
		t.Fatal(err)
	}
	got := fileRecords(es, "/w/app")
	want := []string{"a.go read", "b.go edited", "c.go written", "d.go edited", "e.ipynb edited"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("files %q, want %q", got, want)
	}
}
