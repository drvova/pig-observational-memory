package agents

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ids"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/jsfmt"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/tokens"
)

// ReflectorArgs are one reflector run's inputs.
type ReflectorArgs struct {
	WorkerArgs
	Reflections  []ledger.Reflection
	Observations []ledger.Observation
}

var recordReflectionsSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"reflections": map[string]any{
			"type":     "array",
			"minItems": 1,
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"content":                  map[string]any{"type": "string", "minLength": 1},
					"supportingObservationIds": map[string]any{"type": "array", "minItems": 1, "items": map[string]any{"type": "string", "minLength": 1}},
				},
				"required": []any{"content", "supportingObservationIds"},
			},
		},
	},
	"required": []any{"reflections"},
}

// SupportIDSummary describes the support ids of accepted reflections.
type SupportIDSummary struct {
	ReflectionCount   int            `json:"reflectionCount"`
	TotalSupportIDs   int            `json:"totalSupportIds"`
	MinSupportIDs     int            `json:"minSupportIds"`
	MaxSupportIDs     int            `json:"maxSupportIds"`
	AverageSupportIDs float64        `json:"averageSupportIds"`
	Histogram         map[string]int `json:"histogram"`
}

// SummarizeSupportIDCounts summarizes how many observations each reflection cites.
func SummarizeSupportIDCounts(reflections []ledger.Reflection) SupportIDSummary {
	summary := SupportIDSummary{Histogram: map[string]int{}}
	if len(reflections) == 0 {
		return summary
	}
	summary.ReflectionCount = len(reflections)
	for i, reflection := range reflections {
		count := len(reflection.SupportingObservationIDs)
		summary.TotalSupportIDs += count
		if i == 0 || count < summary.MinSupportIDs {
			summary.MinSupportIDs = count
		}
		if count > summary.MaxSupportIDs {
			summary.MaxSupportIDs = count
		}
		summary.Histogram[fmt.Sprint(count)]++
	}
	summary.AverageSupportIDs = float64(summary.TotalSupportIDs) / float64(len(reflections))
	return summary
}

// NormalizeSupportingObservationIDs keeps a proposal's ids when every one is
// an allowed observation, deduplicated and in pool order.
func NormalizeSupportingObservationIDs(supporting, allowed []string) ([]string, bool) {
	if len(supporting) == 0 {
		return nil, false
	}
	order := map[string]int{}
	for i, id := range allowed {
		if _, seen := order[id]; !seen {
			order[id] = i
		}
	}
	seen := map[string]bool{}
	for _, id := range supporting {
		if _, ok := order[id]; !ok {
			return nil, false
		}
		seen[id] = true
	}
	if len(seen) == 0 {
		return nil, false
	}
	out := make([]string, 0, len(seen))
	for id := range seen {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return order[out[i]] < order[out[j]] })
	return out, true
}

func normalizeReflectionContent(content string) (string, bool) {
	normalized := serialize.TruncateRecordContent(strings.TrimSpace(content))
	if normalized == "" || strings.ContainsAny(normalized, "\r\n") {
		return "", false
	}
	return normalized, true
}

// RunReflector distills durable reflections from the active observations. It
// returns nil and no error when nothing new was stable enough, and a
// *StreamError when the model call failed without recording anything.
func RunReflector(ctx context.Context, args ReflectorArgs) ([]ledger.Reflection, error) {
	if len(args.Observations) == 0 {
		return nil, nil
	}
	coverage := ReflectionCoverageMap(args.Observations, args.Reflections)
	debuglog.Log(ctx, "reflector.agent_start", map[string]any{
		"activeObservationCount":     len(args.Observations),
		"reflectionCount":            len(args.Reflections),
		"coverageSummaryByRelevance": SummarizeCoverageByRelevance(args.Observations, coverage),
	})
	allowed := make([]string, 0, len(args.Observations))
	for _, observation := range args.Observations {
		allowed = append(allowed, observation.ID)
	}
	existing := map[string]bool{}
	for _, reflection := range args.Reflections {
		existing[reflection.ID] = true
	}
	accumulated := map[string]ledger.Reflection{}
	order := []string{}
	toolCalls, rawProposed, accepted, duplicateCount, rejectedCount := 0, 0, 0, 0, 0
	recordReflections := Tool{
		Name:        "record_reflections",
		Label:       "Record reflections",
		Description: "Record new durable reflections with supporting observation ids.",
		Parameters:  recordReflectionsSchema,
		Execute: func(_ context.Context, params map[string]any) (ToolResult, error) {
			toolCalls++
			items, _ := params["reflections"].([]any)
			rawProposed += len(items)
			added, duplicates, rejected := 0, 0, 0
			for _, raw := range items {
				proposal, _ := raw.(map[string]any)
				if proposal == nil {
					rejected++
					continue
				}
				rawContent, _ := proposal["content"].(string)
				content, okContent := normalizeReflectionContent(rawContent)
				proposedIDs, _ := StringList(proposal["supportingObservationIds"])
				supporting, okIDs := NormalizeSupportingObservationIDs(proposedIDs, allowed)
				if !okContent || !okIDs {
					rejected++
					continue
				}
				id := ids.HashID(content)
				if existing[id] {
					duplicates++
					continue
				}
				if _, dup := accumulated[id]; dup {
					duplicates++
					continue
				}
				accumulated[id] = ledger.Reflection{ID: id, Content: content, SupportingObservationIDs: supporting, TokenCount: tokens.EstimateStringTokens(content)}
				order = append(order, id)
				added++
			}
			accepted += added
			duplicateCount += duplicates
			rejectedCount += rejected
			text := fmt.Sprintf("Recorded %d %s; %d %s; %d rejected. Total this run: %d.", added, jsfmt.Plural(added, "reflection"), duplicates, jsfmt.Plural(duplicates, "duplicate"), rejected, len(accumulated))
			return ToolResult{Text: text, Details: map[string]any{"added": added, "duplicates": duplicates, "rejected": rejected, "total": len(accumulated)}}, nil
		},
	}

	reflectionLines := make([]string, 0, len(args.Reflections))
	for _, reflection := range args.Reflections {
		reflectionLines = append(reflectionLines, ledger.ReflectionToSummaryLine(reflection))
	}
	observationLines := make([]string, 0, len(args.Observations))
	for _, observation := range args.Observations {
		observationLines = append(observationLines, ObservationToReflectorLine(observation, CoverageTierForObservation(observation, coverage)))
	}
	userText := "CURRENT REFLECTIONS:\n" + joinOrEmpty(reflectionLines) + "\n\nCURRENT OBSERVATIONS:\n" + joinOrEmpty(observationLines) + "\n\nCrystallize any missing durable facts or patterns into new reflections. If nothing is stable enough, do not call the tool."

	result, err := RunLoop(ctx, args.Stream, args.loopConfig("reflector"), ReflectorSystem, userText, []Tool{recordReflections})
	if err != nil {
		return nil, err
	}
	acceptedReflections := make([]ledger.Reflection, 0, len(order))
	for _, id := range order {
		acceptedReflections = append(acceptedReflections, accumulated[id])
	}
	if len(acceptedReflections) == 0 && result.StreamError != nil {
		return nil, result.StreamError
	}
	after := ReflectionCoverageMap(args.Observations, append(append([]ledger.Reflection{}, args.Reflections...), acceptedReflections...))
	reason := "accepted_nonempty"
	if len(acceptedReflections) == 0 {
		if toolCalls == 0 {
			reason = "no_tool_call"
		} else {
			reason = "all_filtered"
		}
	}
	debuglog.Log(ctx, "reflector.result", map[string]any{
		"reason":                         reason,
		"toolCallCount":                  toolCalls,
		"rawProposedReflectionCount":     rawProposed,
		"acceptedReflectionCount":        accepted,
		"duplicateReflectionCount":       duplicateCount,
		"rejectedReflectionCount":        rejectedCount,
		"acceptedSupportIdCounts":        SummarizeSupportIDCounts(acceptedReflections),
		"coverageTransitionsByRelevance": SummarizeCoverageTransitionsByRelevance(args.Observations, coverage, after),
	})
	if len(acceptedReflections) == 0 {
		return nil, nil
	}
	return acceptedReflections, nil
}
