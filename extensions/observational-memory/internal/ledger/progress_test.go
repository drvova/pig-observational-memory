package ledger_test

import (
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

func TestCoverageMarkerOnLedgerEntry(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		reflections("om-eeeeeeeeeeee", "raw-1"),
		tf.ObservationsDroppedEntry("om-drop-1", []string{"aaaaaaaaaaaa"}, "om-eeeeeeeeeeee"),
		tf.TextCustomMessage("raw-2", "bbbbbbbb"),
	}
	if ledger.LatestCoverageIndex(entries, ledger.OMObservationsDropped) != 1 {
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
	if ledger.LatestCoverageIndex(entries, ledger.OMObservationsRecorded) != 1 || latestMarker(entries, ledger.OMObservationsRecorded) != "raw-2" {
		t.Fatal("max covered position not chosen")
	}
}

func TestInvalidMarkersIgnored(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.OldV2ObservationEntry("v2-obs"),
		coverage("om-obs-invalid", "missing"),
		tf.TextCustomMessage("raw-2", "bbbbbbbb"),
	}
	if ledger.LatestCoverageIndex(entries, ledger.OMObservationsRecorded) != -1 || ledger.LatestCoverageIndex(entries, ledger.OMReflectionsRecorded) != -1 {
		t.Fatal("invalid markers counted")
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
	if ledger.CompactionRangeStartIndex(entries) != 2 {
		t.Fatal("range should start at first kept entry")
	}
	missing := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.CompactionEntry("cmp-1", tf.CompactionArgs{FirstKeptEntryID: "gone"}),
		tf.TextCustomMessage("raw-2", "aaaa"),
	}
	if ledger.CompactionRangeStartIndex(missing) != 2 {
		t.Fatal("range should start after the compaction when the id is missing")
	}
}

// latestMarker is the id of the entry the latest coverage marker of customType reaches, "" for
// none.
func latestMarker(entries []ledger.Entry, customType string) string {
	if i := ledger.LatestCoverageIndex(entries, customType); i >= 0 {
		return entries[i].ID
	}
	return ""
}
