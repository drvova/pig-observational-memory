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
	return boundWorkerMemory(reflections, observations, maxTokens, fromOldest, ObservationToSummaryLine)
}

// BoundObserverMemory is BoundWorkerMemory for the observer's prompt, which
// carries observations as ObservationToObserverLine (their structured fields
// in braces): each observation is costed by that longer line, so the rendered
// prior observations stay within maxTokens.
func BoundObserverMemory(reflections []Reflection, observations []Observation, maxTokens int) WorkerMemorySlice {
	return boundWorkerMemory(reflections, observations, maxTokens, false, ObservationToObserverLine)
}

func boundWorkerMemory(reflections []Reflection, observations []Observation, maxTokens int, fromOldest bool, observationLine func(Observation) string) WorkerMemorySlice {
	if maxTokens <= 0 {
		return WorkerMemorySlice{Reflections: reflections, Observations: observations}
	}
	_, reservedTokens := pick(observations, observationLine, maxTokens/2, fromOldest)
	keptReflections, reflectionTokens := pick(reflections, ReflectionToSummaryLine, maxTokens-reservedTokens, false)
	keptObservations, _ := pick(observations, observationLine, maxTokens-reflectionTokens, fromOldest)
	return WorkerMemorySlice{
		Reflections:         keptReflections,
		Observations:        keptObservations,
		OmittedReflections:  len(reflections) - len(keptReflections),
		OmittedObservations: len(observations) - len(keptObservations),
	}
}
