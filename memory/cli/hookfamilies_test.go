package cli

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
