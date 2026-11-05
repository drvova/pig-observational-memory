package transcript

import (
	"fmt"

	"github.com/drvova/pig-observational-memory/memory/session"
)

// Generic reads the engine's own interchange format (docs/universal.md, "Any other harness"),
// one JSON object per line, for a harness whose adapter writes it:
//
//	{"id":"m1","timestamp":"2026-05-01T10:00:00Z","role":"user","text":"…"}
//	{"id":"m2","role":"assistant","text":"…","tools":[{"id":"c1","name":"bash","arguments":{"command":"ls"}}]}
//	{"id":"m3","role":"tool","toolCallId":"c1","toolName":"bash","text":"…","isError":false}
//
// A line without an id gets its position ("g" and the line number), which is stable while the
// file only grows. A line that is already a Pi message entry ({"type":"message",…}) is read as is.
func Generic(lines []map[string]any) []session.Entry {
	var out []session.Entry
	for i, l := range lines {
		if str(l["type"]) == "message" {
			out = append(out, session.FromMap(l))
			continue
		}
		id := str(l["id"])
		if id == "" {
			id = fmt.Sprintf("g%06d", i)
		}
		ts := str(l["timestamp"])
		body := str(l["text"])
		switch str(l["role"]) {
		case "user":
			out = append(out, message(id, ts, map[string]any{"role": "user", "content": []any{text(body)}}, nil))
		case "assistant":
			content := []any{}
			if body != "" {
				content = append(content, text(body))
			}
			for _, raw := range blocks(l["tools"]) {
				t, _ := raw.(map[string]any)
				content = append(content, map[string]any{"type": "toolCall", "id": str(t["id"]), "name": str(t["name"]), "arguments": t["arguments"]})
			}
			out = append(out, message(id, ts, map[string]any{"role": "assistant", "content": content}, nil))
		case "tool":
			isError, _ := l["isError"].(bool)
			out = append(out, message(id, ts, map[string]any{"role": "toolResult", "toolCallId": str(l["toolCallId"]), "toolName": str(l["toolName"]), "content": []any{text(body)}, "isError": isError}, nil))
		}
	}
	return out
}

// Formats are the readers by harness name, as om's --harness flag names them.
var Formats = map[string]Format{
	"claude-code": ClaudeCode,
	"droid":       Droid,
	"deepagents":  DeepAgents,
	"cursor":      Cursor,
	"qoder":       Qoder,
	"cline":       Cline,
	"neovate":     Neovate,
	"iflow":       IFlow,
	"cortex":      Cortex,
	"auggie":      Auggie,
	"kiro":        Kiro,
	"copilot":     Copilot,
	"gemini":      Gemini,
	"qwen":        Qwen,
	"kimi":        Kimi,
	"codex":       Codex,
	"pi":          Pi,
	"generic":     Generic,
}
