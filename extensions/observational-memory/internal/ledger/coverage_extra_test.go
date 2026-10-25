package ledger_test

import (
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
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

	if got := latestMarker(entries, ledger.OMObservationsRecorded); got != "raw-1" {
		t.Fatalf("coverage %q", got)
	}
}
