// Package agents runs the observer, reflector and dropper memory workers.
package agents

import "github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ledger"

// CoverageTier is how many current reflections cite an observation.
type CoverageTier string

// The coverage tiers.
const (
	CoverageNone    CoverageTier = "none"
	CoveragePartial CoverageTier = "partial"
	CoverageStrong  CoverageTier = "strong"
)

// CoverageTiers lists the tiers.
var CoverageTiers = []CoverageTier{CoverageNone, CoveragePartial, CoverageStrong}

// CoverageDropRank orders drop candidates: strongly covered observations first.
var CoverageDropRank = map[CoverageTier]int{CoverageStrong: 0, CoveragePartial: 1, CoverageNone: 2}

// CoverageBucket counts observations and their tokens.
type CoverageBucket struct {
	Count  int `json:"count"`
	Tokens int `json:"tokens"`
}

// CoverageSummaryByRelevance is a per-relevance, per-tier count.
type CoverageSummaryByRelevance map[ledger.Relevance]map[CoverageTier]*CoverageBucket

// CoverageTransitionSummaryByRelevance counts tier transitions per relevance.
type CoverageTransitionSummaryByRelevance map[ledger.Relevance]map[string]*CoverageBucket

// ReflectionSupportCounts counts how many reflections cite each observation id.
func ReflectionSupportCounts(reflections []ledger.Reflection) map[string]int {
	counts := map[string]int{}
	for _, reflection := range reflections {
		unique := map[string]bool{}
		for _, id := range reflection.SupportingObservationIDs {
			if unique[id] {
				continue
			}
			unique[id] = true
			counts[id]++
		}
	}
	return counts
}

// CoverageTierForCount maps a support count to a tier.
func CoverageTierForCount(count int) CoverageTier {
	switch {
	case count <= 0:
		return CoverageNone
	case count == 1:
		return CoveragePartial
	}
	return CoverageStrong
}

// ReflectionCoverageMap is each observation's coverage tier.
func ReflectionCoverageMap(observations []ledger.Observation, reflections []ledger.Reflection) map[string]CoverageTier {
	counts := ReflectionSupportCounts(reflections)
	coverage := make(map[string]CoverageTier, len(observations))
	for _, observation := range observations {
		coverage[observation.ID] = CoverageTierForCount(counts[observation.ID])
	}
	return coverage
}

func emptyBucket() map[CoverageTier]*CoverageBucket {
	return map[CoverageTier]*CoverageBucket{CoverageNone: {}, CoveragePartial: {}, CoverageStrong: {}}
}

// EmptyCoverageSummaryByRelevance is a zeroed summary.
func EmptyCoverageSummaryByRelevance() CoverageSummaryByRelevance {
	summary := CoverageSummaryByRelevance{}
	for _, relevance := range ledger.RelevanceValues {
		summary[relevance] = emptyBucket()
	}
	return summary
}

// CoverageTierForObservation is the observation's tier, none when unknown.
func CoverageTierForObservation(observation ledger.Observation, coverage map[string]CoverageTier) CoverageTier {
	if tier, ok := coverage[observation.ID]; ok {
		return tier
	}
	return CoverageNone
}

// SummarizeCoverageByRelevance counts observations per relevance and tier.
func SummarizeCoverageByRelevance(observations []ledger.Observation, coverage map[string]CoverageTier) CoverageSummaryByRelevance {
	summary := EmptyCoverageSummaryByRelevance()
	for _, observation := range observations {
		buckets, ok := summary[observation.Relevance]
		if !ok {
			buckets = emptyBucket()
			summary[observation.Relevance] = buckets
		}
		bucket := buckets[CoverageTierForObservation(observation, coverage)]
		bucket.Count++
		bucket.Tokens += observation.TokenCount
	}
	return summary
}

// SummarizeCoverageByRelevanceForIDs summarizes the named observations.
func SummarizeCoverageByRelevanceForIDs(ids []string, observations []ledger.Observation, coverage map[string]CoverageTier) CoverageSummaryByRelevance {
	byID := map[string]ledger.Observation{}
	for _, observation := range observations {
		byID[observation.ID] = observation
	}
	selected := []ledger.Observation{}
	for _, id := range ids {
		if observation, ok := byID[id]; ok {
			selected = append(selected, observation)
		}
	}
	return SummarizeCoverageByRelevance(selected, coverage)
}

// EmptyCoverageTransitionSummaryByRelevance is a zeroed transition summary.
func EmptyCoverageTransitionSummaryByRelevance() CoverageTransitionSummaryByRelevance {
	summary := CoverageTransitionSummaryByRelevance{}
	for _, relevance := range ledger.RelevanceValues {
		summary[relevance] = map[string]*CoverageBucket{}
	}
	return summary
}

// SummarizeCoverageTransitionsByRelevance counts "before->after" tier changes.
func SummarizeCoverageTransitionsByRelevance(observations []ledger.Observation, before, after map[string]CoverageTier) CoverageTransitionSummaryByRelevance {
	summary := EmptyCoverageTransitionSummaryByRelevance()
	for _, observation := range observations {
		b := CoverageTierForObservation(observation, before)
		a := CoverageTierForObservation(observation, after)
		if a == b {
			continue
		}
		key := string(b) + "->" + string(a)
		transitions, ok := summary[observation.Relevance]
		if !ok {
			transitions = map[string]*CoverageBucket{}
			summary[observation.Relevance] = transitions
		}
		bucket := transitions[key]
		if bucket == nil {
			bucket = &CoverageBucket{}
			transitions[key] = bucket
		}
		bucket.Count++
		bucket.Tokens += observation.TokenCount
	}
	return summary
}

// ObservationToDropperLine renders an observation with its coverage tier.
func ObservationToDropperLine(observation ledger.Observation, coverage CoverageTier) string {
	return "[" + observation.ID + "] " + observation.Timestamp + " [" + string(observation.Relevance) + "] [coverage: " + string(coverage) + "] " + observation.Content
}

// ObservationToReflectorLine renders an observation with its coverage tier.
func ObservationToReflectorLine(observation ledger.Observation, coverage CoverageTier) string {
	return ObservationToDropperLine(observation, coverage)
}
