package ledger_test

import (
	"reflect"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

func coverage(id, coversUpTo string) ledger.Entry {
	return tf.ObservationsRecordedEntry(id, []ledger.Observation{tf.Observation("aaaaaaaaaaaa")}, coversUpTo)
}

func reflections(id, coversUpTo string) ledger.Entry {
	return tf.ReflectionsRecordedEntry(id, []ledger.Reflection{tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})}, coversUpTo)
}

func TestIsSourceEntry(t *testing.T) {
	if !ledger.IsSourceEntry(tf.TextCustomMessage("raw-1", "abcd")) || !ledger.IsSourceEntry(tf.BranchSummary("sum-1", "abcd")) {
		t.Fatal("source entries not detected")
	}
	if ledger.IsSourceEntry(coverage("om-1", "raw-1")) || ledger.IsSourceEntry(tf.CompactionEntry("cmp-1", tf.CompactionArgs{})) {
		t.Fatal("non-source entries detected as source")
	}
}

func TestEntryIndexByID(t *testing.T) {
	entries := []ledger.Entry{tf.TextCustomMessage("raw-1", "abcd"), tf.TextCustomMessage("raw-2", "efgh")}
	index := ledger.EntryIndexByID(entries)
	if index["raw-1"] != 0 || index["raw-2"] != 1 {
		t.Fatal("wrong indexes")
	}
}

func TestRawTokensAfterIndex(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		coverage("om-1", "raw-1"),
		tf.TextCustomMessage("raw-2", "bbbbbbbb"),
		tf.CompactionEntry("cmp-1", tf.CompactionArgs{FirstKeptEntryID: "raw-2"}),
		tf.BranchSummary("sum-1", "cccccccccccc"),
	}
	if ledger.RawTokensAfterIndex(entries, 0) != 5 || ledger.RawTokensAfterIndex(entries, 1) != 5 || ledger.RawTokensAfterIndex(entries, 2) != 3 {
		t.Fatalf("raw tokens: %d %d %d", ledger.RawTokensAfterIndex(entries, 0), ledger.RawTokensAfterIndex(entries, 1), ledger.RawTokensAfterIndex(entries, 2))
	}
}

func TestIndependentCoverageClocks(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		coverage("om-aaaaaaaaaaaa", "raw-1"),
		tf.TextCustomMessage("raw-2", "bbbbbbbb"),
		reflections("om-eeeeeeeeeeee", "raw-2"),
		tf.TextCustomMessage("raw-3", "cccccccccccc"),
		tf.ObservationsDroppedEntry("om-drop-1", []string{"aaaaaaaaaaaa"}, "om-eeeeeeeeeeee"),
		tf.TextCustomMessage("raw-4", "dddddddddddddddd"),
	}
	if got := ledger.RawTokensSinceObservationCoverage(entries); got != 9 {
		t.Fatalf("observation clock %d", got)
	}
	if got := ledger.RawTokensSinceReflectionCoverage(entries); got != 7 {
		t.Fatalf("reflection clock %d", got)
	}
	if got := ledger.RawTokensSinceDropCoverage(entries); got != 7 {
		t.Fatalf("drop clock %d", got)
	}
}

func TestCoverageMarkerOnLedgerEntry(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		reflections("om-eeeeeeeeeeee", "raw-1"),
		tf.ObservationsDroppedEntry("om-drop-1", []string{"aaaaaaaaaaaa"}, "om-eeeeeeeeeeee"),
		tf.TextCustomMessage("raw-2", "bbbbbbbb"),
	}
	if ledger.LatestCoverageIndex(entries, ledger.OMObservationsDropped) != 1 || ledger.RawTokensSinceDropCoverage(entries) != 2 {
		t.Fatal("ledger-entry coverage marker not honoured")
	}
}

func TestMaxCoveredPosition(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.TextCustomMessage("raw-2", "bbbbbbbb"),
		coverage("om-aaaaaaaaaaaa", "raw-2"),
		tf.ObservationsRecordedEntry("om-bbbbbbbbbbbb", []ledger.Observation{tf.Observation("bbbbbbbbbbbb")}, "raw-1"),
		tf.TextCustomMessage("raw-3", "cccccccccccc"),
	}
	if ledger.LatestCoverageIndex(entries, ledger.OMObservationsRecorded) != 1 || ledger.LatestCoverageMarkerID(entries, ledger.OMObservationsRecorded) != "raw-2" || ledger.RawTokensSinceObservationCoverage(entries) != 3 {
		t.Fatal("max covered position not chosen")
	}
}

func TestEarlierCoverageMarker(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.TextCustomMessage("raw-2", "bbbbbbbb"),
		tf.TextCustomMessage("raw-3", "cccccccccccc"),
		coverage("om-obs", "raw-3"),
		reflections("om-ref", "raw-2"),
	}
	if ledger.LatestCoverageMarkerID(entries, ledger.OMObservationsRecorded) != "raw-3" || ledger.LatestCoverageMarkerID(entries, ledger.OMReflectionsRecorded) != "raw-2" {
		t.Fatal("latest markers wrong")
	}
	if ledger.EarlierCoverageMarkerID(entries, "raw-3", "raw-2") != "raw-2" || ledger.EarlierCoverageMarkerID(entries, "raw-1", "") != "raw-1" || ledger.EarlierCoverageMarkerID(entries, "missing", "raw-2") != "raw-2" || ledger.EarlierCoverageMarkerID(entries, "missing-a", "missing-b") != "" {
		t.Fatal("earlier marker wrong")
	}
}

func TestInvalidMarkersIgnored(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.OldV2ObservationEntry("v2-obs"),
		coverage("om-obs-invalid", "missing"),
		tf.TextCustomMessage("raw-2", "bbbbbbbb"),
	}
	if ledger.RawTokensSinceObservationCoverage(entries) != 3 || ledger.LatestCoverageIndex(entries, ledger.OMReflectionsRecorded) != -1 {
		t.Fatal("invalid markers counted")
	}
}

func TestRawTokensSinceLastCompactionWithV2(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.CompactionEntry("cmp-1", tf.CompactionArgs{FirstKeptEntryID: "raw-1"}),
		tf.OldV2ObservationEntry("v2-obs"),
		tf.TextCustomMessage("raw-2", "bbbbbbbb"),
	}
	if got := ledger.RawTokensSinceLastCompaction(entries); got != 3 {
		t.Fatalf("raw since compaction %d", got)
	}
}

func TestCompactionRangeStart(t *testing.T) {
	if ledger.CompactionRangeStartIndex([]ledger.Entry{tf.TextCustomMessage("raw-1", "aaaa")}) != 0 {
		t.Fatal("range should start at root")
	}
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.CompactionEntry("cmp-1", tf.CompactionArgs{FirstKeptEntryID: "raw-2"}),
		tf.TextCustomMessage("raw-2", "aaaa"),
		tf.TextCustomMessage("raw-3", "aaaa"),
	}
	if ledger.CompactionRangeStartIndex(entries) != 2 || ledger.RawTokensSinceLastCompaction(entries) != 2 {
		t.Fatal("range should start at first kept entry")
	}
	missing := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.CompactionEntry("cmp-1", tf.CompactionArgs{FirstKeptEntryID: "gone"}),
		tf.TextCustomMessage("raw-2", "aaaa"),
	}
	if ledger.CompactionRangeStartIndex(missing) != 2 || ledger.RawTokensSinceLastCompaction(missing) != 1 {
		t.Fatal("range should start after the compaction when the id is missing")
	}
}

func TestObservedTokensSinceLastCompaction(t *testing.T) {
	if ledger.ObservedTokensSinceLastCompaction([]ledger.Entry{tf.TextCustomMessage("raw-1", "aaaaaaaaaaaa")}) != 0 {
		t.Fatal("expected zero without coverage")
	}
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaaaaaaaaaa"),
		tf.TextCustomMessage("raw-2", "aaaaaaaa"),
		coverage("om-1", "raw-1"),
		tf.TextCustomMessage("raw-3", "aaaa"),
		reflections("om-ref", "raw-3"),
	}
	if ledger.ObservedTokensSinceLastCompaction(entries) != 3 || ledger.RawTokensSinceLastCompaction(entries) != 6 {
		t.Fatal("observed/raw counts wrong")
	}
	before := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaaaaaaaaaa"),
		coverage("om-1", "raw-1"),
		tf.CompactionEntry("cmp-1", tf.CompactionArgs{FirstKeptEntryID: "raw-2"}),
		tf.TextCustomMessage("raw-2", "aaaaaaaa"),
	}
	if ledger.ObservedTokensSinceLastCompaction(before) != 0 {
		t.Fatal("coverage before the boundary counted")
	}
	after := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaaaaaaaaaa"),
		tf.CompactionEntry("cmp-1", tf.CompactionArgs{FirstKeptEntryID: "raw-2"}),
		tf.TextCustomMessage("raw-2", "aaaaaaaa"),
		tf.TextCustomMessage("raw-3", "aaaa"),
		coverage("om-1", "raw-3"),
		tf.TextCustomMessage("raw-4", "aaaaaaaaaaaa"),
	}
	if ledger.ObservedTokensSinceLastCompaction(after) != 3 {
		t.Fatal("count from the boundary wrong")
	}
}

func TestUnobservedSourceSpanBefore(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaaaaaaaaaa"),
		coverage("om-1", "raw-1"),
		tf.TextCustomMessage("raw-2", "aaaaaaaa"),
		tf.TextCustomMessage("raw-3", "aaaa"),
		tf.TextCustomMessage("raw-4", "aaaa"),
	}
	if ledger.UnobservedSourceSpanBefore(entries, 2) != nil || ledger.UnobservedSourceSpanBefore(entries, 1) != nil {
		t.Fatal("expected no gap when coverage reaches the cut")
	}
	if got := ledger.UnobservedSourceSpanBefore(entries, 4); !reflect.DeepEqual(*got, ledger.UnobservedSourceSpan{FirstIndex: 2, LastIndex: 3, EntryCount: 2, Tokens: 3}) {
		t.Fatalf("gap %+v", got)
	}
	uncovered := []ledger.Entry{tf.TextCustomMessage("raw-1", "aaaa"), tf.TextCustomMessage("raw-2", "aaaa")}
	if got := ledger.UnobservedSourceSpanBefore(uncovered, 2); !reflect.DeepEqual(*got, ledger.UnobservedSourceSpan{FirstIndex: 0, LastIndex: 1, EntryCount: 2, Tokens: 2}) {
		t.Fatalf("uncovered gap %+v", got)
	}
	compacted := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.CompactionEntry("cmp-1", tf.CompactionArgs{FirstKeptEntryID: "raw-2"}),
		tf.TextCustomMessage("raw-2", "aaaa"),
		tf.TextCustomMessage("raw-3", "aaaa"),
	}
	if got := ledger.UnobservedSourceSpanBefore(compacted, 3); !reflect.DeepEqual(*got, ledger.UnobservedSourceSpan{FirstIndex: 2, LastIndex: 2, EntryCount: 1, Tokens: 1}) {
		t.Fatalf("compacted gap %+v", got)
	}
}

func TestObservedTokensSinceReflectionCoverage(t *testing.T) {
	if ledger.ObservedTokensSinceReflectionCoverage([]ledger.Entry{tf.TextCustomMessage("raw-1", "aaaaaaaaaaaa")}) != 0 {
		t.Fatal("expected zero without coverage")
	}
	first := []ledger.Entry{tf.TextCustomMessage("raw-1", "aaaaaaaaaaaa"), tf.TextCustomMessage("raw-2", "aaaaaaaa"), coverage("om-1", "raw-1")}
	if ledger.ObservedTokensSinceReflectionCoverage(first) != 3 {
		t.Fatal("everything observed before any reflection should count")
	}
	between := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaaaaaaaaaa"),
		coverage("om-1", "raw-1"),
		reflections("om-ref", "raw-1"),
		tf.TextCustomMessage("raw-2", "aaaaaaaa"),
		tf.TextCustomMessage("raw-3", "aaaa"),
		coverage("om-2", "raw-2"),
		tf.TextCustomMessage("raw-4", "aaaaaaaaaaaaaaaa"),
	}
	if ledger.ObservedTokensSinceReflectionCoverage(between) != 2 {
		t.Fatal("only source between the reflection marker and the frontier should count")
	}
	caughtUp := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaaaaaaaaaa"),
		coverage("om-1", "raw-1"),
		reflections("om-ref", "raw-1"),
		tf.TextCustomMessage("raw-2", "aaaaaaaaaaaaaaaaaaaaaaaa"),
	}
	if ledger.ObservedTokensSinceReflectionCoverage(caughtUp) != 0 {
		t.Fatal("caught-up reflection coverage should be zero")
	}
}

func TestRealTokensSinceAnchor(t *testing.T) {
	usage := func(total int) map[string]any {
		return map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "ok"}}, "stopReason": "stop", "usage": map[string]any{"totalTokens": total, "input": 0, "output": 0, "cacheRead": 0, "cacheWrite": 0}}
	}
	entries := []ledger.Entry{
		tf.MessageEntry("a-1", usage(1000)),
		coverage("om-1", "a-1"),
		tf.MessageEntry("a-2", usage(1500)),
	}
	if got, ok := ledger.RealTokensSinceAnchor(entries, ledger.OMObservationsRecorded, 2500); !ok || got != 1500 {
		t.Fatalf("growth since coverage %d %v", got, ok)
	}
	if _, ok := ledger.RealTokensSinceAnchor(entries, ledger.OMObservationsRecorded, 500); ok {
		t.Fatal("a shrunk context must be unmeasurable")
	}
	compacted := append(entries, tf.CompactionEntry("cmp-1", tf.CompactionArgs{FirstKeptEntryID: "a-2"}))
	if _, ok := ledger.RealTokensSinceAnchor(compacted, ledger.OMObservationsRecorded, 3000); ok {
		t.Fatal("no post-compaction baseline must be unmeasurable")
	}
	after := append(compacted, tf.MessageEntry("a-3", usage(400)))
	if got, ok := ledger.RealTokensSinceAnchor(after, ledger.OMObservationsRecorded, 900); !ok || got != 500 {
		t.Fatalf("growth since compaction %d %v", got, ok)
	}
	if got, ok := ledger.RealTokensSinceAnchor([]ledger.Entry{tf.TextCustomMessage("raw-1", "aaaa")}, ledger.OMObservationsRecorded, 700); !ok || got != 700 {
		t.Fatalf("no anchor should measure from zero: %d %v", got, ok)
	}
}
