package ledger

// FoldedLedger is the memory state folded from the branch root.
type FoldedLedger struct {
	// Observations are the first-valid records through the fold boundary, dropped ones included.
	Observations []Observation
	// ActiveObservations are the observations no folded drop entry tombstoned.
	ActiveObservations []Observation
	// DroppedObservationIDs are the tombstoned ids, known or not.
	DroppedObservationIDs map[string]bool
	// Reflections are the first-valid records through the fold boundary.
	Reflections      []Reflection
	ObservationsByID map[string]Observation
	ReflectionsByID  map[string]Reflection
}

func foldEndIndex(entries []Entry, upToEntryID string) int {
	if upToEntryID == "" {
		return len(entries) - 1
	}
	for i, entry := range entries {
		if entry.ID == upToEntryID {
			return i
		}
	}
	return len(entries) - 1
}

// FoldLedger folds valid V3 memory entries from the branch root through
// upToEntryID (inclusive; "" means the tip). Unknown custom entries, old V2
// entries, invalid data and compaction details are ignored. Observations and
// reflections use first-valid-record-wins; drops are tombstones kept even when
// the dropped id is unknown at fold time.
func FoldLedger(entries []Entry, upToEntryID string) FoldedLedger {
	folded := FoldedLedger{
		DroppedObservationIDs: map[string]bool{},
		ObservationsByID:      map[string]Observation{},
		ReflectionsByID:       map[string]Reflection{},
	}
	end := foldEndIndex(entries, upToEntryID)
	for i := 0; i <= end && i < len(entries); i++ {
		entry := entries[i]
		if entry.Type != "custom" {
			continue
		}
		switch entry.CustomType {
		case OMObservationsRecorded:
			data, ok := ObservationsRecordedDataFrom(entry.Data)
			if !ok {
				continue
			}
			for _, observation := range data.Observations {
				if _, seen := folded.ObservationsByID[observation.ID]; !seen {
					folded.ObservationsByID[observation.ID] = observation
					folded.Observations = append(folded.Observations, observation)
				}
			}
		case OMReflectionsRecorded:
			data, ok := ReflectionsRecordedDataFrom(entry.Data)
			if !ok {
				continue
			}
			for _, reflection := range data.Reflections {
				if _, seen := folded.ReflectionsByID[reflection.ID]; !seen {
					folded.ReflectionsByID[reflection.ID] = reflection
					folded.Reflections = append(folded.Reflections, reflection)
				}
			}
		case OMObservationsDropped:
			data, ok := ObservationsDroppedDataFrom(entry.Data)
			if !ok {
				continue
			}
			for _, id := range data.ObservationIDs {
				folded.DroppedObservationIDs[id] = true
			}
		}
	}
	folded.ActiveObservations = []Observation{}
	for _, observation := range folded.Observations {
		if !folded.DroppedObservationIDs[observation.ID] {
			folded.ActiveObservations = append(folded.ActiveObservations, observation)
		}
	}
	if folded.Observations == nil {
		folded.Observations = []Observation{}
	}
	if folded.Reflections == nil {
		folded.Reflections = []Reflection{}
	}
	return folded
}
