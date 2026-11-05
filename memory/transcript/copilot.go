package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/drvova/pig-observational-memory/memory/session"
)

// Copilot reads a GitHub Copilot CLI session log (session-state/<session>/events.jsonl), one
// session event per line ({id, parentId, timestamp, type, data}): user.message is a user entry,
// assistant.message an assistant entry (its reasoningText, content and toolRequests), and
// tool.execution_complete the toolResult of its toolCallId. Ephemeral events are never written
// to the log; a subagent's events (agentId) and every other type are not the conversation.
func Copilot(lines []map[string]any) []session.Entry {
	toolNames := map[string]string{}
	var out []session.Entry
	for _, l := range lines {
		if eph, _ := l["ephemeral"].(bool); eph || str(l["agentId"]) != "" {
			continue
		}
		data, _ := l["data"].(map[string]any)
		id, ts := str(l["id"]), str(l["timestamp"])
		if id == "" || data == nil {
			continue
		}
		switch str(l["type"]) {
		case "user.message":
			if c := str(data["content"]); c != "" {
				out = append(out, message(id, ts, map[string]any{"role": "user", "content": []any{text(c)}}, nil))
			}
		case "assistant.message":
			var content []any
			if r := str(data["reasoningText"]); r != "" {
				content = append(content, map[string]any{"type": "thinking", "thinking": r})
			}
			if c := str(data["content"]); c != "" {
				content = append(content, text(c))
			}
			requests, _ := data["toolRequests"].([]any)
			for _, raw := range requests {
				r, _ := raw.(map[string]any)
				callID, name := str(r["toolCallId"]), str(r["name"])
				toolNames[callID] = name
				args := r["arguments"]
				if s, ok := args.(string); ok {
					var parsed any
					if json.Unmarshal([]byte(s), &parsed) == nil {
						args = parsed
					}
				}
				content = append(content, map[string]any{"type": "toolCall", "id": callID, "name": name, "arguments": args})
			}
			if len(content) > 0 {
				out = append(out, message(id, ts, map[string]any{"role": "assistant", "content": content}, nil))
			}
		case "tool.execution_complete":
			callID := str(data["toolCallId"])
			success, _ := data["success"].(bool)
			body := ""
			if r, ok := data["result"].(map[string]any); ok {
				body = str(r["content"])
			}
			if e, ok := data["error"].(map[string]any); ok && body == "" {
				body = str(e["message"])
			}
			out = append(out, message(id, ts, map[string]any{"role": "toolResult", "toolCallId": callID, "toolName": toolNames[callID], "content": []any{text(body)}, "isError": !success}, nil))
		}
	}
	return out
}

// CopilotEventsPath is the session log Copilot CLI keeps for a session:
// ${COPILOT_HOME:-~/.copilot}/session-state/<session>/events.jsonl.
func CopilotEventsPath(sessionID string) string {
	if sessionID == "" {
		return ""
	}
	home := os.Getenv("COPILOT_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(h, ".copilot")
	}
	return filepath.Join(home, "session-state", sessionID, "events.jsonl")
}
