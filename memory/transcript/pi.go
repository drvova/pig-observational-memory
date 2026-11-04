package transcript

import "github.com/drvova/pig-observational-memory/memory/session"

// Pi reads a Pi (or PiG) session file: its entries are already the engine's shape, so this
// keeps the active branch, walked back from the newest entry through parentId. The engine's
// own entries in it (written when the engine ran inside Pi) are kept too.
func Pi(lines []map[string]any) []session.Entry {
	byID := map[string]map[string]any{}
	leaf := ""
	for _, l := range lines {
		id := str(l["id"])
		if id == "" || str(l["type"]) == "session" {
			continue
		}
		byID[id] = l
		leaf = id
	}
	var out []session.Entry
	seen := map[string]bool{}
	for id := leaf; id != "" && !seen[id] && byID[id] != nil; id = str(byID[id]["parentId"]) {
		seen[id] = true
		out = append(out, session.FromMap(byID[id]))
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
