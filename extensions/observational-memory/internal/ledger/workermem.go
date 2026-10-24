package ledger

import (
	"sort"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/jsfmt"
)

// WorkerMemorySlice is the prior memory a worker prompt carries.
type WorkerMemorySlice struct {
	Reflections         []Reflection
	Observations        []Observation
	OmittedReflections  int
	OmittedObservations int
	// Subjects are canonical subjects of the whole memory with their note counts, most used
	// first (CountCanonicalSubjects); only the observer's prompt carries them
	// (BoundObserverMemory). Within a budget only those the kept observations do not already
	// show are listed, within their own cap; OmittedSubjects counts the rest.
	Subjects        []SubjectCount
	OmittedSubjects int
	// Canonical maps each observation's subject to its Canonical over the whole memory
	// (ObservationSubjects); set by BoundObserverMemory, nil otherwise.
	Canonical map[string]Canonical
}

func lineTokens(line string) int { return (jsfmt.UTF16Len(line)+3)/4 + 1 }

// pick keeps records whose rendered lines fit budget, starting from the newest
// or the oldest end, and returns them in original order.
func pick[T any](records []T, line func(T) string, budget int, fromOldest bool) ([]T, int) {
	indexes := []int{}
	tokens := 0
	for n := 0; n < len(records); n++ {
		i := len(records) - 1 - n
		if fromOldest {
			i = n
		}
		cost := lineTokens(line(records[i]))
		// Skip a record that does not fit instead of stopping: a single long
		// newest record must not hide shorter older ones that still fit.
		if tokens+cost > budget {
			continue
		}
		tokens += cost
		indexes = append(indexes, i)
	}
	sort.Ints(indexes)
	kept := make([]T, 0, len(indexes))
	for _, i := range indexes {
		kept = append(kept, records[i])
	}
	return kept, tokens
}

// BoundWorkerMemory bounds the prior memory a worker prompt carries.
// Observations get at least half the budget, reflections the rest (newest
// first), and each side reclaims what the other leaves unused. fromOldest
// serves the dropper, whose job is pruning old observations. A non-positive
// budget returns everything. Each record is costed by its summary line
// (ObservationToSummaryLine, ReflectionToSummaryLine), the form the reflector
// and dropper prompts carry.
func BoundWorkerMemory(reflections []Reflection, observations []Observation, maxTokens int, fromOldest bool) WorkerMemorySlice {
	return boundWorkerMemory(reflections, observations, maxTokens, fromOldest, ObservationToSummaryLine, nil)
}

// SubjectsHeader heads the observer prompt's list of canonical subjects
// (WorkerMemorySlice.Subjects), after a blank line.
const SubjectsHeader = "CURRENT SUBJECTS (subject: notes, most used first):"

// subjectsBudgetShare is the share of the worker-memory budget the subject list may take
// at most (1/subjectsBudgetShare), so it never crowds out reflections or observations.
const subjectsBudgetShare = 8

// BoundObserverMemory is BoundWorkerMemory for the observer's prompt, which
// carries observations as ObservationToCanonicalObserverLine (their structured
// fields in braces, the subject canonical): each observation is costed by that
// longer line, so the rendered prior observations stay within maxTokens. It
// also carries canonical subjects with their note counts (SubjectCount.Line),
// counted over every observation, kept or not, so the observer can reuse a
// subject whose notes did not fit. The list takes at most an eighth of the
// budget, its header included, and lists only what the observations reserved
// for the prompt do not already show: subjects with two or more notes, and
// subjects none of whose notes is reserved. Reflections and observations share
// the rest as in BoundWorkerMemory, so the whole stays within maxTokens. With
// no budget every subject is listed.
func BoundObserverMemory(reflections []Reflection, observations []Observation, maxTokens int) WorkerMemorySlice {
	canonical := ObservationSubjects(observations)
	line := func(o Observation) string { return ObservationToCanonicalObserverLine(o, canonical[o.Subject]) }
	slice := boundWorkerMemory(reflections, observations, maxTokens, false, line, func(reserved []Observation) ([]SubjectCount, int, int) {
		all := CountCanonicalSubjects(observations)
		if maxTokens <= 0 {
			return all, 0, 0
		}
		shown := map[string]bool{}
		for _, o := range reserved {
			shown[canonical[o.Subject].Subject] = true
		}
		candidates := []SubjectCount{}
		for _, s := range all {
			if s.Count >= 2 || !shown[s.Subject] {
				candidates = append(candidates, s)
			}
		}
		// The header line and the blank line before it.
		header := lineTokens(SubjectsHeader) + 1
		kept, tokens := pickRanked(candidates, SubjectCount.Line, maxTokens/subjectsBudgetShare-header)
		if len(kept) == 0 {
			return nil, 0, len(all)
		}
		return kept, tokens + header, len(all) - len(kept)
	})
	slice.Canonical = canonical
	return slice
}

// ObserverLines are the slice's observations as the observer reads them
// (ObservationToCanonicalObserverLine with the slice's Canonical).
func (s WorkerMemorySlice) ObserverLines() []string {
	lines := make([]string, 0, len(s.Observations))
	for _, o := range s.Observations {
		lines = append(lines, ObservationToCanonicalObserverLine(o, s.Canonical[o.Subject]))
	}
	return lines
}

// boundWorkerMemory reserves half the budget for observations, then lets subjects (when
// given: the kept list, its tokens and how many were left out, from the reserved
// observations) take their share, reflections what remains, and observations the rest.
func boundWorkerMemory(reflections []Reflection, observations []Observation, maxTokens int, fromOldest bool, observationLine func(Observation) string, subjects func(reserved []Observation) ([]SubjectCount, int, int)) WorkerMemorySlice {
	if maxTokens <= 0 {
		slice := WorkerMemorySlice{Reflections: reflections, Observations: observations}
		if subjects != nil {
			slice.Subjects, _, _ = subjects(observations)
		}
		return slice
	}
	reserved, reservedTokens := pick(observations, observationLine, maxTokens/2, fromOldest)
	var keptSubjects []SubjectCount
	subjectTokens, omittedSubjects := 0, 0
	if subjects != nil {
		keptSubjects, subjectTokens, omittedSubjects = subjects(reserved)
	}
	keptReflections, reflectionTokens := pick(reflections, ReflectionToSummaryLine, maxTokens-reservedTokens-subjectTokens, false)
	keptObservations, _ := pick(observations, observationLine, maxTokens-reflectionTokens-subjectTokens, fromOldest)
	return WorkerMemorySlice{
		Reflections:         keptReflections,
		Observations:        keptObservations,
		OmittedReflections:  len(reflections) - len(keptReflections),
		OmittedObservations: len(observations) - len(keptObservations),
		Subjects:            keptSubjects,
		OmittedSubjects:     omittedSubjects,
	}
}

// pickRanked keeps records in their order (a ranking) while their lines fit
// budget, skipping one that does not fit as pick does.
func pickRanked[T any](records []T, line func(T) string, budget int) ([]T, int) {
	var kept []T
	tokens := 0
	for _, record := range records {
		cost := lineTokens(line(record))
		if tokens+cost > budget {
			continue
		}
		tokens += cost
		kept = append(kept, record)
	}
	return kept, tokens
}
