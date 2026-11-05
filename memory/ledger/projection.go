package ledger

import "math"

// Projection is the memory an agent sees.
type Projection struct {
	Observations []Observation
	Reflections  []Reflection
	// Aliases are the subject aliases the projection's subjects are read with
	// (CanonicalSubjectsWithAliases), in ledger order; nil when none.
	Aliases []SubjectAlias
}

// CompactionProjectionConfig bounds the observation pool for a fold decision.
type CompactionProjectionConfig struct {
	ObservationsPoolMaxTokens float64
}

// CompactionProjection is the projection a compaction renders.
type CompactionProjection struct {
	Projection
	FullFold bool
	Details  MemoryDetails
}

type boundaryKind int

const (
	boundaryEntry boundaryKind = iota
	boundaryTip
	boundaryNone
)

type projectionBoundary struct {
	kind    boundaryKind
	entryID string
}

func entryBoundary(id string) projectionBoundary {
	return projectionBoundary{kind: boundaryEntry, entryID: id}
}
func tipBoundary() projectionBoundary  { return projectionBoundary{kind: boundaryTip} }
func noneBoundary() projectionBoundary { return projectionBoundary{kind: boundaryNone} }

func boundaryIndex(entries []Entry, index map[string]int, boundary projectionBoundary) int {
	switch boundary.kind {
	case boundaryTip:
		return len(entries) - 1
	case boundaryNone:
		return -1
	}
	if idx, ok := index[boundary.entryID]; ok {
		return idx
	}
	return -1
}

func isCoveredAtOrBefore(coversUpToID string, index map[string]int, boundaryIdx int) bool {
	covered, ok := index[coversUpToID]
	if !ok {
		return false
	}
	return covered >= 0 && boundaryIdx >= 0 && covered <= boundaryIdx
}

// aliasFolds reports whether the alias entry data at position i folds through the observations
// boundary: by its claim (ObservationsThroughID) when that entry is on the branch, else by i.
func aliasFolds(data SubjectsAliasedData, i int, index map[string]int, observationsBoundary int) bool {
	if _, ok := index[data.ObservationsThroughID]; ok {
		return isCoveredAtOrBefore(data.ObservationsThroughID, index, observationsBoundary)
	}
	return observationsBoundary >= 0 && i <= observationsBoundary
}

type foldOptions struct {
	observations projectionBoundary
	reflections  projectionBoundary
	drops        projectionBoundary
}

func foldProjection(entries []Entry, options foldOptions) Projection {
	index := EntryIndexByID(entries)
	observationsBoundary := boundaryIndex(entries, index, options.observations)
	reflectionsBoundary := boundaryIndex(entries, index, options.reflections)
	dropsBoundary := boundaryIndex(entries, index, options.drops)
	observations := []Observation{}
	reflections := []Reflection{}
	seenObservations := map[string]bool{}
	seenReflections := map[string]bool{}
	dropped := map[string]bool{}
	byID := map[string]Observation{}
	replacements := newReplacementTracker(func(id string) (Observation, bool) {
		o, ok := byID[id]
		return o, ok
	}, func(id string) bool {
		return hiddenBy(id, observations, dropped, reflections)
	})
	var aliases []SubjectAlias
	for i, entry := range entries {
		if data, ok := ObservationsRecordedEntry(entry); ok && isCoveredAtOrBefore(data.CoversUpToID, index, observationsBoundary) {
			for _, observation := range data.Observations {
				if seenObservations[observation.ID] {
					continue
				}
				seenObservations[observation.ID] = true
				byID[observation.ID] = observation
				observations = append(observations, observation)
			}
			replacements.recorded(data)
			continue
		}
		if data, ok := ReflectionsRecordedEntry(entry); ok && isCoveredAtOrBefore(data.CoversUpToID, index, reflectionsBoundary) {
			for _, reflection := range data.Reflections {
				if seenReflections[reflection.ID] {
					continue
				}
				seenReflections[reflection.ID] = true
				reflections = append(reflections, reflection)
			}
			continue
		}
		if data, ok := ObservationsDroppedEntry(entry); ok {
			// A replacement holds only where its record's drop folds (replace.go).
			if isCoveredAtOrBefore(data.CoversUpToID, index, dropsBoundary) {
				for _, id := range data.ObservationIDs {
					dropped[id] = true
				}
				replacements.dropped(data)
			}
			continue
		}
		// An alias entry groups the subjects of the observations it was checked against, so it
		// folds with them: when the observations it claims (ObservationsThroughID) fold, though
		// the reflector appended it after them. It moves no coverage marker. One without the
		// claim folds by its own position, through the observations' boundary.
		if data, ok := SubjectsAliasedEntry(entry); ok && aliasFolds(data, i, index, observationsBoundary) {
			aliases = append(aliases, data.Aliases...)
		}
	}
	// Every link is read through level 1's replacements, as FoldLedger reads it.
	active := []Observation{}
	for _, observation := range observations {
		if !dropped[observation.ID] {
			active = append(active, ResolveObservationLinks(observation, replacements.by, nil))
		}
	}
	for i, r := range reflections {
		reflections[i] = ResolveReflectionCitations(r, replacements.by, nil)
	}
	return Projection{Observations: active, Reflections: reflections, Aliases: aliases}
}

// latestV3CompactionDetails is the om.folded details of the newest compaction
// entry on the branch. Pi puts only the newest compaction's summary in
// context, so when that compaction was written by Pi's native summarizer (the
// hook delegated) or by an older version, no V3 record is in context, however
// much an earlier V3 compaction held.
func latestV3CompactionDetails(entries []Entry) (MemoryDetails, bool) {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == "compaction" {
			return MemoryDetailsFrom(entries[i].Details)
		}
	}
	return MemoryDetails{}, false
}

// FullProjection folds every valid record through upToEntryID ("" means the tip).
func FullProjection(entries []Entry, upToEntryID string) Projection {
	boundary := tipBoundary()
	if upToEntryID != "" {
		boundary = entryBoundary(upToEntryID)
	}
	return foldProjection(entries, foldOptions{observations: boundary, reflections: boundary, drops: boundary})
}

// VisibleProjection is what the agent currently sees: the latest compaction's
// memory details, or, for a boundary, the projection a compaction there would
// render.
func VisibleProjection(entries []Entry, upToEntryID string) Projection {
	if upToEntryID == "" {
		if details, ok := latestV3CompactionDetails(entries); ok {
			return Projection{
				Observations: append([]Observation{}, details.Observations...),
				Reflections:  append([]Reflection{}, details.Reflections...),
				Aliases:      append([]SubjectAlias(nil), details.Aliases...),
			}
		}
		return Projection{Observations: []Observation{}, Reflections: []Reflection{}}
	}
	return BuildCompactionProjection(entries, upToEntryID, CompactionProjectionConfig{ObservationsPoolMaxTokens: math.Inf(1)}).Projection
}

// LatestFullFoldBoundaryID is the entry through which the latest full-fold
// compaction folded the ledger: its recorded fold boundary, else its retention
// boundary, when that entry is on the branch.
func LatestFullFoldBoundaryID(entries []Entry) string {
	index := EntryIndexByID(entries)
	for i := len(entries) - 1; i >= 0; i-- {
		entry := entries[i]
		if entry.Type != "compaction" {
			continue
		}
		details, ok := MemoryDetailsFrom(entry.Details)
		if !ok || !details.FullFold {
			continue
		}
		if details.FoldThroughEntryID != "" {
			if _, present := index[details.FoldThroughEntryID]; present {
				return details.FoldThroughEntryID
			}
		}
		if entry.FirstKeptEntryID == "" {
			continue
		}
		if _, present := index[entry.FirstKeptEntryID]; !present {
			continue
		}
		return entry.FirstKeptEntryID
	}
	return ""
}

// BuildCompactionProjection decides what a compaction folding through
// firstKeptEntryID renders: new observations through the boundary, with
// reflections and drops applied through the latest full-fold boundary, or a
// full fold when the visible observation pool reached its limit.
func BuildCompactionProjection(entries []Entry, firstKeptEntryID string, cfg CompactionProjectionConfig) CompactionProjection {
	maintenance := noneBoundary()
	if fullFoldBoundaryID := LatestFullFoldBoundaryID(entries); fullFoldBoundaryID != "" {
		maintenance = entryBoundary(fullFoldBoundaryID)
	}
	normal := foldProjection(entries, foldOptions{
		observations: entryBoundary(firstKeptEntryID),
		reflections:  maintenance,
		drops:        maintenance,
	})
	observationTokens := 0
	for _, observation := range normal.Observations {
		observationTokens += observation.TokenCount
	}
	fullFold := float64(observationTokens) >= cfg.ObservationsPoolMaxTokens
	projection := normal
	if fullFold {
		projection = FullProjection(entries, firstKeptEntryID)
	}
	details := MemoryDetails{
		Type:               OMFolded,
		Version:            1,
		FullFold:           fullFold,
		Observations:       projection.Observations,
		Reflections:        projection.Reflections,
		FoldThroughEntryID: firstKeptEntryID,
		Aliases:            projection.Aliases,
	}
	return CompactionProjection{Projection: projection, FullFold: fullFold, Details: details}
}
