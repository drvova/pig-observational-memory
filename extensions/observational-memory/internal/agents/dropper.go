package agents

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/jsfmt"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
)

// DropperArgs are one dropper run's inputs.
type DropperArgs struct {
	WorkerArgs
	Reflections []ledger.Reflection
	// Observations are the active observations the dropper may propose to
	// drop: the slice that fits the worker memory budget.
	Observations []ledger.Observation
	// ActiveObservations is the whole active pool. The over-target gate and
	// the pool figures in the prompt measure it, not the bounded slice, so a
	// pool that drifted above target is still pruned when the slice alone
	// looks under target (upstream issue #102). nil means Observations is the
	// whole pool.
	ActiveObservations []ledger.Observation
	TargetTokens       int
}

// RelevanceDropRank orders drop candidates: low relevance first.
var RelevanceDropRank = map[ledger.Relevance]int{"low": 0, "medium": 1, "high": 2, "critical": 3}

var dropObservationsSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"ids":    map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string", "minLength": 1}},
		"reason": map[string]any{"type": "string"},
	},
	"required": []any{"ids"},
}

func relevanceCounts(observations []ledger.Observation) map[ledger.Relevance]int {
	counts := map[ledger.Relevance]int{"low": 0, "medium": 0, "high": 0, "critical": 0}
	for _, observation := range observations {
		counts[observation.Relevance]++
	}
	return counts
}

// NormalizeDropObservationIDs keeps the known ids, deduplicated, in proposal order.
func NormalizeDropObservationIDs(proposed []string, observations []ledger.Observation) ([]string, bool) {
	if len(proposed) == 0 {
		return nil, false
	}
	allowed := map[string]bool{}
	for _, observation := range observations {
		allowed[observation.ID] = true
	}
	seen := map[string]bool{}
	result := []string{}
	for _, id := range proposed {
		if !allowed[id] || seen[id] {
			continue
		}
		seen[id] = true
		result = append(result, id)
	}
	if len(result) == 0 {
		return nil, false
	}
	return result, true
}

func timestampRank(timestamp string) float64 {
	if t, ok := serialize.ParseTimestamp(timestamp); ok {
		return float64(t.UnixMilli())
	}
	return math.Inf(1)
}

// SelectDropCandidates orders proposals by coverage (strong first), relevance
// (low first), age (oldest first) and proposal order, and keeps at most
// maxDrops known observations.
func SelectDropCandidates(proposed []string, observations []ledger.Observation, maxDrops int, reflections []ledger.Reflection) []string {
	if maxDrops <= 0 || len(proposed) == 0 {
		return []string{}
	}
	byID := map[string]ledger.Observation{}
	for _, observation := range observations {
		byID[observation.ID] = observation
	}
	coverage := ReflectionCoverageMap(observations, reflections)
	type candidate struct {
		id          string
		index       int
		observation ledger.Observation
	}
	firstIndex := map[string]int{}
	candidates := []candidate{}
	for i, id := range proposed {
		if _, seen := firstIndex[id]; seen {
			continue
		}
		firstIndex[id] = i
		observation, ok := byID[id]
		if !ok {
			continue
		}
		candidates = append(candidates, candidate{id: id, index: i, observation: observation})
	}
	sort.SliceStable(candidates, func(a, b int) bool {
		ca, cb := candidates[a], candidates[b]
		if d := CoverageDropRank[CoverageTierForObservation(ca.observation, coverage)] - CoverageDropRank[CoverageTierForObservation(cb.observation, coverage)]; d != 0 {
			return d < 0
		}
		if d := RelevanceDropRank[ca.observation.Relevance] - RelevanceDropRank[cb.observation.Relevance]; d != 0 {
			return d < 0
		}
		ra, rb := timestampRank(ca.observation.Timestamp), timestampRank(cb.observation.Timestamp)
		if ra != rb && !(math.IsInf(ra, 1) && math.IsInf(rb, 1)) {
			return ra < rb
		}
		return ca.index < cb.index
	})
	if len(candidates) > maxDrops {
		candidates = candidates[:maxDrops]
	}
	ids := make([]string, 0, len(candidates))
	for _, c := range candidates {
		ids = append(ids, c.id)
	}
	return ids
}

// DropFloorMaxFullness is the pool fullness (active tokens over target) up to
// which PreserveSpecifics holds. Above it, memory pressure wins and the
// selected drops go through unfiltered, so the pool cannot grow without bound.
const DropFloorMaxFullness = 2.0

// PreserveSpecifics removes from dropped every observation whose specifics
// (numbers, relative dates, names; see ledger.Specifics) are not all carried by
// a reflection that cites it. Dropping such an observation would delete the
// detail outright, since no reflection holds it. The check is textual, not a
// model judgment, so the same ledger always keeps the same observations.
// It returns the drops that remain and the ids it kept.
func PreserveSpecifics(dropped []string, observations []ledger.Observation, reflections []ledger.Reflection) ([]string, []string) {
	byID := map[string]ledger.Observation{}
	for _, observation := range observations {
		byID[observation.ID] = observation
	}
	citing := map[string][]string{}
	for _, reflection := range reflections {
		for _, id := range reflection.SupportingObservationIDs {
			citing[id] = append(citing[id], reflection.Content)
		}
	}
	remaining := []string{}
	kept := []string{}
	for _, id := range dropped {
		observation, ok := byID[id]
		if ok && len(ledger.MissingSpecifics(observation.Content, citing[id])) > 0 {
			kept = append(kept, id)
			continue
		}
		remaining = append(remaining, id)
	}
	return remaining, kept
}

// RunDropper asks the model which observations are safe to remove and returns
// the selected ids, nil when none, or a *StreamError when the call failed.
func RunDropper(ctx context.Context, args DropperArgs) ([]string, error) {
	if len(args.Observations) == 0 {
		return nil, nil
	}
	pool := args.ActiveObservations
	if pool == nil {
		pool = args.Observations
	}
	metrics := ObservationPoolMetrics(pool, args.TargetTokens)
	// The dropper can only drop what it sees.
	maxDrops := min(metrics.MaxDropsAllowed, len(args.Observations))
	coverage := ReflectionCoverageMap(args.Observations, args.Reflections)
	debuglog.Log(ctx, "dropper.agent_start", map[string]any{
		"activeObservationCount":     len(pool),
		"visibleObservationCount":    len(args.Observations),
		"reflectionCount":            len(args.Reflections),
		"observationTokens":          metrics.ObservationTokens,
		"targetTokens":               args.TargetTokens,
		"tokensOverTarget":           metrics.TokensOverTarget,
		"fullness":                   metrics.Fullness,
		"maxDropsAllowed":            maxDrops,
		"relevanceCounts":            relevanceCounts(args.Observations),
		"coverageSummaryByRelevance": SummarizeCoverageByRelevance(args.Observations, coverage),
	})
	if maxDrops <= 0 {
		debuglog.Log(ctx, "dropper.result", map[string]any{
			"reason":                             "not_over_target",
			"toolCallCount":                      0,
			"rawRequestedIdsCount":               0,
			"acceptedCandidateCount":             0,
			"selectedDropsCount":                 0,
			"selectedDropTokens":                 0,
			"selectedCoverageSummaryByRelevance": SummarizeCoverageByRelevanceForIDs(nil, args.Observations, coverage),
			"maxDropsAllowed":                    maxDrops,
		})
		return nil, nil
	}
	allowed := map[string]ledger.Observation{}
	for _, observation := range args.Observations {
		allowed[observation.ID] = observation
	}
	proposedIDs := []string{}
	proposed := map[string]bool{}
	toolCalls, rawRequested, missing, criticalCandidates, duplicateInRequest, duplicateInRun := 0, 0, 0, 0, 0, 0
	dropObservations := Tool{
		Name:        "drop_observations",
		Label:       "Drop observations",
		Description: "Propose active observation ids that are safe to remove from compacted memory.",
		Parameters:  dropObservationsSchema,
		Execute: func(_ context.Context, params map[string]any) (ToolResult, error) {
			toolCalls++
			requested, _ := StringList(params["ids"])
			rawRequested += len(requested)
			seenInRequest := map[string]bool{}
			added, reqMissing, reqCritical, reqDup, reqDupRun := 0, 0, 0, 0, 0
			for _, id := range requested {
				observation, ok := allowed[id]
				if !ok {
					missing++
					reqMissing++
					continue
				}
				if seenInRequest[id] {
					duplicateInRequest++
					reqDup++
					continue
				}
				seenInRequest[id] = true
				if proposed[id] {
					duplicateInRun++
					reqDupRun++
					continue
				}
				proposed[id] = true
				proposedIDs = append(proposedIDs, id)
				if observation.Relevance == "critical" {
					criticalCandidates++
					reqCritical++
				}
				added++
			}
			debuglog.Log(ctx, "dropper.tool_call", map[string]any{
				"toolCallCount":             toolCalls,
				"rawRequestedIdsCount":      len(requested),
				"acceptedIdsCount":          added,
				"missingIdsCount":           reqMissing,
				"criticalCandidateIdsCount": reqCritical,
				"duplicateInRequestCount":   reqDup,
				"duplicateInRunCount":       reqDupRun,
				"totalCandidates":           len(proposedIDs),
				"maxDropsAllowed":           maxDrops,
			})
			text := fmt.Sprintf("Queued %d drop %s. Candidates this run: %d. Maximum drops allowed: %d.", added, jsfmt.Plural(added, "candidate"), len(proposedIDs), maxDrops)
			return ToolResult{Text: text, Details: map[string]any{"added": added, "totalCandidates": len(proposedIDs), "maxDropsAllowed": maxDrops}}, nil
		},
	}

	reflectionLines := make([]string, 0, len(args.Reflections))
	for _, reflection := range args.Reflections {
		reflectionLines = append(reflectionLines, ledger.ReflectionToSummaryLine(reflection))
	}
	observationLines := make([]string, 0, len(args.Observations))
	for _, observation := range args.Observations {
		observationLines = append(observationLines, ObservationToDropperLine(observation, CoverageTierForObservation(observation, coverage)))
	}
	fullnessPercent := int(math.Round(metrics.Fullness * 100))
	userText := "CURRENT REFLECTIONS:\n" + joinOrEmpty(reflectionLines) + "\n\nCURRENT OBSERVATIONS:\n" + joinOrEmpty(observationLines) +
		fmt.Sprintf("\n\nActive observation pool: ~%s tokens; target: ~%s tokens; fullness against target: ~%s%%; over target by ~%s tokens.\n", jsfmt.Locale(metrics.ObservationTokens), jsfmt.Locale(args.TargetTokens), jsfmt.Locale(fullnessPercent), jsfmt.Locale(metrics.TokensOverTarget)) +
		fmt.Sprintf("Maximum drops allowed this run: %s %s. This maximum is sized to move the active pool toward the target if every proposed drop is clearly safe.\n", jsfmt.Locale(maxDrops), jsfmt.Plural(maxDrops, "observation")) +
		"This maximum is a hard upper bound, not a target. Drop fewer or none if fewer observations are clearly safe."

	result, err := RunLoop(ctx, args.Stream, args.loopConfig("dropper"), DropperSystem, userText, []Tool{dropObservations})
	if err != nil {
		return nil, err
	}
	dropped := SelectDropCandidates(proposedIDs, args.Observations, maxDrops, args.Reflections)
	var preserved []string
	if metrics.Fullness <= DropFloorMaxFullness {
		dropped, preserved = PreserveSpecifics(dropped, args.Observations, args.Reflections)
	}
	if len(dropped) == 0 && result.StreamError != nil {
		return nil, result.StreamError
	}
	reason := "selected_nonempty"
	switch {
	case len(dropped) > 0:
	case len(preserved) > 0:
		reason = "all_preserved"
	case toolCalls == 0:
		reason = "no_tool_call"
	case len(proposedIDs) == 0:
		reason = "all_filtered"
	default:
		reason = "selected_empty"
	}
	selectedTokens := 0
	for _, id := range dropped {
		selectedTokens += allowed[id].TokenCount
	}
	debuglog.Log(ctx, "dropper.result", map[string]any{
		"reason":                             reason,
		"toolCallCount":                      toolCalls,
		"rawRequestedIdsCount":               rawRequested,
		"missingIdsCount":                    missing,
		"criticalCandidateIdsCount":          criticalCandidates,
		"duplicateInRequestCount":            duplicateInRequest,
		"duplicateInRunCount":                duplicateInRun,
		"acceptedCandidateCount":             len(proposedIDs),
		"selectedDropsCount":                 len(dropped),
		"selectedDropTokens":                 selectedTokens,
		"selectedCoverageSummaryByRelevance": SummarizeCoverageByRelevanceForIDs(dropped, args.Observations, coverage),
		"maxDropsAllowed":                    maxDrops,
		"preservedForSpecificsCount":         len(preserved),
	})
	if len(dropped) == 0 {
		return nil, nil
	}
	return dropped, nil
}
