package ledger_test

import (
	"testing"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/testfix"
)

// Coverage is claimed only by entries memory accepts: one malformed record
// disqualifies the entry for both, so its span is observed again.
func TestCoverageRequiresValidRecords(t *testing.T) {
	good := tf.ObservationsRecordedEntry("om-good", []ledger.Observation{tf.Observation("aaaaaaaaaaaa")}, "raw-1")
	bad := tf.Entry(map[string]any{"type": "custom", "id": "om-bad", "parentId": nil, "timestamp": tf.DefaultTimestamp, "customType": ledger.OMObservationsRecorded,
		"data": map[string]any{"coversUpToId": "raw-2", "observations": []any{
			map[string]any{"id": "bbbbbbbbbbbb", "content": "fine", "timestamp": tf.DefaultTimestamp, "relevance": "medium", "sourceEntryIds": []any{"raw-2"}, "tokenCount": 5},
			map[string]any{"id": "cccccccccccc", "content": "broken", "timestamp": tf.DefaultTimestamp, "relevance": "normal", "sourceEntryIds": []any{"raw-2"}, "tokenCount": 5},
		}}})
	entries := []ledger.Entry{tf.TextCustomMessage("raw-1", "aaaa"), good, tf.TextCustomMessage("raw-2", "bbbb"), bad}

	if got := ledger.LatestCoverageMarkerID(entries, ledger.OMObservationsRecorded); got != "raw-1" {
		t.Fatalf("coverage %q", got)
	}
	if ledger.RawTokensSinceObservationCoverage(entries) == 0 {
		t.Fatal("the span behind the malformed entry must stay unobserved")
	}
}

// The unobserved span can exclude entries that need no coverage.
func TestUnobservedSpanHonoursPredicate(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.ObservationsRecordedEntry("om-cov", []ledger.Observation{tf.Observation("aaaaaaaaaaaa")}, "raw-1"),
		tf.RawMessage("empty", "", map[string]any{"message": map[string]any{"role": "assistant", "content": []any{}, "stopReason": "aborted"}}),
		tf.TextCustomMessage("raw-3", "cccc"),
	}
	if span := ledger.UnobservedSourceSpanBefore(entries, 3); span == nil || span.EntryCount != 1 || span.FirstIndex != 2 {
		t.Fatalf("unfiltered span %+v", span)
	}
	if span := ledger.UnobservedSourceSpanBeforeWhere(entries, 3, func(e ledger.Entry) bool { return e.ID != "empty" }); span != nil {
		t.Fatalf("filtered span %+v", span)
	}
}

// The straddling marker is the earliest one reaching the cut, and only when it
// read source before the cut that no earlier marker covers.
func TestStraddlingCoverageMarker(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		coverage("om-1", "raw-1"),
		tf.RawMessage("empty", "", map[string]any{"message": map[string]any{"role": "assistant", "content": []any{}, "stopReason": "aborted"}}),
		tf.TextCustomMessage("raw-3", "cccc"),
		tf.TextCustomMessage("raw-4", "dddd"),
		tf.TextCustomMessage("raw-5", "eeee"),
		coverage("om-5", "raw-5"),
		tf.TextCustomMessage("raw-7", "ffff"),
		coverage("om-7", "raw-7"),
	}
	renders := func(e ledger.Entry) bool { return e.ID != "empty" }
	straddling := func(entries []ledger.Entry, cutIndex int, needsCoverage func(ledger.Entry) bool) string {
		return ledger.StraddlingCoverageMarkerID(entries, cutIndex, ledger.OMObservationsRecorded, needsCoverage)
	}
	if got := straddling(entries, 4, renders); got != "raw-5" {
		t.Fatalf("straddling %q", got)
	}
	if got, unfiltered := straddling(entries, 3, renders), straddling(entries, 3, nil); got != "" || unfiltered != "raw-5" {
		t.Fatalf("straddling %q, unfiltered %q", got, unfiltered)
	}
	if got := straddling(entries, 7, renders); got != "" {
		t.Fatalf("straddling with om-5 covering everything before the cut %q", got)
	}
	if got := straddling(entries[:6], 4, renders); got != "" {
		t.Fatalf("straddling with no marker reaching the cut %q", got)
	}
}
