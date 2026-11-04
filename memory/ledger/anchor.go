package ledger

// A record's anchor is where it sits on the current branch: the branch index of its first
// source entry. Memory v2's summary ends every line with that entry's id, bounds every omitted
// span by anchors, and the memory tool's from and through stages filter by the same anchors,
// so a span the summary prints lists exactly the records its query returns
// (docs/memory-v2.md, "Selection").

// ObservationAnchor is o's first source entry id and its index in positions (EntryIndexByID
// of the branch). ok is false when the record cites no source or its first source is off the
// branch; the id is then still returned, so a caller can label it.
func ObservationAnchor(o Observation, positions map[string]int) (id string, index int, ok bool) {
	if len(o.SourceEntryIDs) == 0 {
		return "", -1, false
	}
	id = o.SourceEntryIDs[0]
	index, ok = positions[id]
	if !ok {
		return id, -1, false
	}
	return id, index, true
}

// ReflectionAnchor is the earliest anchor (ObservationAnchor) among the observations r cites
// that byID holds (dropped ones included, as FoldedLedger.ObservationsByID keeps them). ok is
// false when none of them is anchored on the branch; id is then the first cited observation's
// first source, "" when none is known.
func ReflectionAnchor(r Reflection, byID map[string]Observation, positions map[string]int) (id string, index int, ok bool) {
	index = -1
	for _, cited := range r.SupportingObservationIDs {
		o, known := byID[cited]
		if !known {
			continue
		}
		sourceID, at, anchored := ObservationAnchor(o, positions)
		if id == "" && !ok {
			id = sourceID
		}
		if anchored && (!ok || at < index) {
			id, index, ok = sourceID, at, true
		}
	}
	return id, index, ok
}
