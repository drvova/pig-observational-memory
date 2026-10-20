package ledger_test

import (
	"reflect"
	"testing"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/testfix"
)

func withSources(ids ...string) func(*ledger.Observation) {
	return func(o *ledger.Observation) { o.SourceEntryIDs = ids }
}

func withContent(content string) func(*ledger.Observation) {
	return func(o *ledger.Observation) { o.Content = content }
}

func TestFoldThroughTarget(t *testing.T) {
	obs1 := tf.Observation("aaaaaaaaaaaa", withSources("raw-1"))
	obs2 := tf.Observation("bbbbbbbbbbbb", withSources("raw-2"))
	ref1 := tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.ObservationsRecordedEntry("om-aaaaaaaaaaaa", []ledger.Observation{obs1}, "raw-1"),
		tf.TextCustomMessage("raw-2", "bbbb"),
		tf.ReflectionsRecordedEntry("om-eeeeeeeeeeee", []ledger.Reflection{ref1}, "raw-2"),
		tf.ObservationsRecordedEntry("om-bbbbbbbbbbbb", []ledger.Observation{obs2}, "raw-2"),
	}
	folded := ledger.FoldLedger(entries, "om-eeeeeeeeeeee")
	if !reflect.DeepEqual(tf.IDs(folded.Observations), []string{"aaaaaaaaaaaa"}) || !reflect.DeepEqual(tf.IDs(folded.ActiveObservations), []string{"aaaaaaaaaaaa"}) {
		t.Fatalf("unexpected observations %v", tf.IDs(folded.Observations))
	}
	if !reflect.DeepEqual(tf.RefIDs(folded.Reflections), []string{"eeeeeeeeeeee"}) {
		t.Fatalf("unexpected reflections %v", tf.RefIDs(folded.Reflections))
	}
	if _, ok := folded.ObservationsByID["bbbbbbbbbbbb"]; ok {
		t.Fatal("observation past the fold boundary was folded")
	}
}

func TestFoldTombstones(t *testing.T) {
	obs1 := tf.Observation("aaaaaaaaaaaa")
	obs2 := tf.Observation("bbbbbbbbbbbb")
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.ObservationsRecordedEntry("om-a", []ledger.Observation{obs1, obs2}, "raw-1"),
		tf.ObservationsDroppedEntry("om-drop-1", []string{"aaaaaaaaaaaa"}, "raw-1"),
	}
	folded := ledger.FoldLedger(entries, "")
	if !reflect.DeepEqual(tf.IDs(folded.Observations), []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb"}) {
		t.Fatalf("history lost: %v", tf.IDs(folded.Observations))
	}
	if !reflect.DeepEqual(tf.IDs(folded.ActiveObservations), []string{"bbbbbbbbbbbb"}) {
		t.Fatalf("drop not applied: %v", tf.IDs(folded.ActiveObservations))
	}
	if !folded.DroppedObservationIDs["aaaaaaaaaaaa"] {
		t.Fatal("tombstone missing")
	}
	if !reflect.DeepEqual(folded.ObservationsByID["aaaaaaaaaaaa"], obs1) {
		t.Fatal("dropped observation record lost")
	}
}

func TestFoldFirstValidWins(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.ObservationsRecordedEntry("om-1", []ledger.Observation{tf.Observation("aaaaaaaaaaaa", withContent("first observation"))}, "raw-1"),
		tf.ObservationsRecordedEntry("om-2", []ledger.Observation{tf.Observation("aaaaaaaaaaaa", withContent("duplicate observation"))}, "raw-1"),
		tf.ReflectionsRecordedEntry("om-3", []ledger.Reflection{tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"}, func(r *ledger.Reflection) { r.Content = "first reflection" })}, "raw-1"),
		tf.ReflectionsRecordedEntry("om-4", []ledger.Reflection{tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"}, func(r *ledger.Reflection) { r.Content = "duplicate reflection" })}, "raw-1"),
	}
	folded := ledger.FoldLedger(entries, "")
	if folded.ObservationsByID["aaaaaaaaaaaa"].Content != "first observation" || folded.ReflectionsByID["eeeeeeeeeeee"].Content != "first reflection" {
		t.Fatal("first valid record did not win")
	}
	if len(folded.Observations) != 1 || len(folded.Reflections) != 1 {
		t.Fatal("duplicates were folded")
	}
}

func TestFoldUnknownDropIDs(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.ObservationsDroppedEntry("om-drop-1", []string{"deadbeef0000"}, "raw-1"),
	}
	folded := ledger.FoldLedger(entries, "")
	if !folded.DroppedObservationIDs["deadbeef0000"] || len(folded.ActiveObservations) != 0 {
		t.Fatal("unknown tombstone not retained")
	}
}

func TestFoldIgnoresV2AndUnknown(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.OldV2ObservationEntry("v2-obs"),
		tf.Entry(map[string]any{"type": "custom", "id": "unknown", "parentId": nil, "timestamp": tf.DefaultTimestamp, "customType": "other.memory", "data": map[string]any{"any": true}}),
	}
	folded := ledger.FoldLedger(entries, "")
	if len(folded.Observations) != 0 || len(folded.Reflections) != 0 || len(folded.ActiveObservations) != 0 {
		t.Fatal("unknown entries folded")
	}
}

func TestFoldBranchPathOnly(t *testing.T) {
	mainBranch := []ledger.Entry{
		tf.BranchSummary("root", "root summary"),
		tf.TextCustomMessage("raw-main", "main"),
		tf.ObservationsRecordedEntry("main-ledger", []ledger.Observation{tf.Observation("aaaa00000000", withSources("raw-main"))}, "raw-main"),
	}
	forkBranch := []ledger.Entry{
		tf.BranchSummary("root", "root summary"),
		tf.TextCustomMessage("raw-fork", "fork"),
		tf.ObservationsRecordedEntry("fork-ledger", []ledger.Observation{tf.Observation("bbbb00000000", withSources("raw-fork"))}, "raw-fork"),
	}
	if !reflect.DeepEqual(tf.IDs(ledger.FoldLedger(mainBranch, "").Observations), []string{"aaaa00000000"}) || !reflect.DeepEqual(tf.IDs(ledger.FoldLedger(forkBranch, "").Observations), []string{"bbbb00000000"}) {
		t.Fatal("fold crossed branches")
	}
}
