package ledger

// RecalledObservation is an observation located in the ledger with its sources.
type RecalledObservation struct {
	Observation            Observation
	ObservationEntryID     string
	ObservationRecordIndex int
	// Status is "active" or "dropped".
	Status                string
	SourceEntryIDs        []string
	SourceEntries         []Entry
	MissingSourceEntryIDs []string
	NonSourceEntryIDs     []string
}

// RecalledReflection is a reflection located in the ledger.
type RecalledReflection struct {
	Reflection            Reflection
	ReflectionEntryID     string
	ReflectionRecordIndex int
}

// RecallResult is the evidence behind a memory id. Status is "not_found" or
// "found"; Kind is "observation", "reflection" or "mixed" when found.
type RecallResult struct {
	Status                          string
	MemoryID                        string
	Kind                            string
	Reflections                     []RecalledReflection
	Observations                    []RecalledObservation
	SourceEntries                   []Entry
	MissingSourceEntryIDs           []string
	NonSourceEntryIDs               []string
	MissingSupportingObservationIDs []string
	Collision                       bool
	Partial                         bool
}

type indexedObservation struct {
	observation Observation
	entryID     string
	entryIndex  int
	recordIndex int
}

type indexedReflection struct {
	reflection  Reflection
	entryID     string
	entryIndex  int
	recordIndex int
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func uniqueEntriesByID(entries []Entry) []Entry {
	seen := map[string]bool{}
	out := []Entry{}
	for _, entry := range entries {
		if seen[entry.ID] {
			continue
		}
		seen[entry.ID] = true
		out = append(out, entry)
	}
	return out
}

func indexLedger(entries []Entry) ([]indexedObservation, []indexedReflection, map[string]bool) {
	observations := []indexedObservation{}
	reflections := []indexedReflection{}
	dropped := map[string]bool{}
	for entryIndex, entry := range entries {
		if data, ok := ObservationsRecordedEntry(entry); ok {
			for recordIndex, observation := range data.Observations {
				observations = append(observations, indexedObservation{observation, entry.ID, entryIndex, recordIndex})
			}
			continue
		}
		if data, ok := ReflectionsRecordedEntry(entry); ok {
			for recordIndex, reflection := range data.Reflections {
				reflections = append(reflections, indexedReflection{reflection, entry.ID, entryIndex, recordIndex})
			}
			continue
		}
		if data, ok := ObservationsDroppedEntry(entry); ok {
			for _, id := range data.ObservationIDs {
				dropped[id] = true
			}
		}
	}
	return observations, reflections, dropped
}

func resolveObservationSources(entries []Entry, indexed indexedObservation) RecalledObservation {
	sourceEntryIDs := uniqueStrings(indexed.observation.SourceEntryIDs)
	byID := map[string]Entry{}
	for _, entry := range entries {
		if _, seen := byID[entry.ID]; !seen {
			byID[entry.ID] = entry
		} else {
			byID[entry.ID] = entry
		}
	}
	recalled := RecalledObservation{
		Observation:            indexed.observation,
		ObservationEntryID:     indexed.entryID,
		ObservationRecordIndex: indexed.recordIndex,
		Status:                 "active",
		SourceEntryIDs:         sourceEntryIDs,
		SourceEntries:          []Entry{},
		MissingSourceEntryIDs:  []string{},
		NonSourceEntryIDs:      []string{},
	}
	for _, id := range sourceEntryIDs {
		entry, ok := byID[id]
		if !ok {
			recalled.MissingSourceEntryIDs = append(recalled.MissingSourceEntryIDs, id)
			continue
		}
		if !IsSourceEntry(entry) {
			recalled.NonSourceEntryIDs = append(recalled.NonSourceEntryIDs, id)
			continue
		}
		recalled.SourceEntries = append(recalled.SourceEntries, entry)
	}
	return recalled
}

func notFound(memoryID string) RecallResult {
	return RecallResult{
		Status:                          "not_found",
		MemoryID:                        memoryID,
		Reflections:                     []RecalledReflection{},
		Observations:                    []RecalledObservation{},
		SourceEntries:                   []Entry{},
		MissingSourceEntryIDs:           []string{},
		NonSourceEntryIDs:               []string{},
		MissingSupportingObservationIDs: []string{},
	}
}

// RecallMemorySources locates a memory id on the branch and resolves the source
// entries behind it (directly for an observation, through supporting
// observations for a reflection).
func RecallMemorySources(entries []Entry, memoryID string) RecallResult {
	indexedObservations, indexedReflections, dropped := indexLedger(entries)
	// Links and citations are read through level 1's replacements, as the fold reads them, so
	// a recalled record names the records standing for its targets (replace.go).
	if replacedBy := FoldLedgerUnresolved(entries, "").ReplacedBy; len(replacedBy) > 0 {
		for i := range indexedObservations {
			indexedObservations[i].observation = ResolveObservationLinks(indexedObservations[i].observation, replacedBy, nil)
		}
		for i := range indexedReflections {
			indexedReflections[i].reflection = ResolveReflectionCitations(indexedReflections[i].reflection, replacedBy, nil)
		}
	}
	directMatches := []indexedObservation{}
	for _, indexed := range indexedObservations {
		if indexed.observation.ID == memoryID {
			directMatches = append(directMatches, indexed)
		}
	}
	reflectionMatches := []indexedReflection{}
	for _, indexed := range indexedReflections {
		if indexed.reflection.ID == memoryID {
			reflectionMatches = append(reflectionMatches, indexed)
		}
	}
	if len(directMatches) == 0 && len(reflectionMatches) == 0 {
		return notFound(memoryID)
	}

	observationsByID := map[string]indexedObservation{}
	for _, indexed := range indexedObservations {
		if _, seen := observationsByID[indexed.observation.ID]; !seen {
			observationsByID[indexed.observation.ID] = indexed
		}
	}

	recalledKeys := map[string]bool{}
	recalled := []RecalledObservation{}
	missingSupporting := []string{}
	add := func(indexed indexedObservation) {
		key := indexed.entryID + ":" + itoa(indexed.recordIndex)
		if recalledKeys[key] {
			return
		}
		recalledKeys[key] = true
		item := resolveObservationSources(entries, indexed)
		if dropped[indexed.observation.ID] {
			item.Status = "dropped"
		} else {
			item.Status = "active"
		}
		recalled = append(recalled, item)
	}
	for _, match := range directMatches {
		add(match)
	}
	for _, match := range reflectionMatches {
		for _, id := range uniqueStrings(match.reflection.SupportingObservationIDs) {
			indexed, ok := observationsByID[id]
			if !ok {
				missingSupporting = append(missingSupporting, id)
				continue
			}
			add(indexed)
		}
	}

	result := RecallResult{
		Status:       "found",
		MemoryID:     memoryID,
		Reflections:  []RecalledReflection{},
		Observations: recalled,
	}
	for _, match := range reflectionMatches {
		result.Reflections = append(result.Reflections, RecalledReflection{Reflection: match.reflection, ReflectionEntryID: match.entryID, ReflectionRecordIndex: match.recordIndex})
	}
	allSources := []Entry{}
	missingSources := []string{}
	nonSources := []string{}
	for _, item := range recalled {
		allSources = append(allSources, item.SourceEntries...)
		missingSources = append(missingSources, item.MissingSourceEntryIDs...)
		nonSources = append(nonSources, item.NonSourceEntryIDs...)
	}
	result.SourceEntries = uniqueEntriesByID(allSources)
	result.MissingSourceEntryIDs = uniqueStrings(missingSources)
	result.NonSourceEntryIDs = uniqueStrings(nonSources)
	result.MissingSupportingObservationIDs = uniqueStrings(missingSupporting)
	switch {
	case len(directMatches) > 0 && len(reflectionMatches) > 0:
		result.Kind = "mixed"
	case len(reflectionMatches) > 0:
		result.Kind = "reflection"
	default:
		result.Kind = "observation"
	}
	result.Collision = len(directMatches)+len(reflectionMatches) > 1
	result.Partial = len(result.MissingSourceEntryIDs) > 0 || len(result.NonSourceEntryIDs) > 0 || len(result.MissingSupportingObservationIDs) > 0
	return result
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := n < 0
	if negative {
		n = -n
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if negative {
		return "-" + string(digits)
	}
	return string(digits)
}
