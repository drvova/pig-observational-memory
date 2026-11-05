package transcript

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/drvova/pig-observational-memory/memory/session"
)

// Kimi reads a Kimi CLI session's wire log (~/.kimi/sessions/<md5(work dir)>/<session>/wire.jsonl),
// which, unlike its context.jsonl, carries timestamps and tool errors and is never rotated: one
// record per line, {timestamp (unix seconds), message: {type, payload}}. TurnBegin is the user's
// input; the ContentPart (text, think) and ToolCall records until the next ToolResult or turn are
// one assistant entry; a ToolResult is a toolResult entry. An entry's id is its line's position.
func Kimi(lines []map[string]any) []session.Entry {
	toolNames := map[string]string{}
	var out []session.Entry
	var group *ccGroup
	flush := func() {
		if group != nil {
			out = append(out, message(group.ids[0], group.timestamp, map[string]any{"role": "assistant", "content": group.blocks}, group.ids[1:]))
			group = nil
		}
	}
	for i, l := range lines {
		m, _ := l["message"].(map[string]any)
		if m == nil {
			continue
		}
		p, _ := m["payload"].(map[string]any)
		id, ts := fmt.Sprintf("kw%06d", i), unixTime(l["timestamp"])
		assistant := func(b map[string]any) {
			if group == nil {
				group = &ccGroup{timestamp: ts}
			}
			group.ids = append(group.ids, id)
			group.blocks = append(group.blocks, b)
		}
		switch str(m["type"]) {
		case "TurnBegin":
			flush()
			body := ""
			switch in := p["user_input"].(type) {
			case string:
				body = in
			default:
				body = resultText(in)
			}
			if body != "" {
				out = append(out, message(id, ts, map[string]any{"role": "user", "content": []any{text(body)}}, nil))
			}
		case "ContentPart":
			switch str(p["type"]) {
			case "text":
				if t := str(p["text"]); t != "" {
					assistant(text(t))
				}
			case "think":
				if t := str(p["think"]); t != "" {
					assistant(map[string]any{"type": "thinking", "thinking": t})
				}
			}
		case "ToolCall":
			fn, _ := p["function"].(map[string]any)
			name := str(fn["name"])
			toolNames[str(p["id"])] = name
			var args any = str(fn["arguments"])
			var decoded any
			if json.Unmarshal([]byte(str(fn["arguments"])), &decoded) == nil {
				args = decoded
			}
			assistant(map[string]any{"type": "toolCall", "id": str(p["id"]), "name": name, "arguments": args})
		case "ToolResult":
			flush()
			call := str(p["tool_call_id"])
			rv, _ := p["return_value"].(map[string]any)
			isError, _ := rv["is_error"].(bool)
			body := resultText(rv["output"])
			if body == "" {
				body = str(rv["message"])
			}
			out = append(out, message(id, ts, map[string]any{"role": "toolResult", "toolCallId": call, "toolName": toolNames[call], "content": []any{text(body)}, "isError": isError}, nil))
		case "TurnEnd":
			flush()
		}
	}
	flush()
	return out
}

func unixTime(v any) string {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) {
		return ""
	}
	sec, frac := math.Modf(f)
	return time.Unix(int64(sec), int64(frac*1e9)).UTC().Format("2006-01-02T15:04:05.000Z")
}

// KimiWirePath is the wire log of a Kimi CLI session in work dir cwd: Kimi's hooks hand no
// transcript path, so it is found as Kimi names it ($KIMI_SHARE_DIR, ~/.kimi).
func KimiWirePath(cwd, sessionID string) string {
	root := os.Getenv("KIMI_SHARE_DIR")
	if root == "" {
		home, _ := os.UserHomeDir()
		root = filepath.Join(home, ".kimi")
	}
	sum := md5.Sum([]byte(cwd))
	return filepath.Join(root, "sessions", hex.EncodeToString(sum[:]), sessionID, "wire.jsonl")
}
