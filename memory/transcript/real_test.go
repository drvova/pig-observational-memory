package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/session"
)

// checkEntries asserts what the engine relies on: unique ids, no empty entry (Pi writes an
// aborted or failed request's assistant message with none), and every tool result answering a
// tool call made before it.
func checkEntries(t *testing.T, name string, es []session.Entry) map[string]int {
	t.Helper()
	seen, calls := map[string]bool{}, map[string]bool{}
	roles := map[string]int{}
	for _, e := range es {
		if seen[e.ID] {
			t.Fatalf("%s: id %s twice", name, e.ID)
		}
		seen[e.ID] = true
		role, _ := e.Message["role"].(string)
		if role == "" || role == "system" {
			continue // Pi's model changes, custom entries, its system prompt and the like
		}
		roles[role]++
		content, _ := e.Message["content"].([]any)
		stop, _ := e.Message["stopReason"].(string)
		if len(content) == 0 && stop != "aborted" && stop != "error" {
			t.Fatalf("%s: %s (%s) is empty", name, e.ID, role)
		}
		for _, raw := range content {
			if b, _ := raw.(map[string]any); b["type"] == "toolCall" {
				calls[b["id"].(string)] = true
			}
		}
		if role == "toolResult" && !calls[e.Message["toolCallId"].(string)] {
			t.Fatalf("%s: %s answers call %v made nowhere before it", name, e.ID, e.Message["toolCallId"])
		}
	}
	return roles
}

func TestRealSamples(t *testing.T) {
	read := func(file string, format Format) []session.Entry {
		es, err := File{Path: filepath.Join("testdata", "real", file), Format: format}.Entries()
		if err != nil {
			t.Fatal(err)
		}
		return es
	}
	// Gemini's recorder keeps most tool results on the call itself (its result), not in a user
	// record: the legacy session's results must all be read.
	if r := checkEntries(t, "gemini legacy", read("gemini-legacy-session.json", Gemini)); r["user"] != 3 || r["assistant"] != 37 || r["toolResult"] != 82 {
		t.Fatalf("gemini legacy %v", r)
	}
	es := read("gemini-set-patch.jsonl", Gemini)
	checkEntries(t, "gemini set/patch", es)
	if roles(es) != "m1:user m2:assistant m2#r0:toolResult m3:user" || es[1].Message["content"].([]any)[0].(map[string]any)["text"] != "Updated model response" || es[2].Message["content"].([]any)[0].(map[string]any)["text"] != "masked-output" {
		t.Fatalf("gemini set/patch %s %v", roles(es), es)
	}
	if r := checkEntries(t, "oh my pi", read("oh-my-pi-session.jsonl", Pi)); r["user"] != 6 || r["assistant"] != 55 || r["toolResult"] != 56 {
		t.Fatalf("oh my pi %v", r)
	}
	if r := checkEntries(t, "qwen", read("qwen-chat-records.jsonl", Qwen)); r["toolResult"] != 1 || r["user"] != 1 {
		t.Fatalf("qwen %v", r)
	}
	if r := checkEntries(t, "kimi", read("kimi-wire.jsonl", Kimi)); r["toolResult"] != 1 {
		t.Fatalf("kimi %v", r)
	}

	// Claude Code (here through OpenClaude, built from its source) keeps recent messages through
	// a compaction on a side chain and relinks them after the anchor: they are read there. A snip
	// trims only what the model sees later, so the messages it lists stay in memory.
	es = read("openclaude-preserved-segment.jsonl", ClaudeCode)
	checkEntries(t, "preserved segment", es)
	if len(es) != 5 || es[3].ID != "00000000-0000-4000-8000-000000000013" || es[4].ID != "00000000-0000-4000-8000-000000000014" {
		t.Fatalf("preserved segment %s", roles(es))
	}
	if es = read("openclaude-snip.jsonl", ClaudeCode); len(es) != 5 {
		t.Fatalf("snip %s", roles(es))
	}
	if es = read("cline-string-content.messages.json", Cline); len(es) != 1 || es[0].Message["content"].([]any)[0].(map[string]any)["text"] != "Bun 1.3.14 is current." {
		t.Fatalf("cline %v", es)
	}
	if r := checkEntries(t, "neovate", read("neovate-tool.jsonl", Neovate)); r["toolResult"] != 1 {
		t.Fatalf("neovate %v", r)
	}
	if es = read("copilot-user-message.json", Copilot); roles(es) != "11111111-1111-1111-1111-111111111111:user" {
		t.Fatalf("copilot %s", roles(es))
	}

	// Goose's stored messages, as SQLite rows: a tool request, then its result in the legacy
	// (bare content list) and the new ({content, isError}) shapes.
	var rows []map[string]any
	for i, n := range []string{"3", "4", "5"} {
		data, err := os.ReadFile(filepath.Join("testdata", "real", "goose-message-"+n+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		content, _ := json.Marshal(m["content"])
		meta, _ := json.Marshal(m["metadata"])
		rows = append(rows, map[string]any{"message_id": "msg_" + n, "role": m["role"], "content_json": string(content), "metadata_json": string(meta), "created_timestamp": float64(i)})
	}
	rows[2]["content_json"] = `[{"type":"toolResponse","id":"tool123","toolResult":{"status":"success","value":{"content":[{"type":"text","text":"New format output"}],"isError":false}}}]`
	es = GooseRows(rows)
	checkEntries(t, "goose", es)
	if len(es) != 3 || es[1].Message["content"].([]any)[0].(map[string]any)["text"] != "Tool output text" || es[2].Message["content"].([]any)[0].(map[string]any)["text"] != "New format output" {
		t.Fatalf("goose %s %v", roles(es), es)
	}
}

// Transcripts the harnesses wrote (copied from tools that parse them): every one reads with no
// duplicate ids, no empty message and no tool result without an earlier call, and holds the
// conversation (a user message and an answer).
func TestHarnessWrittenSamples(t *testing.T) {
	cases := []struct {
		file   string
		format Format
	}{
		{"cursor-agent-cli.jsonl", Cursor}, {"cursor-ide.jsonl", Cursor}, {"copilot-cli-events.jsonl", Copilot},
		{"droid-compaction.jsonl", Droid}, {"droid-fork.jsonl", Droid}, {"cortex.history.jsonl", Cortex},
		{"kiro-v3.messages.jsonl", Kiro}, {"kiro-v2.jsonl", Kiro}, {"claude-code-compaction.jsonl", ClaudeCode},
		{"claude-code-preserved.jsonl", ClaudeCode}, {"codex-rollout.jsonl", Codex}, {"gemini-jsonl.jsonl", Gemini},
		{"qwen-chat.jsonl", Qwen}, {"kimi-legacy-wire.jsonl", Kimi}, {"pi-tool-calls.jsonl", Pi},
		{"qoder-cli.jsonl", Qoder}, {"cline-cli.messages.json", Cline},
	}
	for _, c := range cases {
		es, err := File{Path: filepath.Join("testdata", "real", c.file), Format: c.format}.Entries()
		if err != nil {
			t.Fatalf("%s: %v", c.file, err)
		}
		r := checkEntries(t, c.file, es)
		// The compaction sample keeps only the lines from its compactions on: no answer is left.
		if r["user"] == 0 || r["assistant"] == 0 && c.file != "claude-code-compaction.jsonl" {
			t.Errorf("%s: %v", c.file, r)
		}
		// Context a harness injects as a user message is not something the user said.
		for _, e := range es {
			if e.Message["role"] != "user" {
				continue
			}
			for _, raw := range e.Message["content"].([]any) {
				if b, _ := raw.(map[string]any); b["type"] == "text" && (strings.HasPrefix(strings.TrimSpace(b["text"].(string)), "<system-reminder>") || strings.HasPrefix(strings.TrimSpace(b["text"].(string)), "<local-command-stdout>") || strings.Contains(b["text"].(string), "[scrubbed")) {
					t.Errorf("%s: %s reads injected context as the user's: %.60s", c.file, e.ID, b["text"])
				}
			}
		}
		t.Logf("%-30s %v", c.file, r)
	}
}
