package ledger_test

import (
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// A text names the most specific subject already in the ledger whose words it holds, read
// through the aliases; plural forms match as canonicalization folds them.
func TestSubjectInText(t *testing.T) {
	subjects := []string{"running", "running shoes", "half marathon/pace", "ci"}
	aliases := []ledger.SubjectAlias{{From: "continuous integration", To: "ci"}}
	for _, c := range []struct{ text, want string }{
		{"Bought new running shoes for 120 USD.", "running"},
		{"Her half marathons pace is 5:10.", "half marathon"},
		{"The continuous integration run takes 9 minutes.", "ci"},
		{"Nothing named here.", ""},
		{"", ""},
	} {
		if got := ledger.SubjectInText(c.text, subjects, aliases); got != c.want {
			t.Errorf("%q: %q, want %q", c.text, got, c.want)
		}
	}
}

// Extraction progress is the furthest observation or om.extracted marker.
func TestExtractionProgressIndex(t *testing.T) {
	entries := []ledger.Entry{
		tf.RawMessage("u1", "a"),
		tf.RawMessage("u2", "b"),
		tf.ObservationsRecordedEntry("om1", []ledger.Observation{tf.Observation("aaaaaaaaaaaa")}, "u1"),
		tf.RawMessage("u3", "c"),
	}
	if got := ledger.ExtractionProgressIndex(entries); got != 0 {
		t.Fatalf("progress %d", got)
	}
	entries = append(entries, tf.Entry(map[string]any{"type": "custom", "id": "x1", "customType": ledger.OMExtracted, "data": map[string]any{"coversUpToId": "u3"}}))
	if got := ledger.ExtractionProgressIndex(entries); got != 3 {
		t.Fatalf("progress %d", got)
	}
	// A malformed marker claims nothing.
	entries = append(entries, tf.RawMessage("u4", "d"), tf.Entry(map[string]any{"type": "custom", "id": "x2", "customType": ledger.OMExtracted, "data": map[string]any{}}))
	if got := ledger.ExtractionProgressIndex(entries); got != 3 {
		t.Fatalf("progress %d", got)
	}
}

// Memory v2's extraction starts after v1's coverage, inside the compaction range, and never
// before the latest full fold's boundary: a v1 compaction that moved its cut back (firstKept u2)
// and folded through u3 leaves u2 and u3 to no extraction even with no coverage marker on the
// branch; the boundary entry itself is read when nothing covered it.
func TestExtractionStartIndex(t *testing.T) {
	details := tf.MemoryDetails(tf.MemoryDetailsArgs{FullFold: true, FoldThroughEntryID: "u3"})
	entries := []ledger.Entry{
		tf.RawMessage("u1", "a"),
		tf.RawMessage("u2", "b"),
		tf.RawMessage("u3", "c"),
		tf.CompactionEntry("c1", tf.CompactionArgs{FirstKeptEntryID: "u2", Details: details}),
		tf.RawMessage("u4", "d"),
	}
	if got := ledger.ExtractionStartIndex(entries); got != 2 {
		t.Fatalf("start %d, want the fold boundary u3 (2)", got)
	}
	entries = append(entries[:2], append([]ledger.Entry{tf.ObservationsRecordedEntry("om1", []ledger.Observation{tf.Observation("aaaaaaaaaaaa")}, "u3")}, entries[2:]...)...)
	if got := ledger.ExtractionStartIndex(entries); got != 4 {
		t.Fatalf("start %d, want after v1's coverage of u3 (index 4)", got)
	}
	// A native compaction has no fold boundary: the range start stands.
	native := []ledger.Entry{tf.RawMessage("u1", "a"), tf.RawMessage("u2", "b"), tf.CompactionEntry("c1", tf.CompactionArgs{FirstKeptEntryID: "u2"}), tf.RawMessage("u3", "c")}
	if got := ledger.ExtractionStartIndex(native); got != 1 {
		t.Fatalf("start %d, want the range start u2 (1)", got)
	}
}
