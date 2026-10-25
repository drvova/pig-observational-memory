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
