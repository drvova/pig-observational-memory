package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/hostinfo"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

func history() []map[string]any {
	var h []map[string]any
	data := `[
	 {"role":"system","content":"You are a coding agent. Docs live in docs/handbook/SYSTEM_ONLY.md."},
	 {"role":"user","content":[{"type":"text","text":"Fix the crash in services/api/handler.go: it raises ConnectionRefusedError when PORT=8443 is set."}]},
	 {"role":"assistant","content":[{"type":"think","think":"Read the handler first."},{"type":"text","text":"Reading the handler."}],
	  "tool_calls":[{"id":"Read:0","type":"function","function":{"name":"Read","arguments":"{\"file_path\":\"services/api/handler.go\"}"}}]},
	 {"role":"tool","tool_call_id":"Read:0","content":"package api\n// dials 10.0.0.7:5432"},
	 {"role":"assistant","content":"The handler dials the database before the config loads; I will move the dial into Start."}
	]`
	if err := json.Unmarshal([]byte(data), &h); err != nil {
		panic(err)
	}
	return h
}

func TestConvertHistory(t *testing.T) {
	c := convertHistory(history())
	if c.SystemMessages != 1 || !strings.Contains(c.SystemText, "SYSTEM_ONLY") {
		t.Fatalf("system %d %q", c.SystemMessages, c.SystemText)
	}
	if len(c.Entries) != 4 {
		t.Fatalf("%d entries", len(c.Entries))
	}
	a := c.Entries[1].Message
	content := a["content"].([]any)
	if a["role"] != "assistant" || len(content) != 3 || content[0].(map[string]any)["type"] != "thinking" {
		t.Fatalf("assistant %v", a)
	}
	call := content[2].(map[string]any)
	if call["type"] != "toolCall" || call["arguments"].(map[string]any)["file_path"] != "services/api/handler.go" {
		t.Fatalf("call %v", call)
	}
	r := c.Entries[2].Message
	if r["role"] != "toolResult" || r["toolName"] != "Read" || r["toolCallId"] != "Read:0" {
		t.Fatalf("result %v", r)
	}
	if c.Entries[0].ID != "cb000001" || c.Entries[0].Timestamp >= c.Entries[1].Timestamp {
		t.Fatalf("ids and times %s %s %s", c.Entries[0].ID, c.Entries[0].Timestamp, c.Entries[1].Timestamp)
	}
}

// A replay runs level 0 and the selection with no model request, is byte-identical between runs,
// and scores atoms as the benchmark does: verbatim in the summary.
func TestReplaySession(t *testing.T) {
	counter := testfix.CountRequests(t)
	root := t.TempDir()
	old := hostinfo.AgentDir
	hostinfo.AgentDir = func() (string, error) { return root, nil }
	t.Cleanup(func() { hostinfo.AgentDir = old })
	s := Session{SampleID: "s:1", Client: "test", H: history(), Atoms: []Atom{
		{Kind: "path", Value: "services/api/handler.go"},
		{Kind: "kv", Value: "PORT=8443"},
		{Kind: "path", Value: "docs/handbook/SYSTEM_ONLY.md"},
		{Kind: "sha", Value: "deadbeef1"},
	}}
	a := replaySession(root, s, 0, true)
	b := replaySession(root, s, 0, true)
	if a.Error != "" || a.S == "" || a.S != b.S {
		t.Fatalf("replay %q, not deterministic or empty:\n%s\n---\n%s", a.Error, a.S, b.S)
	}
	if !strings.Contains(a.S, "services/api/handler.go") || !strings.Contains(a.S, "PORT=8443") {
		t.Fatalf("summary lacks the user's details:\n%s", a.S)
	}
	if a.Recall != 0.5 || a.SystemOnly != 1 || a.SourceRecall != 0.5 || a.ReplayedRecall != 1 {
		t.Fatalf("scores %+v", a)
	}
	if a.Kinds["path"] != [2]int{2, 1} {
		t.Fatalf("kinds %v", a.Kinds)
	}
	tiny := replaySession(root, s, 1, false)
	if tiny.Error != "" || tiny.S != "" || tiny.Recall > a.Recall {
		t.Fatalf("a one-token budget %+v", tiny)
	}
	if counter.Total() != 0 {
		t.Fatalf("requests %v", counter.ByStage())
	}
}

func TestScoreBoundary(t *testing.T) {
	b := Boundary{K: 1, S: "Done: wrote cmd/server/main.go. Next steps: add tests for cmd/server/main.go and pkg/x.go.",
		AtomsF: map[string][]string{"path": {"cmd/server/main.go", "pkg/y.go"}, "sha": {"abc1234"}}, DoneFiles: []string{"/repo/cmd/server/main.go", "notes.md"}}
	s := scoreBoundary(b)
	if s.NAtoms != 3 || s.R == nil || *s.R != 1.0/3 || s.Triplet != 1 {
		t.Fatalf("score %+v", s)
	}
	empty := scoreBoundary(Boundary{K: 2, S: "x"})
	if empty.R != nil {
		t.Fatal("a boundary without atoms has no R_k")
	}
}

func TestRunCommands(t *testing.T) {
	root := t.TempDir()
	old := hostinfo.AgentDir
	t.Cleanup(func() { hostinfo.AgentDir = old })
	data := filepath.Join(root, "data")
	os.MkdirAll(filepath.Join(data, "e2"), 0o755)
	os.MkdirAll(filepath.Join(data, "e3"), 0o755)
	chain, _ := json.Marshal(Chain{Sess: "c", Client: "x", NBounds: 2, PerK: []Boundary{
		{K: 1, S: "a/b.go", AtomsF: map[string][]string{"path": {"a/b.go", "c/d.go"}}},
		{K: 2, S: "", AtomsF: map[string][]string{"path": {"a/b.go"}}},
	}})
	os.WriteFile(filepath.Join(data, "e3", "chains.jsonl"), append(chain, '\n'), 0o644)
	sess, _ := json.Marshal(Session{SampleID: "s1", Client: "x", H: history(), Atoms: []Atom{{Kind: "path", Value: "services/api/handler.go"}}})
	os.WriteFile(filepath.Join(data, "e2", "sessions.jsonl"), append(sess, '\n'), 0o644)
	var out, errb bytes.Buffer
	if code := run([]string{"production", "-data", data}, &out, &errb); code != 0 {
		t.Fatalf("production %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "| 1 | 50.0% | 1 |") || !strings.Contains(out.String(), "ratio mean = 0.00") {
		t.Fatalf("production report:\n%s", out.String())
	}
	out.Reset()
	jsonl := filepath.Join(root, "out.jsonl")
	if code := run([]string{"retention", "-data", data, "-budgets", "16384,0", "-out", jsonl}, &out, &errb); code != 0 {
		t.Fatalf("retention %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "| om-level0@16384 | 100.0% | 1 | 0 |") || !strings.Contains(out.String(), "| om-level0 | 100.0% |") {
		t.Fatalf("retention report:\n%s", out.String())
	}
	lines, _ := os.ReadFile(jsonl)
	if strings.Count(string(lines), "\n") != 2 {
		t.Fatalf("out:\n%s", lines)
	}
	if code := run([]string{"retention"}, &out, &errb); code != 2 {
		t.Fatalf("no -data: %d", code)
	}
}
