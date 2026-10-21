package agents

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ids"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/jsfmt"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/tokens"
)

// ObservationTimestampPattern constrains an observation's time.
const ObservationTimestampPattern = "^[0-9]{4}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}$"

// observationTimestamp enforces the pattern the tool schema declares. Pi's
// agent loop validates tool arguments against the schema before running a
// tool; this loop does not, so the observer checks the field itself.
var observationTimestamp = regexp.MustCompile(ObservationTimestampPattern)

// WorkerArgs are what every worker call shares.
type WorkerArgs struct {
	Model   host.Model
	APIKey  *string
	Headers map[string]any
	Env     map[string]string
	// MaxTurns bounds the loop; 0 is unlimited.
	MaxTurns int
	// MaxOutputTokens is the loop's output budget; 0 uses AgentLoopMaxTokens.
	MaxOutputTokens int
	// ThinkingLevel is the reasoning level; "" means "low".
	ThinkingLevel string
	Stream        StreamFunc
}

func (a WorkerArgs) loopConfig(stage string) LoopConfig {
	level := a.ThinkingLevel
	if level == "" {
		level = "low"
	}
	return LoopConfig{
		Stage:         stage,
		Model:         a.Model,
		APIKey:        a.APIKey,
		Headers:       a.Headers,
		Env:           a.Env,
		MaxTokens:     a.MaxOutputTokens,
		ThinkingLevel: level,
		MaxTurns:      a.MaxTurns,
	}
}

// ObserverArgs are one observer run's inputs.
type ObserverArgs struct {
	WorkerArgs
	PriorReflections      []string
	PriorObservations     []string
	Chunk                 string
	AllowedSourceEntryIDs []string
}

var recordObservationsSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"observations": map[string]any{
			"type":        "array",
			"minItems":    1,
			"description": "Batch of new observations. May be empty only if the tool is not called at all.",
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"timestamp": map[string]any{
						"type":        "string",
						"pattern":     ObservationTimestampPattern,
						"description": "Observation time in local 'YYYY-MM-DD HH:MM' format.",
					},
					"content": map[string]any{
						"type":        "string",
						"minLength":   1,
						"description": "Single-line plain prose. No markdown, no tags, no embedded timestamp.",
					},
					"relevance": map[string]any{
						"type": "string",
						"enum": []any{"low", "medium", "high", "critical"},
					},
					"sourceEntryIds": map[string]any{
						"type":     "array",
						"minItems": 1,
						"items":    map[string]any{"type": "string", "minLength": 1},
						"description": "Exact source entry ids from the chunk that directly support this observation. " +
							"Use only ids shown in '[Source entry id: ...]' labels; never invent ids.",
					},
				},
				"required": []any{"timestamp", "content", "relevance", "sourceEntryIds"},
			},
		},
	},
	"required": []any{"observations"},
}

// NormalizeSourceEntryIDs keeps a proposal's ids when every one is allowed,
// deduplicated and in branch order; any unknown id rejects the proposal.
func NormalizeSourceEntryIDs(sourceEntryIDs, allowed []string) ([]string, bool) {
	if len(sourceEntryIDs) == 0 {
		return nil, false
	}
	order := map[string]int{}
	for i, id := range allowed {
		if _, seen := order[id]; !seen {
			order[id] = i
		}
	}
	seen := map[string]bool{}
	for _, id := range sourceEntryIDs {
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

// RunObserver compresses one conversation chunk into observations. It returns
// nil observations and no error for a deliberate empty result, and a
// *StreamError when the model call failed without recording anything.
func RunObserver(ctx context.Context, args ObserverArgs) ([]ledger.Observation, error) {
	result, err := RunObserverDetailed(ctx, args)
	return result.Observations, err
}

// ObserverResult is one observer run's outcome.
type ObserverResult struct {
	Observations []ledger.Observation
	// Partial is true when the run ended before the model worked through the
	// whole chunk: a stream failure after some batches, or the turn cap. The
	// caller must then claim coverage only through the last source entry the
	// observations cite, or the unread rest of the chunk is marked observed.
	Partial bool
	// StreamError is the failure that cut a partial run short, when one did.
	StreamError *StreamError
}

// Observed wraps a complete observation list.
func Observed(observations []ledger.Observation) ObserverResult {
	return ObserverResult{Observations: observations}
}

// LastCitedSourceEntryID is the last id in chunk order that any observation
// cites, or "" when none cites a chunk entry.
func LastCitedSourceEntryID(observations []ledger.Observation, chunkOrder []string) string {
	cited := map[string]bool{}
	for _, observation := range observations {
		for _, id := range observation.SourceEntryIDs {
			cited[id] = true
		}
	}
	last := ""
	for _, id := range chunkOrder {
		if cited[id] {
			last = id
		}
	}
	return last
}

// normalizeObservationContent trims an observation and folds line breaks and
// runs of whitespace into single spaces: the prompt asks for single-line
// prose, and a multi-line record would break the one-line-per-record rendering
// in summaries and worker prompts.
func normalizeObservationContent(content string) (string, bool) {
	normalized := serialize.TruncateRecordContent(strings.Join(strings.Fields(content), " "))
	return normalized, normalized != ""
}

// RunObserverDetailed is RunObserver with the run's completeness.
func RunObserverDetailed(ctx context.Context, args ObserverArgs) (ObserverResult, error) {
	conversation := strings.TrimSpace(args.Chunk)
	if conversation == "" {
		return ObserverResult{}, nil
	}
	accumulated := map[string]ledger.Observation{}
	order := []string{}
	recordObservations := Tool{
		Name:  "record_observations",
		Label: "Record observations",
		Description: "Record a batch of new observations distilled from the conversation chunk. " +
			"Call this multiple times as you work through the chunk. Stop calling when coverage is complete, " +
			"then emit a short plain-text confirmation to end the run.",
		Parameters: recordObservationsSchema,
		Execute: func(_ context.Context, params map[string]any) (ToolResult, error) {
			added, duplicates, rejected, invalid := 0, 0, 0, 0
			items, _ := params["observations"].([]any)
			for _, raw := range items {
				obs, _ := raw.(map[string]any)
				if obs == nil {
					rejected++
					continue
				}
				proposedIDs, _ := StringList(obs["sourceEntryIds"])
				sourceEntryIDs, ok := NormalizeSourceEntryIDs(proposedIDs, args.AllowedSourceEntryIDs)
				if !ok {
					rejected++
					continue
				}
				rawContent, _ := obs["content"].(string)
				content, okContent := normalizeObservationContent(rawContent)
				timestamp, _ := obs["timestamp"].(string)
				relevance, _ := obs["relevance"].(string)
				if !okContent || !observationTimestamp.MatchString(timestamp) || !ledger.IsRelevance(relevance) {
					invalid++
					continue
				}
				id := ids.HashID(content)
				if _, dup := accumulated[id]; dup {
					duplicates++
					continue
				}
				accumulated[id] = ledger.Observation{
					ID:             id,
					Content:        content,
					Timestamp:      timestamp,
					Relevance:      ledger.Relevance(relevance),
					SourceEntryIDs: sourceEntryIDs,
					TokenCount:     tokens.ObservationLineTokenCount(tokens.ObservationLine{ID: id, Timestamp: timestamp, Relevance: relevance, Content: content}),
				}
				order = append(order, id)
				added++
			}
			rejectedPart := ""
			if rejected > 0 {
				rejectedPart = fmt.Sprintf(" %d %s rejected for missing or invalid sourceEntryIds.", rejected, jsfmt.Plural(rejected, "observation"))
			}
			if invalid > 0 {
				rejectedPart += fmt.Sprintf(" %d %s rejected for empty content, a timestamp not in 'YYYY-MM-DD HH:MM' form, or an unknown relevance.", invalid, jsfmt.Plural(invalid, "observation"))
			}
			duplicatePart := "."
			if duplicates > 0 {
				duplicatePart = fmt.Sprintf("(%d %s skipped).", duplicates, jsfmt.Plural(duplicates, "duplicate"))
			}
			ack := fmt.Sprintf("Recorded %d new %s ", added, jsfmt.Plural(added, "observation")) + duplicatePart + rejectedPart +
				fmt.Sprintf(" Total so far this run: %d. ", len(accumulated)) +
				"Continue if the chunk still has uncovered content; otherwise stop calling the tool and emit a short plain-text confirmation."
			return ToolResult{Text: ack, Details: map[string]any{"added": added, "duplicates": duplicates, "rejected": rejected + invalid, "total": len(accumulated)}}, nil
		},
	}

	userText := "Current local time: " + serialize.NowTimestamp() + `

CURRENT REFLECTIONS:
` + joinOrEmpty(args.PriorReflections) + `

CURRENT OBSERVATIONS:
` + joinOrEmpty(args.PriorObservations) + `

Compress the following new conversation chunk into observations by calling record_observations one or more times. Do not restate facts already present in current reflections or current observations. Prefer inline conversation timestamps when assigning times; fall back to the current local time above only if no message timestamp applies. Stop calling the tool and reply with a short plain-text confirmation once the chunk is fully covered.

NEW CONVERSATION CHUNK:
` + conversation

	result, err := RunLoop(ctx, args.Stream, args.loopConfig("observer"), ObserverSystem, userText, []Tool{recordObservations})
	if err != nil {
		return ObserverResult{}, err
	}
	if len(accumulated) == 0 {
		if result.StreamError != nil {
			return ObserverResult{}, result.StreamError
		}
		return ObserverResult{}, nil
	}
	observations := make([]ledger.Observation, 0, len(order))
	for _, id := range order {
		observations = append(observations, accumulated[id])
	}
	return ObserverResult{Observations: observations, Partial: result.StreamError != nil || result.HitMaxTurns, StreamError: result.StreamError}, nil
}
