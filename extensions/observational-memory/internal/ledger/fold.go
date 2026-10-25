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
	// Aliases are the subject aliases recorded through the fold boundary, in ledger order;
	// whether each applies is decided when it is read (CheckSubjectAliases). nil when none.
	Aliases []SubjectAlias
	// CitedBy are the reverse links of the reflections' citations, computed by the fold and
	// never stored: for each observation id a reflection cites, the ids of the reflections that
	// cite it, in ledger order, so retiring a record shows what loses support. A level-1 merge
	// keeps its sources' entry ids, not their record ids, so it cites no record.
	CitedBy map[string][]string
	// ReplacedBy maps each record level 1 replaced to the record that replaced it (memory v2;
	// replace.go). FoldLedger reads every link through it to the record standing for its target
	// (Standing); FoldLedgerUnresolved leaves the links as written.
	ReplacedBy map[string]string
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
// the dropped id is unknown at fold time; subject aliases are kept in ledger
// order.
//
// Every link (supersedes, possibly_updates, conflicts, reflection citations, and so CitedBy) is
// read through level 1's replacements to the record standing for its target (Standing).
func FoldLedger(entries []Entry, upToEntryID string) FoldedLedger {
	folded := FoldLedgerUnresolved(entries, upToEntryID)
	ResolveFolded(&folded, nil)
	return folded
}

// FoldLedgerUnresolved is FoldLedger with every link as it was written and ReplacedBy read, for
// the point-in-time view, which follows only the replacements that held at its bound
// (ResolveFolded).
func FoldLedgerUnresolved(entries []Entry, upToEntryID string) FoldedLedger {
	folded := FoldedLedger{
		DroppedObservationIDs: map[string]bool{},
		ObservationsByID:      map[string]Observation{},
		ReflectionsByID:       map[string]Reflection{},
	}
	end := foldEndIndex(entries, upToEntryID)
	replacements := newReplacementTracker(func(id string) (Observation, bool) {
		o, ok := folded.ObservationsByID[id]
		return o, ok
	}, func(id string) bool {
		return hiddenBy(id, folded.Observations, folded.DroppedObservationIDs, folded.Reflections)
	})
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
			replacements.recorded(data)
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
			replacements.dropped(data)
		case OMSubjectsAliased:
			if data, ok := SubjectsAliasedDataFrom(entry.Data); ok {
				folded.Aliases = append(folded.Aliases, data.Aliases...)
			}
		}
	}
	folded.ReplacedBy = replacements.by
	if folded.Observations == nil {
		folded.Observations = []Observation{}
	}
	if folded.Reflections == nil {
		folded.Reflections = []Reflection{}
	}
	folded.derive()
	return folded
}

// ResolveFolded reads every link of folded (its records', its reflections' citations, and so
// CitedBy) through folded.ReplacedBy to the record standing for its target (Standing); follow,
// when set, limits the chains to the replacements it accepts. Nothing changes without
// replacements, as under v1.
func ResolveFolded(folded *FoldedLedger, follow func(id string) bool) {
	if len(folded.ReplacedBy) == 0 {
		return
	}
	observations := make([]Observation, len(folded.Observations))
	for i, o := range folded.Observations {
		observations[i] = ResolveObservationLinks(o, folded.ReplacedBy, follow)
		folded.ObservationsByID[o.ID] = observations[i]
	}
	folded.Observations = observations
	reflections := make([]Reflection, len(folded.Reflections))
	for i, r := range folded.Reflections {
		reflections[i] = ResolveReflectionCitations(r, folded.ReplacedBy, follow)
		folded.ReflectionsByID[r.ID] = reflections[i]
	}
	folded.Reflections = reflections
	folded.derive()
}

// derive computes the active records and CitedBy from the records and reflections.
func (folded *FoldedLedger) derive() {
	folded.ActiveObservations = []Observation{}
	for _, observation := range folded.Observations {
		if !folded.DroppedObservationIDs[observation.ID] {
			folded.ActiveObservations = append(folded.ActiveObservations, observation)
		}
	}
	folded.CitedBy = map[string][]string{}
	for _, reflection := range folded.Reflections {
		for _, id := range reflection.SupportingObservationIDs {
			if !slicesContains(folded.CitedBy[id], reflection.ID) {
				folded.CitedBy[id] = append(folded.CitedBy[id], reflection.ID)
			}
		}
	}
}

// ConsolidatedObservationIDs are the ids of the observations first recorded (first valid record
// wins, as FoldLedger reads them) in a batch memory v2's level 1 wrote
// (ObservationsRecordedData.Consolidated).
func ConsolidatedObservationIDs(entries []Entry) map[string]bool {
	seen := map[string]bool{}
	out := map[string]bool{}
	for _, entry := range entries {
		data, ok := ObservationsRecordedEntry(entry)
		if !ok {
			continue
		}
		for _, o := range data.Observations {
			if seen[o.ID] {
				continue
			}
			seen[o.ID] = true
			if data.Consolidated {
				out[o.ID] = true
			}
		}
	}
	return out
}
