package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/neutral"
	"github.com/drvova/pig-observational-memory/memory/session"
)

func write(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "t.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func roles(es []session.Entry) string {
	var out []string
	for _, e := range es {
		r, _ := e.Message["role"].(string)
		out = append(out, e.ID+":"+r)
	}
	return strings.Join(out, " ")
}

// The active branch is read across a compaction boundary; one assistant message's lines are
// one entry under the first uuid (the others aliases); a tool result is a toolResult entry
// named by its call; sidechain, meta and the harness's compaction summary are left out.
func TestClaudeCode(t *testing.T) {
	p := write(t,
		`{"type":"user","uuid":"u1","parentUuid":null,"timestamp":"2026-05-01T10:00:00.000Z","message":{"role":"user","content":"Deploy on port 8080."}}`,
		`{"type":"assistant","uuid":"a1","parentUuid":"u1","timestamp":"2026-05-01T10:00:01.000Z","message":{"id":"msg1","role":"assistant","content":[{"type":"text","text":"Checking the config."}]}}`,
		`{"type":"assistant","uuid":"a2","parentUuid":"a1","timestamp":"2026-05-01T10:00:02.000Z","message":{"id":"msg1","role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Bash","input":{"command":"cat app.yaml"}}]}}`,
		`{"type":"user","uuid":"r1","parentUuid":"a2","timestamp":"2026-05-01T10:00:03.000Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"port: 8080","is_error":false}]}}`,
		`{"type":"assistant","uuid":"s1","parentUuid":"r1","isSidechain":true,"message":{"id":"x","role":"assistant","content":[{"type":"text","text":"subagent"}]}}`,
		`{"type":"system","uuid":"b1","parentUuid":null,"logicalParentUuid":"r1","subtype":"compact_boundary"}`,
		`{"type":"user","uuid":"c1","parentUuid":"b1","isCompactSummary":true,"message":{"role":"user","content":"This session is being continued..."}}`,
		`{"type":"user","uuid":"m1","parentUuid":"c1","isMeta":true,"message":{"role":"user","content":"<command-name>"}}`,
		`{"type":"user","uuid":"u2","parentUuid":"m1","timestamp":"2026-05-02T09:00:00.000Z","message":{"role":"user","content":"Now migrate the database."}}`,
	)
	es, err := File{Path: p, Format: ClaudeCode}.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if got := roles(es); got != "u1:user a1:assistant r1:toolResult u2:user" {
		t.Fatalf("entries %s", got)
	}
	if a := neutral.Aliases(es[1]); len(a) != 1 || a[0] != "a2" {
		t.Fatalf("aliases %v", a)
	}
	if es[2].Message["toolName"] != "Bash" || es[2].Message["toolCallId"] != "t1" {
		t.Fatalf("tool result %+v", es[2].Message)
	}
	if blocks := es[1].Message["content"].([]any); len(blocks) != 2 || blocks[1].(map[string]any)["type"] != "toolCall" {
		t.Fatalf("assistant blocks %v", blocks)
	}
	if ms, _ := es[0].Message["timestamp"].(float64); ms == 0 {
		t.Fatal("no message timestamp")
	}
	// A stored entry naming an alias lands after the merged entry.
	b := neutral.Branch(es, []neutral.Stored{{After: "a2", Entry: map[string]any{"type": "custom", "id": "om1"}}})
	if b[2].ID != "om1" {
		t.Fatalf("alias placement %s", roles(b))
	}
}

// A user line with origin.kind other than "human" (a background subagent's result, a message
// from another session) is a custom_message, never a user entry; its meta lines stay left out,
// and a line with origin.kind "human" or no origin stays a user entry.
func TestClaudeCodeOrigin(t *testing.T) {
	es, err := File{Path: filepath.Join("testdata", "synthetic", "claude-code-origin.jsonl"), Format: ClaudeCode}.Entries()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range es {
		kind := e.Type
		if e.Type == "message" {
			kind = e.Message["role"].(string)
		} else if e.CustomType != "" {
			kind += "(" + e.CustomType + ")"
		}
		got = append(got, e.ID+":"+kind)
	}
	want := "h1:user a1:assistant r1:toolResult a2:assistant n1:custom_message(origin:task-notification) p2:custom_message(origin:peer) a3:assistant u1:user"
	if strings.Join(got, " ") != want {
		t.Fatalf("entries\n got %s\nwant %s", strings.Join(got, " "), want)
	}
	n1 := es[4]
	blocks, _ := n1.Content.([]any)
	if len(blocks) != 1 || !strings.Contains(blocks[0].(map[string]any)["text"].(string), "<task-notification>") || n1.Timestamp != "2026-11-06T09:01:00.000Z" {
		t.Fatalf("notification entry %+v", n1.Raw)
	}
	for _, e := range es {
		if e.Type == "message" && e.Message["role"] == "user" {
			if text := blocksText(e.Message["content"]); strings.Contains(text, "always") || strings.Contains(text, "Forget") || strings.Contains(text, "Never") {
				t.Fatalf("a non-human line became a user entry: %s", e.ID)
			}
		}
	}
	// A Claude Code-shaped reader whose lines carry an origin with no kind (Cline) keeps them.
	if kind := originKind(map[string]any{"origin": map[string]any{"mode": "user", "source": "cli"}}); kind != "" {
		t.Fatalf("origin without kind read as %q", kind)
	}
}

func blocksText(v any) string {
	var out []string
	for _, raw := range blocks(v) {
		if b, ok := raw.(map[string]any); ok {
			s, _ := b["text"].(string)
			out = append(out, s)
		}
	}
	return strings.Join(out, "\n")
}

// Codex: assistant-side items group into one entry; tool output is a toolResult named by
// its call; Codex's own context messages are left out; ids are line positions.
func TestCodex(t *testing.T) {
	args, _ := json.Marshal(map[string]any{"command": []string{"ls"}})
	p := write(t,
		`{"type":"session_meta","payload":{"id":"s"}}`,
		`{"timestamp":"2026-05-01T10:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>cwd</environment_context>"}]}}`,
		`{"timestamp":"2026-05-01T10:00:01Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"List the files."}]}}`,
		`{"timestamp":"2026-05-01T10:00:02Z","type":"response_item","payload":{"type":"reasoning","summary":[{"type":"summary_text","text":"Run ls."}]}}`,
		`{"timestamp":"2026-05-01T10:00:03Z","type":"response_item","payload":{"type":"function_call","name":"shell","arguments":`+string(mustJSON(string(args)))+`,"call_id":"c1"}}`,
		`{"timestamp":"2026-05-01T10:00:04Z","type":"response_item","payload":{"type":"function_call_output","call_id":"c1","output":"a.go"}}`,
		`{"timestamp":"2026-05-01T10:00:05Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"One file: a.go."}]}}`,
		`{"timestamp":"2026-05-01T10:00:05Z","type":"event_msg","payload":{"type":"agent_message"}}`,
	)
	es, _ := File{Path: p, Format: Codex}.Entries()
	if got := roles(es); got != "cx000002:user cx000003:assistant cx000005:toolResult cx000006:assistant" {
		t.Fatalf("entries %s", got)
	}
	call := es[1].Message["content"].([]any)[1].(map[string]any)
	if call["name"] != "shell" || call["arguments"].(map[string]any)["command"] == nil || es[2].Message["toolName"] != "shell" {
		t.Fatalf("call %v result %v", call, es[2].Message)
	}
}

func mustJSON(s string) []byte { b, _ := json.Marshal(s); return b }

// Pi: the active branch from the newest entry; generic: the interchange lines.
func TestPiAndGeneric(t *testing.T) {
	p := write(t,
		`{"type":"session","id":"h"}`,
		`{"type":"message","id":"e1","parentId":null,"message":{"role":"user","content":"a"}}`,
		`{"type":"message","id":"x","parentId":"e1","message":{"role":"user","content":"abandoned"}}`,
		`{"type":"message","id":"e2","parentId":"e1","message":{"role":"assistant","content":[]}}`,
	)
	es, _ := File{Path: p, Format: Pi}.Entries()
	if got := roles(es); got != "e1:user e2:assistant" {
		t.Fatalf("pi %s", got)
	}
	g := write(t,
		`{"role":"user","text":"Ship it.","timestamp":"2026-05-01T10:00:00Z"}`,
		`{"id":"m2","role":"assistant","text":"Running tests.","tools":[{"id":"c1","name":"bash","arguments":{"command":"go test"}}]}`,
		`{"id":"m3","role":"tool","toolCallId":"c1","toolName":"bash","text":"ok","isError":false}`,
	)
	es, _ = File{Path: g, Format: Generic}.Entries()
	if got := roles(es); got != "g000000:user m2:assistant m3:toolResult" {
		t.Fatalf("generic %s", got)
	}
	if es, err := (File{Path: filepath.Join(t.TempDir(), "none"), Format: Generic}).Entries(); err != nil || len(es) != 0 {
		t.Fatalf("missing file %v %v", es, err)
	}
}
