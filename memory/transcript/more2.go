package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/drvova/pig-observational-memory/memory/session"
)

// IFlow reads an iFlow CLI transcript (~/.iflow/projects/<slug>/<session>.jsonl): Claude Code-style
// records, appended. A compression writes its summary as a new chain root (isCompactSummary,
// parentUuid null), so the records are read in file order, each chained to the one before, and
// the summary, its isMeta caveat and subagent records are left out by the Claude Code reader. A
// thinking block's text is in "text".
func IFlow(lines []map[string]any) []session.Entry {
	var cc []map[string]any
	parent := ""
	for _, l := range lines {
		t, id := str(l["type"]), str(l["uuid"])
		if (t != "user" && t != "assistant") || id == "" {
			continue
		}
		c := map[string]any{}
		for k, v := range l {
			c[k] = v
		}
		c["parentUuid"], c["logicalParentUuid"] = parent, nil
		if msg, ok := l["message"].(map[string]any); ok {
			m := map[string]any{}
			for k, v := range msg {
				m[k] = v
			}
			var content []any
			for _, raw := range blocks(msg["content"]) {
				b, _ := raw.(map[string]any)
				if str(b["type"]) == "thinking" {
					if _, nested := b["thinking"].(map[string]any); nested {
						b = map[string]any{"type": "thinking", "thinking": str(b["text"])}
					}
				}
				content = append(content, b)
			}
			if content != nil {
				m["content"] = content
			}
			c["message"] = m
		}
		cc = append(cc, c)
		parent = id
	}
	return ClaudeCode(cc)
}

// Cortex reads a Cortex Code history file (~/.snowflake/cortex/conversations/<session>.history.jsonl),
// "merged" lines (one message {role, id, content}) whose last copy of an id stands at its first
// place, or "flat" lines (one block each, {message_id, content: [block]}) grouped by message.
// Blocks: text, thinking {thinking: {text}}, tool_use {tool_use: {tool_use_id, name, input}} and,
// in a user message, tool_result {tool_result: {tool_use_id, name, content, status}}. Compaction
// summaries live in the session's .json file, not here.
func Cortex(lines []map[string]any) []session.Entry {
	type msg struct {
		role, ts string
		content  []any
	}
	byID := map[string]*msg{}
	var order []string
	for _, l := range lines {
		id := str(l["message_id"])
		flat := id != ""
		if !flat {
			id = str(l["id"])
		}
		if id == "" {
			continue
		}
		m := byID[id]
		if m == nil {
			m = &msg{}
			byID[id] = m
			order = append(order, id)
		}
		m.role = str(l["role"])
		if m.ts == "" {
			m.ts = firstString(l, "user_sent_time", "assistant_sent_time", "created_on")
		}
		if flat {
			m.content = append(m.content, blocks(l["content"])...)
		} else {
			m.content = blocks(l["content"])
		}
	}
	var out []session.Entry
	for _, id := range order {
		m := byID[id]
		var content []any
		n := 0
		for _, raw := range m.content {
			b, _ := raw.(map[string]any)
			switch str(b["type"]) {
			case "text":
				if internal, _ := b["internalOnly"].(bool); !internal {
					content = append(content, text(str(b["text"])))
				}
			case "thinking":
				th, _ := b["thinking"].(map[string]any)
				content = append(content, map[string]any{"type": "thinking", "thinking": str(th["text"])})
			case "tool_use":
				tu, _ := b["tool_use"].(map[string]any)
				content = append(content, map[string]any{"type": "toolCall", "id": str(tu["tool_use_id"]), "name": str(tu["name"]), "arguments": tu["input"]})
			case "tool_result":
				tr, _ := b["tool_result"].(map[string]any)
				rid := id
				if n > 0 {
					rid = fmt.Sprintf("%s#%d", id, n)
				}
				n++
				out = append(out, message(rid, m.ts, map[string]any{"role": "toolResult", "toolCallId": str(tr["tool_use_id"]), "toolName": str(tr["name"]), "content": []any{text(resultText(tr["content"]))}, "isError": str(tr["status"]) == "error"}, nil))
			}
		}
		if len(content) == 0 || (m.role != "user" && m.role != "assistant") {
			continue
		}
		if m.role == "user" && n > 0 {
			continue
		}
		out = append(out, message(id, m.ts, map[string]any{"role": m.role, "content": content}, nil))
	}
	return out
}

func firstString(l map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := str(l[k]); s != "" {
			return s
		}
	}
	return ""
}

// CortexHistoryPath is a Cortex Code session's message history:
// ${SNOWFLAKE_HOME:-~/.snowflake}/cortex/conversations/<session>.history.jsonl (the hook's
// transcript_path names the session's .json metadata beside it).
func CortexHistoryPath(_, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	root := os.Getenv("SNOWFLAKE_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		root = filepath.Join(home, ".snowflake")
	}
	return filepath.Join(root, "cortex", "conversations", sessionID+".history.jsonl")
}

// Auggie reads an Augment CLI session (~/.augment/sessions/<conversation>.json, one JSON document
// rewritten whole): each chatHistory exchange is a user request (its request_id), the tool
// results its request nodes carry (type 1), and the reply (response_text and its tool_use nodes,
// type 5) under <request_id>:a. Summary exchanges (isHistorySummary) are left out.
func Auggie(lines []map[string]any) []session.Entry {
	if len(lines) == 0 {
		return nil
	}
	history, _ := lines[0]["chatHistory"].([]any)
	var out []session.Entry
	for _, raw := range history {
		item, _ := raw.(map[string]any)
		if summary, _ := item["isHistorySummary"].(bool); summary {
			continue
		}
		ex, _ := item["exchange"].(map[string]any)
		id := str(ex["request_id"])
		if id == "" {
			continue
		}
		ts := str(item["finishedAt"])
		n := 0
		for _, rn := range blocks(ex["request_nodes"]) {
			node, _ := rn.(map[string]any)
			tr, ok := node["tool_result_node"].(map[string]any)
			if !ok {
				continue
			}
			isError, _ := tr["is_error"].(bool)
			out = append(out, message(fmt.Sprintf("%s#%d", id, n), ts, map[string]any{"role": "toolResult", "toolCallId": str(tr["tool_use_id"]), "content": []any{text(resultText(tr["content"]))}, "isError": isError}, nil))
			n++
		}
		if req := str(ex["request_message"]); req != "" {
			out = append(out, message(id, ts, map[string]any{"role": "user", "content": []any{text(req)}}, nil))
		}
		var content []any
		if resp := str(ex["response_text"]); resp != "" {
			content = append(content, text(resp))
		}
		for _, rn := range blocks(ex["response_nodes"]) {
			node, _ := rn.(map[string]any)
			tu, ok := node["tool_use"].(map[string]any)
			if !ok {
				continue
			}
			var args any = str(tu["input_json"])
			var parsed any
			if json.Unmarshal([]byte(str(tu["input_json"])), &parsed) == nil {
				args = parsed
			}
			content = append(content, map[string]any{"type": "toolCall", "id": str(tu["tool_use_id"]), "name": str(tu["tool_name"]), "arguments": args})
		}
		if len(content) > 0 {
			out = append(out, message(id+":a", ts, map[string]any{"role": "assistant", "content": content}, nil))
		}
	}
	return out
}

// AuggieSessionPath is the session file Auggie keeps for a conversation:
// ~/.augment/sessions/<conversation>.json.
func AuggieSessionPath(_, conversationID string) string {
	home, err := os.UserHomeDir()
	if err != nil || conversationID == "" {
		return ""
	}
	return filepath.Join(home, ".augment", "sessions", conversationID+".json")
}
