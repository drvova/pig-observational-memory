package agents

import (
	"math"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// PoolMetrics describes the active observation pool against its target.
type PoolMetrics struct {
	ObservationTokens      int
	TargetTokens           int
	TokensOverTarget       int
	Fullness               float64
	ActiveObservationCount int
	DroppableCount         int
	MaxDropsAllowed        int
	OverTarget             bool
	Ready                  bool
}

// ObservationTokenSum counts the full rendered lines (id + timestamp +
// relevance + content), not bare content: the pool budget caps how much
// observation text is re-rendered into future contexts.
func ObservationTokenSum(observations []ledger.Observation) int {
	sum := 0
	for _, observation := range observations {
		sum += tokens.ObservationLineTokenCount(tokens.ObservationLine{ID: observation.ID, Timestamp: observation.Timestamp, Relevance: string(observation.Relevance), Content: observation.Content})
	}
	return sum
}

// ObservationPoolFullness is pool tokens over target tokens, 0 when either is non-positive.
func ObservationPoolFullness(observationTokens, targetTokens int) float64 {
	if observationTokens <= 0 || targetTokens <= 0 {
		return 0
	}
	return float64(observationTokens) / float64(targetTokens)
}

// DroppableObservationCount is how many observations may be dropped.
func DroppableObservationCount(observations []ledger.Observation) int { return len(observations) }

// MaxDropCountForPool sizes the drop allowance to move the pool toward target.
func MaxDropCountForPool(observations []ledger.Observation, observationTokens, targetTokens int) int {
	count := len(observations)
	if count == 0 || observationTokens <= 0 || targetTokens < 0 {
		return 0
	}
	over := observationTokens - targetTokens
	if over <= 0 {
		return 0
	}
	average := float64(observationTokens) / float64(count)
	if average <= 0 || math.IsNaN(average) || math.IsInf(average, 0) {
		return 0
	}
	estimated := int(math.Ceil(float64(over) / average))
	return min(count, max(1, estimated))
}

// ObservationPoolMetrics measures the active pool against targetTokens.
func ObservationPoolMetrics(observations []ledger.Observation, targetTokens int) PoolMetrics {
	observationTokens := ObservationTokenSum(observations)
	maxDrops := MaxDropCountForPool(observations, observationTokens, targetTokens)
	over := targetTokens >= 0 && observationTokens > targetTokens
	return PoolMetrics{
		ObservationTokens:      observationTokens,
		TargetTokens:           targetTokens,
		TokensOverTarget:       max(0, observationTokens-targetTokens),
		Fullness:               ObservationPoolFullness(observationTokens, targetTokens),
		ActiveObservationCount: len(observations),
		DroppableCount:         DroppableObservationCount(observations),
		MaxDropsAllowed:        maxDrops,
		OverTarget:             over,
		Ready:                  over && maxDrops > 0,
	}
}
