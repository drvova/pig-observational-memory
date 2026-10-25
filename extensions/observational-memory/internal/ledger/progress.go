package ledger

import (
	"math"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
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

// LatestCoverageMarkerID is the id of the furthest entry a marker of customType covers.
func LatestCoverageMarkerID(entries []Entry, customType string) string {
	index := EntryIndexByID(entries)
	latestIndex := -1
	latestID := ""
	for _, entry := range entries {
		coversUpTo, ok := coverageEntry(entry, customType)
		if !ok {
			continue
		}
		covered, ok := index[coversUpTo]
		if ok && covered > latestIndex {
			latestIndex = covered
			latestID = coversUpTo
		}
	}
	return latestID
}

// StraddlingCoverageMarkerID is the id covered by the earliest marker of
// customType that reaches cutIndex, when that marker straddles the cut: no
// earlier marker covers some source entry before cutIndex, so this marker read
// source before the cut as well as at or after it. "" when no marker straddles
// the cut. Only source entries for which needsCoverage is true count (nil
// counts every source entry), as in UnobservedSourceSpanBeforeWhere.
func StraddlingCoverageMarkerID(entries []Entry, cutIndex int, customType string, needsCoverage func(Entry) bool) string {
	index := EntryIndexByID(entries)
	before := -1
	reaching := -1
	for _, entry := range entries {
		coversUpTo, ok := coverageEntry(entry, customType)
		if !ok {
			continue
		}
		covered, ok := index[coversUpTo]
		if !ok {
			continue
		}
		if covered < cutIndex {
			before = max(before, covered)
		} else if reaching == -1 || covered < reaching {
			reaching = covered
		}
	}
	if reaching == -1 {
		return ""
	}
	for i := before + 1; i < cutIndex; i++ {
		if IsSourceEntry(entries[i]) && (needsCoverage == nil || needsCoverage(entries[i])) {
			return entries[reaching].ID
		}
	}
	return ""
}

// EarlierCoverageMarkerID is whichever of two marker ids sits earlier on the branch.
func EarlierCoverageMarkerID(entries []Entry, firstID, secondID string) string {
	if firstID == "" {
		return secondID
	}
	if secondID == "" {
		return firstID
	}
	index := EntryIndexByID(entries)
	firstIndex, firstOK := index[firstID]
	secondIndex, secondOK := index[secondID]
	if !firstOK {
		if !secondOK {
			return ""
		}
		return secondID
	}
	if !secondOK {
		return firstID
	}
	if firstIndex <= secondIndex {
		return firstID
	}
	return secondID
}

// RawTokensAfterIndex sums estimated source tokens after index.
func RawTokensAfterIndex(entries []Entry, index int) int {
	return RawTokensAfterIndexWhere(entries, index, nil)
}

// RawTokensAfterIndexWhere is RawTokensAfterIndex over the source entries
// include accepts; a nil include accepts every source entry.
func RawTokensAfterIndexWhere(entries []Entry, index int, include func(Entry) bool) int {
	total := 0
	for i := max(0, index+1); i < len(entries); i++ {
		if IsSourceEntry(entries[i]) && (include == nil || include(entries[i])) {
			total += tokens.EstimateEntryTokens(entries[i])
		}
	}
	return total
}

// RawTokensSinceCoverage sums estimated source tokens after the latest marker of customType.
func RawTokensSinceCoverage(entries []Entry, customType string) int {
	return RawTokensAfterIndex(entries, LatestCoverageIndex(entries, customType))
}

// RawTokensSinceObservationCoverage is the unobserved source backlog.
func RawTokensSinceObservationCoverage(entries []Entry) int {
	return RawTokensSinceCoverage(entries, OMObservationsRecorded)
}

// RawTokensSinceReflectionCoverage is the source backlog since the last reflection marker.
func RawTokensSinceReflectionCoverage(entries []Entry) int {
	return RawTokensSinceCoverage(entries, OMReflectionsRecorded)
}

// ObservedTokensSinceReflectionCoverage is the source the observer has covered
// since the latest reflection marker: the material new reflections could draw
// on. A large unobserved backlog does not make the reflector due.
func ObservedTokensSinceReflectionCoverage(entries []Entry) int {
	reflectionIndex := LatestCoverageIndex(entries, OMReflectionsRecorded)
	observationIndex := LatestCoverageIndex(entries, OMObservationsRecorded)
	if observationIndex <= reflectionIndex {
		return 0
	}
	total := 0
	for i := reflectionIndex + 1; i <= observationIndex; i++ {
		if IsSourceEntry(entries[i]) {
			total += tokens.EstimateEntryTokens(entries[i])
		}
	}
	return total
}

// RawTokensSinceDropCoverage is the source backlog since the last drop marker.
func RawTokensSinceDropCoverage(entries []Entry) int {
	return RawTokensSinceCoverage(entries, OMObservationsDropped)
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

// ContextTokensFromUsage reads provider-reported context size: totalTokens
// when positive, else the sum of the four components when all are numbers.
func ContextTokensFromUsage(usage any) (int, bool) {
	u, ok := usage.(map[string]any)
	if !ok {
		return 0, false
	}
	if total, ok := finiteNumber(u["totalTokens"]); ok && total > 0 {
		return int(total), true
	}
	sum := 0.0
	for _, key := range []string{"input", "output", "cacheRead", "cacheWrite"} {
		n, ok := finiteNumber(u[key])
		if !ok {
			return 0, false
		}
		sum += n
	}
	if sum > 0 {
		return int(sum), true
	}
	return 0, false
}

func validAssistantContextTokens(entry Entry) (int, bool) {
	if entry.Type != "message" || entry.Message == nil {
		return 0, false
	}
	if entry.Message["role"] != "assistant" {
		return 0, false
	}
	if stop, _ := entry.Message["stopReason"].(string); stop == "aborted" || stop == "error" {
		return 0, false
	}
	return ContextTokensFromUsage(entry.Message["usage"])
}

// RealContextTokensAfterCompaction is the first valid assistant usage after a
// compaction: the post-compaction baseline. The compaction entry's own usage
// is the summary call's and is never used.
func RealContextTokensAfterCompaction(entries []Entry, compactionIdx int) (int, bool) {
	for i := compactionIdx + 1; i < len(entries); i++ {
		if t, ok := validAssistantContextTokens(entries[i]); ok {
			return t, true
		}
	}
	return 0, false
}

// RealContextTokensAtCoverage is the last valid assistant usage at or before a
// coverage index.
func RealContextTokensAtCoverage(entries []Entry, coverageIdx int) (int, bool) {
	for i := coverageIdx; i >= 0; i-- {
		if t, ok := validAssistantContextTokens(entries[i]); ok {
			return t, true
		}
	}
	return 0, false
}

// RealTokensSinceAnchor is the provider-reported context growth since the most
// recent anchor (a compaction, or the coverage marker of customType; "" means
// no coverage). It reports false when the baseline cannot be measured or the
// current context shrank below it.
func RealTokensSinceAnchor(entries []Entry, customType string, currentContextTokens int) (int, bool) {
	coverageIdx := -1
	if customType != "" {
		coverageIdx = LatestCoverageIndex(entries, customType)
	}
	compactionIdx := FindLastCompactionIndex(entries)
	if compactionIdx > coverageIdx {
		baseline, ok := RealContextTokensAfterCompaction(entries, compactionIdx)
		if !ok {
			return 0, false
		}
		delta := currentContextTokens - baseline
		if delta < 0 {
			return 0, false
		}
		return delta, true
	}
	if coverageIdx >= 0 {
		baseline, ok := RealContextTokensAtCoverage(entries, coverageIdx)
		if !ok {
			return 0, false
		}
		delta := currentContextTokens - baseline
		if delta < 0 {
			return 0, false
		}
		return delta, true
	}
	return max(0, currentContextTokens), true
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

// RawTokensSinceLastCompaction sums estimated source tokens in the current compaction range.
func RawTokensSinceLastCompaction(entries []Entry) int {
	return RawTokensAfterIndex(entries, CompactionRangeStartIndex(entries)-1)
}

// ObservedTokensSinceLastCompaction sums the estimated source tokens in the
// current compaction range that observation coverage has reached.
func ObservedTokensSinceLastCompaction(entries []Entry) int {
	start := CompactionRangeStartIndex(entries)
	coverageIndex := LatestCoverageIndex(entries, OMObservationsRecorded)
	if coverageIndex < start {
		return 0
	}
	total := 0
	for i := start; i <= coverageIndex; i++ {
		if IsSourceEntry(entries[i]) {
			total += tokens.EstimateEntryTokens(entries[i])
		}
	}
	return total
}

// UnobservedSourceSpan is the unobserved source before a cut.
type UnobservedSourceSpan struct {
	FirstIndex int
	LastIndex  int
	EntryCount int
	Tokens     int
}

// UnobservedSourceSpanBefore is the source inside the current compaction
// range, before cutIndex (exclusive), that observation coverage has not
// reached; nil when coverage reaches the cut.
func UnobservedSourceSpanBefore(entries []Entry, cutIndex int) *UnobservedSourceSpan {
	return UnobservedSourceSpanBeforeWhere(entries, cutIndex, nil)
}

// UnobservedSourceSpanBeforeWhere is UnobservedSourceSpanBefore counting only
// source entries for which needsCoverage is true (nil counts every source
// entry). The compaction hook passes the observer's own renderability test so
// an entry the observer could never read (no renderable content) does not hold
// the cut.
func UnobservedSourceSpanBeforeWhere(entries []Entry, cutIndex int, needsCoverage func(Entry) bool) *UnobservedSourceSpan {
	coverageIndex := LatestCoverageIndex(entries, OMObservationsRecorded)
	start := max(coverageIndex+1, CompactionRangeStartIndex(entries))
	span := UnobservedSourceSpan{FirstIndex: -1, LastIndex: -1}
	for i := start; i < cutIndex && i < len(entries); i++ {
		if !IsSourceEntry(entries[i]) || (needsCoverage != nil && !needsCoverage(entries[i])) {
			continue
		}
		if span.FirstIndex == -1 {
			span.FirstIndex = i
		}
		span.LastIndex = i
		span.EntryCount++
		span.Tokens += tokens.EstimateEntryTokens(entries[i])
	}
	if span.FirstIndex == -1 {
		return nil
	}
	return &span
}
