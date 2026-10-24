package ledger

import (
	"strconv"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/jsfmt"
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

// DatedReflectionsNote follows ContextUsageInstructions when reflection lines carry dates.
const DatedReflectionsNote = "A date or date range in parentheses after a reflection id is when the observations it distills were recorded."

// DatedReflectionLine renders "[id] (date) content", or "[id] content" when
// the reflection has no known date.
func DatedReflectionLine(reflection Reflection, dates map[string]string) string {
	if date := dates[reflection.ID]; date != "" {
		return "[" + reflection.ID + "] (" + date + ") " + reflection.Content
	}
	return ReflectionToSummaryLine(reflection)
}

// ReflectionDateRanges dates each reflection by the observations it cites:
// "YYYY-MM-DD" when they share a day, "YYYY-MM-DD to YYYY-MM-DD" otherwise.
// Cited observations count even after a drop, since the dates are read from
// the folded ledger's records, not the active pool. Reflections citing no
// dated observation are absent.
func ReflectionDateRanges(folded FoldedLedger) map[string]string {
	dates := map[string]string{}
	for _, reflection := range folded.Reflections {
		first, last := "", ""
		for _, id := range reflection.SupportingObservationIDs {
			observation, ok := folded.ObservationsByID[id]
			if !ok || len(observation.Timestamp) < 10 {
				continue
			}
			day := observation.Timestamp[:10]
			if first == "" || day < first {
				first = day
			}
			if day > last {
				last = day
			}
		}
		switch {
		case first == "":
		case first == last:
			dates[reflection.ID] = first
		default:
			dates[reflection.ID] = first + " to " + last
		}
	}
	return dates
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
	ClippedObservations int
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

// SummaryObservationsRecencyShare is the share of the observation budget kept
// for the newest observations whatever they say, so recent working context
// always survives.
const SummaryObservationsRecencyShare = 0.25

// selectObservationsWithinBudget keeps observations in three passes, each
// newest first and skipping a record that does not fit: the newest ones up to
// the recency share of budget, then those carrying specifics (numbers, relative
// dates, names; see Specifics), then the rest. Kept records come back in
// original order. Without the middle pass a summary under pressure loses its
// oldest facts first, whatever they hold; with it, an old line naming a price or
// a date outlasts newer generic chatter. The passes read only the text, so the
// same records and budget always keep the same lines.
func selectObservationsWithinBudget(observations []Observation, budget int) ([]Observation, int) {
	kept := make([]bool, len(observations))
	tokens := 0
	pass := func(limit int, eligible func(Observation) bool) {
		for i := len(observations) - 1; i >= 0; i-- {
			if kept[i] || !eligible(observations[i]) {
				continue
			}
			cost := estimateTokens(ObservationToSummaryLine(observations[i])) + 1
			if tokens+cost > limit {
				continue
			}
			tokens += cost
			kept[i] = true
		}
	}
	all := func(Observation) bool { return true }
	pass(int(float64(budget)*SummaryObservationsRecencyShare), all)
	pass(budget, func(o Observation) bool { return HasSpecifics(o.Content) })
	pass(budget, all)
	out := []Observation{}
	for i, k := range kept {
		if k {
			out = append(out, observations[i])
		}
	}
	return out, tokens
}

// RenderOptions add detail to a rendered summary.
type RenderOptions struct {
	// ReflectionDates (see ReflectionDateRanges) date the reflection lines.
	ReflectionDates map[string]string
	// ClippedOverflow lists observations the budget cannot hold in full as
	// clipped lines (see selectObservationTiers) instead of omitting them.
	ClippedOverflow bool
}

// ClippedObservationRunes is the content length of a clipped observation line.
const ClippedObservationRunes = 80

// ClippedObservationLine renders "[id] YYYY-MM-DD content", the content cut to
// ClippedObservationRunes.
func ClippedObservationLine(observation Observation) string {
	date, _, _ := strings.Cut(observation.Timestamp, " ")
	content := []rune(observation.Content)
	text := string(content)
	if len(content) > ClippedObservationRunes {
		text = strings.TrimSpace(string(content[:ClippedObservationRunes])) + "…"
	}
	return "[" + observation.ID + "] " + date + " " + text
}

// ClippedObservationsHeading opens the clipped observation lines.
const ClippedObservationsHeading = "## Clipped observations\nLines cut to fit the budget; recall an id for the full record."

// selectObservationTiers lays observations out the way nodecode lays out its
// index: in full within half of budget (selectObservationsWithinBudget), then
// the rest as clipped lines while the whole fits, specifics first and newest
// first; and when that clips every one with room to spare, clipped lines
// become full again in the same order while the whole still fits.
func selectObservationTiers(observations []Observation, budget int) (full, clipped []bool) {
	full = make([]bool, len(observations))
	clipped = make([]bool, len(observations))
	described, tokens := selectObservationsWithinBudget(observations, budget/2)
	byID := map[string]bool{}
	for _, o := range described {
		byID[o.ID] = true
	}
	order := []int{}
	for _, specific := range []bool{true, false} {
		for i := len(observations) - 1; i >= 0; i-- {
			if byID[observations[i].ID] {
				full[i] = true
			} else if HasSpecifics(observations[i].Content) == specific {
				order = append(order, i)
			}
		}
	}
	for _, i := range order {
		cost := estimateTokens(ClippedObservationLine(observations[i])) + 1
		if tokens+cost > budget {
			continue
		}
		tokens += cost
		clipped[i] = true
	}
	for i := range observations {
		if !full[i] && !clipped[i] {
			return full, clipped
		}
	}
	for _, i := range order {
		delta := estimateTokens(ObservationToSummaryLine(observations[i])) - estimateTokens(ClippedObservationLine(observations[i]))
		if tokens+delta > budget {
			continue
		}
		tokens += delta
		full[i], clipped[i] = true, false
	}
	return full, clipped
}

// RenderSummaryWithBudget renders the memory summary with no options.
func RenderSummaryWithBudget(reflections []Reflection, observations []Observation, maxTokens int) RenderedSummary {
	return RenderSummaryWithOptions(reflections, observations, maxTokens, RenderOptions{})
}

// RenderSummaryWithOptions renders the memory summary. With maxTokens > 0,
// observations get at least half the budget (newest first), reflections the
// rest, and each side reclaims what the other leaves unused.
func RenderSummaryWithOptions(reflections []Reflection, observations []Observation, maxTokens int, options RenderOptions) RenderedSummary {
	reflectionLine := func(reflection Reflection) string { return DatedReflectionLine(reflection, options.ReflectionDates) }
	instructions := ContextUsageInstructions
	if len(options.ReflectionDates) > 0 {
		instructions += "\n\n" + DatedReflectionsNote
	}
	if len(reflections) == 0 && len(observations) == 0 {
		return RenderedSummary{Reflections: []Reflection{}, Observations: []Observation{}}
	}
	keptReflections := reflections
	keptObservations := observations
	var clippedObservations []Observation
	if maxTokens > 0 {
		fixed := estimateTokens(instructions) + estimateTokens("## Reflections\n## Observations\n\n\n\n") + 40
		budget := max(0, maxTokens-fixed)
		_, reservedTokens := selectObservationsWithinBudget(observations, int(float64(budget)*SummaryObservationsMinShare))
		pickedReflections, reflectionTokens := selectWithinBudget(reflections, reflectionLine, budget-reservedTokens)
		keptReflections = pickedReflections
		keptObservations, _ = selectObservationsWithinBudget(observations, budget-reflectionTokens)
		if options.ClippedOverflow && len(keptObservations) < len(observations) {
			full, clip := selectObservationTiers(observations, budget-reflectionTokens-estimateTokens(ClippedObservationsHeading)-2)
			keptObservations = []Observation{}
			for i, o := range observations {
				if full[i] {
					keptObservations = append(keptObservations, o)
				} else if clip[i] {
					clippedObservations = append(clippedObservations, o)
				}
			}
		}
	}
	omittedReflections := len(reflections) - len(keptReflections)
	omittedObservations := len(observations) - len(keptObservations) - len(clippedObservations)
	parts := []string{instructions}
	if len(keptReflections) > 0 {
		lines := make([]string, 0, len(keptReflections))
		for _, reflection := range keptReflections {
			lines = append(lines, reflectionLine(reflection))
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
	if len(clippedObservations) > 0 {
		lines := make([]string, 0, len(clippedObservations))
		for _, observation := range clippedObservations {
			lines = append(lines, ClippedObservationLine(observation))
		}
		parts = append(parts, ClippedObservationsHeading+"\n"+strings.Join(lines, "\n"))
	}
	if omittedReflections > 0 || omittedObservations > 0 {
		omitted := []string{}
		if omittedReflections > 0 {
			omitted = append(omitted, strconv.Itoa(omittedReflections)+" older "+jsfmt.Plural(omittedReflections, "reflection"))
		}
		if omittedObservations > 0 {
			omitted = append(omitted, strconv.Itoa(omittedObservations)+" "+jsfmt.Plural(omittedObservations, "observation"))
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
		ClippedObservations: len(clippedObservations),
	}
}

// RenderSummary renders the memory summary text.
func RenderSummary(reflections []Reflection, observations []Observation, maxTokens int) string {
	return RenderSummaryWithBudget(reflections, observations, maxTokens).Text
}
