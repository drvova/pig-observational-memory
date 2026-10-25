package memquery

import (
	"strings"
	"testing"

	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// The real session's relabelled correction, read by the memory tool: today the relabelled March 8
// record is superseded by the relabelled correction; as the memory stood after the first
// compaction (before the second relabel), by the correction then current; and before the first
// compaction, the original record by the original correction.
func TestNotesFollowLevelOneReplacements(t *testing.T) {
	for _, replaces := range []bool{true, false} {
		env := Env{Entries: tf.RelabelledCorrectionBranch(replaces), V2: true}
		for _, c := range []struct {
			query, id, supersededBy string
		}{
			{"notes", tf.RelabelMarch8Label, tf.RelabelCorrectionLabel},
			{"notes | through 0588a446 asof", tf.RelabelMarch8Label, tf.RelabelCorrection},
			{"notes | through bb9e752f asof", tf.RelabelMarch8, tf.RelabelCorrection},
		} {
			table := notesTable(t, env, c.query)
			by, listed := cell(t, table, c.id, "superseded_by")
			if !listed || !strings.Contains(by, c.supersededBy) || strings.Contains(by, laterMark) && !strings.Contains(c.query, "asof") {
				t.Fatalf("replaces=%v %s: %s listed %v, superseded_by %q, want %s", replaces, c.query, c.id, listed, by, c.supersededBy)
			}
		}
		if _, listed := cell(t, notesTable(t, env, "notes"), tf.RelabelMarch8, "id"); listed {
			t.Fatalf("replaces=%v: the dropped record is listed", replaces)
		}
	}
}
