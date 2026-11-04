package transcript

import (
	"fmt"

	"github.com/drvova/pig-observational-memory/memory/session"
)

// Gemini reads a Gemini CLI chat recording (~/.gemini/tmp/<project>/chats/session-*.jsonl):
// append-only JSONL whose records re-appear under their id whenever they change (the last copy
// stands, at the first copy's place), with $rewindTo dropping a record and every later one and
// $set/$patch lines that are metadata. A user record's text is the user's message, its
// functionResponse parts tool results (the error flag is a response "error"); a gemini record is
// the assistant's text, its thoughts and its tool calls. info, warning and error records are the
// CLI's own and are left out.
func Gemini(lines []map[string]any) []session.Entry {
	type rec struct {
		order int
		m     map[string]any
	}
	byID := map[string]*rec{}
	var order []string
	for _, l := range lines {
		if to := str(l["$rewindTo"]); to != "" {
			if r, ok := byID[to]; ok {
				for _, id := range order[r.order:] {
					delete(byID, id)
				}
				order = order[:r.order]
			}
			continue
		}
		if patch, ok := l["$patch"].(map[string]any); ok {
			if r, ok := byID[str(patch["id"])]; ok {
				for _, k := range []string{"content", "toolCalls"} {
					if v, ok := patch[k]; ok {
						r.m[k] = v
					}
				}
			}
			continue
		}
		id := str(l["id"])
		if id == "" || l["type"] == nil {
			continue // the header line, $set
		}
		if r, ok := byID[id]; ok {
			r.m = l
			continue
		}
		byID[id] = &rec{order: len(order), m: l}
		order = append(order, id)
	}
	var out []session.Entry
	for _, id := range order {
		l := byID[id].m
		ts := str(l["timestamp"])
		switch str(l["type"]) {
		case "user":
			var content []any
			n := 0
			for _, raw := range blocks(l["content"]) {
				p, _ := raw.(map[string]any)
				if fr, ok := p["functionResponse"].(map[string]any); ok {
					rid := id
					if n > 0 {
						rid = fmt.Sprintf("%s#%d", id, n)
					}
					n++
					resp, _ := fr["response"].(map[string]any)
					body, isError := str(resp["output"]), false
					if e := resp["error"]; e != nil {
						body, isError = fmt.Sprint(e), true
					}
					out = append(out, message(rid, ts, map[string]any{"role": "toolResult", "toolCallId": str(fr["id"]), "toolName": str(fr["name"]), "content": []any{text(body)}, "isError": isError}, nil))
				} else if t := str(p["text"]); t != "" {
					content = append(content, text(t))
				}
			}
			if len(content) > 0 && n == 0 {
				out = append(out, message(id, ts, map[string]any{"role": "user", "content": content}, nil))
			}
		case "gemini":
			var content []any
			for _, raw := range blocks(l["thoughts"]) {
				th, _ := raw.(map[string]any)
				if d := str(th["description"]); d != "" {
					content = append(content, map[string]any{"type": "thinking", "thinking": d})
				}
			}
			for _, raw := range blocks(l["content"]) {
				if p, _ := raw.(map[string]any); str(p["text"]) != "" {
					content = append(content, text(str(p["text"])))
				}
			}
			for _, raw := range blocks(l["toolCalls"]) {
				tc, _ := raw.(map[string]any)
				content = append(content, map[string]any{"type": "toolCall", "id": str(tc["id"]), "name": str(tc["name"]), "arguments": tc["args"]})
			}
			if len(content) > 0 {
				out = append(out, message(id, ts, map[string]any{"role": "assistant", "content": content}, nil))
			}
		}
	}
	return out
}
