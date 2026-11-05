package ledger

import (
	"math"
)

// IsSourceEntry reports an entry that carries conversation content.
func IsSourceEntry(entry Entry) bool {
	switch entry.Type {
	case "message", "custom_message", "branch_summary":
		return true
	}
	return false
}

// EntryIndexByID maps ids to branch indexes.
func EntryIndexByID(entries []Entry) map[string]int {
	index := make(map[string]int, len(entries))
	for i, entry := range entries {
		index[entry.ID] = i
	}
	return index
}

// EntryIndexForID is the branch index of entryID, or -1.
func EntryIndexForID(entries []Entry, entryID string) int {
	if entryID == "" {
		return -1
	}
	if idx, ok := EntryIndexByID(entries)[entryID]; ok {
		return idx
	}
	return -1
}

// coverageEntry reads a coverage marker entry of customType. Coverage is a
// positional claim made by a valid record entry, so it applies the same guard
// memory does: an entry memory rejects (one malformed record) must not advance
// coverage either, or its span is never re-observed.
func coverageEntry(entry Entry, customType string) (string, bool) {
	if entry.Type != "custom" || entry.CustomType != customType {
		return "", false
	}
	switch customType {
	case OMObservationsRecorded:
		if data, ok := ObservationsRecordedEntry(entry); ok {
			return data.CoversUpToID, true
		}
	case OMReflectionsRecorded:
		if data, ok := ReflectionsRecordedEntry(entry); ok {
			return data.CoversUpToID, true
		}
	case OMObservationsDropped:
		if data, ok := ObservationsDroppedEntry(entry); ok {
			return data.CoversUpToID, true
		}
	case OMExtracted:
		if data, ok := ExtractedEntry(entry); ok {
			return data.CoversUpToID, true
		}
	}
	return "", false
}

// ExtractionProgressIndex is the branch index of the furthest source entry memory v2's
// level-0 extraction has read: the furthest entry an om.observations.recorded or om.extracted
// marker covers, or -1. Coverage is a positional claim, so every source entry at or before it
// was read; a v1 observer's coverage counts as read too, so v2 never re-extracts what v1
// observed.
func ExtractionProgressIndex(entries []Entry) int {
	return max(LatestCoverageIndex(entries, OMObservationsRecorded), LatestCoverageIndex(entries, OMExtracted))
}

// ExtractionStartIndex is the branch index memory v2's level-0 extraction starts reading at:
// after the furthest entry an earlier extraction or a v1 observer covered
// (ExtractionProgressIndex), never before the current compaction range, and never before the
// latest full fold's boundary (LatestFullFoldBoundaryID), so the first v2 compaction after a
// v1 one never re-extracts what that compaction folded (docs/memory-v2.md, "Migration >
// Records and boundaries"). The boundary entry itself is read unless coverage reached it: a v1
// fold boundary equal to its cut is a retained entry no record may cover yet.
func ExtractionStartIndex(entries []Entry) int {
	start := max(ExtractionProgressIndex(entries)+1, CompactionRangeStartIndex(entries))
	if fold := EntryIndexForID(entries, LatestFullFoldBoundaryID(entries)); fold > start {
		start = fold
	}
	return start
}

// LatestCoverageIndex is the branch index of the furthest entry a marker of
// customType covers, or -1.
func LatestCoverageIndex(entries []Entry, customType string) int {
	index := EntryIndexByID(entries)
	latest := -1
	for _, entry := range entries {
		coversUpTo, ok := coverageEntry(entry, customType)
		if !ok {
			continue
		}
		covered, ok := index[coversUpTo]
		if ok && covered > latest {
			latest = covered
		}
	}
	return latest
}

// FindLastCompactionIndex is the branch index of the latest compaction entry, or -1.
func FindLastCompactionIndex(entries []Entry) int {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == "compaction" {
			return i
		}
	}
	return -1
}

func finiteNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, !math.IsNaN(n) && !math.IsInf(n, 0)
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// UsageNumber reads one provider usage count as an int: 0 and false when it is not a
// finite number.
func UsageNumber(v any) (int, bool) {
	n, ok := finiteNumber(v)
	if !ok {
		return 0, false
	}
	return int(n), true
}

// CompactionRangeStartIndex is where Pi's next compaction range starts: the
// retained boundary of the latest compaction, the entry after it when the
// boundary id is missing, or 0 before any compaction.
func CompactionRangeStartIndex(entries []Entry) int {
	compactionIndex := FindLastCompactionIndex(entries)
	if compactionIndex == -1 {
		return 0
	}
	firstKept := EntryIndexForID(entries, entries[compactionIndex].FirstKeptEntryID)
	if firstKept == -1 {
		return compactionIndex + 1
	}
	return firstKept
}
