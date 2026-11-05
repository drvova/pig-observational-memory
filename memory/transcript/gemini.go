package transcript

import (
	"fmt"

	"github.com/drvova/pig-observational-memory/memory/session"
)

// Gemini reads a Gemini CLI chat recording (~/.gemini/tmp/<project>/chats/session-*.jsonl):
// append-only JSONL whose records re-appear under their id whenever they change (the last copy
// stands, at the first copy's place), with $rewindTo dropping a record and every later one,
// $set {messages} replacing the whole list (a checkpoint), $patch {updates} merging fields into
// records (tool calls merged by their id), and the header and other $set lines metadata. The
// legacy single-JSON session ({sessionId, ..., messages}) is read as one checkpoint. A user
// record's text is the user's message, its functionResponse parts tool results; a gemini record
// is the assistant's text, its thoughts and its tool calls, whose own result (functionResponse
// parts, the error flag in status or response.error) is the call's tool result unless a user
// record carries it. info, warning and error records are the CLI's own and are left out.
func Gemini(lines []map[string]any) []session.Entry {
	type rec struct {
		order int
		m     map[string]any
	}
	byID := map[string]*rec{}
	var order []string
	add := func(l map[string]any) {
		id := str(l["id"])
		if id == "" || l["type"] == nil {
			return
		}
		if r, ok := byID[id]; ok {
			r.m = l
			return
		}
		byID[id] = &rec{order: len(order), m: l}
		order = append(order, id)
	}
	checkpoint := func(msgs []any) {
		byID, order = map[string]*rec{}, nil
		for _, raw := range msgs {
			if m, ok := raw.(map[string]any); ok {
				add(m)
			}
		}
	}
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
		if set, ok := l["$set"].(map[string]any); ok {
			if msgs, ok := set["messages"].([]any); ok {
				checkpoint(msgs)
			}
			continue
		}
		if patch, ok := l["$patch"].(map[string]any); ok {
			updates, _ := patch["updates"].([]any)
			if len(updates) == 0 {
				updates = []any{patch}
			}
			for _, raw := range updates {
				u, _ := raw.(map[string]any)
				r, ok := byID[str(u["id"])]
				if !ok {
					continue
				}
				merged := map[string]any{}
				for k, v := range r.m {
					merged[k] = v
				}
				for k, v := range u {
					if k == "toolCalls" {
						merged[k] = mergeCalls(r.m[k], v)
					} else if k != "id" {
						merged[k] = v
					}
				}
				r.m = merged
			}
			continue
		}
		if msgs, ok := l["messages"].([]any); ok && l["type"] == nil {
			checkpoint(msgs)
			continue
		}
		add(l)
	}
	answered := map[string]bool{}
	for _, id := range order {
		l := byID[id].m
		if str(l["type"]) != "user" {
			continue
		}
		for _, raw := range blocks(l["content"]) {
			p, _ := raw.(map[string]any)
			if fr, ok := p["functionResponse"].(map[string]any); ok {
				answered[str(fr["id"])] = true
			}
		}
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
			var results []session.Entry
			for i, raw := range blocks(l["toolCalls"]) {
				tc, _ := raw.(map[string]any)
				callID := str(tc["id"])
				content = append(content, map[string]any{"type": "toolCall", "id": callID, "name": str(tc["name"]), "arguments": tc["args"]})
				if tc["result"] == nil || answered[callID] {
					continue
				}
				body, isError := geminiResult(tc["result"]), str(tc["status"]) == "error"
				results = append(results, message(fmt.Sprintf("%s#r%d", id, i), ts, map[string]any{"role": "toolResult", "toolCallId": callID, "toolName": str(tc["name"]), "content": []any{text(body)}, "isError": isError}, nil))
			}
			if len(content) > 0 {
				out = append(out, message(id, ts, map[string]any{"role": "assistant", "content": content}, nil))
				out = append(out, results...)
			}
		}
	}
	return out
}

// mergeCalls merges a $patch's tool calls into a record's, by call id.
func mergeCalls(old, patch any) any {
	calls, _ := old.([]any)
	updates, _ := patch.([]any)
	out := make([]any, 0, len(calls))
	index := map[string]int{}
	for _, raw := range calls {
		c, _ := raw.(map[string]any)
		copied := map[string]any{}
		for k, v := range c {
			copied[k] = v
		}
		index[str(copied["id"])] = len(out)
		out = append(out, copied)
	}
	for _, raw := range updates {
		u, _ := raw.(map[string]any)
		i, ok := index[str(u["id"])]
		if !ok {
			index[str(u["id"])] = len(out)
			out = append(out, u)
			continue
		}
		c := out[i].(map[string]any)
		for k, v := range u {
			c[k] = v
		}
	}
	return out
}

// geminiResult is a tool call's result as text: its functionResponse parts' output (or error),
// else its text parts, else the value as it is.
func geminiResult(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	var parts []string
	for _, raw := range blocks(v) {
		p, _ := raw.(map[string]any)
		if fr, ok := p["functionResponse"].(map[string]any); ok {
			resp, _ := fr["response"].(map[string]any)
			if out := str(resp["output"]); out != "" {
				parts = append(parts, out)
			} else if e := resp["error"]; e != nil {
				parts = append(parts, fmt.Sprint(e))
			}
		} else if t := str(p["text"]); t != "" {
			parts = append(parts, t)
		}
	}
	return joinLines(parts)
}
