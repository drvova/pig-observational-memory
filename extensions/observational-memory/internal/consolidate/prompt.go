package consolidate

import (
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// PromptInput is what the level-1 request may show, and the room it has.
type PromptInput struct {
	// Cut are the source entries the compaction removes, in branch order.
	Cut []ledger.Entry
	// SkipTools are the memory tools whose calls and results the cut range leaves out.
	SkipTools []string
	// Candidates are the level-0 records of the cut range, in ledger order.
	Candidates []ledger.Observation
	// Active are the other active records competing for the next summary, in ledger order.
	Active []ledger.Observation
	// Reflections are the active reflections, in ledger order.
	Reflections []ledger.Reflection
	// Instructions are PiG's customInstructions for this compaction.
	Instructions string
	// Available is the room for the user text, in estimated tokens: the window less PiG's
	// safety margin, the output cap, the system prompt and the tool schema.
	Available int
	// MemoryBudget bounds the current-memory section: the summary budget, since only what can
	// compete for the next summary is worth showing.
	MemoryBudget int
}

// Prompt is the level-1 user text and what it shows; validation accepts only ids it shows.
type Prompt struct {
	Text        string
	Tokens      int
	Candidates  []ledger.Observation
	Active      []ledger.Observation
	Reflections []ledger.Reflection
	// OmittedCandidates, OmittedActive and OmittedReflections did not fit.
	OmittedCandidates, OmittedActive, OmittedReflections int
	// CutShown of CutTotal source entries were shown.
	CutShown, CutTotal int
}

// The section headings of the level-1 input.
const (
	headingCut          = "COMPACTED CONVERSATION (leaves the context with this compaction):"
	headingCandidates   = "CANDIDATES (records code extracted from the compacted conversation; label, merge or leave them):"
	headingMemory       = "CURRENT MEMORY (active records and reflections competing for the next summary; you may merge records, cite them, or replace reflections):"
	headingReflections  = "Reflections:"
	headingRecords      = "Records:"
	headingSubjects     = "CURRENT SUBJECTS:"
	headingInstructions = "INSTRUCTIONS FOR THIS COMPACTION (from the user):"
)

// RecordLine is how the level-1 input shows a record: its observer line (id, time, relevance,
// text, structured fields), then an attempt's outcome.
func RecordLine(o ledger.Observation) string {
	line := ledger.ObservationToObserverLine(o)
	if o.Outcome != "" {
		line += " (outcome: " + o.Outcome + ")"
	}
	return line
}

// ReflectionLine is how the level-1 input shows a reflection: its id, text and supports.
func ReflectionLine(r ledger.Reflection) string {
	return ledger.ReflectionToSummaryLine(r) + " (supports: " + strings.Join(r.SupportingObservationIDs, " ") + ")"
}

// BuildPrompt builds the level-1 input under in.Available (docs/memory-v2.md, level 1's "Input,
// in order"): the cut range, its level-0 records as candidates, the current memory bounded by
// the summary budget, the subjects shown, and PiG's customInstructions, in that order. Room is
// given by priority instead: the instructions, then the candidates (one that does not fit is
// skipped), then the current memory newest first, then the subjects, and the cut range gets
// what remains (what does not fit stays level 0 only). Whatever is left out is said: the
// omitted candidates, the older memory and the later cut entries each get a line whose room is
// reserved before anything is filled, so the model is always told what it is not shown. ok is
// false when no candidate fits, so no request is worth making.
func BuildPrompt(in PromptInput) (Prompt, bool) {
	out := Prompt{CutTotal: len(in.Cut)}
	instructions := ""
	if text := strings.TrimSpace(in.Instructions); text != "" {
		instructions = headingInstructions + "\n" + text
	}
	fixed := strings.Join([]string{headingCut, headingCandidates, instructions}, "\n\n")
	remaining := in.Available - tokens.EstimateStringTokens(fixed)
	// The notices, at their largest counts: what is left out of each part is said within the
	// room reserved here.
	candidateNotice := tokens.EstimateStringTokens("\n" + omittedCandidatesLine(len(in.Candidates)))
	memoryNotice := 0
	if len(in.Active)+len(in.Reflections) > 0 {
		memoryNotice = tokens.EstimateStringTokens("\n\n" + headingMemory + "\n" + omittedMemoryLine(len(in.Active), len(in.Reflections)))
	}
	cutNotice := max(tokens.EstimateStringTokens(cutNotShown), tokens.EstimateStringTokens(laterEntriesNote(len(in.Cut))))
	remaining -= candidateNotice + memoryNotice + cutNotice
	if remaining <= 0 {
		return out, false
	}

	candidateLines := []string{}
	for _, o := range in.Candidates {
		line := RecordLine(o)
		cost := tokens.EstimateStringTokens(line + "\n")
		if cost > remaining {
			out.OmittedCandidates++
			continue
		}
		remaining -= cost
		candidateLines = append(candidateLines, line)
		out.Candidates = append(out.Candidates, o)
	}
	if len(out.Candidates) == 0 {
		return out, false
	}
	if out.OmittedCandidates > 0 {
		candidateLines = append(candidateLines, omittedCandidatesLine(out.OmittedCandidates))
	}

	// The current memory under the summary budget: reflections, then records, each newest first;
	// the first of each that does not fit ends its list, so what is left out is the older part.
	// Its heading and omitted line are paid from the reserved notice.
	memory := min(remaining, in.MemoryBudget)
	memoryCost := tokens.EstimateStringTokens(headingReflections + "\n" + headingRecords + "\n")
	reflectionLines, recordLines := []string{}, []string{}
	full := memoryCost > memory
	for i := len(in.Reflections) - 1; i >= 0; i-- {
		line := ReflectionLine(in.Reflections[i])
		cost := tokens.EstimateStringTokens(line + "\n")
		if full || memoryCost+cost > memory {
			full = true
			out.OmittedReflections++
			continue
		}
		memoryCost += cost
		reflectionLines = append([]string{line}, reflectionLines...)
		out.Reflections = append([]ledger.Reflection{in.Reflections[i]}, out.Reflections...)
	}
	// Records fill what the reflections left, newest first, stopping at their own first misfit.
	full = memoryCost > memory
	for i := len(in.Active) - 1; i >= 0; i-- {
		line := RecordLine(in.Active[i])
		cost := tokens.EstimateStringTokens(line + "\n")
		if full || memoryCost+cost > memory {
			full = true
			out.OmittedActive++
			continue
		}
		memoryCost += cost
		recordLines = append([]string{line}, recordLines...)
		out.Active = append([]ledger.Observation{in.Active[i]}, out.Active...)
	}
	memorySection := ""
	if len(in.Active)+len(in.Reflections) > 0 {
		parts := []string{headingMemory}
		if len(reflectionLines) > 0 {
			parts = append(parts, headingReflections+"\n"+strings.Join(reflectionLines, "\n"))
		}
		if len(recordLines) > 0 {
			parts = append(parts, headingRecords+"\n"+strings.Join(recordLines, "\n"))
		}
		if out.OmittedActive+out.OmittedReflections > 0 {
			parts = append(parts, omittedMemoryLine(out.OmittedActive, out.OmittedReflections))
		}
		memorySection = strings.Join(parts, "\n")
		remaining -= memoryCost
	}

	subjectsSection := ""
	if subjects := shownSubjects(out.Candidates, out.Active); len(subjects) > 0 {
		section := headingSubjects + " " + strings.Join(subjects, ", ")
		if cost := tokens.EstimateStringTokens("\n\n" + section); cost <= remaining {
			subjectsSection = section
			remaining -= cost
		}
	}

	cutText := cutNotShown
	if remaining > 0 && len(in.Cut) > 0 {
		serialized := serialize.SerializeSourceAddressedBranchEntries(in.Cut, serialize.SourceAddressedOptions{MaxTokens: remaining, SkipTools: in.SkipTools})
		if serialized.Text != "" {
			out.CutShown = len(serialized.EntryIDs)
			cutText = serialized.Text
			if rest := countAfter(in.Cut, serialized.CoversUpToID); rest > 0 {
				cutText += laterEntriesNote(rest)
			}
		}
	}

	sections := []string{headingCut + "\n" + cutText, headingCandidates + "\n" + strings.Join(candidateLines, "\n")}
	for _, s := range []string{memorySection, subjectsSection, instructions} {
		if s != "" {
			sections = append(sections, s)
		}
	}
	out.Text = strings.Join(sections, "\n\n")
	out.Tokens = tokens.EstimateStringTokens(out.Text)
	return out, true
}

// cutNotShown stands for the cut range when none of it fits.
const cutNotShown = "(not shown: no room; its records above stand for it)"

// laterEntriesNote is the note after a cut range whose last n entries did not fit.
func laterEntriesNote(n int) string {
	return fmt.Sprintf("\n\n[%d later entries not shown: no room; their records above stand for them]", n)
}

// omittedCandidatesLine is the line after the candidates when n of them did not fit.
func omittedCandidatesLine(n int) string {
	return fmt.Sprintf("omitted: %d more candidates are not shown; they stand as code extracted them and cannot be cited.", n)
}

// omittedMemoryLine is the line closing the current memory when records and reflections of it
// did not fit.
func omittedMemoryLine(records, reflections int) string {
	return fmt.Sprintf("omitted: %d older records and %d older reflections are not shown; they stay as they are and cannot be cited.", records, reflections)
}

// shownSubjects are the distinct subjects of the records shown, in order.
func shownSubjects(lists ...[]ledger.Observation) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, list := range lists {
		for _, o := range list {
			if o.Subject != "" && !seen[o.Subject] {
				seen[o.Subject] = true
				out = append(out, o.Subject)
			}
		}
	}
	return out
}

// countAfter is how many entries of cut come after the entry id (all of them when id is not
// among them).
func countAfter(cut []ledger.Entry, id string) int {
	for i, entry := range cut {
		if entry.ID == id {
			return len(cut) - i - 1
		}
	}
	return len(cut)
}
