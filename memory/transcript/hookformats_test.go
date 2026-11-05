package transcript

import (
	"crypto/md5"
	"encoding/hex"
	"path/filepath"
	"strings"
	"testing"
)

// The samples are the ones the format research built from each harness's own types.

// Gemini: a re-appended record stands once, at its first place, in its last form; the user
// record's functionResponse is the tool result; header and $set lines are not entries.
func TestGemini(t *testing.T) {
	p := write(t,
		`{"sessionId":"3f2a","projectHash":"ab12","startTime":"2026-11-04T10:15:00.000Z","lastUpdated":"2026-11-04T10:15:00.000Z","kind":"main"}`,
		`{"id":"u1","timestamp":"2026-11-04T10:15:02.000Z","type":"user","content":[{"text":"List the Go files"}]}`,
		`{"id":"g1","timestamp":"2026-11-04T10:15:04.000Z","type":"gemini","content":"I'll list them.","thoughts":[{"subject":"Plan","description":"Use ls","timestamp":"2026-11-04T10:15:03.000Z"}],"model":"gemini-2.5-pro"}`,
		`{"id":"g1","timestamp":"2026-11-04T10:15:04.000Z","type":"gemini","content":"I'll list them.","thoughts":[],"model":"gemini-2.5-pro","toolCalls":[{"id":"call-1","name":"run_shell_command","args":{"command":"ls *.go"},"result":[{"functionResponse":{"id":"call-1","name":"run_shell_command","response":{"output":"main.go"}}}],"status":"success","timestamp":"2026-11-04T10:15:05.000Z"}]}`,
		`{"id":"u2","timestamp":"2026-11-04T10:15:05.100Z","type":"user","content":[{"functionResponse":{"id":"call-1","name":"run_shell_command","response":{"output":"main.go"}}}]}`,
		`{"id":"x1","timestamp":"2026-11-04T10:15:06.000Z","type":"info","content":"cli note"}`,
		`{"id":"g2","timestamp":"2026-11-04T10:15:07.000Z","type":"gemini","content":"There is one file: main.go.","thoughts":[],"tokens":null,"model":"gemini-2.5-pro"}`,
		`{"$set":{"lastUpdated":"2026-11-04T10:15:07.000Z"}}`,
		`{"id":"u3","timestamp":"2026-11-04T10:16:00.000Z","type":"user","content":[{"text":"undo me"}]}`,
		`{"$rewindTo":"u3"}`,
	)
	es, err := File{Path: p, Format: Gemini}.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if got := roles(es); got != "u1:user g1:assistant u2:toolResult g2:assistant" {
		t.Fatalf("entries %s", got)
	}
	blocks := es[1].Message["content"].([]any)
	if len(blocks) != 2 || blocks[1].(map[string]any)["name"] != "run_shell_command" {
		t.Fatalf("assistant %v", blocks)
	}
	if es[2].Message["toolCallId"] != "call-1" || es[2].Message["isError"] != false {
		t.Fatalf("result %v", es[2].Message)
	}
}

// Qwen: the parentUuid chain, thought parts as thinking, functionCall as tool calls, the
// tool_result record's status as the error flag.
func TestQwen(t *testing.T) {
	p := write(t,
		`{"uuid":"a1","parentUuid":null,"sessionId":"s-123","timestamp":"2026-11-04T10:15:02.000Z","type":"user","message":{"role":"user","parts":[{"text":"List the Go files"}]}}`,
		`{"uuid":"a2","parentUuid":"a1","sessionId":"s-123","timestamp":"2026-11-04T10:15:04.000Z","type":"assistant","message":{"role":"model","parts":[{"text":"Checking","thought":true},{"text":"I'll list them."},{"functionCall":{"id":"call_1","name":"run_shell_command","args":{"command":"ls *.go"}}}]}}`,
		`{"uuid":"a3","parentUuid":"a2","sessionId":"s-123","timestamp":"2026-11-04T10:15:05.000Z","type":"tool_result","message":{"role":"user","parts":[{"functionResponse":{"id":"call_1","name":"run_shell_command","response":{"output":"no such file"}}}]},"toolCallResult":{"callId":"call_1","status":"error"}}`,
		`{"uuid":"c1","parentUuid":"a3","sessionId":"s-123","timestamp":"2026-11-04T10:15:06.000Z","type":"user","subtype":"chat_compression","message":{"role":"user","parts":[{"text":"summary"}]}}`,
		`{"uuid":"a4","parentUuid":"c1","sessionId":"s-123","timestamp":"2026-11-04T10:15:07.000Z","type":"assistant","message":{"role":"model","parts":[{"text":"There is one file: main.go."}]}}`,
	)
	es, _ := File{Path: p, Format: Qwen}.Entries()
	if got := roles(es); got != "a1:user a2:assistant a3:toolResult a4:assistant" {
		t.Fatalf("entries %s", got)
	}
	if b := es[1].Message["content"].([]any); b[0].(map[string]any)["type"] != "thinking" || b[2].(map[string]any)["type"] != "toolCall" {
		t.Fatalf("assistant %v", b)
	}
	if es[2].Message["isError"] != true {
		t.Fatal("the failed result is not an error")
	}
}

// Kimi: TurnBegin is the user, content parts and tool calls one assistant entry until the
// result, the result's is_error kept, unix timestamps converted.
func TestKimi(t *testing.T) {
	p := write(t,
		`{"type":"metadata","protocol_version":"1.10"}`,
		`{"timestamp":1793787302.1,"message":{"type":"TurnBegin","payload":{"user_input":"List the Go files"}}}`,
		`{"timestamp":1793787303.0,"message":{"type":"StepBegin","payload":{"n":1}}}`,
		`{"timestamp":1793787303.5,"message":{"type":"ContentPart","payload":{"type":"think","think":"Use ls."}}}`,
		`{"timestamp":1793787303.9,"message":{"type":"ContentPart","payload":{"type":"text","text":"I'll list them."}}}`,
		`{"timestamp":1793787304.2,"message":{"type":"ToolCall","payload":{"type":"function","id":"tool_abc","function":{"name":"Shell","arguments":"{\"command\": \"ls *.go\"}"},"extras":null}}}`,
		`{"timestamp":1793787305.0,"message":{"type":"ToolResult","payload":{"tool_call_id":"tool_abc","return_value":{"is_error":true,"output":"permission denied","message":"","display":[],"extras":null}}}}`,
		`{"timestamp":1793787306.0,"message":{"type":"ContentPart","payload":{"type":"text","text":"It failed."}}}`,
		`{"timestamp":1793787307.0,"message":{"type":"TurnEnd","payload":{}}}`,
	)
	es, _ := File{Path: p, Format: Kimi}.Entries()
	if got := roles(es); got != "kw000001:user kw000003:assistant kw000006:toolResult kw000007:assistant" {
		t.Fatalf("entries %s", got)
	}
	call := es[1].Message["content"].([]any)[2].(map[string]any)
	if call["name"] != "Shell" || call["arguments"].(map[string]any)["command"] != "ls *.go" {
		t.Fatalf("call %v", call)
	}
	if es[2].Message["isError"] != true || es[2].Message["toolName"] != "Shell" || !strings.HasPrefix(es[0].Timestamp, "2026-") {
		t.Fatalf("result %v ts %s", es[2].Message, es[0].Timestamp)
	}
	t.Setenv("KIMI_SHARE_DIR", "/k")
	sum := md5.Sum([]byte("/w/app"))
	if p := KimiWirePath("/w/app", "s1"); p != filepath.Join("/k", "sessions", hex.EncodeToString(sum[:]), "s1", "wire.jsonl") {
		t.Fatal(p)
	}
}

// Factory Droid: message lines in order, the tool_result in a user message a toolResult entry;
// the header and the harness's compaction_state are not entries.
func TestDroid(t *testing.T) {
	p := write(t,
		`{"type":"session_start","id":"s1","title":"t","owner":"me","version":2,"cwd":"/p"}`,
		`{"type":"message","id":"m1","timestamp":"2026-11-04T10:00:01.000Z","message":{"role":"user","content":[{"type":"text","text":"List files"}]}}`,
		`{"type":"message","id":"m2","timestamp":"2026-11-04T10:00:02.000Z","parentId":"m1","message":{"role":"assistant","content":[{"type":"thinking","thinking":"ls","signature":"x"},{"type":"text","text":"Listing."},{"type":"tool_use","id":"t1","name":"Execute","input":{"command":"ls"}}]}}`,
		`{"type":"message","id":"m3","timestamp":"2026-11-04T10:00:03.000Z","parentId":"m2","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"a.go\nb.go","is_error":false}]}}`,
		`{"type":"compaction_state","id":"c1","timestamp":"2026-11-04T10:00:03.500Z","summaryText":"earlier","summaryTokens":3,"removedCount":0}`,
		`{"type":"message","id":"m4","timestamp":"2026-11-04T10:00:04.000Z","parentId":"m3","message":{"role":"assistant","content":"Two files."}}`,
	)
	es, err := File{Path: p, Format: Droid}.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if got := roles(es); got != "m1:user m2:assistant m3:toolResult m4:assistant" {
		t.Fatalf("entries %s", got)
	}
	if es[2].Message["toolName"] != "Execute" || es[2].Message["content"].([]any)[0].(map[string]any)["text"] != "a.go\nb.go" {
		t.Fatalf("result %v", es[2].Message)
	}
	if len(es[1].Message["content"].([]any)) != 3 {
		t.Fatalf("assistant %v", es[1].Message)
	}
}

// Copilot CLI: user and assistant messages, the tool request a toolCall and its
// execution_complete the toolResult; the header, tool start and ephemeral events are not entries.
func TestCopilot(t *testing.T) {
	p := write(t,
		`{"id":"e1","parentId":null,"timestamp":"2026-11-04T10:00:00.000Z","type":"session.start","data":{"sessionId":"s1","copilotVersion":"1.0.105"}}`,
		`{"id":"e2","parentId":"e1","timestamp":"2026-11-04T10:00:01.000Z","type":"user.message","data":{"content":"List files"}}`,
		`{"id":"e3","parentId":"e2","timestamp":"2026-11-04T10:00:02.000Z","type":"assistant.message","data":{"messageId":"m1","content":"Listing.","reasoningText":"use ls","toolRequests":[{"toolCallId":"c1","name":"bash","arguments":{"command":"ls"}}]}}`,
		`{"id":"e4","parentId":"e3","timestamp":"2026-11-04T10:00:02.100Z","type":"tool.execution_start","data":{"toolCallId":"c1","toolName":"bash","arguments":{"command":"ls"}}}`,
		`{"id":"e5","parentId":"e4","timestamp":"2026-11-04T10:00:03.000Z","type":"tool.execution_complete","data":{"toolCallId":"c1","success":false,"error":{"message":"denied"}}}`,
		`{"id":"e6","parentId":"e5","timestamp":"2026-11-04T10:00:03.500Z","type":"assistant.message_delta","ephemeral":true,"data":{"deltaContent":"x"}}`,
		`{"id":"e7","parentId":"e5","timestamp":"2026-11-04T10:00:04.000Z","type":"assistant.message","data":{"messageId":"m2","content":"It failed."}}`,
		`{"id":"e8","parentId":"e7","timestamp":"2026-11-04T10:00:05.000Z","type":"assistant.message","agentId":"sub1","data":{"messageId":"m3","content":"subagent"}}`,
	)
	es, err := File{Path: p, Format: Copilot}.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if got := roles(es); got != "e2:user e3:assistant e5:toolResult e7:assistant" {
		t.Fatalf("entries %s", got)
	}
	if es[2].Message["toolName"] != "bash" || es[2].Message["isError"] != true || len(es[1].Message["content"].([]any)) != 3 {
		t.Fatalf("result %v assistant %v", es[2].Message, es[1].Message)
	}
	t.Setenv("COPILOT_HOME", "/c")
	if got := CopilotEventsPath("s1"); got != filepath.Join("/c", "session-state", "s1", "events.jsonl") {
		t.Fatalf("path %s", got)
	}
}

// Oh My Pi: the 256-byte title slot and the header come before Pi-shaped entries.
func TestOhMyPi(t *testing.T) {
	title := `{"type":"title","title":"t"}`
	title += strings.Repeat(" ", 255-len(title))
	p := write(t,
		title,
		`{"type":"session","version":3,"id":"s1","cwd":"/p"}`,
		`{"type":"message","id":"e1","parentId":null,"timestamp":"2026-11-04T10:00:00.000Z","message":{"role":"user","content":"hi","timestamp":1}}`,
		`{"type":"model_usage","id":"e2","parentId":"e1","timestamp":"2026-11-04T10:00:01.000Z"}`,
		`{"type":"message","id":"e3","parentId":"e2","timestamp":"2026-11-04T10:00:02.000Z","message":{"role":"assistant","content":[{"type":"text","text":"hello"}],"timestamp":2}}`,
	)
	es, err := File{Path: p, Format: Pi}.Entries()
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 3 || es[0].ID != "e1" || es[2].ID != "e3" {
		t.Fatalf("entries %v", es)
	}
}
