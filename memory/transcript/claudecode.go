package transcript

import (
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/session"
)

// ClaudeCode reads a Claude Code session transcript (~/.claude/projects/<project>/<session>.jsonl):
// the active branch, walked back from the newest line through parentUuid and across a
// compaction's boundary (logicalParentUuid), so the whole conversation is read, not only what
// followed the last compaction. The lines of one assistant message (one per block, sharing
// message.id) are one assistant entry, under the first line's uuid; a user line's tool_result
// blocks are toolResult entries. Subagent (isSidechain), meta and transcript-only lines, the
// harness's own compaction summary (isCompactSummary), attachments and API errors are not the
// conversation and are left out.
func ClaudeCode(lines []map[string]any) []session.Entry {
	byID := map[string]map[string]any{}
	leaf := ""
	for _, l := range lines {
		id := str(l["uuid"])
		if id == "" {
			continue
		}
		byID[id] = l
		if b, _ := l["isSidechain"].(bool); !b && (str(l["type"]) == "user" || str(l["type"]) == "assistant") {
			leaf = id
		}
	}
	var chain []map[string]any
	seen := map[string]bool{}
	for id := leaf; id != "" && !seen[id]; {
		seen[id] = true
		l := byID[id]
		if l == nil {
			break
		}
		chain = append(chain, l)
		next := str(l["parentUuid"])
		if next == "" {
			next = str(l["logicalParentUuid"])
		}
		id = next
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	toolNames := map[string]string{}
	var out []session.Entry
	var group *ccGroup
	flush := func() {
		if group != nil {
			out = append(out, message(group.ids[0], group.timestamp, map[string]any{"role": "assistant", "content": group.blocks}, group.ids[1:]))
			group = nil
		}
	}
	for _, l := range chain {
		if skipClaudeLine(l) {
			continue
		}
		msg, _ := l["message"].(map[string]any)
		id, ts := str(l["uuid"]), str(l["timestamp"])
		switch str(l["type"]) {
		case "assistant":
			mid := str(msg["id"])
			if group == nil || mid == "" || group.messageID != mid {
				flush()
				group = &ccGroup{messageID: mid, timestamp: ts}
			}
			group.ids = append(group.ids, id)
			for _, raw := range blocks(msg["content"]) {
				b, _ := raw.(map[string]any)
				switch str(b["type"]) {
				case "text":
					group.blocks = append(group.blocks, text(str(b["text"])))
				case "thinking":
					group.blocks = append(group.blocks, map[string]any{"type": "thinking", "thinking": str(b["thinking"])})
				case "tool_use":
					toolNames[str(b["id"])] = str(b["name"])
					group.blocks = append(group.blocks, map[string]any{"type": "toolCall", "id": str(b["id"]), "name": str(b["name"]), "arguments": b["input"]})
				}
			}
		case "user":
			flush()
			var content []any
			n := 0
			for _, raw := range blocks(msg["content"]) {
				b, _ := raw.(map[string]any)
				switch str(b["type"]) {
				case "tool_result":
					rid := id
					if n > 0 {
						rid = fmt.Sprintf("%s#%d", id, n)
					}
					n++
					callID := str(b["tool_use_id"])
					isError, _ := b["is_error"].(bool)
					out = append(out, message(rid, ts, map[string]any{"role": "toolResult", "toolCallId": callID, "toolName": toolNames[callID], "content": []any{text(resultText(b["content"]))}, "isError": isError}, nil))
				case "text":
					content = append(content, text(str(b["text"])))
				case "image":
					content = append(content, map[string]any{"type": "image"})
				}
			}
			if len(content) > 0 && n == 0 {
				out = append(out, message(id, ts, map[string]any{"role": "user", "content": content}, nil))
			}
		}
	}
	flush()
	return out
}

type ccGroup struct {
	messageID, timestamp string
	ids                  []string
	blocks               []any
}

func skipClaudeLine(l map[string]any) bool {
	for _, flag := range []string{"isSidechain", "isMeta", "isCompactSummary", "isVisibleInTranscriptOnly", "isApiErrorMessage"} {
		if b, _ := l[flag].(bool); b {
			return true
		}
	}
	t := str(l["type"])
	return t != "user" && t != "assistant"
}

// blocks is a content value as a block list: a string is one text block.
func blocks(v any) []any {
	switch c := v.(type) {
	case []any:
		return c
	case string:
		if strings.TrimSpace(c) == "" {
			return nil
		}
		return []any{text(c)}
	}
	return nil
}
