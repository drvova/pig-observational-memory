package transcript

import (
	"github.com/drvova/pig-observational-memory/memory/session"
)

// DeepAgents reads a Deep Agents Code hook transcript (~/.deepagents/transcripts/<slug>--<hash>.jsonl,
// hooks/transcript.py's TranscriptRecord lines: {sequence, record_id, role, content, name}),
// in sequence order. Content is a string or the provider's blocks; an assistant's tool_use and
// thinking blocks are kept as they are Anthropic-shaped. The writer records no tool call id on
// a tool result, so each result answers the earliest unanswered call of the same tool name, and
// carries no id when there is none. System records are not the conversation.
func DeepAgents(lines []map[string]any) []session.Entry {
	var out []session.Entry
	pending := map[string][]string{}
	for _, l := range lines {
		id, ts := str(l["record_id"]), str(l["timestamp"])
		if id == "" {
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
				case "thinking":
					content = append(content, map[string]any{"type": "thinking", "thinking": str(b["thinking"])})
				case "tool_use":
					name := str(b["name"])
					pending[name] = append(pending[name], str(b["id"]))
					content = append(content, map[string]any{"type": "toolCall", "id": str(b["id"]), "name": name, "arguments": b["input"]})
				}
			}
			if len(content) > 0 {
				out = append(out, message(id, ts, map[string]any{"role": "assistant", "content": content}, nil))
			}
		case "tool":
			name, callID := str(l["name"]), ""
			if ids := pending[name]; len(ids) > 0 {
				callID, pending[name] = ids[0], ids[1:]
			}
			out = append(out, message(id, ts, map[string]any{"role": "toolResult", "toolCallId": callID, "toolName": name, "content": []any{text(resultText(l["content"]))}, "isError": false}, nil))
		}
	}
	return out
}
