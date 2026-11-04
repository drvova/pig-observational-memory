package transcript

import (
	"fmt"

	"github.com/drvova/pig-observational-memory/memory/session"
)

// Qwen reads a Qwen Code chat recording (~/.qwen/projects/<cwd>/chats/<session>.jsonl): JSONL
// records chained by parentUuid, walked back from the newest, as Claude Code's are. A user record
// without a subtype is the user's message; an assistant record's genai parts are its text,
// reasoning (thought parts) and tool calls (functionCall); a tool_result record's functionResponse
// parts are the results, failed when its toolCallResult says so. System records and user records
// with a subtype (compression, rewind, titles) are the CLI's own and left out.
func Qwen(lines []map[string]any) []session.Entry {
	byID := map[string]map[string]any{}
	leaf := ""
	for _, l := range lines {
		if id := str(l["uuid"]); id != "" {
			byID[id] = l
			leaf = id
		}
	}
	var chain []map[string]any
	seen := map[string]bool{}
	for id := leaf; id != "" && !seen[id] && byID[id] != nil; id = str(byID[id]["parentUuid"]) {
		seen[id] = true
		chain = append(chain, byID[id])
	}
	var out []session.Entry
	for i := len(chain) - 1; i >= 0; i-- {
		l := chain[i]
		id, ts := str(l["uuid"]), str(l["timestamp"])
		msg, _ := l["message"].(map[string]any)
		parts := blocks(msg["parts"])
		switch str(l["type"]) {
		case "user":
			if l["subtype"] != nil {
				continue
			}
			var content []any
			for _, raw := range parts {
				if p, _ := raw.(map[string]any); str(p["text"]) != "" && p["thought"] != true {
					content = append(content, text(str(p["text"])))
				}
			}
			if len(content) > 0 {
				out = append(out, message(id, ts, map[string]any{"role": "user", "content": content}, nil))
			}
		case "assistant":
			var content []any
			for _, raw := range parts {
				p, _ := raw.(map[string]any)
				switch {
				case p["functionCall"] != nil:
					fc, _ := p["functionCall"].(map[string]any)
					content = append(content, map[string]any{"type": "toolCall", "id": str(fc["id"]), "name": str(fc["name"]), "arguments": fc["args"]})
				case p["thought"] == true:
					content = append(content, map[string]any{"type": "thinking", "thinking": str(p["text"])})
				case str(p["text"]) != "":
					content = append(content, text(str(p["text"])))
				}
			}
			if len(content) > 0 {
				out = append(out, message(id, ts, map[string]any{"role": "assistant", "content": content}, nil))
			}
		case "tool_result":
			result, _ := l["toolCallResult"].(map[string]any)
			isError := str(result["status"]) == "error" || result["errorType"] != nil
			n := 0
			for _, raw := range parts {
				p, _ := raw.(map[string]any)
				fr, ok := p["functionResponse"].(map[string]any)
				if !ok {
					continue
				}
				rid := id
				if n > 0 {
					rid = fmt.Sprintf("%s#%d", id, n)
				}
				n++
				resp, _ := fr["response"].(map[string]any)
				body := str(resp["output"])
				if body == "" && resp["error"] != nil {
					body = fmt.Sprint(resp["error"])
				}
				out = append(out, message(rid, ts, map[string]any{"role": "toolResult", "toolCallId": str(fr["id"]), "toolName": str(fr["name"]), "content": []any{text(body)}, "isError": isError}, nil))
			}
		}
	}
	return out
}
