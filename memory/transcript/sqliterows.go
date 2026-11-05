package transcript

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/drvova/pig-observational-memory/memory/session"
)

// jsonColumn decodes a TEXT column that holds JSON.
func jsonColumn(v any) any {
	s, ok := v.(string)
	if !ok {
		return v
	}
	var out any
	if json.Unmarshal([]byte(s), &out) != nil {
		return nil
	}
	return out
}

func unixSeconds(v any) string {
	f, ok := v.(float64)
	if !ok {
		return ""
	}
	return time.Unix(int64(f), 0).UTC().Format(time.RFC3339)
}

// CrushRows reads Crush's messages rows (internal/db: id, role, parts JSON [{type, data}],
// created_at in Unix seconds, is_summary_message): user text, assistant text, reasoning and
// tool calls (input a JSON string), and a tool row's tool results. Rows are updated in place as
// they stream and kept by compaction, whose summary row is left out, as are system rows.
func CrushRows(rows []map[string]any) []session.Entry {
	var out []session.Entry
	for _, r := range rows {
		id, role, ts := str(r["id"]), str(r["role"]), unixSeconds(r["created_at"])
		if f, _ := r["is_summary_message"].(float64); f != 0 || id == "" {
			continue
		}
		parts, _ := jsonColumn(r["parts"]).([]any)
		var content []any
		n := 0
		for _, raw := range parts {
			p, _ := raw.(map[string]any)
			data, _ := p["data"].(map[string]any)
			switch str(p["type"]) {
			case "text":
				if hidden, _ := data["hidden"].(bool); !hidden {
					content = append(content, text(str(data["text"])))
				}
			case "reasoning":
				content = append(content, map[string]any{"type": "thinking", "thinking": str(data["thinking"])})
			case "tool_call":
				var args any = str(data["input"])
				var parsed any
				if json.Unmarshal([]byte(str(data["input"])), &parsed) == nil {
					args = parsed
				}
				content = append(content, map[string]any{"type": "toolCall", "id": str(data["id"]), "name": str(data["name"]), "arguments": args})
			case "tool_result":
				rid := id
				if n > 0 {
					rid = fmt.Sprintf("%s#%d", id, n)
				}
				n++
				isError, _ := data["is_error"].(bool)
				out = append(out, message(rid, ts, map[string]any{"role": "toolResult", "toolCallId": str(data["tool_call_id"]), "toolName": str(data["name"]), "content": []any{text(str(data["content"]))}, "isError": isError}, nil))
			}
		}
		if (role == "user" || role == "assistant") && len(content) > 0 {
			out = append(out, message(id, ts, map[string]any{"role": role, "content": content}, nil))
		}
	}
	return out
}

// CrushDBPath is the database Crush keeps for a directory: the nearest .crush directory from
// cwd up to the repository root, else <cwd>/.crush (internal/config/load.go), holding crush.db.
func CrushDBPath(cwd, _ string) string {
	if cwd == "" {
		return ""
	}
	for dir := cwd; ; dir = filepath.Dir(dir) {
		if st, err := os.Stat(filepath.Join(dir, ".crush")); err == nil && st.IsDir() {
			return filepath.Join(dir, ".crush", "crush.db")
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil || filepath.Dir(dir) == dir {
			break
		}
	}
	return filepath.Join(cwd, ".crush", "crush.db")
}

// GooseRows reads Goose's messages rows (crates/goose session_manager: message_id, role,
// content_json, metadata_json): text, thinking, toolRequest {id, toolCall {status, value {name,
// arguments}}} and, in a user message, toolResponse {id, toolResult {status, value}}, whose value
// is {content, isError} or, in the legacy shape, the bare content list. A compaction rewrites the rows but keeps the originals (agent-invisible) and adds
// a summary (compactionSummary), which is left out.
func GooseRows(rows []map[string]any) []session.Entry {
	var out []session.Entry
	for _, r := range rows {
		id, role := str(r["message_id"]), str(r["role"])
		meta, _ := jsonColumn(r["metadata_json"]).(map[string]any)
		if summary, _ := meta["compactionSummary"].(bool); summary || id == "" {
			continue
		}
		ts := unixSeconds(r["created_timestamp"])
		blocksList, _ := jsonColumn(r["content_json"]).([]any)
		var content []any
		n := 0
		for _, raw := range blocksList {
			b, _ := raw.(map[string]any)
			switch str(b["type"]) {
			case "text":
				content = append(content, text(str(b["text"])))
			case "thinking":
				content = append(content, map[string]any{"type": "thinking", "thinking": str(b["thinking"])})
			case "toolRequest":
				call, _ := b["toolCall"].(map[string]any)
				value, _ := call["value"].(map[string]any)
				content = append(content, map[string]any{"type": "toolCall", "id": str(b["id"]), "name": str(value["name"]), "arguments": value["arguments"]})
			case "toolResponse":
				res, _ := b["toolResult"].(map[string]any)
				var isError bool
				var body string
				switch value := res["value"].(type) {
				case map[string]any: // CallToolResult {content, isError}
					isError, _ = value["isError"].(bool)
					body = resultText(value["content"])
				case []any: // the legacy shape: a bare content list
					body = resultText(value)
				}
				if str(res["status"]) == "error" {
					isError, body = true, fmt.Sprint(res["error"])
				}
				rid := id
				if n > 0 {
					rid = fmt.Sprintf("%s#%d", id, n)
				}
				n++
				out = append(out, message(rid, ts, map[string]any{"role": "toolResult", "toolCallId": str(b["id"]), "content": []any{text(body)}, "isError": isError}, nil))
			}
		}
		if (role == "user" || role == "assistant") && len(content) > 0 && !(role == "user" && n > 0) {
			out = append(out, message(id, ts, map[string]any{"role": role, "content": content}, nil))
		}
	}
	return out
}

// GooseDBPath is Goose's session database: ${GOOSE_PATH_ROOT}/data/sessions/sessions.db, else
// ~/.local/share/goose/sessions/sessions.db on Linux (crates/goose/src/config/paths.rs); elsewhere
// unconfirmed, so "" (nothing is recorded).
func GooseDBPath(_, _ string) string {
	if root := os.Getenv("GOOSE_PATH_ROOT"); root != "" {
		return filepath.Join(root, "data", "sessions", "sessions.db")
	}
	home, err := os.UserHomeDir()
	if err != nil || runtime.GOOS != "linux" {
		return ""
	}
	return filepath.Join(home, ".local", "share", "goose", "sessions", "sessions.db")
}

// AmazonQRows reads Amazon Q Developer CLI's conversation row (one ConversationState JSON per
// directory, rewritten whole): each history entry's user message (a prompt, or tool results
// with their status) and assistant message (Response or ToolUse with its tool_uses). Ids come
// from the assistant's message_id: the user message before it is <message_id>:u. A compaction
// drops early history from the row; records om already took stay in its store.
func AmazonQRows(rows []map[string]any) []session.Entry {
	if len(rows) == 0 {
		return nil
	}
	state, _ := jsonColumn(rows[0]["value"]).(map[string]any)
	history, _ := state["history"].([]any)
	var out []session.Entry
	for i, raw := range history {
		h, _ := raw.(map[string]any)
		user, _ := h["user"].(map[string]any)
		assistant, _ := h["assistant"].(map[string]any)
		var body map[string]any
		for _, k := range []string{"Response", "ToolUse"} {
			if b, ok := assistant[k].(map[string]any); ok {
				body = b
			}
		}
		mid := str(body["message_id"])
		if mid == "" {
			mid = fmt.Sprintf("%s-%d", str(state["conversation_id"]), i)
		}
		ts := str(user["timestamp"])
		content, _ := user["content"].(map[string]any)
		if p, ok := content["Prompt"].(map[string]any); ok {
			out = append(out, message(mid+":u", ts, map[string]any{"role": "user", "content": []any{text(str(p["prompt"]))}}, nil))
		}
		if tr, ok := content["ToolUseResults"].(map[string]any); ok {
			results, _ := tr["tool_use_results"].([]any)
			for j, rr := range results {
				r, _ := rr.(map[string]any)
				var parts []string
				for _, c := range blocks(r["content"]) {
					cm, _ := c.(map[string]any)
					if t, ok := cm["Text"].(string); ok {
						parts = append(parts, t)
					} else if j, ok := cm["Json"]; ok {
						b, _ := json.Marshal(j)
						parts = append(parts, string(b))
					}
				}
				out = append(out, message(fmt.Sprintf("%s:r%d", mid, j), ts, map[string]any{"role": "toolResult", "toolCallId": str(r["tool_use_id"]), "content": []any{text(joinLines(parts))}, "isError": str(r["status"]) == "Error"}, nil))
			}
		}
		if body == nil {
			continue
		}
		var ac []any
		if t := str(body["content"]); t != "" {
			ac = append(ac, text(t))
		}
		uses, _ := body["tool_uses"].([]any)
		for _, u := range uses {
			um, _ := u.(map[string]any)
			ac = append(ac, map[string]any{"type": "toolCall", "id": str(um["id"]), "name": str(um["name"]), "arguments": um["args"]})
		}
		if len(ac) > 0 {
			out = append(out, message(mid, ts, map[string]any{"role": "assistant", "content": ac}, nil))
		}
	}
	return out
}

func joinLines(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += "\n"
		}
		out += p
	}
	return out
}

// AmazonQDBPath is Amazon Q Developer CLI's database: the platform data directory's
// amazon-q/data.sqlite3 (~/.local/share on Linux, ~/Library/Application Support on macOS).
func AmazonQDBPath(_, _ string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	if runtime.GOOS == "darwin" {
		return filepath.Join(home, "Library", "Application Support", "amazon-q", "data.sqlite3")
	}
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "amazon-q", "data.sqlite3")
	}
	return filepath.Join(home, ".local", "share", "amazon-q", "data.sqlite3")
}

// AmazonQConversation is the conversation Amazon Q keeps for directory cwd (its row's key), ""
// when there is none: its hook payloads name no session.
func AmazonQConversation(cwd string) string {
	if cwd == "" {
		return ""
	}
	rows, err := QuerySQLite(AmazonQDBPath("", ""), "SELECT value FROM conversations WHERE key = "+SQLQuote(cwd))
	if err != nil || len(rows) == 0 {
		return ""
	}
	state, _ := jsonColumn(rows[0]["value"]).(map[string]any)
	return str(state["conversation_id"])
}
