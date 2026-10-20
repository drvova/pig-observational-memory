package ledger_test

import (
	"reflect"
	"testing"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/testfix"
)

const (
	obs1ID     = "aaaaaaaaaaaa"
	obs2ID     = "bbbbbbbbbbbb"
	ref1ID     = "cccccccccccc"
	sameID     = "dddddddddddd"
	missingObs = "eeeeeeeeeeee"
)

func sourceEntry(id string) ledger.Entry {
	return tf.Entry(map[string]any{"type": "custom_message", "id": id, "timestamp": "2026-05-19T00:00:00.000Z", "content": "source " + id})
}

func nonSourceEntry(id string) ledger.Entry {
	return tf.Entry(map[string]any{"type": "custom", "id": id, "customType": "not-source", "data": map[string]any{}})
}

func recallObs(id string, sources ...string) ledger.Observation {
	return ledger.Observation{ID: id, Content: "Observation " + id, Timestamp: "2026-05-19 00:00", Relevance: "high", TokenCount: 4, SourceEntryIDs: sources}
}

func recallRef(id string, supporting ...string) ledger.Reflection {
	return ledger.Reflection{ID: id, Content: "Reflection " + id, TokenCount: 5, SupportingObservationIDs: supporting}
}

func obsEntry(id string, observations ...ledger.Observation) ledger.Entry {
	return tf.ObservationsRecordedEntry(id, observations, "src-1")
}

func refEntry(id string, reflections ...ledger.Reflection) ledger.Entry {
	return tf.ReflectionsRecordedEntry(id, reflections, "src-1")
}

func entryIDs(entries []ledger.Entry) []string {
	ids := []string{}
	for _, e := range entries {
		ids = append(ids, e.ID)
	}
	return ids
}

func TestRecallActiveObservation(t *testing.T) {
	entries := []ledger.Entry{sourceEntry("src-1"), obsEntry("obs-entry-1", recallObs(obs1ID, "src-1"))}
	result := ledger.RecallMemorySources(entries, obs1ID)
	if result.Status != "found" || result.Kind != "observation" || len(result.Observations) != 1 || result.Observations[0].Observation.ID != obs1ID || result.Observations[0].Status != "active" || !reflect.DeepEqual(entryIDs(result.Observations[0].SourceEntries), []string{"src-1"}) || result.Partial {
		t.Fatalf("recall %+v", result)
	}
}

func TestRecallDroppedObservation(t *testing.T) {
	entries := []ledger.Entry{sourceEntry("src-1"), obsEntry("obs-entry-1", recallObs(obs1ID, "src-1")), tf.ObservationsDroppedEntry("drop-entry-1", []string{obs1ID}, "src-1")}
	result := ledger.RecallMemorySources(entries, obs1ID)
	if result.Status != "found" || len(result.Observations) != 1 || result.Observations[0].Status != "dropped" || !reflect.DeepEqual(entryIDs(result.Observations[0].SourceEntries), []string{"src-1"}) {
		t.Fatalf("dropped recall %+v", result)
	}
}

func TestRecallReflection(t *testing.T) {
	entries := []ledger.Entry{
		sourceEntry("src-1"), sourceEntry("src-2"),
		obsEntry("obs-entry-1", recallObs(obs1ID, "src-1"), recallObs(obs2ID, "src-2")),
		refEntry("ref-entry-1", recallRef(ref1ID, obs1ID, obs2ID)),
	}
	result := ledger.RecallMemorySources(entries, ref1ID)
	if result.Status != "found" || result.Kind != "reflection" || len(result.Reflections) != 1 || result.Reflections[0].Reflection.ID != ref1ID {
		t.Fatalf("reflection recall %+v", result)
	}
	gotObs := []string{}
	for _, m := range result.Observations {
		gotObs = append(gotObs, m.Observation.ID)
	}
	if !reflect.DeepEqual(gotObs, []string{obs1ID, obs2ID}) || !reflect.DeepEqual(entryIDs(result.SourceEntries), []string{"src-1", "src-2"}) || result.Partial {
		t.Fatalf("reflection sources %v %v", gotObs, entryIDs(result.SourceEntries))
	}
}

func TestRecallReflectionDroppedSupport(t *testing.T) {
	entries := []ledger.Entry{sourceEntry("src-1"), obsEntry("obs-entry-1", recallObs(obs1ID, "src-1")), refEntry("ref-entry-1", recallRef(ref1ID, obs1ID)), tf.ObservationsDroppedEntry("drop-entry-1", []string{obs1ID}, "src-1")}
	result := ledger.RecallMemorySources(entries, ref1ID)
	if len(result.Observations) != 1 || result.Observations[0].Status != "dropped" {
		t.Fatal("supporting observation should be dropped")
	}
}

func TestRecallPartialSources(t *testing.T) {
	entries := []ledger.Entry{nonSourceEntry("custom-1"), obsEntry("obs-entry-1", recallObs(obs1ID, "missing-src", "custom-1"))}
	result := ledger.RecallMemorySources(entries, obs1ID)
	if !result.Partial || !reflect.DeepEqual(result.MissingSourceEntryIDs, []string{"missing-src"}) || !reflect.DeepEqual(result.NonSourceEntryIDs, []string{"custom-1"}) || len(result.Observations[0].SourceEntries) != 0 {
		t.Fatalf("partial recall %+v", result)
	}
}

func TestRecallMissingSupporting(t *testing.T) {
	result := ledger.RecallMemorySources([]ledger.Entry{refEntry("ref-entry-1", recallRef(ref1ID, missingObs))}, ref1ID)
	if !result.Partial || !reflect.DeepEqual(result.MissingSupportingObservationIDs, []string{missingObs}) || len(result.Observations) != 0 {
		t.Fatalf("missing supporting %+v", result)
	}
}

func TestRecallNotFound(t *testing.T) {
	result := ledger.RecallMemorySources([]ledger.Entry{sourceEntry("src-1"), obsEntry("obs-entry-1", recallObs(obs1ID, "src-1"))}, "ffffffffffff")
	if result.Status != "not_found" || result.MemoryID != "ffffffffffff" || result.Collision || result.Partial {
		t.Fatalf("not found %+v", result)
	}
}

func TestRecallIgnoresV2(t *testing.T) {
	entries := []ledger.Entry{
		tf.Entry(map[string]any{"type": "custom", "id": "old-obs-entry", "customType": "om.observation", "data": map[string]any{"records": []ledger.Observation{recallObs(obs1ID, "src-1")}, "coversFromId": "src-1", "coversUpToId": "src-1", "tokenCount": 10}}),
		tf.Entry(map[string]any{"type": "compaction", "id": "old-compaction", "firstKeptEntryId": "src-1", "details": map[string]any{"type": "observational-memory", "version": 4, "observations": []ledger.Observation{recallObs(obs2ID, "src-1")}, "reflections": []ledger.Reflection{recallRef(ref1ID, obs2ID)}}}),
	}
	for _, id := range []string{obs1ID, obs2ID, ref1ID} {
		if ledger.RecallMemorySources(entries, id).Status != "not_found" {
			t.Fatalf("V2 record %s was recalled", id)
		}
	}
}

func TestRecallCollision(t *testing.T) {
	entries := []ledger.Entry{sourceEntry("src-1"), obsEntry("obs-entry-1", recallObs(sameID, "src-1")), refEntry("ref-entry-1", recallRef(sameID, sameID))}
	result := ledger.RecallMemorySources(entries, sameID)
	if result.Kind != "mixed" || !result.Collision || len(result.Observations) != 1 || len(result.Reflections) != 1 {
		t.Fatalf("collision %+v", result)
	}
}
