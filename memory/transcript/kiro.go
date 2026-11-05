package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/drvova/pig-observational-memory/memory/session"
)

// Kiro reads a Kiro session, in either layout:
//
//   - V3 (~/.kiro/sessions/<bucket>/sess_<id>/messages.jsonl): {id, timestamp, payload: {type, ...}}
//     records: user {content}, assistant {content, operationType Say | Reasoning | Summary},
//     tool_call {toolCallId, toolName, args}, tool_result {toolCallId, content, success}. A
//     compaction writes a tombstone and its summary (operationType Summary), which is left out with
//     reasoning bodies Kiro does not keep ("..."), injected context (agent_note), the system prompt
//     (session_start), subagent markers (their streams are separate files) and bookkeeping.
//   - V2 (~/.kiro/sessions/cli/<id>.jsonl): {kind: Prompt | AssistantMessage | ToolResults, data:
//     {message_id, content: [{kind: text | thinking | toolUse | toolResult, data}], meta}}.
//
// The record layouts are taken from Apache SkyWalking AI Sessionizer's Kiro adapter and the
// sessions it measured (kiro-cli 2.28.0); Kiro itself is closed source.
func Kiro(lines []map[string]any) []session.Entry {
	toolNames := map[string]string{}
	seen := map[string]int{}
	uniq := func(id string) string {
		seen[id]++
		if n := seen[id]; n > 1 {
			return fmt.Sprintf("%s#%d", id, n-1)
		}
		return id
	}
	var out []session.Entry
	for _, l := range lines {
		if p, ok := l["payload"].(map[string]any); ok {
			id, ts := str(l["id"]), str(l["timestamp"])
			if id == "" {
				continue
			}
			switch str(p["type"]) {
			case "user":
				if c := str(p["content"]); c != "" {
					out = append(out, message(uniq(id), ts, map[string]any{"role": "user", "content": []any{text(c)}}, nil))
				}
			case "assistant":
				c := str(p["content"])
				switch str(p["operationType"]) {
				case "Say":
					if c != "" {
						out = append(out, message(uniq(id), ts, map[string]any{"role": "assistant", "content": []any{text(c)}}, nil))
					}
				case "Reasoning":
					if c != "" && c != "..." {
						out = append(out, message(uniq(id), ts, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "thinking", "thinking": c}}}, nil))
					}
				}
			case "tool_call":
				callID, name := str(p["toolCallId"]), str(p["toolName"])
				toolNames[callID] = name
				out = append(out, message(uniq(id), ts, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "toolCall", "id": callID, "name": name, "arguments": p["args"]}}}, nil))
			case "tool_result":
				callID := str(p["toolCallId"])
				ok, _ := p["success"].(bool)
				out = append(out, message(uniq(id), ts, map[string]any{"role": "toolResult", "toolCallId": callID, "toolName": toolNames[callID], "content": []any{text(resultText(p["content"]))}, "isError": !ok}, nil))
			}
			continue
		}
		data, _ := l["data"].(map[string]any)
		id := str(data["message_id"])
		if id == "" {
			continue
		}
		meta, _ := data["meta"].(map[string]any)
		ts := unixTime(meta["timestamp"])
		var content []any
		n := 0
		for _, raw := range blocks(data["content"]) {
			c, _ := raw.(map[string]any)
			switch str(c["kind"]) {
			case "text":
				if t := str(c["data"]); t != "" {
					content = append(content, text(t))
				}
			case "thinking":
				if th, _ := c["data"].(map[string]any); str(th["text"]) != "" {
					content = append(content, map[string]any{"type": "thinking", "thinking": str(th["text"])})
				}
			case "toolUse":
				u, _ := c["data"].(map[string]any)
				callID, name := str(u["toolUseId"]), str(u["name"])
				toolNames[callID] = name
				content = append(content, map[string]any{"type": "toolCall", "id": callID, "name": name, "arguments": u["input"]})
			case "toolResult":
				r, _ := c["data"].(map[string]any)
				callID := str(r["toolUseId"])
				var parts []string
				for _, rc := range blocks(r["content"]) {
					rcm, _ := rc.(map[string]any)
					if s, ok := rcm["data"].(string); ok {
						parts = append(parts, s)
					} else if rcm["data"] != nil {
						b, _ := json.Marshal(rcm["data"])
						parts = append(parts, string(b))
					}
				}
				out = append(out, message(uniq(fmt.Sprintf("%s#%d", id, n)), ts, map[string]any{"role": "toolResult", "toolCallId": callID, "toolName": toolNames[callID], "content": []any{text(joinLines(parts))}, "isError": str(r["status"]) == "error"}, nil))
				n++
			}
		}
		role := map[string]string{"Prompt": "user", "AssistantMessage": "assistant"}[str(l["kind"])]
		if role != "" && len(content) > 0 {
			out = append(out, message(uniq(id), ts, map[string]any{"role": role, "content": content}, nil))
		}
	}
	return out
}

// KiroSessionPath finds a Kiro session's messages file from the id its hooks name: the V3
// directory sess_<id> (or <id>, when the id already carries the prefix) under any workspace
// bucket of ${KIRO_HOME:-~/.kiro}/sessions, else the V2 cli/<id>.jsonl. "" when neither exists:
// that the hooks' session id names these directories is not documented, so om then records the
// prompts itself.
func KiroSessionPath(_, sessionID string) string {
	if sessionID == "" {
		return ""
	}
	root := os.Getenv("KIRO_HOME")
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		root = filepath.Join(home, ".kiro")
	}
	sessions := filepath.Join(root, "sessions")
	for _, dir := range []string{"sess_" + sessionID, sessionID} {
		if matches, _ := filepath.Glob(filepath.Join(sessions, "*", glob(dir), "messages.jsonl")); len(matches) > 0 {
			return matches[0]
		}
	}
	if p := filepath.Join(sessions, "cli", sessionID+".jsonl"); fileExists(p) {
		return p
	}
	return ""
}

func glob(s string) string {
	out := []rune{}
	for _, r := range s {
		if r == '*' || r == '?' || r == '[' || r == '\\' {
			out = append(out, '\\')
		}
		out = append(out, r)
	}
	return string(out)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
