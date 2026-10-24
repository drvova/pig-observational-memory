package agents

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ids"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/jsfmt"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// ReflectorArgs are one reflector run's inputs.
type ReflectorArgs struct {
	WorkerArgs
	Reflections  []ledger.Reflection
	Observations []ledger.Observation
	// Subjects are the memory's canonical subjects with their note counts, most used first
	// (ledger.CountCanonicalSubjectsWithAliases). With two or more the prompt lists them and
	// the run offers alias_subjects; with fewer the prompt is as it was without subjects.
	Subjects []ledger.SubjectCount
	// SubjectsPresent are the subjects of the memory's active notes as written, which the
	// kernel checks a proposed alias against (ledger.AcceptNewSubjectAliases).
	SubjectsPresent []string
	// Aliases are the subject aliases already recorded, in ledger order.
	Aliases []ledger.SubjectAlias
	// AliasesInForce are the recorded aliases that apply (ledger.CheckSubjectAliases), within
	// what the subject list leaves of its budget share (ledger.BoundAliasList). With Subjects
	// listed, the prompt lists them after the subjects so the run can correct or undo one.
	AliasesInForce []ledger.SubjectAlias
	// RecordAliases, when set, receives the aliases the run's alias_subjects calls had
	// accepted, once, when the run ends without failing; it is not called when none was.
	RecordAliases func([]ledger.SubjectAlias)
}

// aliasSubjectsSchema is the alias_subjects parameter schema.
var aliasSubjectsSchema = map[string]any{
	"type": "object",
	"properties": map[string]any{
		"aliases": map[string]any{
			"type":     "array",
			"minItems": 1,
			"maxItems": ledger.MaxSubjectAliasesPerEntry,
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"from":   map[string]any{"type": "string", "minLength": 1, "description": "A subject from CURRENT SUBJECTS, the one with fewer notes, or the from of an alias in CURRENT ALIASES to correct or undo it."},
					"to":     map[string]any{"type": "string", "minLength": 1, "description": "The subject from CURRENT SUBJECTS that names the same topic, the one with more notes; from itself undoes the alias in force for from."},
					"reason": map[string]any{"type": "string", "description": "Why both name the same topic for this user, in a few words."},
				},
				"required": []any{"from", "to"},
			},
		},
	},
	"required": []any{"aliases"},
}

// aliasSubjectsTool is the reflector's alias_subjects tool: each call's aliases are checked by
// the kernel after the recorded ones and those accepted earlier this run
// (ledger.AcceptNewSubjectAliases), and the accepted ones are appended to *accepted.
func aliasSubjectsTool(args ReflectorArgs, accepted *[]ledger.SubjectAlias) Tool {
	return Tool{
		Name:        "alias_subjects",
		Label:       "Alias subjects",
		Description: "Declare that a subject names the same topic as another subject for this user, so their notes group together.",
		Parameters:  aliasSubjectsSchema,
		Execute: func(_ context.Context, params map[string]any) (ToolResult, error) {
			items, _ := params["aliases"].([]any)
			proposed := []ledger.SubjectAlias{}
			malformed := 0
			for _, raw := range items {
				m, _ := raw.(map[string]any)
				from, _ := m["from"].(string)
				to, _ := m["to"].(string)
				reason, _ := m["reason"].(string)
				alias := ledger.SubjectAlias{From: ledger.NormalizeSubject(from), To: ledger.NormalizeSubject(to), Reason: strings.Join(strings.Fields(reason), " ")}
				if alias.From == "" || alias.To == "" || len(*accepted)+len(proposed) >= ledger.MaxSubjectAliasesPerEntry {
					malformed++
					continue
				}
				proposed = append(proposed, alias)
			}
			recorded := append(append([]ledger.SubjectAlias(nil), args.Aliases...), *accepted...)
			added, refused := ledger.AcceptNewSubjectAliases(args.SubjectsPresent, recorded, proposed)
			*accepted = append(*accepted, added...)
			parts := []string{fmt.Sprintf("Recorded %d %s.", len(added), jsfmt.Plural(len(added), "alias"))}
			for _, r := range refused {
				parts = append(parts, fmt.Sprintf("Ignored %s -> %s: %s.", r.Alias.From, r.Alias.To, r.Reason))
			}
			if malformed > 0 {
				parts = append(parts, fmt.Sprintf("Ignored %d %s without from and to, or over the limit of %d this run.", malformed, jsfmt.Plural(malformed, "alias"), ledger.MaxSubjectAliasesPerEntry))
			}
			return ToolResult{Text: strings.Join(parts, " "), Details: map[string]any{"added": len(added), "refused": len(refused) + malformed, "total": len(*accepted)}}, nil
		},
	}
}

// aliasesSection is the reflector prompt's list of the aliases in force, after a blank line;
// "" when none is.
func aliasesSection(aliases []ledger.SubjectAlias) string {
	if len(aliases) == 0 {
		return ""
	}
	lines := make([]string, len(aliases))
	for i, a := range aliases {
		lines[i] = a.Line()
	}
	return "\n\n" + ledger.AliasesHeader + "\n" + strings.Join(lines, "\n")
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
				// Like observations, count the rendered line ([id] content) so the
				// stored footprint matches what summaries and prompts re-render.
				accumulated[id] = ledger.Reflection{ID: id, Content: content, SupportingObservationIDs: supporting, TokenCount: tokens.EstimateStringTokens(ledger.ReflectionToSummaryLine(ledger.Reflection{ID: id, Content: content}))}
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
	tools := []Tool{recordReflections}
	subjects, closing := "", ""
	var aliases []ledger.SubjectAlias
	if len(args.Subjects) >= 2 {
		subjects = subjectsSection(args.Subjects) + aliasesSection(args.AliasesInForce)
		closing = " If two current subjects name the same topic for this user, call alias_subjects."
		tools = append(tools, aliasSubjectsTool(args, &aliases))
	}
	userText := "CURRENT REFLECTIONS:\n" + joinOrEmpty(reflectionLines) + "\n\nCURRENT OBSERVATIONS:\n" + joinOrEmpty(observationLines) + subjects + "\n\nCrystallize any missing durable facts or patterns into new reflections. If nothing is stable enough, do not call the tool." + closing

	result, err := RunLoop(ctx, args.Stream, args.loopConfig("reflector"), args.system(kernel.TextReflectorRules, ReflectorInstructions), userText, tools)
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
	if len(aliases) > 0 && args.RecordAliases != nil {
		args.RecordAliases(aliases)
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
		"acceptedAliasCount":             len(aliases),
		"coverageTransitionsByRelevance": SummarizeCoverageTransitionsByRelevance(args.Observations, coverage, after),
	})
	if len(acceptedReflections) == 0 {
		return nil, nil
	}
	return acceptedReflections, nil
}
