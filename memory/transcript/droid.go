package transcript

import (
	"github.com/drvova/pig-observational-memory/memory/session"
)

// Droid reads a Factory Droid session (~/.factory/sessions/<cwd slug>/<session>.jsonl): its
// message lines in file order, each one whole message ({type: message, id, timestamp, message:
// {role, content}}) whose content is Anthropic-style blocks, as Claude Code's are, so each line
// is read as a Claude Code line chained to the one before it. The session_start header and the
// harness's own compaction_state summaries are not the conversation and are left out, as Droid's
// own reader skips every other line type.
func Droid(lines []map[string]any) []session.Entry {
	var cc []map[string]any
	parent := ""
	for _, l := range lines {
		msg, _ := l["message"].(map[string]any)
		id := str(l["id"])
		role := str(msg["role"])
		if str(l["type"]) != "message" || id == "" || (role != "user" && role != "assistant") {
			continue
		}
		m := map[string]any{"id": id, "role": role, "content": msg["content"]}
		cc = append(cc, map[string]any{"type": role, "uuid": id, "parentUuid": parent, "timestamp": l["timestamp"], "message": m})
		parent = id
	}
	return ClaudeCode(cc)
}
