package ledger

// runInputUsage is the input usage an assistant message reported (input plus cache read and
// cache write), false for a message without a usable report.
func runInputUsage(entry Entry) (int, bool) {
	if entry.Type != "message" || entry.Message == nil || entry.Message["role"] != "assistant" {
		return 0, false
	}
	if stop, _ := entry.Message["stopReason"].(string); stop == "aborted" || stop == "error" {
		return 0, false
	}
	usage, ok := entry.Message["usage"].(map[string]any)
	if !ok {
		return 0, false
	}
	input, ok := UsageNumber(usage["input"])
	if !ok {
		return 0, false
	}
	cacheRead, _ := UsageNumber(usage["cacheRead"])
	cacheWrite, _ := UsageNumber(usage["cacheWrite"])
	return input + cacheRead + cacheWrite, true
}

// NextRunHeadroom is the largest growth of the context one run caused on this branch since
// the previous compaction (docs/memory-v2.md, "The summary budget"): the difference in input
// usage (input plus cache read and cache write) between the last assistant messages of
// consecutive runs, a run starting at each user message. Negative differences are ignored;
// with fewer than two runs that reported usage it is 0.
func NextRunHeadroom(entries []Entry) int {
	runs := []int{}
	last, has := 0, false
	for i := FindLastCompactionIndex(entries) + 1; i < len(entries); i++ {
		entry := entries[i]
		if entry.Type != "message" || entry.Message == nil {
			continue
		}
		if entry.Message["role"] == "user" {
			if has {
				runs = append(runs, last)
			}
			has = false
			continue
		}
		if n, ok := runInputUsage(entry); ok {
			last, has = n, true
		}
	}
	if has {
		runs = append(runs, last)
	}
	headroom := 0
	for i := 1; i < len(runs); i++ {
		headroom = max(headroom, runs[i]-runs[i-1])
	}
	return headroom
}
