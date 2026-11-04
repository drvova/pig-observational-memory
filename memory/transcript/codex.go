package transcript

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/session"
)

// Codex reads a Codex CLI rollout (~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl): its
// response_item lines. The file only grows, so an entry's id is its line's position ("cx" and
// the line number). Consecutive assistant-side items (a message, reasoning, tool calls) are one
// assistant entry under the first one's id; a tool call's output is a toolResult entry. The
// instructions and environment context Codex sends as user or developer messages are not the
// conversation and are left out.
func Codex(lines []map[string]any) []session.Entry {
	toolNames := map[string]string{}
	var out []session.Entry
	var group *ccGroup
	flush := func() {
		if group != nil {
			out = append(out, message(group.ids[0], group.timestamp, map[string]any{"role": "assistant", "content": group.blocks}, group.ids[1:]))
			group = nil
		}
	}
	assistant := func(id, ts string, b map[string]any) {
		if group == nil {
			group = &ccGroup{timestamp: ts}
		}
		group.ids = append(group.ids, id)
		group.blocks = append(group.blocks, b)
	}
	for i, l := range lines {
		if str(l["type"]) != "response_item" {
			continue
		}
		p, _ := l["payload"].(map[string]any)
		id, ts := fmt.Sprintf("cx%06d", i), str(l["timestamp"])
		switch str(p["type"]) {
		case "message":
			role := str(p["role"])
			body := ""
			for _, raw := range blocks(p["content"]) {
				b, _ := raw.(map[string]any)
				if t := str(b["type"]); t == "input_text" || t == "output_text" || t == "text" {
					body += str(b["text"])
				}
			}
			switch {
			case role == "assistant":
				if strings.TrimSpace(body) != "" {
					assistant(id, ts, text(body))
				}
			case role == "user" && !codexContext(body):
				flush()
				out = append(out, message(id, ts, map[string]any{"role": "user", "content": []any{text(body)}}, nil))
			}
		case "reasoning":
			summary := ""
			for _, raw := range blocks(p["summary"]) {
				b, _ := raw.(map[string]any)
				summary += str(b["text"])
			}
			if summary != "" {
				assistant(id, ts, map[string]any{"type": "thinking", "thinking": summary})
			}
		case "function_call", "custom_tool_call", "local_shell_call":
			call, name := str(p["call_id"]), str(p["name"])
			if name == "" {
				name = "shell"
			}
			toolNames[call] = name
			var args any = p["input"]
			if s := str(p["arguments"]); s != "" {
				var decoded any
				if json.Unmarshal([]byte(s), &decoded) == nil {
					args = decoded
				} else {
					args = s
				}
			} else if action, ok := p["action"]; ok {
				args = action
			}
			assistant(id, ts, map[string]any{"type": "toolCall", "id": call, "name": name, "arguments": args})
		case "function_call_output", "custom_tool_call_output":
			flush()
			call := str(p["call_id"])
			body := resultText(p["output"])
			if body == "" {
				if s, ok := p["output"].(string); ok {
					body = s
				}
			}
			out = append(out, message(id, ts, map[string]any{"role": "toolResult", "toolCallId": call, "toolName": toolNames[call], "content": []any{text(body)}, "isError": false}, nil))
		}
	}
	flush()
	return out
}

// codexContext reports a user message Codex writes itself: the environment context and the
// instructions it prepends.
func codexContext(body string) bool {
	b := strings.TrimSpace(body)
	for _, prefix := range []string{"<environment_context>", "<user_instructions>", "# AGENTS.md instructions", "<turn_aborted>"} {
		if strings.HasPrefix(b, prefix) {
			return true
		}
	}
	return b == ""
}
