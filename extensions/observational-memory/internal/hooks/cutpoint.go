package hooks

import "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"

// sessionEntryToContextMessages is Pi's sessionEntryToContextMessages for the
// entries the cut point logic reads.
func sessionEntryToContextMessages(entry map[string]any) []map[string]any {
	switch kind, _ := entry["type"].(string); kind {
	case "message":
		message, isObject := entry["message"].(map[string]any)
		if !isObject {
			if entry["message"] == nil {
				return nil
			}
			return []map[string]any{{}}
		}
		return []map[string]any{message}
	case "custom_message":
		content := entry["content"]
		if content == nil {
			content = []any{}
		}
		return []map[string]any{{"role": "custom", "content": content}}
	case "branch_summary":
		if summary, _ := entry["summary"].(string); summary != "" {
			return []map[string]any{{"role": "branchSummary", "summary": summary}}
		}
	case "compaction":
		summary, _ := entry["summary"].(string)
		messages := []map[string]any{}
		if system, ok := entry["systemMessage"].(map[string]any); ok && len(system) > 0 {
			messages = append(messages, system)
		}
		return append(messages, map[string]any{"role": "compactionSummary", "summary": summary})
	}
	return nil
}

// isCutPointMessage mirrors Pi's isCutPointMessage: never a tool result, which
// must follow its call.
func isCutPointMessage(message map[string]any) bool {
	switch role, _ := message["role"].(string); role {
	case "user", "assistant", "bashExecution", "custom", "branchSummary", "compactionSummary":
		return true
	}
	return false
}

// IsCutPointEntry reports whether Pi accepts the entry as a compaction cut point.
func IsCutPointEntry(entry ledger.Entry) bool {
	if entry.Type == "compaction" {
		return false
	}
	for _, message := range sessionEntryToContextMessages(entry.ToMap()) {
		if isCutPointMessage(message) {
			return true
		}
	}
	return false
}

// findCutPointAtOrBefore is the nearest valid cut point at or before index, not before startIndex; -1 when none.
func findCutPointAtOrBefore(entries []ledger.Entry, index, startIndex int) int {
	for i := min(index, len(entries)-1); i >= startIndex && i >= 0; i-- {
		if IsCutPointEntry(entries[i]) {
			return i
		}
	}
	return -1
}
