package ledger

import "testing"

func TestAnchors(t *testing.T) {
	positions := map[string]int{"e1": 0, "e2": 1, "e3": 2}
	if id, at, ok := ObservationAnchor(Observation{SourceEntryIDs: []string{"e2", "e1"}}, positions); id != "e2" || at != 1 || !ok {
		t.Fatalf("anchor %q %d %v", id, at, ok)
	}
	if id, _, ok := ObservationAnchor(Observation{SourceEntryIDs: []string{"gone", "e1"}}, positions); id != "gone" || ok {
		t.Fatalf("an off-branch first source anchored: %q %v", id, ok)
	}
	if _, _, ok := ObservationAnchor(Observation{}, positions); ok {
		t.Fatal("no source anchored")
	}
	byID := map[string]Observation{
		"a": {SourceEntryIDs: []string{"e3"}},
		"b": {SourceEntryIDs: []string{"e2"}},
		"c": {SourceEntryIDs: []string{"gone"}},
	}
	if id, at, ok := ReflectionAnchor(Reflection{SupportingObservationIDs: []string{"c", "a", "b", "unknown"}}, byID, positions); id != "e2" || at != 1 || !ok {
		t.Fatalf("reflection anchor %q %d %v", id, at, ok)
	}
	if id, _, ok := ReflectionAnchor(Reflection{SupportingObservationIDs: []string{"c"}}, byID, positions); id != "gone" || ok {
		t.Fatalf("off-branch reflection anchor %q %v", id, ok)
	}
}
