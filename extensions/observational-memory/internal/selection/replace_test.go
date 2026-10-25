package selection_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/extract"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/selection"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// A real v2 session rendered both "March 8" (level 1's label of the first record) and "March 15,
// not March 8 (was 4dfb3f34dd2c)" as current, the pointer naming a record level 1 had dropped.
// Read through level 1's replacements, the correction supersedes the relabelled record: the old
// date is not shown, it is dropped with the summary, and the pointer names the record recall
// reads it from.
func TestRenderFollowsLevelOneReplacements(t *testing.T) {
	for _, replaces := range []bool{true, false} {
		entries := testfix.RelabelledCorrectionBranch(replaces)
		in := input(entries, 10_000)
		r := selection.Render(in)
		rows := noteRows(t, r.Text)
		correction, shown := rows[testfix.RelabelCorrectionLabel]
		if !shown || correction["content"] != "Correction: the half marathon is on March 15, not March 8. (was "+testfix.RelabelMarch8Label+")" {
			t.Fatalf("replaces=%v: correction row %v:\n%s", replaces, correction, r.Text)
		}
		if _, shown := rows[testfix.RelabelMarch8Label]; shown || strings.Contains(r.Text, testfix.RelabelMarch8+")") {
			t.Fatalf("replaces=%v: the corrected date is still shown, or the pointer names a dropped id:\n%s", replaces, r.Text)
		}
		if _, shown := rows[testfix.RelabelShoesLabel]; !shown {
			t.Fatalf("replaces=%v: the sibling record is missing:\n%s", replaces, r.Text)
		}
		drops := extract.Drops(in.Observations, r.Observations, r.Reflections, in.Folded.ObservationsByID)
		if !slices.Contains(drops, testfix.RelabelMarch8Label) {
			t.Fatalf("replaces=%v: drops %v", replaces, drops)
		}
		for _, o := range r.Observations {
			if o.ID == testfix.RelabelMarch8Label || o.ID == testfix.RelabelMarch8 {
				t.Fatalf("replaces=%v: shows %s", replaces, o.ID)
			}
		}
	}
}

// A standing reflection cites a record level 1 then merged with another (as a validator that did
// not refuse it wrote): the citation stays on the dropped record, so the reflection rendering never
// drops the merge, and its other source's specific ("budget 400") stays shown. The same holds for
// a correction superseding the merged record.
func TestRenderKeepsAMergeAStandingLinkNamed(t *testing.T) {
	for _, reflection := range []bool{true, false} {
		for _, replaces := range []bool{true, false} {
			entries := testfix.MergedLinkedBranch(reflection, replaces)
			in := input(entries, 10_000)
			r := selection.Render(in)
			if !strings.Contains(r.Text, "budget 400") {
				t.Fatalf("reflection=%v replaces=%v: the merged specific is gone:\n%s", reflection, replaces, r.Text)
			}
			drops := extract.Drops(in.Observations, r.Observations, r.Reflections, in.Folded.ObservationsByID)
			if slices.Contains(drops, testfix.MergedM) {
				t.Fatalf("reflection=%v replaces=%v: drops the merge %v", reflection, replaces, drops)
			}
		}
	}
}
