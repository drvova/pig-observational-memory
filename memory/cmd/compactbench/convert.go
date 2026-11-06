package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/memory/session"
)

// replayBase is the clock the replayed messages are stamped with: Compact-Bench's histories
// carry no timestamps, so each message is one second after the previous one, from a fixed
// date, which keeps a replay byte-identical between runs.
var replayBase = time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)

// converted is one Compact-Bench history as Pi-shaped entries.
type converted struct {
	Entries []session.Entry
	// SystemMessages counts the history's system messages, which are not conversation: a
	// harness's transcript does not hold its system prompt, so they are not replayed.
	SystemMessages int
	// SystemText is their text, so the report can count the atoms only they hold.
	SystemText string
}

// convertHistory turns a Compact-Bench history H (chat-completions shaped: roles system, user,
// assistant and tool; assistant content parts "text" and "think"; tool calls under tool_calls
// with JSON-string arguments; tool results under tool_call_id) into the Pi message entries the
// engine reads, as memory/transcript does for each harness's own file. Nothing is clipped:
// the engine reads the whole history, as it would in a session.
func convertHistory(h []map[string]any) converted {
	var out converted
	toolNames := map[string]string{}
	var system []string
	for i, m := range h {
		id := fmt.Sprintf("cb%06d", i)
		ts := replayBase.Add(time.Duration(i) * time.Second)
		role := str(m["role"])
		var msg map[string]any
		switch role {
		case "system":
			out.SystemMessages++
			system = append(system, contentText(m["content"]))
			continue
		case "user":
			text := contentText(m["content"])
			if strings.TrimSpace(text) == "" {
				continue
			}
			msg = map[string]any{"role": "user", "content": []any{textBlock(text)}}
		case "assistant":
			content := assistantContent(m["content"])
			calls, _ := m["tool_calls"].([]any)
			for _, raw := range calls {
				call, _ := raw.(map[string]any)
				fn, _ := call["function"].(map[string]any)
				callID, name := str(call["id"]), str(fn["name"])
				toolNames[callID] = name
				content = append(content, map[string]any{"type": "toolCall", "id": callID, "name": name, "arguments": arguments(fn["arguments"])})
			}
			if len(content) == 0 {
				continue
			}
			stop := "stop"
			if len(calls) > 0 {
				stop = "toolUse"
			}
			msg = map[string]any{"role": "assistant", "content": content, "stopReason": stop}
		case "tool":
			callID := str(m["tool_call_id"])
			msg = map[string]any{"role": "toolResult", "toolCallId": callID, "toolName": toolNames[callID], "content": []any{textBlock(contentText(m["content"]))}, "isError": false}
		default:
			continue
		}
		msg["timestamp"] = float64(ts.UnixMilli())
		out.Entries = append(out.Entries, session.FromMap(map[string]any{
			"type": "message", "id": id, "timestamp": ts.Format("2006-01-02T15:04:05.000Z"), "message": msg,
		}))
	}
	out.SystemText = strings.Join(system, "\n")
	return out
}

// assistantContent is an assistant message's text and thinking blocks.
func assistantContent(v any) []any {
	switch c := v.(type) {
	case string:
		if strings.TrimSpace(c) == "" {
			return []any{}
		}
		return []any{textBlock(c)}
	case []any:
		out := []any{}
		for _, raw := range c {
			p, _ := raw.(map[string]any)
			switch str(p["type"]) {
			case "text":
				if t := str(p["text"]); strings.TrimSpace(t) != "" {
					out = append(out, textBlock(t))
				}
			case "think", "thinking":
				t := str(p["think"])
				if t == "" {
					t = str(p["thinking"])
				}
				if strings.TrimSpace(t) != "" {
					out = append(out, map[string]any{"type": "thinking", "thinking": t})
				}
			}
		}
		return out
	}
	return []any{}
}

// contentText is a message's text: a string, or its text parts joined by newlines.
func contentText(v any) string {
	switch c := v.(type) {
	case string:
		return c
	case []any:
		var parts []string
		for _, raw := range c {
			p, _ := raw.(map[string]any)
			if str(p["type"]) == "text" {
				parts = append(parts, str(p["text"]))
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// arguments is a tool call's arguments as an object: the decoded JSON string, or the raw
// string under "input" when it is not an object.
func arguments(v any) map[string]any {
	switch a := v.(type) {
	case map[string]any:
		return a
	case string:
		var m map[string]any
		if json.Unmarshal([]byte(a), &m) == nil && m != nil {
			return m
		}
		return map[string]any{"input": a}
	}
	return map[string]any{}
}

func textBlock(s string) map[string]any { return map[string]any{"type": "text", "text": s} }

func str(v any) string {
	s, _ := v.(string)
	return s
}
