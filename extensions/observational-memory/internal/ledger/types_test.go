package ledger_test

import (
	"reflect"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

func obsMap(o ledger.Observation, changes map[string]any) map[string]any {
	m := tf.ToJSONValue(o).(map[string]any)
	for k, v := range changes {
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	return m
}

func refMap(r ledger.Reflection, changes map[string]any) map[string]any {
	m := tf.ToJSONValue(r).(map[string]any)
	for k, v := range changes {
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	return m
}

func TestTypeConstants(t *testing.T) {
	if ledger.OMObservationsRecorded != "om.observations.recorded" || ledger.OMReflectionsRecorded != "om.reflections.recorded" || ledger.OMObservationsDropped != "om.observations.dropped" || ledger.OMFolded != "om.folded" {
		t.Fatal("custom type constants changed")
	}
}

func TestObservationGuard(t *testing.T) {
	if !ledger.IsObservation(obsMap(tf.Observation("aaaaaaaaaaaa"), nil)) {
		t.Fatal("valid observation rejected")
	}
	if ledger.IsObservation(obsMap(tf.Observation("bbbbbbbbbbbb"), map[string]any{"sourceEntryIds": []any{}})) {
		t.Fatal("empty sourceEntryIds accepted")
	}
	if ledger.IsObservation(obsMap(tf.Observation("cccccccccccc"), map[string]any{"sourceEntryIds": nil})) {
		t.Fatal("missing sourceEntryIds accepted")
	}
	if ledger.IsObservation(obsMap(tf.Observation("dddddddddddd"), map[string]any{"tokenCount": nil})) {
		t.Fatal("missing tokenCount accepted")
	}
}

func TestReflectionGuard(t *testing.T) {
	if !ledger.IsReflection(refMap(tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"}), nil)) {
		t.Fatal("valid reflection rejected")
	}
	if ledger.IsReflection(refMap(tf.Reflection("ffffffffffff", nil), map[string]any{"supportingObservationIds": nil})) {
		t.Fatal("missing supportingObservationIds accepted")
	}
	if ledger.IsReflection(refMap(tf.Reflection("111111111111", nil), map[string]any{"tokenCount": nil})) {
		t.Fatal("missing tokenCount accepted")
	}
}

func TestEntryDataGuards(t *testing.T) {
	obsData := tf.ToJSONValue(map[string]any{"observations": []ledger.Observation{tf.Observation("aaaaaaaaaaaa")}, "coversUpToId": "raw-1"})
	refData := tf.ToJSONValue(map[string]any{"reflections": []ledger.Reflection{tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})}, "coversUpToId": "raw-2"})
	dropData := tf.ToJSONValue(map[string]any{"observationIds": []string{"aaaaaaaaaaaa"}, "coversUpToId": "ref-entry-1"})
	if !ledger.IsObservationsRecordedData(obsData) || !ledger.IsReflectionsRecordedData(refData) || !ledger.IsObservationsDroppedData(dropData) {
		t.Fatal("valid data rejected")
	}
	if ledger.IsObservationsRecordedData(tf.ToJSONValue(map[string]any{"observations": []any{}, "coversUpToId": "raw-1"})) {
		t.Fatal("empty observations accepted")
	}
	if ledger.IsReflectionsRecordedData(tf.ToJSONValue(map[string]any{"reflections": []any{}, "coversUpToId": "raw-1"})) {
		t.Fatal("empty reflections accepted")
	}
	if ledger.IsObservationsDroppedData(tf.ToJSONValue(map[string]any{"observationIds": []any{}, "coversUpToId": "raw-1"})) {
		t.Fatal("empty drops accepted")
	}
}

func TestBuilders(t *testing.T) {
	if ledger.BuildObservationsRecordedData(nil, "raw-1") != nil || ledger.BuildReflectionsRecordedData(nil, "raw-1") != nil || ledger.BuildObservationsDroppedData(nil, "raw-1") != nil {
		t.Fatal("empty builders returned data")
	}
	obs := tf.Observation("aaaaaaaaaaaa")
	got := ledger.BuildObservationsRecordedData([]ledger.Observation{obs}, "raw-1")
	if !reflect.DeepEqual(*got, ledger.ObservationsRecordedData{Observations: []ledger.Observation{obs}, CoversUpToID: "raw-1"}) {
		t.Fatalf("unexpected observations data %+v", got)
	}
	ref := tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})
	gotRef := ledger.BuildReflectionsRecordedData([]ledger.Reflection{ref}, "raw-1")
	if !reflect.DeepEqual(*gotRef, ledger.ReflectionsRecordedData{Reflections: []ledger.Reflection{ref}, CoversUpToID: "raw-1"}) {
		t.Fatalf("unexpected reflections data %+v", gotRef)
	}
	gotDrop := ledger.BuildObservationsDroppedData([]string{"aaaaaaaaaaaa"}, "ref-entry-1")
	if !reflect.DeepEqual(*gotDrop, ledger.ObservationsDroppedData{ObservationIDs: []string{"aaaaaaaaaaaa"}, CoversUpToID: "ref-entry-1"}) {
		t.Fatalf("unexpected drop data %+v", gotDrop)
	}
}

func TestRecognizesEntries(t *testing.T) {
	if !ledger.IsObservationsRecordedEntry(tf.ObservationsRecordedEntry("om-a", []ledger.Observation{tf.Observation("aaaaaaaaaaaa")}, "raw-1")) {
		t.Fatal("observations entry not recognized")
	}
	if !ledger.IsReflectionsRecordedEntry(tf.ReflectionsRecordedEntry("om-e", []ledger.Reflection{tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})}, "raw-1")) {
		t.Fatal("reflections entry not recognized")
	}
	if !ledger.IsObservationsDroppedEntry(tf.ObservationsDroppedEntry("om-drop-1", []string{"aaaaaaaaaaaa"}, "om-e")) {
		t.Fatal("drops entry not recognized")
	}
}

func TestMemoryDetailsGuard(t *testing.T) {
	details := tf.MemoryDetails(tf.MemoryDetailsArgs{FullFold: true, Observations: []ledger.Observation{tf.Observation("aaaaaaaaaaaa")}, Reflections: []ledger.Reflection{tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})}})
	if !ledger.IsMemoryDetails(details) {
		t.Fatal("valid details rejected")
	}
	if ledger.IsObservationsRecordedEntry(tf.OldV2ObservationEntry("v2-entry")) {
		t.Fatal("V2 entry accepted")
	}
	if ledger.IsMemoryDetails(tf.OldV2CompactionDetails()) {
		t.Fatal("V2 details accepted")
	}
}

func TestObservationStructuredFields(t *testing.T) {
	o := tf.Observation("aaaaaaaaaaaa")
	o.Kind, o.Subject, o.Scope, o.Supersedes = "correction", "gaming hours", "hard difficulty", []string{"bbbbbbbbbbbb"}
	got, ok := ledger.ObservationFrom(tf.ToJSONValue(o))
	if !ok || !reflect.DeepEqual(got, o) {
		t.Fatalf("round trip %+v %v", got, ok)
	}
	old := obsMap(tf.Observation("cccccccccccc"), nil)
	for _, key := range []string{"kind", "subject", "scope", "supersedes"} {
		if _, present := old[key]; present {
			t.Fatalf("empty %s not omitted", key)
		}
	}
	got, ok = ledger.ObservationFrom(old)
	if !ok || got.Kind != "" || got.Subject != "" || got.Scope != "" || got.Supersedes != nil {
		t.Fatalf("old record %+v %v", got, ok)
	}
	lenient := obsMap(tf.Observation("dddddddddddd"), map[string]any{"kind": "rumor", "subject": "  Online   Courses ", "scope": 7, "supersedes": []any{"bbbbbbbbbbbb", "not-an-id", "dddddddddddd"}})
	got, ok = ledger.ObservationFrom(lenient)
	if !ok || got.Kind != "" || got.Subject != "online courses" || got.Scope != "" || !reflect.DeepEqual(got.Supersedes, []string{"bbbbbbbbbbbb"}) {
		t.Fatalf("lenient decode %+v %v", got, ok)
	}
	// Scope folds whitespace as the observer does, and one bad supersedes
	// element drops only itself.
	spaced := obsMap(tf.Observation("eeeeeeeeeeee"), map[string]any{"scope": " hard  difficulty ", "supersedes": []any{"bbbbbbbbbbbb", "", 3, "bbbbbbbbbbbb", "cccccccccccc"}})
	got, ok = ledger.ObservationFrom(spaced)
	if !ok || got.Scope != "hard difficulty" || !reflect.DeepEqual(got.Supersedes, []string{"bbbbbbbbbbbb", "cccccccccccc"}) {
		t.Fatalf("scope/supersedes decode %+v %v", got, ok)
	}
	data, ok := ledger.ObservationsRecordedDataFrom(map[string]any{"observations": []any{tf.ToJSONValue(o)}, "coversUpToId": "raw-1"})
	if !ok || !reflect.DeepEqual(data.Observations[0], o) {
		t.Fatalf("recorded payload dropped structured fields: %+v", data)
	}
}
