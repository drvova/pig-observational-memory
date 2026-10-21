package ledger_test

import (
	"reflect"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

func withTokens(n int) func(*ledger.Observation) {
	return func(o *ledger.Observation) { o.TokenCount = n }
}

func TestFullProjectionFoldsDrops(t *testing.T) {
	obs1 := tf.Observation("aaaaaaaaaaaa")
	obs2 := tf.Observation("bbbbbbbbbbbb")
	ref1 := tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.ObservationsRecordedEntry("om-a", []ledger.Observation{obs1, obs2}, "raw-1"),
		tf.ReflectionsRecordedEntry("om-e", []ledger.Reflection{ref1}, "raw-1"),
		tf.ObservationsDroppedEntry("om-drop-1", []string{"aaaaaaaaaaaa"}, "om-e"),
	}
	projection := ledger.FullProjection(entries, "")
	if !reflect.DeepEqual(tf.IDs(projection.Observations), []string{"bbbbbbbbbbbb"}) || !reflect.DeepEqual(tf.RefIDs(projection.Reflections), []string{"eeeeeeeeeeee"}) {
		t.Fatalf("projection %v %v", tf.IDs(projection.Observations), tf.RefIDs(projection.Reflections))
	}
}

func TestVisibleProjection(t *testing.T) {
	noCompaction := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.ObservationsRecordedEntry("om-a", []ledger.Observation{tf.Observation("aaaaaaaaaaaa")}, "raw-1"),
	}
	if visible := ledger.VisibleProjection(noCompaction, ""); len(visible.Observations) != 0 || len(visible.Reflections) != 0 {
		t.Fatal("visible projection should be empty without a V3 compaction")
	}
	obs1 := tf.Observation("aaaaaaaaaaaa")
	ref1 := tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})
	obs2 := tf.Observation("bbbbbbbbbbbb")
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.CompactionEntry("cmp-1", tf.CompactionArgs{FirstKeptEntryID: "raw-1", Details: tf.MemoryDetails(tf.MemoryDetailsArgs{Observations: []ledger.Observation{obs1}})}),
		tf.TextCustomMessage("raw-2", "bbbb"),
		tf.CompactionEntry("cmp-2", tf.CompactionArgs{FirstKeptEntryID: "raw-2", Details: tf.MemoryDetails(tf.MemoryDetailsArgs{FullFold: true, Observations: []ledger.Observation{obs2}, Reflections: []ledger.Reflection{ref1}})}),
	}
	visible := ledger.VisibleProjection(entries, "")
	if !reflect.DeepEqual(visible.Observations, []ledger.Observation{obs2}) || !reflect.DeepEqual(visible.Reflections, []ledger.Reflection{ref1}) {
		t.Fatalf("visible %+v", visible)
	}
	v2 := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.CompactionEntry("cmp-v2", tf.CompactionArgs{FirstKeptEntryID: "raw-1", Details: tf.OldV2CompactionDetails()}),
	}
	if visible := ledger.VisibleProjection(v2, ""); len(visible.Observations) != 0 || len(visible.Reflections) != 0 {
		t.Fatal("V2 details should be ignored")
	}
}

func TestLatestFullFoldBoundary(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.CompactionEntry("cmp-1", tf.CompactionArgs{FirstKeptEntryID: "raw-1", Details: tf.MemoryDetails(tf.MemoryDetailsArgs{FullFold: true})}),
		tf.TextCustomMessage("raw-2", "bbbb"),
		tf.CompactionEntry("cmp-2", tf.CompactionArgs{FirstKeptEntryID: "raw-2", Details: tf.MemoryDetails(tf.MemoryDetailsArgs{FullFold: false})}),
		tf.TextCustomMessage("raw-3", "cccc"),
		tf.CompactionEntry("cmp-3", tf.CompactionArgs{FirstKeptEntryID: "raw-3", Details: tf.MemoryDetails(tf.MemoryDetailsArgs{FullFold: true})}),
	}
	if ledger.LatestFullFoldBoundaryID(entries) != "raw-3" {
		t.Fatal("latest full-fold boundary wrong")
	}
}

func TestFirstNormalCompaction(t *testing.T) {
	obs1 := tf.Observation("aaaaaaaaaaaa", withSources("raw-2"), withTokens(10))
	ref1 := tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.TextCustomMessage("raw-2", "bbbb"),
		tf.ObservationsRecordedEntry("om-a", []ledger.Observation{obs1}, "raw-2"),
		tf.ReflectionsRecordedEntry("om-e", []ledger.Reflection{ref1}, "raw-2"),
		tf.ObservationsDroppedEntry("om-drop-1", []string{"aaaaaaaaaaaa"}, "raw-2"),
	}
	result := ledger.BuildCompactionProjection(entries, "raw-2", ledger.CompactionProjectionConfig{ObservationsPoolMaxTokens: 100})
	if result.FullFold || !reflect.DeepEqual(tf.IDs(result.Observations), []string{"aaaaaaaaaaaa"}) || len(result.Reflections) != 0 {
		t.Fatalf("first normal compaction %+v", result)
	}
	if result.Details.Type != "om.folded" || result.Details.Version != 1 || result.Details.FullFold {
		t.Fatal("details wrong")
	}
}

func maintenanceEntries(obs1, obs2 ledger.Observation, ref1, ref2 ledger.Reflection) []ledger.Entry {
	return []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.ObservationsRecordedEntry("om-a", []ledger.Observation{obs1}, "raw-1"),
		tf.ReflectionsRecordedEntry("om-e", []ledger.Reflection{ref1}, "raw-1"),
		tf.CompactionEntry("cmp-full", tf.CompactionArgs{FirstKeptEntryID: "raw-1", Details: tf.MemoryDetails(tf.MemoryDetailsArgs{FullFold: true, Observations: []ledger.Observation{obs1}, Reflections: []ledger.Reflection{ref1}})}),
		tf.TextCustomMessage("raw-2", "bbbb"),
		tf.ObservationsRecordedEntry("om-b", []ledger.Observation{obs2}, "raw-2"),
		tf.ReflectionsRecordedEntry("om-f", []ledger.Reflection{ref2}, "raw-2"),
		tf.ObservationsDroppedEntry("om-drop-2", []string{"aaaaaaaaaaaa"}, "raw-2"),
	}
}

func TestNormalCompactionKeepsMaintenanceAtFullFoldBoundary(t *testing.T) {
	obs1 := tf.Observation("aaaaaaaaaaaa", withTokens(5))
	obs2 := tf.Observation("bbbbbbbbbbbb", withTokens(5))
	ref1 := tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})
	ref2 := tf.Reflection("ffffffffffff", []string{"bbbbbbbbbbbb"})
	result := ledger.BuildCompactionProjection(maintenanceEntries(obs1, obs2, ref1, ref2), "raw-2", ledger.CompactionProjectionConfig{ObservationsPoolMaxTokens: 100})
	if result.FullFold || !reflect.DeepEqual(tf.IDs(result.Observations), []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb"}) || !reflect.DeepEqual(tf.RefIDs(result.Reflections), []string{"eeeeeeeeeeee"}) {
		t.Fatalf("normal compaction %v %v", tf.IDs(result.Observations), tf.RefIDs(result.Reflections))
	}
}

func TestFullCompactionAppliesThroughBoundary(t *testing.T) {
	obs1 := tf.Observation("aaaaaaaaaaaa", withTokens(80))
	obs2 := tf.Observation("bbbbbbbbbbbb", withTokens(30))
	ref1 := tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})
	ref2 := tf.Reflection("ffffffffffff", []string{"bbbbbbbbbbbb"})
	result := ledger.BuildCompactionProjection(maintenanceEntries(obs1, obs2, ref1, ref2), "raw-2", ledger.CompactionProjectionConfig{ObservationsPoolMaxTokens: 100})
	if !result.FullFold || !reflect.DeepEqual(tf.IDs(result.Observations), []string{"bbbbbbbbbbbb"}) || !reflect.DeepEqual(tf.RefIDs(result.Reflections), []string{"eeeeeeeeeeee", "ffffffffffff"}) {
		t.Fatalf("full compaction %v %v", tf.IDs(result.Observations), tf.RefIDs(result.Reflections))
	}
}

func TestDanglingMarkersIgnored(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.ObservationsRecordedEntry("om-a", []ledger.Observation{tf.Observation("aaaaaaaaaaaa", withTokens(10))}, "missing"),
		tf.ReflectionsRecordedEntry("om-e", []ledger.Reflection{tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})}, "missing"),
		tf.ObservationsDroppedEntry("om-drop-1", []string{"aaaaaaaaaaaa"}, "missing"),
	}
	projection := ledger.FullProjection(entries, "raw-1")
	if len(projection.Observations) != 0 || len(projection.Reflections) != 0 {
		t.Fatal("dangling markers were projected")
	}
}

func TestProjectionFirstCoveredWins(t *testing.T) {
	first := tf.Observation("aaaaaaaaaaaa", withContent("first observation"))
	firstRef := tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"}, func(r *ledger.Reflection) { r.Content = "first reflection" })
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.ObservationsRecordedEntry("om-obs-1", []ledger.Observation{first}, "raw-1"),
		tf.ObservationsRecordedEntry("om-obs-2", []ledger.Observation{tf.Observation("aaaaaaaaaaaa", withContent("second observation"))}, "raw-1"),
		tf.ReflectionsRecordedEntry("om-ref-1", []ledger.Reflection{firstRef}, "raw-1"),
		tf.ReflectionsRecordedEntry("om-ref-2", []ledger.Reflection{tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"}, func(r *ledger.Reflection) { r.Content = "second reflection" })}, "raw-1"),
	}
	projection := ledger.FullProjection(entries, "raw-1")
	if !reflect.DeepEqual(projection.Observations, []ledger.Observation{first}) || !reflect.DeepEqual(projection.Reflections, []ledger.Reflection{firstRef}) {
		t.Fatal("first covered record did not win")
	}
}

func TestFullFoldPressureInclusive(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.ObservationsRecordedEntry("om-a", []ledger.Observation{tf.Observation("aaaaaaaaaaaa", withTokens(50))}, "raw-1"),
	}
	if !ledger.BuildCompactionProjection(entries, "raw-1", ledger.CompactionProjectionConfig{ObservationsPoolMaxTokens: 50}).FullFold {
		t.Fatal("pool at max should full-fold")
	}
}

func TestDiffProjection(t *testing.T) {
	visible := ledger.Projection{Observations: []ledger.Observation{tf.Observation("aaaaaaaaaaaa")}, Reflections: []ledger.Reflection{}}
	full := ledger.Projection{Observations: []ledger.Observation{tf.Observation("aaaaaaaaaaaa"), tf.Observation("bbbbbbbbbbbb")}, Reflections: []ledger.Reflection{tf.Reflection("eeeeeeeeeeee", []string{"bbbbbbbbbbbb"})}}
	diff := ledger.DiffProjection(visible, full)
	if !reflect.DeepEqual(tf.IDs(diff.ObservationsOnlyInFull), []string{"bbbbbbbbbbbb"}) || !reflect.DeepEqual(tf.RefIDs(diff.ReflectionsOnlyInFull), []string{"eeeeeeeeeeee"}) {
		t.Fatal("diff wrong")
	}
}

func movedCutBranch(details any) []ledger.Entry {
	obsA := tf.Observation("aaaaaaaaaaaa", withSources("raw-3"), withTokens(50))
	obsB := tf.Observation("bbbbbbbbbbbb", withSources("raw-3"), withTokens(50))
	obsC := tf.Observation("cccccccccccc", withSources("raw-5"), withTokens(5))
	refR := tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})
	return []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.TextCustomMessage("raw-2", "bbbb"),
		tf.TextCustomMessage("raw-3", "cccc"),
		tf.TextCustomMessage("raw-4", "dddd"),
		tf.ObservationsRecordedEntry("om-obs-1", []ledger.Observation{obsA, obsB}, "raw-3"),
		tf.ReflectionsRecordedEntry("om-ref-1", []ledger.Reflection{refR}, "raw-3"),
		tf.ObservationsDroppedEntry("om-drop-1", []string{"aaaaaaaaaaaa"}, "raw-3"),
		tf.CompactionEntry("cmp-1", tf.CompactionArgs{FirstKeptEntryID: "raw-2", Details: details}),
		tf.TextCustomMessage("raw-5", "eeee"),
		tf.ObservationsRecordedEntry("om-obs-2", []ledger.Observation{obsC}, "raw-5"),
	}
}

func TestMovedCutFoldBoundary(t *testing.T) {
	projection := ledger.BuildCompactionProjection(movedCutBranch(nil)[:7], "raw-3", ledger.CompactionProjectionConfig{ObservationsPoolMaxTokens: 1})
	if !projection.FullFold || projection.Details.FoldThroughEntryID != "raw-3" {
		t.Fatal("fold boundary not recorded")
	}
	obsB := tf.Observation("bbbbbbbbbbbb", withSources("raw-3"), withTokens(50))
	refR := tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})
	details := tf.MemoryDetails(tf.MemoryDetailsArgs{FullFold: true, Observations: []ledger.Observation{obsB}, Reflections: []ledger.Reflection{refR}, FoldThroughEntryID: "raw-3"})
	entries := movedCutBranch(details)
	if ledger.LatestFullFoldBoundaryID(entries) != "raw-3" {
		t.Fatal("recorded fold boundary not used")
	}
	next := ledger.BuildCompactionProjection(entries, "raw-5", ledger.CompactionProjectionConfig{ObservationsPoolMaxTokens: 10_000})
	if next.FullFold || !reflect.DeepEqual(tf.IDs(next.Observations), []string{"bbbbbbbbbbbb", "cccccccccccc"}) || !reflect.DeepEqual(tf.RefIDs(next.Reflections), []string{"eeeeeeeeeeee"}) {
		t.Fatalf("later maintenance %v %v", tf.IDs(next.Observations), tf.RefIDs(next.Reflections))
	}
	older := movedCutBranch(tf.MemoryDetails(tf.MemoryDetailsArgs{FullFold: true, Observations: []ledger.Observation{obsB}, Reflections: []ledger.Reflection{refR}}))
	if ledger.LatestFullFoldBoundaryID(older) != "raw-2" {
		t.Fatal("older compactions should fall back to firstKeptEntryId")
	}
}

// After a native (delegated) compaction, its prose summary is what Pi shows:
// no V3 record is visible, however much an earlier V3 compaction held.
func TestVisibleProjectionFollowsNewestCompaction(t *testing.T) {
	obs := tf.Observation("aaaaaaaaaaaa")
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaa"),
		tf.CompactionEntry("cmp-v3", tf.CompactionArgs{FirstKeptEntryID: "raw-1", Details: tf.MemoryDetails(tf.MemoryDetailsArgs{Observations: []ledger.Observation{obs}})}),
		tf.TextCustomMessage("raw-2", "bbbb"),
		tf.CompactionEntry("cmp-native", tf.CompactionArgs{FirstKeptEntryID: "raw-2"}),
	}
	if visible := ledger.VisibleProjection(entries, ""); len(visible.Observations) != 0 || len(visible.Reflections) != 0 {
		t.Fatalf("visible %+v", visible)
	}
	if visible := ledger.VisibleProjection(entries[:3], ""); len(visible.Observations) != 1 {
		t.Fatalf("visible before the native compaction %+v", visible)
	}
}
