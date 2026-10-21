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
