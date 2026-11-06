package transcript

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf16"

	"github.com/drvova/pig-observational-memory/memory/session"
)

// ClaudeCode reads a Claude Code session transcript (~/.claude/projects/<project>/<session>.jsonl):
// the active branch, walked back from the newest line through parentUuid and across a
// compaction's boundary (logicalParentUuid), so the whole conversation is read, not only what
// followed the last compaction. The lines of one assistant message (one per block, sharing
// message.id) are one assistant entry, under the first line's uuid; a user line's tool_result
// blocks are toolResult entries. Subagent (isSidechain), meta and transcript-only lines, the
// harness's own compaction summary (isCompactSummary), attachments and API errors are not the
// conversation and are left out. A compaction that keeps recent messages (a compact_boundary's
// compactMetadata.preservedSegment, head to tail) leaves them on a side chain that Claude Code
// relinks after the anchor; they are read there.
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
	chain = splicePreserved(chain, byID)
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
					// A local command's output (/compact, /cost) is echoed as a user message; it is the
					// CLI's, not the user's.
					if t := strings.TrimSpace(str(b["text"])); !strings.HasPrefix(t, "<local-command-stdout>") && !strings.HasPrefix(t, "<local-command-stderr>") {
						content = append(content, text(str(b["text"])))
					}
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

// KodeSessionPath is the session log Kode writes for a session, in Claude Code's format:
// ${KODE_CONFIG_DIR:-~/.kode}/projects/<cwd with every non-alphanumeric character as ->/<session>.jsonl
// (packages/protocol/src/utils/kodeAgentSessionLog.ts). Kode's hook payload names a plain-text
// rendering instead, which om does not read.
func KodeSessionPath(cwd, sessionID string) string {
	if cwd == "" || sessionID == "" {
		return ""
	}
	root := os.Getenv("KODE_CONFIG_DIR")
	if root == "" {
		root = os.Getenv("ANYKODE_CONFIG_DIR")
	}
	if root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		root = filepath.Join(home, ".kode")
	}
	return filepath.Join(root, "projects", JSSlug(cwd), sessionID+".jsonl")
}

// JSSlug is s with every character that is not an ASCII letter or digit as -, the way
// JavaScript's s.replace(/[^a-zA-Z0-9]/g, "-") does it: one - per UTF-16 code unit, so a
// character outside the Basic Multilingual Plane (an emoji) becomes two and any other non-ASCII
// character one. Claude Code and Kode name a project's folder so.
func JSSlug(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		case r > 0xFFFF:
			b.WriteString("--")
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

// claudeProjectMax is the longest folder name Claude Code keeps whole.
const claudeProjectMax = 200

// ClaudeProjectDir is the name of Claude Code's projects folder for cwd (its sanitizePath): the
// cwd as JSSlug, and when that is longer than 200 characters its first 200, a -, and the base-36
// absolute value of the cwd's 32-bit string hash (h = h*31 + unit over its UTF-16 code units).
func ClaudeProjectDir(cwd string) string {
	slug := JSSlug(cwd)
	if len(slug) <= claudeProjectMax {
		return slug
	}
	var h int32
	for _, u := range utf16.Encode([]rune(cwd)) {
		h = h*31 + int32(u)
	}
	n := int64(h)
	if n < 0 {
		n = -n
	}
	return slug[:claudeProjectMax] + "-" + strconv.FormatInt(n, 36)
}

// splicePreserved puts each compaction's preserved segment (compactMetadata.preservedSegment
// {headUuid, anchorUuid, tailUuid}: the messages kept through the compaction, on a side chain)
// into the walked chain after its anchor, as Claude Code relinks it when it loads the session.
func splicePreserved(chain []map[string]any, byID map[string]map[string]any) []map[string]any {
	in := map[string]bool{}
	for _, l := range chain {
		in[str(l["uuid"])] = true
	}
	for _, l := range chain {
		meta, _ := l["compactMetadata"].(map[string]any)
		seg, _ := meta["preservedSegment"].(map[string]any)
		head, anchor, tail := str(seg["headUuid"]), str(seg["anchorUuid"]), str(seg["tailUuid"])
		if head == "" || tail == "" || in[tail] {
			continue
		}
		var segment []map[string]any
		seen := map[string]bool{}
		for id := tail; id != "" && !seen[id]; id = str(byID[id]["parentUuid"]) {
			seen[id] = true
			line := byID[id]
			if line == nil {
				segment = nil
				break
			}
			segment = append([]map[string]any{line}, segment...)
			if id == head {
				break
			}
		}
		if len(segment) == 0 || str(segment[0]["uuid"]) != head {
			continue
		}
		at := len(chain)
		for i, c := range chain {
			if str(c["uuid"]) == anchor {
				at = i + 1
			}
		}
		for _, s := range segment {
			in[str(s["uuid"])] = true
		}
		chain = append(chain[:at], append(segment, chain[at:]...)...)
		return splicePreserved(chain, byID)
	}
	return chain
}
