package agents

import (
	"context"
	"fmt"
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
	conversation := strings.TrimSpace(args.Chunk)
	if conversation == "" {
		return nil, nil
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
			added, duplicates, rejected := 0, 0, 0
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
				content, _ := obs["content"].(string)
				timestamp, _ := obs["timestamp"].(string)
				relevance, _ := obs["relevance"].(string)
				if content == "" || timestamp == "" || !ledger.IsRelevance(relevance) {
					rejected++
					continue
				}
				content = serialize.TruncateRecordContent(content)
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
			duplicatePart := "."
			if duplicates > 0 {
				duplicatePart = fmt.Sprintf("(%d %s skipped).", duplicates, jsfmt.Plural(duplicates, "duplicate"))
			}
			ack := fmt.Sprintf("Recorded %d new %s ", added, jsfmt.Plural(added, "observation")) + duplicatePart + rejectedPart +
				fmt.Sprintf(" Total so far this run: %d. ", len(accumulated)) +
				"Continue if the chunk still has uncovered content; otherwise stop calling the tool and emit a short plain-text confirmation."
			return ToolResult{Text: ack, Details: map[string]any{"added": added, "duplicates": duplicates, "rejected": rejected, "total": len(accumulated)}}, nil
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
		return nil, err
	}
	if len(accumulated) == 0 {
		if result.StreamError != nil {
			return nil, result.StreamError
		}
		return nil, nil
	}
	observations := make([]ledger.Observation, 0, len(order))
	for _, id := range order {
		observations = append(observations, accumulated[id])
	}
	return observations, nil
}
