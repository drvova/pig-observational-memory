package transcript

import (
	"fmt"

	"github.com/drvova/pig-observational-memory/memory/session"
)

// Cursor reads a Cursor agent transcript (~/.cursor/projects/<slug>/agent-transcripts/<id>/<id>.jsonl,
// the agent-transcript package's lines {role, message: {content: [text | tool_use]}}). The
// format is lossy: it keeps no ids, timestamps or tool results, a tool_use has no call id, and
// reasoning is folded into the text. Each line takes its position as its id (cu<line>), which
// holds while the file is only appended to; a tool call's id is its line and block. The
// metadata and turn_ended lines are not the conversation.
func Cursor(lines []map[string]any) []session.Entry {
	var out []session.Entry
	for i, l := range lines {
		role := str(l["role"])
		if role != "user" && role != "assistant" {
			continue
		}
		id := fmt.Sprintf("cu%06d", i+1)
		msg, _ := l["message"].(map[string]any)
		var content []any
		for j, raw := range blocks(msg["content"]) {
			b, _ := raw.(map[string]any)
			switch str(b["type"]) {
			case "text":
				content = append(content, text(str(b["text"])))
			case "tool_use":
				if role == "assistant" {
					content = append(content, map[string]any{"type": "toolCall", "id": fmt.Sprintf("%s.%d", id, j), "name": str(b["name"]), "arguments": b["input"]})
				}
			}
		}
		if len(content) > 0 {
			out = append(out, message(id, "", map[string]any{"role": role, "content": content}, nil))
		}
	}
	return out
}
