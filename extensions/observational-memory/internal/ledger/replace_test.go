package ledger_test

import (
	"reflect"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// A real v2 session: level 1 relabelled the March 8 record at one compaction and the correction
// superseding it at the next, so the correction (and its label) named a dropped id, the relabelled
// March 8 record was never superseded, and both dates read as current. Every link is read through
// the replacement chain to the record standing for its target, whether level 1's records say what
// they replace (replaces) or only their compaction's drops tell (records the shipped code wrote).
func TestFoldResolvesLinksThroughLevelOneReplacements(t *testing.T) {
	for _, replaces := range []bool{true, false} {
		entries := tf.RelabelledCorrectionBranch(replaces)
		wantReplaced := map[string]string{
			tf.RelabelMarch8:     tf.RelabelMarch8Label,
			tf.RelabelShoes:      tf.RelabelShoesLabel,
			tf.RelabelCorrection: tf.RelabelCorrectionLabel,
		}
		for _, folded := range []ledger.FoldedLedger{ledger.FoldLedger(entries, ""), ledger.FoldLedgerUnresolved(entries, "")} {
			if !reflect.DeepEqual(folded.ReplacedBy, wantReplaced) {
				t.Fatalf("replaces=%v: replaced by %v", replaces, folded.ReplacedBy)
			}
		}
		folded := ledger.FoldLedger(entries, "")
		if got := folded.ObservationsByID[tf.RelabelCorrectionLabel].Supersedes; !reflect.DeepEqual(got, []string{tf.RelabelMarch8Label}) {
			t.Fatalf("replaces=%v: the correction's label supersedes %v", replaces, got)
		}
		if got := folded.ObservationsByID[tf.RelabelCorrection].Supersedes; !reflect.DeepEqual(got, []string{tf.RelabelMarch8Label}) {
			t.Fatalf("replaces=%v: the dropped correction supersedes %v", replaces, got)
		}
		active := tf.IDs(folded.ActiveObservations)
		if !reflect.DeepEqual(active, []string{tf.RelabelMarch8Label, tf.RelabelShoesLabel, tf.RelabelCorrectionLabel}) {
			t.Fatalf("replaces=%v: active %v", replaces, active)
		}
		// The projection a compaction renders reads the same links.
		for _, o := range ledger.FullProjection(entries, "").Observations {
			if o.ID == tf.RelabelCorrectionLabel && !reflect.DeepEqual(o.Supersedes, []string{tf.RelabelMarch8Label}) {
				t.Fatalf("replaces=%v: projection supersedes %v", replaces, o.Supersedes)
			}
		}
		// recall reads the record with the same links.
		recalled := ledger.RecallMemorySources(entries, tf.RelabelCorrectionLabel)
		if len(recalled.Observations) != 1 || !reflect.DeepEqual(recalled.Observations[0].Observation.Supersedes, []string{tf.RelabelMarch8Label}) {
			t.Fatalf("replaces=%v: recall %+v", replaces, recalled.Observations)
		}
		// The unresolved fold keeps the links as written.
		if got := ledger.FoldLedgerUnresolved(entries, "").ObservationsByID[tf.RelabelCorrectionLabel].Supersedes; !reflect.DeepEqual(got, []string{tf.RelabelMarch8}) {
			t.Fatalf("replaces=%v: unresolved supersedes %v", replaces, got)
		}
	}
}

// Chains resolve transitively, a link a chain turns onto its own record is left out, a cycle
// ends, and a reflection's citations and CitedBy follow the standing record.
func TestStandingFollowsChains(t *testing.T) {
	by := map[string]string{"a00000000001": "a00000000002", "a00000000002": "a00000000003", "a00000000009": "a00000000008", "a00000000008": "a00000000009"}
	if got := ledger.Standing("a00000000001", by, nil); got != "a00000000003" {
		t.Fatalf("standing %s", got)
	}
	if got := ledger.Standing("a00000000009", by, nil); got != "a00000000008" {
		t.Fatalf("cycle standing %s", got)
	}
	if got := ledger.Standing("a00000000001", by, func(id string) bool { return id != "a00000000003" }); got != "a00000000002" {
		t.Fatalf("limited standing %s", got)
	}
	o := ledger.ResolveObservationLinks(ledger.Observation{ID: "a00000000003", Supersedes: []string{"a00000000001", "a00000000004"}, Conflicts: []string{"a00000000002"}}, by, nil)
	if !reflect.DeepEqual(o.Supersedes, []string{"a00000000004"}) || o.Conflicts != nil {
		t.Fatalf("resolved %+v", o)
	}

	entries := append(tf.RelabelledCorrectionBranch(true), tf.ReflectionsRecordedEntry("r1", []ledger.Reflection{tf.Reflection("f00000000001", []string{tf.RelabelShoes})}, "bb9e752f"))
	folded := ledger.FoldLedger(entries, "")
	if got := folded.ReflectionsByID["f00000000001"].SupportingObservationIDs; !reflect.DeepEqual(got, []string{tf.RelabelShoesLabel}) {
		t.Fatalf("citations %v", got)
	}
	if got := folded.CitedBy[tf.RelabelShoesLabel]; !reflect.DeepEqual(got, []string{"f00000000001"}) {
		t.Fatalf("cited by %v", folded.CitedBy)
	}
}

// A legacy batch's dropped record that one of its records supersedes was not replaced by it, and
// a dropped record two merges could stand for is replaced by neither: its links stay as written.
func TestLegacyReplacementNeedsOneReading(t *testing.T) {
	old := tf.Observation("a00000000001", func(o *ledger.Observation) { o.Content, o.SourceEntryIDs = "port 8080", []string{"m1"} })
	merge := func(id string) ledger.Observation {
		return tf.Observation(id, func(o *ledger.Observation) {
			o.Content, o.SourceEntryIDs = "port 8080 on staging "+id, []string{"m1", "m2"}
		})
	}
	consolidated := func(observations ...ledger.Observation) []any {
		return []any{tf.ObservationsRecordedEntry("l1", observations, "m2", map[string]any{"data": map[string]any{"observations": observations, "coversUpToId": "m2", "consolidated": true}})}
	}
	base := []any{tf.RawMessage("m1", "port 8080"), tf.RawMessage("m2", "on staging"), tf.ObservationsRecordedEntry("om", []ledger.Observation{old}, "m2")}
	build := func(extra []any) []ledger.Entry {
		out := []ledger.Entry{}
		for _, e := range append(append(base, extra...), tf.ObservationsDroppedEntry("drop", []string{old.ID}, "m2")) {
			out = append(out, e.(ledger.Entry))
		}
		return out
	}
	if got := ledger.FoldLedger(build(consolidated(merge("a00000000002"))), "").ReplacedBy; got[old.ID] != "a00000000002" {
		t.Fatalf("one merge: %v", got)
	}
	if got := ledger.FoldLedger(build(consolidated(merge("a00000000002"), merge("a00000000003"))), "").ReplacedBy; len(got) != 0 {
		t.Fatalf("two merges: %v", got)
	}
	superseding := merge("a00000000002")
	superseding.Supersedes = []string{old.ID}
	if got := ledger.FoldLedger(build(consolidated(superseding)), "").ReplacedBy; len(got) != 0 {
		t.Fatalf("superseded: %v", got)
	}
}

// A merge never stands for a record a standing link hides (a correction's supersede, a
// reflection's citation), whether it says what it replaces or only its compaction's drops tell
// (a session file the shipped validator wrote): the link stays on the dropped record, so it never
// hides the merge and the specifics of its other sources ("budget 400").
func TestMergeNeverStandsForAHiddenRecord(t *testing.T) {
	for _, reflection := range []bool{false, true} {
		for _, replaces := range []bool{true, false} {
			entries := tf.MergedLinkedBranch(reflection, replaces)
			folded := ledger.FoldLedger(entries, "")
			if _, ok := folded.ReplacedBy[tf.MergedX]; ok || folded.ReplacedBy[tf.MergedY] != tf.MergedM {
				t.Fatalf("reflection=%v replaces=%v: replaced by %v", reflection, replaces, folded.ReplacedBy)
			}
			if reflection {
				if got := folded.ReflectionsByID[tf.MergedLink].SupportingObservationIDs; !reflect.DeepEqual(got, []string{tf.MergedX}) {
					t.Fatalf("replaces=%v: citations %v", replaces, got)
				}
			} else if got := folded.ObservationsByID[tf.MergedLink].Supersedes; !reflect.DeepEqual(got, []string{tf.MergedX}) {
				t.Fatalf("replaces=%v: supersedes %v", replaces, got)
			}
			for _, o := range ledger.FullProjection(entries, "").Observations {
				if o.ID == tf.MergedLink && !reflect.DeepEqual(o.Supersedes, []string{tf.MergedX}) && !reflection {
					t.Fatalf("replaces=%v: projection supersedes %v", replaces, o.Supersedes)
				}
			}
		}
	}
}

// A replacement holds only once level 1's own drop entry drops the replaced record: a source the
// union rule kept, or every source when the drop append failed, keeps its links as written, so a
// later correction of it never hides the record that replaced the others.
func TestReplacementNeedsLevelOneDrop(t *testing.T) {
	entries := tf.ReplacedWithoutDropBranch(false)
	folded := ledger.FoldLedger(entries, "")
	if !reflect.DeepEqual(folded.ReplacedBy, map[string]string{tf.MergedX: tf.MergedM}) {
		t.Fatalf("kept back: replaced by %v", folded.ReplacedBy)
	}
	if got := folded.ObservationsByID[tf.MergedLink].Supersedes; !reflect.DeepEqual(got, []string{tf.MergedY}) {
		t.Fatalf("kept back: supersedes %v", got)
	}
	entries = tf.ReplacedWithoutDropBranch(true)
	folded = ledger.FoldLedger(entries, "")
	if len(folded.ReplacedBy) != 0 {
		t.Fatalf("failed drop: replaced by %v", folded.ReplacedBy)
	}
	for _, o := range ledger.FullProjection(entries, "").Observations {
		if o.ID == tf.MergedLink && !reflect.DeepEqual(o.Supersedes, []string{tf.MergedY}) {
			t.Fatalf("failed drop: projection supersedes %v", o.Supersedes)
		}
	}
}
