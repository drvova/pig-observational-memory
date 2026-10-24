package recall_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/recall"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

func expectContains(t *testing.T, text string, fragments ...string) {
	for _, fragment := range fragments {
		if !strings.Contains(text, fragment) {
			t.Fatalf("text lacks %q:\n%s", fragment, text)
		}
	}
}

func teaObservation() ledger.Observation {
	return testfix.Observation("aaaaaaaaaaaa", func(o *ledger.Observation) {
		o.Content = "User likes tea."
		o.SourceEntryIDs = []string{"raw-1"}
	})
}

func TestCallRendering(t *testing.T) {
	if recall.FormatCallForTui("aaaaaaaaaaaa") != "recall aaaaaaaaaaaa" {
		t.Fatal("call rendering changed")
	}
	if recall.FormatCallForTui("") != "recall ..." {
		t.Fatalf("call rendering %q", recall.FormatCallForTui(""))
	}
}

func TestRendersActiveObservationSourceEvidence(t *testing.T) {
	entries := []ledger.Entry{
		testfix.RawMessage("raw-1", "I like tea."),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{teaObservation()}, "raw-1"),
	}

	text, details := recall.Execute(entries, "aaaaaaaaaaaa")

	if details.Status != recall.StatusOK || details.Matches[0].Observation.Status != "active" {
		t.Fatalf("details %+v", details)
	}
	expectContains(t, text, "I like tea.")
	expectContains(t, recall.FormatRenderedResultForTui(&details, text, false), "✓ observation")
}

func TestRendersDroppedObservationsAsRecallable(t *testing.T) {
	entries := []ledger.Entry{
		testfix.RawMessage("raw-1", "I like tea."),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{teaObservation()}, "raw-1"),
		testfix.ObservationsDroppedEntry("om-drop", []string{"aaaaaaaaaaaa"}, "om-obs"),
	}

	text, details := recall.Execute(entries, "aaaaaaaaaaaa")

	if details.Matches[0].Observation.Status != "dropped" {
		t.Fatalf("details %+v", details)
	}
	expectContains(t, text, "dropped from active memory but remains recallable")
	expectContains(t, recall.FormatRenderedResultForTui(&details, text, false), "[dropped]")
}

func TestRendersReflectionRecallWithSupportingObservationsAndSources(t *testing.T) {
	ref := testfix.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"}, func(r *ledger.Reflection) { r.Content = "User likes tea." })
	entries := []ledger.Entry{
		testfix.RawMessage("raw-1", "I like tea."),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{teaObservation()}, "raw-1"),
		testfix.ReflectionsRecordedEntry("om-ref", []ledger.Reflection{ref}, "om-obs"),
	}

	text, details := recall.Execute(entries, "eeeeeeeeeeee")

	if details.Status != recall.StatusOK || len(details.Reflections) != 1 || len(details.Observations) != 1 {
		t.Fatalf("details %+v", details)
	}
	expectContains(t, text, "Reflections:", "[eeeeeeeeeeee] User likes tea.", "Observations:", "Sources:", "I like tea.")
}

func TestReportsMissingSourcesAsPartial(t *testing.T) {
	obs := testfix.Observation("aaaaaaaaaaaa", func(o *ledger.Observation) { o.SourceEntryIDs = []string{"missing-raw"} })
	entries := []ledger.Entry{testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obs}, "om-obs")}

	text, details := recall.Execute(entries, "aaaaaaaaaaaa")

	if details.Status != recall.StatusPartial || !reflect.DeepEqual(details.MissingSourceEntryIDs, []string{"missing-raw"}) {
		t.Fatalf("details %+v", details)
	}
	expectContains(t, text, "missing: missing-raw")
}

func TestReportsInvalidIDs(t *testing.T) {
	text, details := recall.Execute(nil, "not-valid")

	if details.Status != recall.StatusInvalidID {
		t.Fatalf("details %+v", details)
	}
	expectContains(t, text, "Memory id must be 12 lowercase hex characters")
	if recall.FormatHeaderForTui(details) != "× failure" {
		t.Fatalf("header %q", recall.FormatHeaderForTui(details))
	}
}

func TestReportsNotFoundAndIgnoresV2Memory(t *testing.T) {
	text, details := recall.Execute([]ledger.Entry{testfix.OldV2ObservationEntry("v2-obs")}, "aaaaaaaaaaaa")

	if details.Status != recall.StatusNotFound {
		t.Fatalf("details %+v", details)
	}
	expectContains(t, text, "No observation or reflection with id aaaaaaaaaaaa was found")
}

// Sources that resolved are shown even when a sibling source is missing.
func TestObservationRecallShowsResolvedSourcesBesideMissingOnes(t *testing.T) {
	obs := testfix.Observation("aaaaaaaaaaaa", func(o *ledger.Observation) { o.SourceEntryIDs = []string{"raw-1", "missing-raw"} })
	entries := []ledger.Entry{
		testfix.RawMessage("raw-1", "I like tea."),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obs}, "raw-1"),
	}

	text, details := recall.Execute(entries, "aaaaaaaaaaaa")

	if details.Status != recall.StatusPartial {
		t.Fatalf("details %+v", details)
	}
	expectContains(t, text, "missing: missing-raw", "I like tea.")
}

// Details keep empty lists as [] in their JSON, as the TypeScript tool writes them.
func TestRecallDetailsKeepEmptyLists(t *testing.T) {
	entries := []ledger.Entry{
		testfix.RawMessage("raw-1", "I like tea."),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{teaObservation()}, "raw-1"),
	}
	_, details := recall.Execute(entries, "aaaaaaaaaaaa")
	encoded, err := json.Marshal(details)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`"missingSourceEntryIds":[]`, `"nonSourceEntryIds":[]`} {
		if !strings.Contains(string(encoded), fragment) {
			t.Fatalf("details JSON lacks %s: %s", fragment, encoded)
		}
	}
	if strings.Contains(string(encoded), "null") {
		t.Fatalf("details JSON has null: %s", encoded)
	}
}
