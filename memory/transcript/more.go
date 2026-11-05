package transcript

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/drvova/pig-observational-memory/memory/session"
)

// Qoder reads a Qoder CLI transcript (~/.qoder/projects/<slug>/<session>.jsonl): Claude Code-shaped
// records from a Qwen Code lineage, so a file whose messages carry Qwen's parts is read as Qwen
// Code's and any other as Claude Code's.
func Qoder(lines []map[string]any) []session.Entry {
	for _, l := range lines {
		if msg, ok := l["message"].(map[string]any); ok {
			if _, parts := msg["parts"]; parts {
				return Qwen(lines)
			}
			if _, content := msg["content"]; content {
				break
			}
		}
	}
	return ClaudeCode(lines)
}

// unixMillis is a Unix time in milliseconds as an RFC 3339 timestamp.
func unixMillis(ms float64) string {
	return unixTime(ms / 1000)
}

// Cline reads a Cline session's messages file (~/.cline/data/sessions/<id>/<id>.messages.json,
// messages-contract-v1: {version, messages: [{id, role, content: Anthropic blocks, ts}]}), one
// JSON document rewritten whole on each persist, whose messages keep stable ids. Each message is
// read as a Claude Code line chained to the one before it.
func Cline(lines []map[string]any) []session.Entry {
	if len(lines) == 0 {
		return nil
	}
	msgs, _ := lines[0]["messages"].([]any)
	var cc []map[string]any
	parent := ""
	for _, raw := range msgs {
		m, _ := raw.(map[string]any)
		id, role := str(m["id"]), str(m["role"])
		if id == "" || (role != "user" && role != "assistant") {
			continue
		}
		ts := ""
		if v, ok := m["ts"].(float64); ok {
			ts = unixMillis(v)
		}
		cc = append(cc, map[string]any{"type": role, "uuid": id, "parentUuid": parent, "timestamp": ts, "message": map[string]any{"id": id, "role": role, "content": m["content"]}})
		parent = id
	}
	return ClaudeCode(cc)
}

// ClineMessagesPath is the messages file Cline keeps for a session:
// ${CLINE_DATA_DIR:-~/.cline/data}/sessions/<id>/<id>.messages.json.
func ClineMessagesPath(_, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	dir := os.Getenv("CLINE_DATA_DIR")
	if dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		dir = filepath.Join(home, ".cline", "data")
	}
	return filepath.Join(dir, "sessions", sessionID, sessionID+".messages.json")
}

// Neovate reads a Neovate Code session log (~/.neovate/projects/<slug>/<session>.jsonl, append-only
// NormalizedMessage lines {type: message, uuid, parentUuid, timestamp, role, content}) in file
// order: a compaction starts a new chain at its summary, so walking parents would lose what came
// before. User content is text; assistant content is text, reasoning and tool_use; a tool line
// carries tool-result parts {toolCallId, toolName, result: {llmContent, isError}}. The compaction
// summary (uiContent "Chat history compacted successfully."), the older user-role tool_result
// shape (its fields are not confirmed) and config and snapshot lines are
// not the conversation.
func Neovate(lines []map[string]any) []session.Entry {
	var out []session.Entry
	for _, l := range lines {
		id, ts := str(l["uuid"]), str(l["timestamp"])
		if str(l["type"]) != "message" || id == "" || str(l["uiContent"]) == "Chat history compacted successfully." {
			continue
		}
		switch str(l["role"]) {
		case "user":
			var content []any
			for _, raw := range blocks(l["content"]) {
				if b, _ := raw.(map[string]any); str(b["type"]) == "text" {
					content = append(content, text(str(b["text"])))
				}
			}
			if len(content) > 0 {
				out = append(out, message(id, ts, map[string]any{"role": "user", "content": content}, nil))
			}
		case "assistant":
			var content []any
			for _, raw := range blocks(l["content"]) {
				b, _ := raw.(map[string]any)
				switch str(b["type"]) {
				case "text":
					content = append(content, text(str(b["text"])))
				case "reasoning":
					content = append(content, map[string]any{"type": "thinking", "thinking": str(b["text"])})
				case "tool_use":
					content = append(content, map[string]any{"type": "toolCall", "id": str(b["id"]), "name": str(b["name"]), "arguments": b["input"]})
				}
			}
			if len(content) > 0 {
				out = append(out, message(id, ts, map[string]any{"role": "assistant", "content": content}, nil))
			}
		case "tool":
			for i, raw := range blocks(l["content"]) {
				b, _ := raw.(map[string]any)
				if str(b["type"]) != "tool-result" {
					continue
				}
				r, _ := b["result"].(map[string]any)
				isError, _ := r["isError"].(bool)
				rid := id
				if i > 0 {
					rid = fmt.Sprintf("%s#%d", id, i)
				}
				out = append(out, message(rid, ts, map[string]any{"role": "toolResult", "toolCallId": str(b["toolCallId"]), "toolName": str(b["toolName"]), "content": []any{text(resultText(r["llmContent"]))}, "isError": isError}, nil))
			}
		}
	}
	return out
}
