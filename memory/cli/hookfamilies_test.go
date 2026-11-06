package cli

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/transcript"

	"github.com/drvova/pig-observational-memory/memory/testfix"
)

func writeLines(t *testing.T, path string, lines []string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func hookContext(t *testing.T, out string) string {
	t.Helper()
	if strings.TrimSpace(out) == "" {
		return ""
	}
	var r struct {
		Out struct {
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatalf("hook output %q: %v", out, err)
	}
	return r.Out.Context
}

// Gemini CLI: AfterAgent records, PreCompress compacts, and since its SessionStart never says
// it compacted, the next BeforeAgent hands the summary, once.
func TestGeminiHooks(t *testing.T) {
	counter := testfix.CountRequests(t)
	root, home := env(t)
	proj := filepath.Join(home, "proj")
	tr := filepath.Join(home, ".gemini", "tmp", "p", "chats", "session.jsonl")
	lines := []string{`{"sessionId":"g1","projectHash":"p","startTime":"2026-05-01T10:00:00.000Z","lastUpdated":"2026-05-01T10:00:00.000Z","kind":"main"}`}
	for i := range 60 {
		lines = append(lines,
			fmt.Sprintf(`{"id":"u%d","timestamp":"2026-05-01T10:00:00.000Z","type":"user","content":[{"text":"Service svc-%d listens on port %d and caches in /var/cache/svc%d."}]}`, i, i, 8000+i, i),
			fmt.Sprintf(`{"id":"g%d","timestamp":"2026-05-01T10:00:30.000Z","type":"gemini","content":"Noted: svc-%d on port %d.","thoughts":[],"model":"m"}`, i, i, 8000+i))
	}
	writeLines(t, tr, lines)
	payload, _ := json.Marshal(map[string]any{"session_id": "g1", "transcript_path": tr, "cwd": proj, "source": "startup", "trigger": "auto"})
	if ctx := hookContext(t, om(t, string(payload), "hook", "gemini-cli", "SessionStart")); !strings.Contains(ctx, "MCP server om") {
		t.Fatalf("start %q", ctx)
	}
	if ctx := hookContext(t, om(t, string(payload), "hook", "gemini-cli", "BeforeAgent")); ctx != "" {
		t.Fatalf("a summary before any compaction: %q", ctx)
	}
	om(t, string(payload), "hook", "gemini-cli", "AfterAgent")
	if data, err := os.ReadFile(filepath.Join(root, "sessions", "gemini-cli", "g1.jsonl")); err != nil || !strings.Contains(string(data), "om.observations.recorded") {
		t.Fatalf("no level-0 batch: %v", err)
	}
	om(t, string(payload), "hook", "gemini-cli", "PreCompress")
	if ctx := hookContext(t, om(t, string(payload), "hook", "gemini-cli", "BeforeAgent")); !strings.Contains(ctx, "before the compaction") || !strings.Contains(ctx, "8042") {
		t.Fatalf("summary %q", ctx)
	}
	if ctx := hookContext(t, om(t, string(payload), "hook", "gemini-cli", "BeforeAgent")); ctx != "" {
		t.Fatalf("summary handed twice: %q", ctx)
	}
	if counter.Total() != 0 {
		t.Fatalf("requests %v", counter.ByStage())
	}
}

// Kimi CLI's payload names no transcript: om finds its wire file from the cwd and session.
func TestKimiHooks(t *testing.T) {
	root, home := env(t)
	share := filepath.Join(home, "kimi")
	t.Setenv("KIMI_SHARE_DIR", share)
	proj := filepath.Join(home, "proj")
	sum := md5.Sum([]byte(proj))
	lines := []string{`{"type":"metadata","protocol_version":"1.10"}`}
	for i := range 40 {
		ts := 1777629600 + i*10
		lines = append(lines,
			fmt.Sprintf(`{"timestamp":%d,"message":{"type":"TurnBegin","payload":{"user_input":"Service svc-%d listens on port %d."}}}`, ts, i, 8000+i),
			fmt.Sprintf(`{"timestamp":%d,"message":{"type":"ContentPart","payload":{"type":"text","text":"Noted: svc-%d."}}}`, ts+1, i),
			fmt.Sprintf(`{"timestamp":%d,"message":{"type":"TurnEnd","payload":{}}}`, ts+2))
	}
	writeLines(t, filepath.Join(share, "sessions", hex.EncodeToString(sum[:]), "k1", "wire.jsonl"), lines)
	payload, _ := json.Marshal(map[string]any{"session_id": "k1", "cwd": proj, "hook_event_name": "Stop"})
	om(t, string(payload), "hook", "kimi-cli", "Stop")
	if data, err := os.ReadFile(filepath.Join(root, "sessions", "kimi-cli", "k1.jsonl")); err != nil || !strings.Contains(string(data), "svc-7") {
		t.Fatalf("no level-0 batch: %v", err)
	}
}

// Kimi's hooks are [[hooks]] tables in its TOML config, each line marked; uninstall leaves the
// rest of the file as it was.
func TestTOMLHooksRoundTrip(t *testing.T) {
	_, home := env(t)
	cfg := filepath.Join(home, ".kimi", "config.toml")
	before := "default_model = \"k\"\n\n[[hooks]]\nevent = \"Stop\"\ncommand = \"echo mine\"\n"
	writeLines(t, cfg, []string{strings.TrimSuffix(before, "\n")})
	om(t, "", "install", "kimi-cli")
	data, _ := os.ReadFile(cfg)
	if strings.Count(string(data), "[[hooks]]") != 5 || !strings.Contains(string(data), "hook kimi-cli PreCompact") || !strings.Contains(string(data), "echo mine") {
		t.Fatalf("installed:\n%s", data)
	}
	if out := om(t, "", "install", "kimi-cli"); !strings.Contains(out, "unchanged") {
		t.Fatalf("reinstall %s", out)
	}
	om(t, "", "uninstall", "kimi-cli")
	if data, _ = os.ReadFile(cfg); string(data) != before {
		t.Fatalf("after uninstall:\n%q", data)
	}
}

// Factory Droid speaks Claude Code's hooks: Stop records, PreCompact compacts, and the
// SessionStart after Droid's compaction hands om's summary. Its hooks.json is keyed by event.
func TestDroidHooks(t *testing.T) {
	_, home := env(t)
	proj := filepath.Join(home, "proj")
	tr := filepath.Join(home, ".factory", "sessions", "-proj", "d1.jsonl")
	lines := []string{`{"type":"session_start","id":"d1","title":"t","owner":"me","version":2,"cwd":"/proj"}`}
	for i := range 60 {
		lines = append(lines,
			fmt.Sprintf(`{"type":"message","id":"u%d","timestamp":"2026-05-01T10:00:00.000Z","message":{"role":"user","content":[{"type":"text","text":"Service svc-%d listens on port %d and caches in /var/cache/svc%d."}]}}`, i, i, 8000+i, i),
			fmt.Sprintf(`{"type":"message","id":"a%d","timestamp":"2026-05-01T10:00:30.000Z","message":{"role":"assistant","content":[{"type":"text","text":"Noted: svc-%d on port %d."}]}}`, i, i, 8000+i))
	}
	writeLines(t, tr, lines)
	payload := func(source string) string {
		b, _ := json.Marshal(map[string]any{"session_id": "d1", "transcript_path": tr, "cwd": proj, "source": source, "trigger": "auto", "hook_event_name": "x"})
		return string(b)
	}
	om(t, payload(""), "hook", "factory-droid", "Stop")
	om(t, payload(""), "hook", "factory-droid", "PreCompact")
	if ctx := hookContext(t, om(t, payload("compact"), "hook", "factory-droid", "SessionStart")); !strings.Contains(ctx, "before the compaction") || !strings.Contains(ctx, "8042") {
		t.Fatalf("summary %q", ctx)
	}

	cfg := filepath.Join(home, ".factory", "hooks.json")
	writeLines(t, cfg, []string{`{"Stop":[{"hooks":[{"type":"command","command":"echo mine"}]}]}`})
	om(t, "", "install", "factory-droid")
	data, _ := os.ReadFile(cfg)
	var hooks map[string]any
	json.Unmarshal(data, &hooks)
	if len(hooks["Stop"].([]any)) != 2 || hooks["PreCompact"] == nil || hooks["hooks"] != nil {
		t.Fatalf("installed %s", data)
	}
	om(t, "", "uninstall", "factory-droid")
	data, _ = os.ReadFile(cfg)
	hooks = nil
	json.Unmarshal(data, &hooks)
	if len(hooks) != 1 || len(hooks["Stop"].([]any)) != 1 {
		t.Fatalf("after uninstall %s", data)
	}
}

// Copilot CLI: camelCase payloads, om reads the session's events.jsonl, agentStop records,
// preCompact compacts, and a resumed sessionStart hands the summary as a top-level
// additionalContext. Its hooks go flat under settings.json's hooks key.
func TestCopilotHooks(t *testing.T) {
	_, home := env(t)
	cop := filepath.Join(home, ".copilot")
	t.Setenv("COPILOT_HOME", cop)
	proj := filepath.Join(home, "proj")
	lines := []string{`{"id":"e0","parentId":null,"timestamp":"2026-05-01T10:00:00.000Z","type":"session.start","data":{"sessionId":"c1"}}`}
	for i := range 60 {
		lines = append(lines,
			fmt.Sprintf(`{"id":"u%d","timestamp":"2026-05-01T10:00:00.000Z","type":"user.message","data":{"content":"Service svc-%d listens on port %d and caches in /var/cache/svc%d."}}`, i, i, 8000+i, i),
			fmt.Sprintf(`{"id":"a%d","timestamp":"2026-05-01T10:00:30.000Z","type":"assistant.message","data":{"messageId":"m%d","content":"Noted: svc-%d on port %d."}}`, i, i, i, 8000+i))
	}
	writeLines(t, filepath.Join(cop, "session-state", "c1", "events.jsonl"), lines)
	payload := func(source string) string {
		b, _ := json.Marshal(map[string]any{"sessionId": "c1", "timestamp": 1777629600000, "cwd": proj, "source": source, "trigger": "auto", "transcriptPath": filepath.Join(home, "elsewhere.jsonl")})
		return string(b)
	}
	var start map[string]string
	if err := json.Unmarshal([]byte(om(t, payload("startup"), "hook", "copilot", "sessionStart")), &start); err != nil || !strings.Contains(start["additionalContext"], "MCP server om") {
		t.Fatalf("start %v %v", start, err)
	}
	om(t, payload(""), "hook", "copilot", "agentStop")
	om(t, payload(""), "hook", "copilot", "preCompact")
	if err := json.Unmarshal([]byte(om(t, payload("resume"), "hook", "copilot", "sessionStart")), &start); err != nil || !strings.Contains(start["additionalContext"], "8042") {
		t.Fatalf("resume %v %v", start, err)
	}

	cfg := filepath.Join(cop, "settings.json")
	writeLines(t, cfg, []string{`{"model":"x","hooks":{"agentStop":[{"type":"command","bash":"echo mine"}]}}`})
	om(t, "", "install", "copilot")
	data, _ := os.ReadFile(cfg)
	var s struct {
		Model string                      `json:"model"`
		Hooks map[string][]map[string]any `json:"hooks"`
	}
	json.Unmarshal(data, &s)
	if len(s.Hooks["agentStop"]) != 2 || s.Hooks["preCompact"][0]["timeoutSec"] != float64(600) || s.Model != "x" {
		t.Fatalf("installed %s", data)
	}
	om(t, "", "uninstall", "copilot")
	data, _ = os.ReadFile(cfg)
	s.Hooks = nil
	json.Unmarshal(data, &s)
	if len(s.Hooks) != 1 || len(s.Hooks["agentStop"]) != 1 {
		t.Fatalf("after uninstall %s", data)
	}
}

// Oh My Pi and Gentle Shell take Pi's extension in their own agent directories, under their own
// harness names, so their memory is kept apart from Pi's.
func TestPiExtensionHarnesses(t *testing.T) {
	_, home := env(t)
	t.Setenv("PI_CONFIG_DIR", "")
	t.Setenv("GENTLE_SHELL_HOME", "")
	for name, path := range map[string]string{
		"oh-my-pi":     filepath.Join(home, ".omp", "agent", "extensions", "om.ts"),
		"gentle-shell": filepath.Join(home, ".gentle-shell", "agent", "extensions", "om.ts"),
	} {
		om(t, "", "install", name)
		b, err := os.ReadFile(path)
		if err != nil || strings.Contains(string(b), "__OM_") || !strings.Contains(string(b), `const HARNESS: string = "`+name+`"`) {
			t.Fatalf("%s: %v\n%s", name, err, b)
		}
		om(t, "", "uninstall", name)
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s left %s", name, path)
		}
	}
}

// Kode's payload points at a plain-text rendering; om reads the Claude-format session log Kode
// keeps under its root instead, at Stop and PreCompact.
func TestKodeHooks(t *testing.T) {
	root, home := env(t)
	t.Setenv("KODE_CONFIG_DIR", "")
	t.Setenv("ANYKODE_CONFIG_DIR", "")
	proj := filepath.Join(home, "proj")
	slug := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, proj)
	claudeTranscript(t, filepath.Join(home, ".kode", "projects", slug, "k1.jsonl"), 60)
	text := filepath.Join(home, "k1.transcript.txt")
	writeLines(t, text, []string{"user: not this"})
	payload, _ := json.Marshal(map[string]any{"session_id": "k1", "transcript_path": text, "cwd": proj, "hook_event_name": "Stop"})
	om(t, string(payload), "hook", "kode", "Stop")
	if data, err := os.ReadFile(filepath.Join(root, "sessions", "kode", "k1.jsonl")); err != nil || !strings.Contains(string(data), "svc-7") {
		t.Fatalf("no level-0 batch: %v", err)
	}
	om(t, string(payload), "hook", "kode", "PreCompact")
	if data, _ := os.ReadFile(filepath.Join(root, "sessions", "kode", "k1.jsonl")); !strings.Contains(string(data), `"compaction"`) {
		t.Fatal("no compaction stored")
	}
}

// Trae, Letta Code and Windsurf hand their hooks the turn's text: om records it in its own
// transcript, so level 0 sees every turn. Letta's Stop names no session; om takes the one its
// prompt hook recorded for the directory.
func TestRecordedTurns(t *testing.T) {
	root, home := env(t)
	proj := filepath.Join(home, "proj")
	run := func(harness, event string, payload map[string]any) string {
		b, _ := json.Marshal(payload)
		return om(t, string(b), "hook", harness, event)
	}
	for i := range 30 {
		run("trae", "UserPromptSubmit", map[string]any{"session_id": "t1", "cwd": proj, "prompt": fmt.Sprintf("Service svc-%d listens on port %d.", i, 8000+i)})
		run("trae", "Stop", map[string]any{"session_id": "t1", "cwd": proj, "last_assistant_message": fmt.Sprintf("Noted svc-%d.", i)})

		run("letta-code", "UserPromptSubmit", map[string]any{"event_type": "UserPromptSubmit", "working_directory": proj, "prompt": "x", "agent_id": "agent-1", "conversation_id": "default"})
		run("letta-code", "Stop", map[string]any{"event_type": "Stop", "working_directory": proj, "user_message": fmt.Sprintf("Queue q-%d drains into bucket b-%d.", i, i), "assistant_message": "ok"})

		run("devin", "pre_user_prompt", map[string]any{"trajectory_id": "w1", "agent_action_name": "pre_user_prompt", "tool_info": map[string]any{"user_prompt": fmt.Sprintf("Host h-%d runs on rack r-%d.", i, i)}})
		run("devin", "post_cascade_response", map[string]any{"trajectory_id": "w1", "agent_action_name": "post_cascade_response", "tool_info": map[string]any{"response": "### Planner Response\nnoted"}})
	}
	for harness, want := range map[string]string{"trae/t1": "svc-17", "letta-code/default:agent-1": "b-17", "devin/w1": "r-17"} {
		data, err := os.ReadFile(filepath.Join(root, "sessions", harness+".jsonl"))
		if err != nil || !strings.Contains(string(data), want) {
			t.Fatalf("%s: no level-0 record of %s: %v", harness, want, err)
		}
	}
	if data, _ := os.ReadFile(filepath.Join(root, "transcripts", "trae", "t1.jsonl")); strings.Count(string(data), "\n") != 60 || !strings.Contains(string(data), `"id":"t000060"`) {
		t.Fatalf("trae transcript:\n%s", data)
	}
	if out := run("devin", "post_cascade_response", map[string]any{"trajectory_id": "w1", "tool_info": map[string]any{}}); out != "" {
		t.Fatalf("windsurf output %q", out)
	}
	run("letta-code", "PreCompact", map[string]any{"event_type": "PreCompact", "working_directory": proj, "agent_id": "agent-1", "conversation_id": "default"})
	if out := run("letta-code", "UserPromptSubmit", map[string]any{"event_type": "UserPromptSubmit", "working_directory": proj, "prompt": "y", "agent_id": "agent-1", "conversation_id": "default"}); !strings.Contains(out, "before the compaction") || strings.HasPrefix(out, "{") {
		t.Fatalf("letta summary %q", out)
	}
}

// Cursor's hooks.json: version 1, flat commands with timeout in seconds; uninstall leaves the
// user's own hooks.
func TestCursorHooksInstall(t *testing.T) {
	_, home := env(t)
	cfg := filepath.Join(home, ".cursor", "hooks.json")
	writeLines(t, cfg, []string{`{"version":1,"hooks":{"stop":[{"command":"./mine.sh"}]}}`})
	om(t, "", "install", "cursor")
	var c struct {
		Version int                         `json:"version"`
		Hooks   map[string][]map[string]any `json:"hooks"`
	}
	data, _ := os.ReadFile(cfg)
	json.Unmarshal(data, &c)
	if c.Version != 1 || len(c.Hooks["stop"]) != 2 || c.Hooks["preCompact"][0]["timeout"] != float64(600) {
		t.Fatalf("installed %s", data)
	}
	om(t, "", "uninstall", "cursor")
	data, _ = os.ReadFile(cfg)
	c.Hooks = nil
	json.Unmarshal(data, &c)
	if len(c.Hooks) != 1 || len(c.Hooks["stop"]) != 1 {
		t.Fatalf("after uninstall %s", data)
	}
}

// Open Interpreter is a Codex fork: Claude-style hooks over its rollout.
func TestOpenInterpreterHooks(t *testing.T) {
	root, home := env(t)
	rollout := filepath.Join(home, ".openinterpreter", "sessions", "2026", "11", "05", "rollout-x-th9.jsonl")
	lines := []string{`{"timestamp":"2026-11-05T10:00:00Z","type":"session_meta","payload":{"id":"th9","cwd":"/p"}}`}
	for i := range 40 {
		lines = append(lines, fmt.Sprintf(`{"timestamp":"2026-11-05T10:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Queue q-%d drains into bucket b-%d every %d minutes."}]}}`, i, i, i+5))
	}
	writeLines(t, rollout, lines)
	b, _ := json.Marshal(map[string]any{"session_id": "th9", "transcript_path": rollout, "cwd": "/p", "hook_event_name": "Stop"})
	om(t, string(b), "hook", "open-interpreter", "Stop")
	if data, err := os.ReadFile(filepath.Join(root, "sessions", "open-interpreter", "th9.jsonl")); err != nil || !strings.Contains(string(data), "b-17") {
		t.Fatalf("no level-0 batch: %v", err)
	}
}

// Amp's plugin pipes each turn's messages; om records them, tool calls and results included.
// Cline's TaskComplete names its session in sessionContext; om reads that session's messages
// file. Both installs write their own files: Amp's plugin and Cline's executable hook.
func TestAmpAndClineHooks(t *testing.T) {
	root, home := env(t)
	for _, k := range []string{"CLINE_DIR", "CLINE_DATA_DIR"} {
		t.Setenv(k, "")
	}
	for i := range 30 {
		b, _ := json.Marshal(map[string]any{"session_id": "T-1", "cwd": "/p", "messages": []any{
			map[string]any{"role": "user", "id": fmt.Sprintf("u%d", i), "content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("Queue q-%d drains into bucket b-%d.", i, i)}}},
			map[string]any{"role": "assistant", "id": fmt.Sprintf("a%d", i), "content": []any{map[string]any{"type": "tool_use", "id": fmt.Sprintf("c%d", i), "name": "Bash", "input": map[string]any{"cmd": "ls"}}}},
			map[string]any{"role": "user", "id": fmt.Sprintf("r%d", i), "content": []any{map[string]any{"type": "tool_result", "toolUseID": fmt.Sprintf("c%d", i), "output": "a.go", "status": "done"}}},
		}})
		om(t, string(b), "hook", "amp", "agent.end")
	}
	if data, err := os.ReadFile(filepath.Join(root, "sessions", "amp", "T-1.jsonl")); err != nil || !strings.Contains(string(data), "b-17") {
		t.Fatalf("amp: no level-0 record: %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "transcripts", "amp", "T-1.jsonl")); !strings.Contains(string(data), `"toolCallId":"c3"`) {
		t.Fatalf("amp transcript:\n%s", data)
	}

	var msgs []any
	for i := range 30 {
		msgs = append(msgs, map[string]any{"id": fmt.Sprintf("m%d", i), "role": "user", "content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("Host h-%d runs on rack r-%d.", i, i)}}, "ts": 1762300000000})
	}
	doc, _ := json.MarshalIndent(map[string]any{"version": 1, "sessionId": "s9", "messages": msgs}, "", "  ")
	writeLines(t, filepath.Join(home, ".cline", "data", "sessions", "s9", "s9.messages.json"), []string{string(doc)})
	b, _ := json.Marshal(map[string]any{"hookName": "agent_end", "taskId": "conv_1", "sessionContext": map[string]any{"rootSessionId": "s9"}, "workspaceRoots": []any{"/p"}})
	om(t, string(b), "hook", "cline", "TaskComplete")
	if data, err := os.ReadFile(filepath.Join(root, "sessions", "cline", "s9.jsonl")); err != nil || !strings.Contains(string(data), "r-17") {
		t.Fatalf("cline: no level-0 record: %v", err)
	}

	om(t, "", "install", "amp")
	om(t, "", "install", "cline")
	om(t, "", "install", "neovate-code")
	hook := filepath.Join(home, ".cline", "hooks", "TaskComplete")
	if st, err := os.Stat(hook); err != nil || st.Mode()&0o111 == 0 {
		t.Fatalf("cline hook %v %v", st, err)
	}
	for _, p := range []string{hook, filepath.Join(home, ".config", "amp", "plugins", "om.ts"), filepath.Join(home, ".neovate", "plugins", "om.js")} {
		if b, err := os.ReadFile(p); err != nil || strings.Contains(string(b), "__OM_") {
			t.Fatalf("%s: %v", p, err)
		}
	}
	for _, h := range []string{"amp", "cline", "neovate-code"} {
		om(t, "", "uninstall", h)
	}
	if _, err := os.Stat(hook); !os.IsNotExist(err) {
		t.Fatal("cline hook left")
	}
}

// Cortex Code: om reads <session>.history.jsonl beside the .json the payload names, compacts at
// PreCompact, and hands the summary with the next prompt. iFlow passes fields in the
// environment too.
func TestCortexAndIFlowHooks(t *testing.T) {
	root, home := env(t)
	t.Setenv("SNOWFLAKE_HOME", "")
	conv := filepath.Join(home, ".snowflake", "cortex", "conversations")
	var lines []string
	for i := range 60 {
		lines = append(lines, fmt.Sprintf(`{"role":"user","id":"u%d","content":[{"type":"text","text":"Service svc-%d listens on port %d and caches in /var/cache/svc%d.","is_user_prompt":true}],"user_sent_time":"2026-05-01T10:00:00Z"}`, i, i, 8000+i, i),
			fmt.Sprintf(`{"role":"assistant","id":"a%d","content":[{"type":"text","text":"Noted: svc-%d on port %d."}]}`, i, i, 8000+i))
	}
	writeLines(t, filepath.Join(conv, "c1.history.jsonl"), lines)
	b, _ := json.Marshal(map[string]any{"session_id": "c1", "transcript_path": filepath.Join(conv, "c1.json"), "cwd": "/p", "hook_event_name": "Stop"})
	om(t, string(b), "hook", "cortex-code", "Stop")
	om(t, string(b), "hook", "cortex-code", "PreCompact")
	if ctx := hookContext(t, om(t, string(b), "hook", "cortex-code", "UserPromptSubmit")); !strings.Contains(ctx, "8042") {
		t.Fatalf("cortex summary %q", ctx)
	}

	tr := filepath.Join(home, ".iflow", "projects", "-p", "session-1.jsonl")
	lines = nil
	for i := range 30 {
		lines = append(lines, fmt.Sprintf(`{"uuid":"u%d","parentUuid":null,"sessionId":"session-1","timestamp":"2026-05-01T10:00:00.000Z","type":"user","message":{"role":"user","content":"Queue q-%d drains into bucket b-%d."}}`, i, i, i))
	}
	writeLines(t, tr, lines)
	t.Setenv("IFLOW_SESSION_ID", "session-1")
	t.Setenv("IFLOW_TRANSCRIPT_PATH", tr)
	t.Setenv("IFLOW_CWD", "/p")
	om(t, "", "hook", "iflow-cli", "Stop")
	if data, err := os.ReadFile(filepath.Join(root, "sessions", "iflow-cli", "session-1.jsonl")); err != nil || !strings.Contains(string(data), "b-17") {
		t.Fatalf("iflow: no level-0 record: %v", err)
	}
}

// sqliteDB builds a database with Python's sqlite3 module (the tests' container has no sqlite3
// command) and points om's SQLite reader at python3 alone.
func sqliteDB(t *testing.T, path string, statements ...string) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		for _, p := range []string{"/usr/bin/python3", "/usr/local/bin/python3", "/opt/homebrew/bin/python3"} {
			if _, e := os.Stat(p); e == nil {
				py, err = p, nil
			}
		}
	}
	if err != nil {
		t.Skip("python3 is needed to build the SQLite fixture")
	}
	old := transcript.SQLiteCommands
	transcript.SQLiteCommands = []string{py}
	t.Cleanup(func() { transcript.SQLiteCommands = old })
	// The engine image (OM_ENGINE=lisp) looks the reader up on the PATH of the hook that started
	// it, not in SQLiteCommands: the same python3, alone on PATH.
	if os.Getenv("PATH") == "" {
		bin := t.TempDir()
		if err := os.Symlink(py, filepath.Join(bin, "python3")); err == nil {
			t.Setenv("PATH", bin)
		}
	}
	os.MkdirAll(filepath.Dir(path), 0o700)
	script := "import sqlite3, sys, json\nc = sqlite3.connect(sys.argv[1])\nfor s in json.loads(sys.argv[2]):\n    c.execute(s)\nc.commit()\n"
	stmts, _ := json.Marshal(statements)
	if out, err := exec.Command(py, "-I", "-c", script, path, string(stmts)).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v: %s", err, out)
	}
}

func sqlString(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Crush, Goose and Amazon Q Developer CLI keep sessions in SQLite: om reads each one's rows (in
// the schema from its source) at the turn-end hook, through python3 when there is no sqlite3.
func TestSQLiteHooks(t *testing.T) {
	root, home := env(t)
	t.Setenv("GOOSE_PATH_ROOT", "")
	t.Setenv("XDG_DATA_HOME", "")
	proj := filepath.Join(home, "proj")

	stmts := []string{"CREATE TABLE messages (id TEXT PRIMARY KEY, session_id TEXT, role TEXT, parts TEXT DEFAULT '[]', created_at INTEGER, is_summary_message INTEGER DEFAULT 0)"}
	for i := range 40 {
		user, _ := json.Marshal([]any{map[string]any{"type": "text", "data": map[string]any{"text": fmt.Sprintf("Queue q-%d drains into bucket b-%d.", i, i)}}})
		call, _ := json.Marshal([]any{map[string]any{"type": "tool_call", "data": map[string]any{"id": fmt.Sprintf("c%d", i), "name": "bash", "input": `{"command":"ls"}`}}})
		result, _ := json.Marshal([]any{map[string]any{"type": "tool_result", "data": map[string]any{"tool_call_id": fmt.Sprintf("c%d", i), "name": "bash", "content": "a.go", "is_error": false}}})
		for j, row := range []struct{ role, parts string }{{"user", string(user)}, {"assistant", string(call)}, {"tool", string(result)}} {
			stmts = append(stmts, fmt.Sprintf("INSERT INTO messages VALUES ('m%d-%d', 's1', '%s', %s, %d, 0)", i, j, row.role, sqlString(row.parts), 1762300000+i*10+j))
		}
	}
	stmts = append(stmts, "INSERT INTO messages VALUES ('sum', 's1', 'assistant', '[{\"type\":\"text\",\"data\":{\"text\":\"SUMMARY ROW\"}}]', 1762400000, 1)")
	sqliteDB(t, filepath.Join(proj, ".crush", "crush.db"), stmts...)
	b, _ := json.Marshal(map[string]any{"event": "TurnEnd", "hook_event_name": "TurnEnd", "session_id": "s1", "cwd": proj})
	om(t, string(b), "hook", "crush", "TurnEnd")
	data, err := os.ReadFile(filepath.Join(root, "sessions", "crush", "s1.jsonl"))
	if err != nil || !strings.Contains(string(data), "b-17") || strings.Contains(string(data), "SUMMARY ROW") {
		t.Fatalf("crush: %v", err)
	}

	stmts = []string{"CREATE TABLE messages (id INTEGER PRIMARY KEY AUTOINCREMENT, message_id TEXT, session_id TEXT, role TEXT, content_json TEXT, created_timestamp INTEGER, metadata_json TEXT)"}
	for i := range 40 {
		content, _ := json.Marshal([]any{map[string]any{"type": "text", "text": fmt.Sprintf("Host h-%d runs on rack r-%d.", i, i)}})
		stmts = append(stmts, fmt.Sprintf("INSERT INTO messages (message_id, session_id, role, content_json, created_timestamp, metadata_json) VALUES ('msg_%d', 'g1', 'user', %s, %d, '{\"userVisible\":true,\"agentVisible\":true}')", i, sqlString(string(content)), 1762300000+i))
	}
	sqliteDB(t, filepath.Join(home, ".local", "share", "goose", "sessions", "sessions.db"), stmts...)
	b, _ = json.Marshal(map[string]any{"event": "Stop", "session_id": "g1", "working_dir": proj})
	om(t, string(b), "hook", "goose", "Stop")
	if data, err := os.ReadFile(filepath.Join(root, "sessions", "goose", "g1.jsonl")); (err != nil || !strings.Contains(string(data), "r-17")) && runtime.GOOS == "linux" {
		t.Fatalf("goose: %v", err)
	}

	var history []any
	for i := range 30 {
		history = append(history, map[string]any{
			"user":      map[string]any{"content": map[string]any{"Prompt": map[string]any{"prompt": fmt.Sprintf("Service svc-%d listens on port %d.", i, 8000+i)}}, "timestamp": "2026-05-01T10:00:00Z"},
			"assistant": map[string]any{"Response": map[string]any{"message_id": fmt.Sprintf("am%d", i), "content": "noted"}},
		})
	}
	state, _ := json.Marshal(map[string]any{"conversation_id": "conv-1", "history": history})
	dbPath := transcript.AmazonQDBPath("", "")
	sqliteDB(t, dbPath, "CREATE TABLE conversations (key TEXT PRIMARY KEY, value TEXT)", fmt.Sprintf("INSERT INTO conversations VALUES (%s, %s)", sqlString(proj), sqlString(string(state))))
	b, _ = json.Marshal(map[string]any{"hook_event_name": "stop", "cwd": proj})
	om(t, string(b), "hook", "amazon-q", "stop")
	if data, err := os.ReadFile(filepath.Join(root, "sessions", "amazon-q", "conv-1.jsonl")); err != nil || !strings.Contains(string(data), "svc-17") {
		t.Fatalf("amazon-q: %v", err)
	}

	om(t, "", "install", "goose")
	om(t, "", "install", "crush")
	if b, err := os.ReadFile(filepath.Join(home, ".agents", "plugins", "om", "hooks", "hooks.json")); err != nil || !strings.Contains(string(b), "hook goose Stop") {
		t.Fatalf("goose plugin: %v", err)
	}
	var crushCfg map[string]map[string][]map[string]any
	cb, _ := os.ReadFile(filepath.Join(home, ".config", "crush", "crush.json"))
	json.Unmarshal(cb, &crushCfg)
	if e := crushCfg["hooks"]["TurnEnd"]; len(e) != 1 || e[0]["type"] != nil || e[0]["timeout"] != float64(120) {
		t.Fatalf("crush hooks %s", cb)
	}
	om(t, "", "uninstall", "goose")
	om(t, "", "uninstall", "crush")
	if _, err := os.Stat(filepath.Join(home, ".agents", "plugins", "om", "plugin.json")); !os.IsNotExist(err) {
		t.Fatal("goose plugin left")
	}
}

// Nanocoder passes its prompt in the environment and has no turn-end hook: om records prompts,
// runs level 0 at the next prompt, and writes its hooks under nanocoder.hooks. Kiro's prompt
// hook records the request; its hooks are a file of om's own. CodeBuddy prints om's summary at
// PreCompact for its own compaction.
func TestPromptOnlyHarnesses(t *testing.T) {
	root, home := env(t)
	t.Setenv("NANOCODER_CONFIG_DIR", "")
	for i := range 30 {
		t.Setenv("NANOCODER_SESSION_ID", "ab12cd34")
		t.Setenv("NANOCODER_CWD", "/p")
		t.Setenv("NANOCODER_PROMPT", fmt.Sprintf("Queue q-%d drains into bucket b-%d.", i, i))
		om(t, "", "hook", "nanocoder", "user-prompt-submit")

		b, _ := json.Marshal(map[string]any{"hook_event_name": "UserPromptSubmit", "cwd": "/p", "session_id": "k1", "prompt": fmt.Sprintf("Host h-%d runs on rack r-%d.", i, i)})
		om(t, string(b), "hook", "kiro", "UserPromptSubmit")
		om(t, `{"hook_event_name":"Stop","cwd":"/p","session_id":"k1"}`, "hook", "kiro", "Stop")

		b, _ = json.Marshal(map[string]any{"session_id": "cb1", "cwd": "/p", "prompt": fmt.Sprintf("Service svc-%d listens on port %d and caches in /var/cache/svc%d.", i, 8000+i, i)})
		om(t, string(b), "hook", "codebuddy", "UserPromptSubmit")
		om(t, `{"session_id":"cb1","cwd":"/p","last_assistant_message":"noted"}`, "hook", "codebuddy", "Stop")
	}
	for harness, want := range map[string]string{"nanocoder/ab12cd34": "b-17", "kiro/k1": "r-17", "codebuddy/cb1": "svc-17"} {
		if data, err := os.ReadFile(filepath.Join(root, "sessions", harness+".jsonl")); err != nil || !strings.Contains(string(data), want) {
			t.Fatalf("%s: no level-0 record of %s: %v", harness, want, err)
		}
	}
	if out := om(t, `{"session_id":"cb1","cwd":"/p","trigger":"auto"}`, "hook", "codebuddy", "PreCompact"); !strings.Contains(out, "svc-") || strings.HasPrefix(out, "{") {
		t.Fatalf("codebuddy pre-compact %q", out)
	}

	cfg := filepath.Join(home, ".config", "nanocoder", "agents.config.json")
	writeLines(t, cfg, []string{`{"nanocoder":{"providers":[]}}`})
	om(t, "", "install", "nanocoder")
	om(t, "", "install", "kiro")
	var nc map[string]map[string]map[string][]map[string]any
	data, _ := os.ReadFile(cfg)
	json.Unmarshal(data, &nc)
	if e := nc["nanocoder"]["hooks"]["user-prompt-submit"]; len(e) != 1 || e[0]["timeout"] != float64(30000) || e[0]["type"] != nil {
		t.Fatalf("nanocoder hooks %s", data)
	}
	if b, err := os.ReadFile(filepath.Join(home, ".kiro", "hooks", "om.json")); err != nil || !strings.Contains(string(b), `"trigger": "UserPromptSubmit"`) {
		t.Fatalf("kiro hooks %s %v", b, err)
	}
	om(t, "", "uninstall", "nanocoder")
	om(t, "", "uninstall", "kiro")
	if data, _ := os.ReadFile(cfg); !strings.Contains(string(data), "providers") || strings.Contains(string(data), "hooks") {
		t.Fatalf("nanocoder after uninstall %s", data)
	}
}

// When Kiro's session is under ~/.kiro/sessions/<bucket>/sess_<id>, om reads its messages.jsonl
// (replies and tool results included) instead of recording the prompts itself.
func TestKiroTranscript(t *testing.T) {
	root, home := env(t)
	t.Setenv("KIRO_HOME", "")
	var lines []string
	for i := range 30 {
		lines = append(lines,
			fmt.Sprintf(`{"id":"u%d","timestamp":"2026-10-08T15:01:57.955Z","payload":{"type":"user","content":"Host h-%d runs on rack r-%d.","documents":[],"images":[]}}`, i, i, i),
			fmt.Sprintf(`{"id":"s%d","timestamp":"2026-10-08T15:02:00.868Z","payload":{"type":"assistant","content":"Noted h-%d.","operationType":"Say","executionId":"e%d"}}`, i, i, i))
	}
	writeLines(t, filepath.Join(home, ".kiro", "sessions", "b1", "sess_k9", "messages.jsonl"), lines)
	om(t, `{"hook_event_name":"Stop","cwd":"/p","session_id":"k9"}`, "hook", "kiro", "Stop")
	if data, err := os.ReadFile(filepath.Join(root, "sessions", "kiro", "k9.jsonl")); err != nil || !strings.Contains(string(data), "r-17") {
		t.Fatalf("no level-0 record from Kiro's transcript: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "transcripts", "kiro", "k9.jsonl")); !os.IsNotExist(err) {
		t.Fatal("om recorded the prompts although Kiro's transcript was found")
	}
}

// Codex's hooks (beside notify) read its rollout: Stop records, PreCompact compacts, and the
// SessionStart after a compaction hands the summary back.
func TestCodexHooks(t *testing.T) {
	_, home := env(t)
	rollout := filepath.Join(home, ".codex", "sessions", "2026", "11", "05", "rollout-x-th7.jsonl")
	lines := []string{`{"timestamp":"2026-11-05T10:00:00Z","type":"session_meta","payload":{"id":"th7","cwd":"/p"}}`}
	for i := range 60 {
		lines = append(lines, fmt.Sprintf(`{"timestamp":"2026-11-05T10:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Service svc-%d listens on port %d and caches in /var/cache/svc%d."}]}}`, i, 8000+i, i))
	}
	writeLines(t, rollout, lines)
	payload := func(source string) string {
		b, _ := json.Marshal(map[string]any{"session_id": "th7", "transcript_path": rollout, "cwd": "/p", "hook_event_name": "x", "source": source, "trigger": "auto"})
		return string(b)
	}
	om(t, payload(""), "hook", "codex", "Stop")
	om(t, payload(""), "hook", "codex", "PreCompact")
	if ctx := hookContext(t, om(t, payload("compact"), "hook", "codex", "SessionStart")); !strings.Contains(ctx, "before the compaction") || !strings.Contains(ctx, "svc-58") {
		t.Fatalf("codex summary %q", ctx)
	}
	om(t, "", "install", "codex")
	if b, err := os.ReadFile(filepath.Join(home, ".codex", "hooks.json")); err != nil || !strings.Contains(string(b), "hook codex PreCompact") {
		t.Fatalf("codex hooks.json %s %v", b, err)
	}
}

// The data harnesses with hooks: each records its turns from the payload, Tabnine's settings get
// hooks switched on, and Mastra Code's hooks.json is keyed by event.
func TestDataHarnessHooks(t *testing.T) {
	root, home := env(t)
	for i := range 30 {
		b, _ := json.Marshal(map[string]any{"session_id": "b1", "cwd": "/p", "prompt": fmt.Sprintf("Queue q-%d drains into bucket b-%d.", i, i)})
		om(t, string(b), "hook", "ibm-bob", "UserPromptSubmit")
		om(t, `{"session_id":"b1","cwd":"/p","last_assistant_message":"noted"}`, "hook", "ibm-bob", "Stop")
		b, _ = json.Marshal(map[string]any{"session_id": "t1", "cwd": "/p", "prompt": fmt.Sprintf("Host h-%d runs on rack r-%d.", i, i), "prompt_response": "ok"})
		om(t, string(b), "hook", "tabnine", "AfterAgent")
		b, _ = json.Marshal(map[string]any{"session_id": "m1", "cwd": "/p", "user_message": fmt.Sprintf("Service svc-%d listens on port %d.", i, 8000+i)})
		om(t, string(b), "hook", "mastra-code", "UserPromptSubmit")
		om(t, `{"session_id":"m1","cwd":"/p","assistant_message":"noted"}`, "hook", "mastra-code", "Stop")
	}
	for harness, want := range map[string]string{"ibm-bob/b1": "b-17", "tabnine/t1": "r-17", "mastra-code/m1": "svc-17"} {
		if data, err := os.ReadFile(filepath.Join(root, "sessions", harness+".jsonl")); err != nil || !strings.Contains(string(data), want) {
			t.Fatalf("%s: no level-0 record of %s: %v", harness, want, err)
		}
	}
	om(t, "", "install", "tabnine")
	om(t, "", "install", "mastra-code")
	var tab struct {
		HooksConfig map[string]bool `json:"hooksConfig"`
		Hooks       map[string]any  `json:"hooks"`
	}
	b, _ := os.ReadFile(filepath.Join(home, ".tabnine", "agent", "settings.json"))
	json.Unmarshal(b, &tab)
	if !tab.HooksConfig["enabled"] || tab.Hooks["AfterAgent"] == nil {
		t.Fatalf("tabnine settings %s", b)
	}
	var mc map[string][]map[string]any
	b, _ = os.ReadFile(filepath.Join(home, ".mastracode", "hooks.json"))
	json.Unmarshal(b, &mc)
	if e := mc["Stop"]; len(e) != 1 || e[0]["timeout"] != float64(120000) || e[0]["type"] != "command" {
		t.Fatalf("mastra hooks %s", b)
	}
}
