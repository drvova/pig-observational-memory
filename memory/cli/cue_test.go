package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/neutral"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// appendClaudeRule appends a user line holding text and an assistant reply to a Claude Code
// transcript claudeTranscript wrote.
func appendClaudeRule(t *testing.T, path, text string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	n := strings.Count(string(data), "\n")
	u, _ := json.Marshal(map[string]any{"type": "user", "uuid": fmt.Sprintf("ur%d", n), "parentUuid": fmt.Sprintf("a%d", n/2-1), "timestamp": "2026-05-01T11:00:00.000Z", "message": map[string]any{"role": "user", "content": text}})
	a, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": fmt.Sprintf("ar%d", n), "parentUuid": fmt.Sprintf("ur%d", n), "timestamp": "2026-05-01T11:00:30.000Z", "message": map[string]any{"id": fmt.Sprintf("mr%d", n), "role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Understood."}}}})
	if err := os.WriteFile(path, append(data, []byte(string(u)+"\n"+string(a)+"\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Decision 4 on Claude Code: after a Stop recorded a rule on migrations/, a PostToolUse on a path
// inside it hands one hint plus an om snippet as additionalContext and appends an om.cue entry;
// the same record is not cued again before a compaction; a look-alike path, or cueCap 0, gets no
// cue (scenario 28). No request is made.
func TestClaudeCodeCue(t *testing.T) {
	counter := testfix.CountRequests(t)
	root, home := env(t)
	proj := filepath.Join(home, "proj")
	tr := filepath.Join(home, "t.jsonl")
	claudeTranscript(t, tr, 3)
	appendClaudeRule(t, tr, "Don't touch `migrations/`.")
	stop, _ := json.Marshal(map[string]any{"session_id": "s1", "transcript_path": tr, "cwd": proj})
	om(t, string(stop), "hook", "claude-code", "Stop")
	tool := func(path string) string {
		b, _ := json.Marshal(map[string]any{"session_id": "s1", "transcript_path": tr, "cwd": proj, "hook_event_name": "PostToolUse",
			"tool_name": "Edit", "tool_input": map[string]any{"file_path": path}, "tool_response": map[string]any{"stdout": "ok"}})
		return string(b)
	}
	var got struct {
		Out struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if out := om(t, tool("migrations-old/0001.sql"), "hook", "claude-code", "PostToolUse"); strings.TrimSpace(out) != "" {
		t.Fatalf("a look-alike path got %q", out)
	}
	if err := json.Unmarshal([]byte(om(t, tool("migrations/0001_users.sql"), "hook", "claude-code", "PostToolUse")), &got); err != nil ||
		got.Out.Event != "PostToolUse" || !strings.Contains(got.Out.Context, "a standing rule names this") || !strings.Contains(got.Out.Context, "om.Recall(`") {
		t.Fatalf("cue %+v (%v)", got, err)
	}
	if out := om(t, tool("migrations/0002_teams.sql"), "hook", "claude-code", "PostToolUse"); strings.TrimSpace(out) != "" {
		t.Fatalf("the same record cued twice before a compaction: %q", out)
	}
	stored, _ := neutral.StoreFor(root, "claude-code", "s1").Load()
	cues := 0
	for _, s := range stored {
		if s.Entry["customType"] == ledger.OMCue {
			cues++
		}
	}
	if cues != 1 {
		t.Fatalf("%d om.cue entries, want 1", cues)
	}
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(`{"observational-memory":{"cueCap":0}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	om(t, string(stop), "hook", "claude-code", "PreCompact")
	if out := om(t, tool("migrations/0003_x.sql"), "hook", "claude-code", "PostToolUse"); strings.TrimSpace(out) != "" {
		t.Fatalf("cueCap 0 still cued: %q", out)
	}
	if n := counter.Total(); n != 0 {
		t.Fatalf("%d requests", n)
	}
}
