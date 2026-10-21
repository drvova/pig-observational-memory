package ledger

import (
	"strconv"
	"strings"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/jsfmt"
)

// ContextUsageInstructions opens every rendered memory summary.
const ContextUsageInstructions = `These are condensed memories from earlier in this session.

- Reflections: stable, long-lived facts about the user, project, decisions, and constraints. New reflection lines may include ids in brackets.
- Observations: timestamped events from the conversation history, in chronological order. Observation lines include ids in brackets.

Treat these as past records. When entries conflict, the most recent observation reflects the latest known state. Work that prior observations describe as completed should not be redone unless the user explicitly asks to revisit it.

When exact source context is needed for precision or traceability, use the recall tool with the relevant observation or reflection id. This is especially useful when a reflection materially affects a decision or is too compressed to continue confidently. Do not use recall as broad search or inject raw source unless it is needed.`

// ObservationToSummaryLine renders "[id] YYYY-MM-DD HH:MM [relevance] content".
func ObservationToSummaryLine(observation Observation) string {
	return "[" + observation.ID + "] " + observation.Timestamp + " [" + string(observation.Relevance) + "] " + observation.Content
}

// ReflectionToSummaryLine renders "[id] content".
func ReflectionToSummaryLine(reflection Reflection) string {
	return "[" + reflection.ID + "] " + reflection.Content
}

// SummaryObservationsMinShare is the summary budget share reserved for observations.
const SummaryObservationsMinShare = 0.5

// RenderedSummary is a summary and what it kept.
type RenderedSummary struct {
	Text                string
	Reflections         []Reflection
	Observations        []Observation
	OmittedReflections  int
	OmittedObservations int
}

func estimateTokens(text string) int { return (jsfmt.UTF16Len(text) + 3) / 4 }

// selectWithinBudget keeps the newest records whose rendered lines fit budget,
// in original order. A record that does not fit is skipped, not a stopping
// point: one long newest record must not hide shorter older ones.
func selectWithinBudget[T any](records []T, line func(T) string, budget int) ([]T, int) {
	keptIndexes := []int{}
	tokens := 0
	for i := len(records) - 1; i >= 0; i-- {
		cost := estimateTokens(line(records[i])) + 1
		if tokens+cost > budget {
			continue
		}
		tokens += cost
		keptIndexes = append(keptIndexes, i)
	}
	kept := make([]T, 0, len(keptIndexes))
	for i := len(keptIndexes) - 1; i >= 0; i-- {
		kept = append(kept, records[keptIndexes[i]])
	}
	return kept, tokens
}

// RenderSummaryWithBudget renders the memory summary. With maxTokens > 0,
// observations get at least half the budget (newest first), reflections the
// rest, and each side reclaims what the other leaves unused.
func RenderSummaryWithBudget(reflections []Reflection, observations []Observation, maxTokens int) RenderedSummary {
	if len(reflections) == 0 && len(observations) == 0 {
		return RenderedSummary{Reflections: []Reflection{}, Observations: []Observation{}}
	}
	keptReflections := reflections
	keptObservations := observations
	if maxTokens > 0 {
		fixed := estimateTokens(ContextUsageInstructions) + estimateTokens("## Reflections\n## Observations\n\n\n\n") + 40
		budget := max(0, maxTokens-fixed)
		_, reservedTokens := selectWithinBudget(observations, ObservationToSummaryLine, int(float64(budget)*SummaryObservationsMinShare))
		pickedReflections, reflectionTokens := selectWithinBudget(reflections, ReflectionToSummaryLine, budget-reservedTokens)
		keptReflections = pickedReflections
		keptObservations, _ = selectWithinBudget(observations, ObservationToSummaryLine, budget-reflectionTokens)
	}
	omittedReflections := len(reflections) - len(keptReflections)
	omittedObservations := len(observations) - len(keptObservations)
	parts := []string{ContextUsageInstructions}
	if len(keptReflections) > 0 {
		lines := make([]string, 0, len(keptReflections))
		for _, reflection := range keptReflections {
			lines = append(lines, ReflectionToSummaryLine(reflection))
		}
		parts = append(parts, "## Reflections\n"+strings.Join(lines, "\n"))
	}
	if len(keptObservations) > 0 {
		lines := make([]string, 0, len(keptObservations))
		for _, observation := range keptObservations {
			lines = append(lines, ObservationToSummaryLine(observation))
		}
		parts = append(parts, "## Observations\n"+strings.Join(lines, "\n"))
	}
	if omittedReflections > 0 || omittedObservations > 0 {
		omitted := []string{}
		if omittedReflections > 0 {
			omitted = append(omitted, strconv.Itoa(omittedReflections)+" older "+jsfmt.Plural(omittedReflections, "reflection"))
		}
		if omittedObservations > 0 {
			omitted = append(omitted, strconv.Itoa(omittedObservations)+" older "+jsfmt.Plural(omittedObservations, "observation"))
		}
		parts = append(parts, "("+strings.Join(omitted, " and ")+" omitted to fit the summary budget; they remain in the session memory ledger, see /om:view full.)")
	}
	if keptReflections == nil {
		keptReflections = []Reflection{}
	}
	if keptObservations == nil {
		keptObservations = []Observation{}
	}
	return RenderedSummary{
		Text:                strings.Join(parts, "\n\n"),
		Reflections:         keptReflections,
		Observations:        keptObservations,
		OmittedReflections:  omittedReflections,
		OmittedObservations: omittedObservations,
	}
}

// RenderSummary renders the memory summary text.
func RenderSummary(reflections []Reflection, observations []Observation, maxTokens int) string {
	return RenderSummaryWithBudget(reflections, observations, maxTokens).Text
}
