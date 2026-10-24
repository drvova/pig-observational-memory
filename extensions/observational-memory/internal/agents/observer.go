package agents

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ids"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/jsfmt"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
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
	// SystemLines are lines appended to the stage's system prompt: the prompt-line rules
	// (docs/memory-interface.md, section 7) that apply to the worker model for this stage.
	SystemLines []string
}

// system is the stage's system prompt base with the worker's SystemLines appended.
func (a WorkerArgs) system(base string) string {
	if len(a.SystemLines) == 0 {
		return base
	}
	return strings.TrimRight(base, "\n") + "\n\nMemory rules for this model:\n- " + strings.Join(a.SystemLines, "\n- ") + "\n"
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
	PriorReflections []string
	// PriorObservations are "[id] ..." lines, ideally with their structured
	// fields (ledger.ObservationToObserverLine).
	PriorObservations     []string
	Chunk                 string
	AllowedSourceEntryIDs []string
	// Codec is the table format of the rows parameter; nil uses RowsCodec.
	Codec Codec
	// LongSpec carries the codec's longer spec with examples (tables.LongSpec) instead of its
	// short one: the per-model harness sets it for a model that keeps making syntax errors.
	LongSpec bool
	// RecordDecode, when set, is told the outcome of every rows table the model sends:
	// DecodeOK, DecodeError (the codec could not parse it) or DecodeColumns (it parsed, but
	// with the wrong columns or tables). The harness counts these per model.
	RecordDecode func(outcome string)
	// Instruction, when set, is a harness instruction the user message carries before the
	// chunk: a require-kind rule's re-ask over the entries it flagged.
	Instruction string
}

// The outcomes ObserverArgs.RecordDecode is told.
const (
	DecodeOK      = "ok"
	DecodeError   = "error"
	DecodeColumns = "columns"
)

func (a ObserverArgs) spec() string {
	if a.LongSpec {
		return tables.LongSpec(a.codec())
	}
	return a.codec().Spec()
}

func (a ObserverArgs) recordDecode(outcome string) {
	if a.RecordDecode != nil {
		a.RecordDecode(outcome)
	}
}

func (a ObserverArgs) codec() Codec {
	if a.Codec == nil {
		return RowsCodec
	}
	return a.Codec
}

// recordObservationsSchema is the record_observations parameter schema, with
// the rows example written in codec.
func recordObservationsSchema(codec Codec, spec string) map[string]any {
	example := codec.Encode(Table{
		Name:   "observations",
		Fields: observationRowFields,
		Rows:   [][]string{{"2026-05-02 10:30", "high", "fact", "baking", "", "", "entry-a", "User bakes sourdough weekly."}},
		Absent: [][]bool{{false, false, false, false, true, true, false, false}},
	})
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"observations": map[string]any{
				"type":        "array",
				"minItems":    1,
				"description": "Batch of new observations (or pass rows instead). May be empty only if the tool is not called at all.",
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
						"kind": map[string]any{
							"type": "string",
							"enum": observationKindEnum(),
						},
						"subject": map[string]any{
							"type":        "string",
							"description": "Short lowercase noun phrase grouping related observations, e.g. 'baking', 'gaming hours'.",
						},
						"scope": map[string]any{
							"type":        "string",
							"description": "Qualifier that tells two figures apart, e.g. 'hard difficulty', 'on edX'.",
						},
						"supersedes": map[string]any{
							"type":        "array",
							"items":       map[string]any{"type": "string", "pattern": ledger.MemoryIDPattern.String()},
							"description": "Ids of earlier observations this one replaces. Only when the user explicitly corrects or replaces them.",
						},
					},
					"required": []any{"timestamp", "content", "relevance", "sourceEntryIds"},
				},
			},
			"rows": map[string]any{
				"type": "string",
				"description": "Alternative to observations: one table in the " + codec.Name() + " format, e.g.\n" + example + "\n" +
					spec + "\n" +
					"Separate several sourceEntryIds or supersedes ids with spaces; leave optional fields empty.",
			},
		},
	}
}

// observationRowFields are the columns of the record_observations rows table.
var observationRowFields = []string{"timestamp", "relevance", "kind", "subject", "scope", "supersedes", "sourceEntryIds", "content"}

// observationRowRequired are the rows-table columns every table must have.
var observationRowRequired = []string{"timestamp", "relevance", "sourceEntryIds", "content"}

func observationKindEnum() []any {
	out := make([]any, 0, len(ledger.ObservationKindValues))
	for _, kind := range ledger.ObservationKindValues {
		out = append(out, string(kind))
	}
	return out
}

// priorObservationID reads the id a "[id] ..." observation line starts with.
var priorObservationID = regexp.MustCompile(`^\[([a-f0-9]{12})\]`)

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

// observationRowItems decodes a record_observations rows table into the same
// item maps the observations parameter carries, so both share one validation
// path. Absent cells are left out; sourceEntryIds and supersedes split on
// white space. Unknown or missing required columns fail the whole table.
func observationRowItems(codec Codec, text string) ([]any, string, error) {
	tables, err := codec.Decode(strings.TrimSpace(text))
	if err != nil {
		return nil, DecodeError, err
	}
	items, err := observationTableItems(tables)
	if err != nil {
		return nil, DecodeColumns, err
	}
	return items, DecodeOK, nil
}

// observationTableItems checks a decoded rows text's shape (one table, known columns, the
// required ones present) and turns its rows into item maps.
func observationTableItems(tables []Table) ([]any, error) {
	if len(tables) != 1 {
		return nil, fmt.Errorf("expected one table, found %d; send exactly one observations table", len(tables))
	}
	table := tables[0]
	allowed := map[string]bool{}
	for _, field := range observationRowFields {
		allowed[field] = true
	}
	for _, field := range table.Fields {
		if !allowed[field] {
			return nil, fmt.Errorf("unknown column %q; use only %s", field, strings.Join(observationRowFields, ","))
		}
	}
	for _, field := range observationRowRequired {
		if table.Index(field) < 0 {
			return nil, fmt.Errorf("missing column %q; the table needs %s", field, strings.Join(observationRowRequired, ","))
		}
	}
	items := make([]any, 0, len(table.Rows))
	for r := range table.Rows {
		item := map[string]any{}
		for _, field := range table.Fields {
			value, ok := table.Cell(r, field)
			if !ok {
				continue
			}
			if field == "sourceEntryIds" || field == "supersedes" {
				list := []any{}
				for _, id := range strings.Fields(value) {
					list = append(list, id)
				}
				item[field] = list
				continue
			}
			item[field] = value
		}
		items = append(items, item)
	}
	return items, nil
}

// applyObservationStructure copies an item's optional structured fields onto
// observation: a kind outside the enum is cleared, the subject is normalized,
// and supersedes keeps only ids isKnown accepts (never the record's own). It
// returns how many supersedes ids it dropped.
func applyObservationStructure(observation *ledger.Observation, item map[string]any, isKnown func(string) bool) int {
	if kind, _ := item["kind"].(string); ledger.IsObservationKind(kind) {
		observation.Kind = ledger.ObservationKind(kind)
	}
	if subject, ok := item["subject"].(string); ok {
		observation.Subject = ledger.NormalizeSubject(subject)
	}
	if scope, ok := item["scope"].(string); ok {
		observation.Scope = ledger.NormalizeScope(scope)
	}
	proposed, _ := StringList(item["supersedes"])
	if text, ok := item["supersedes"].(string); ok {
		proposed = strings.Fields(text)
	}
	dropped := 0
	seen := map[string]bool{}
	for _, id := range proposed {
		id = strings.TrimSpace(id)
		if seen[id] {
			continue
		}
		seen[id] = true
		if !ledger.IsMemoryID(id) || id == observation.ID || !isKnown(id) {
			dropped++
			continue
		}
		observation.Supersedes = append(observation.Supersedes, id)
	}
	return dropped
}

// ackClipRunes bounds the content echoed next to each id in the tool receipt.
const ackClipRunes = 60

// clipForAck shortens content for the tool receipt, so the model can match
// each recorded id to the observation it wrote.
func clipForAck(content string) string {
	runes := []rune(content)
	if len(runes) <= ackClipRunes {
		return content
	}
	return strings.TrimSpace(string(runes[:ackClipRunes])) + "…"
}

// RunObserverDetailed is RunObserver with the run's completeness.
func RunObserverDetailed(ctx context.Context, args ObserverArgs) (ObserverResult, error) {
	conversation := strings.TrimSpace(args.Chunk)
	if conversation == "" {
		return ObserverResult{}, nil
	}
	codec := args.codec()
	accumulated := map[string]ledger.Observation{}
	order := []string{}
	known := map[string]bool{}
	for _, line := range args.PriorObservations {
		if m := priorObservationID.FindStringSubmatch(line); m != nil {
			known[m[1]] = true
		}
	}
	recordObservations := Tool{
		Name:  "record_observations",
		Label: "Record observations",
		Description: "Record a batch of new observations distilled from the conversation chunk. " +
			"Call this multiple times as you work through the chunk. Stop calling when coverage is complete, " +
			"then emit a short plain-text confirmation to end the run.",
		Parameters: recordObservationsSchema(codec, args.spec()),
		Execute: func(_ context.Context, params map[string]any) (ToolResult, error) {
			added, duplicates, rejected, invalid, droppedSupersedes := 0, 0, 0, 0, 0
			items, _ := params["observations"].([]any)
			if rowsText, _ := params["rows"].(string); strings.TrimSpace(rowsText) != "" {
				rowItems, outcome, err := observationRowItems(codec, rowsText)
				args.recordDecode(outcome)
				if err != nil {
					return ToolResult{Text: "rows not recorded: " + err.Error() + ". Nothing from this call was recorded; fix the table and call again.", Details: map[string]any{"added": 0, "total": len(accumulated)}}, nil
				}
				items = append(append([]any(nil), items...), rowItems...)
			}
			// First pass: validate every item, so the ids a supersedes may name
			// from this same call are only those of items that will be recorded.
			type candidate struct {
				observation ledger.Observation
				item        map[string]any
			}
			var candidates []candidate
			batch := map[string]bool{}
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
				if _, dup := accumulated[id]; dup || batch[id] {
					duplicates++
					continue
				}
				batch[id] = true
				candidates = append(candidates, candidate{item: obs, observation: ledger.Observation{
					ID:             id,
					Content:        content,
					Timestamp:      timestamp,
					Relevance:      ledger.Relevance(relevance),
					SourceEntryIDs: sourceEntryIDs,
					TokenCount:     tokens.ObservationLineTokenCount(tokens.ObservationLine{ID: id, Timestamp: timestamp, Relevance: relevance, Content: content}),
				}})
			}
			// A supersedes id must name an observation already in memory,
			// recorded earlier this run, or validated in this same call.
			isKnown := func(id string) bool {
				_, recorded := accumulated[id]
				return known[id] || recorded || batch[id]
			}
			recordedIDs := make([]string, 0, len(candidates))
			for _, c := range candidates {
				observation := c.observation
				droppedSupersedes += applyObservationStructure(&observation, c.item, isKnown)
				accumulated[observation.ID] = observation
				order = append(order, observation.ID)
				recordedIDs = append(recordedIDs, observation.ID+" ("+clipForAck(observation.Content)+")")
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
			if droppedSupersedes > 0 {
				rejectedPart += fmt.Sprintf(" %d supersedes %s dropped: not the id of a current or newly recorded observation.", droppedSupersedes, jsfmt.Plural(droppedSupersedes, "id"))
			}
			idsPart := ""
			if len(recordedIDs) > 0 {
				idsPart = " Recorded ids (cite them in supersedes to correct these later this run): " + strings.Join(recordedIDs, "; ") + "."
			}
			ack := fmt.Sprintf("Recorded %d new %s ", added, jsfmt.Plural(added, "observation")) + duplicatePart + rejectedPart + idsPart +
				fmt.Sprintf(" Total so far this run: %d. ", len(accumulated)) +
				"Continue if the chunk still has uncovered content; otherwise stop calling the tool and emit a short plain-text confirmation."
			return ToolResult{Text: ack, Details: map[string]any{"added": added, "duplicates": duplicates, "rejected": rejected + invalid, "total": len(accumulated)}}, nil
		},
	}

	instruction := ""
	if strings.TrimSpace(args.Instruction) != "" {
		instruction = "\n\n" + strings.TrimSpace(args.Instruction)
	}
	userText := "Current local time: " + serialize.NowTimestamp() + `

CURRENT REFLECTIONS:
` + joinOrEmpty(args.PriorReflections) + `

CURRENT OBSERVATIONS:
` + joinOrEmpty(args.PriorObservations) + `

Compress the following new conversation chunk into observations by calling record_observations one or more times. Do not restate facts already present in current reflections or current observations. Prefer inline conversation timestamps when assigning times; fall back to the current local time above only if no message timestamp applies. Stop calling the tool and reply with a short plain-text confirmation once the chunk is fully covered.` + instruction + `

ROWS FORMAT (` + codec.Name() + `), for the rows parameter:
` + args.spec() + `

NEW CONVERSATION CHUNK:
` + conversation

	result, err := RunLoop(ctx, args.Stream, args.loopConfig("observer"), args.system(ObserverSystem), userText, []Tool{recordObservations})
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
