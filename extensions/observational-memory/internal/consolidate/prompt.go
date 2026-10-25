package consolidate

import (
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/selection"
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
	// Branch is the current branch with level 0's records folded in, and Folded its ledger:
	// they place the records the current memory leaves out, so each omitted span is written
	// with the memory query that lists it (selection.OmittedSpans).
	Branch []ledger.Entry
	Folded ledger.FoldedLedger
	// Curated are the curated memories that apply in the project, in the index's order; the
	// input lists them while room remains, so level 1 may cite them (Citation).
	Curated []CuratedMemory
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
	// MemoryQueries are the memory queries the omitted spans of the current memory print, in
	// order; none when nothing is omitted or the spans did not fit (the omitted line then says
	// so).
	MemoryQueries []string
	// SubjectsShown reports whether the subjects section fit; when it did not, a line says so.
	SubjectsShown bool
	// Links are the possible updates listed, each between two records the prompt shows;
	// OmittedLinks did not fit, and a line says they stay unsettled.
	Links        []Link
	OmittedLinks int
	// Curated are the curated memories listed; OmittedCurated did not fit, and a line says so.
	Curated        []CuratedMemory
	OmittedCurated int
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
	headingLinks        = "POSSIBLE UPDATES (code found a later record giving a different value of the same type on the same subject and scope; it cannot tell an update from a second value. Settle one with links: confirm when the later value replaces the earlier, reject when both hold. Unsettled ones stay, and both records stay shown):"
	headingInstructions = "INSTRUCTIONS FOR THIS COMPACTION (from the user):"
)

// RecordLine is how the level-1 input shows a record: its observer line (id, time, relevance,
// text, structured fields), then an attempt's outcome and a level-0 status, so a plan is never
// read as done.
func RecordLine(o ledger.Observation) string {
	line := ledger.ObservationToObserverLine(o)
	if o.Outcome != "" {
		line += " (outcome: " + o.Outcome + ")"
	}
	if o.Status != "" {
		line += " (status: " + o.Status + ")"
	}
	return line
}

// ReflectionLine is how the level-1 input shows a reflection: its id, text and supports.
func ReflectionLine(r ledger.Reflection) string {
	return ledger.ReflectionToSummaryLine(r) + " (supports: " + strings.Join(r.SupportingObservationIDs, " ") + ")"
}

// BuildPrompt builds the level-1 input under in.Available (docs/memory-v2.md, level 1's "Input,
// in order"): the cut range, its level-0 records as candidates, the current memory bounded by
// the summary budget, the possible updates between records shown (Links), the subjects shown,
// the curated memories (Curated), and PiG's customInstructions, in that order. Room is given by
// priority instead: the instructions, then the candidates (one that does not fit is skipped),
// then the current memory newest first, then the possible updates, then the subjects, then the
// curated memories, and the cut range gets what remains (what does not fit stays level 0 only). Whatever is left out is said: the omitted
// candidates, the older memory, the possible updates, the subjects, the curated memories and
// the later cut entries
// each get a line
// whose room is reserved before anything is filled, so the model is always told what it is not
// shown; the older memory's line is followed, room permitting, by its omitted spans, each with
// the runnable memory query that lists it (selection.OmittedSpans, as the summary writes
// them). ok is false when no candidate fits, so no request is worth making.
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
		memoryNotice = tokens.EstimateStringTokens("\n\n" + headingMemory + "\n" + omittedMemoryLine(len(in.Active), len(in.Reflections), false))
	}
	subjectsNotice := 0
	if len(shownSubjects(in.Candidates, in.Active)) > 0 {
		subjectsNotice = tokens.EstimateStringTokens("\n\n" + headingSubjects + " " + subjectsNotShown)
	}
	linksNotice := 0
	if all := possibleUpdates(in.Candidates, in.Active); len(all) > 0 {
		linksNotice = tokens.EstimateStringTokens("\n\n" + headingLinks + "\n" + omittedLinksLine(len(all)))
	}
	curatedNotice := 0
	if len(in.Curated) > 0 {
		curatedNotice = tokens.EstimateStringTokens("\n\n" + headingCurated + "\n" + omittedCuratedLine(len(in.Curated)))
	}
	cutNotice := max(tokens.EstimateStringTokens(cutNotShown), tokens.EstimateStringTokens(laterEntriesNote(len(in.Cut))))
	remaining -= candidateNotice + memoryNotice + subjectsNotice + linksNotice + curatedNotice + cutNotice
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

	// The current memory under the summary budget, and the omitted spans of what it leaves out,
	// each with the memory query that lists it. The spans are paid from the room left after the
	// memory shown; when they do not fit, the memory is chosen again under a budget less what
	// they needed, so the reserve only grows (bounded by the room, and the rounds by the
	// records' count, since a round that changes nothing ends the loop). When even the most
	// folded spans do not fit, the omitted line gives the counts alone and says why.
	var memory memoryChoice
	var spans selection.Spans
	spansFit := false
	if len(in.Active)+len(in.Reflections) > 0 {
		reserve := 0
		for range len(in.Active) + len(in.Reflections) + 2 {
			memory = chooseMemory(in, min(remaining-reserve, in.MemoryBudget))
			if memory.omittedRecords+memory.omittedReflections == 0 {
				spans, spansFit = selection.Spans{}, true
				break
			}
			var need int
			spans, need, spansFit = selection.OmittedSpans(selection.SpansInput{
				Reflections:  in.Reflections,
				Observations: append(append([]ledger.Observation{}, in.Candidates...), in.Active...),
				Listed:       memory.listed(in.Candidates),
				Folded:       in.Folded,
				Branch:       in.Branch,
				Room:         remaining - memory.cost,
			})
			if spansFit || need <= reserve || need > remaining {
				break
			}
			reserve = need
		}
	}
	memorySection := ""
	if len(in.Active)+len(in.Reflections) > 0 {
		out.Active, out.Reflections = memory.active, memory.reflections
		out.OmittedActive, out.OmittedReflections = memory.omittedRecords, memory.omittedReflections
		parts := []string{headingMemory}
		if len(memory.reflectionLines) > 0 {
			parts = append(parts, headingReflections+"\n"+strings.Join(memory.reflectionLines, "\n"))
		}
		if len(memory.recordLines) > 0 {
			parts = append(parts, headingRecords+"\n"+strings.Join(memory.recordLines, "\n"))
		}
		if out.OmittedActive+out.OmittedReflections > 0 {
			parts = append(parts, omittedMemoryLine(out.OmittedActive, out.OmittedReflections, spansFit))
			if spansFit && len(spans.Lines) > 0 {
				text := strings.Join(spans.Lines, "\n")
				parts = append(parts, text)
				remaining -= tokens.EstimateStringTokens("\n" + text)
				out.MemoryQueries = spans.Queries
			}
		}
		memorySection = strings.Join(parts, "\n")
		remaining -= memory.cost
	}

	// The possible updates between records shown, each line while it fits, and a line saying how
	// many did not, paid from the room reserved for it.
	linksSection := ""
	if linksNotice > 0 {
		remaining += linksNotice
		shown := append(append([]ledger.Observation{}, out.Candidates...), out.Active...)
		links := possibleUpdates(shown, nil)
		if len(links) > 0 {
			lines := []string{headingLinks}
			cost := tokens.EstimateStringTokens("\n\n" + headingLinks + "\n" + omittedLinksLine(len(links)))
			for _, l := range links {
				line := linkLine(l)
				c := tokens.EstimateStringTokens(line + "\n")
				if cost+c > remaining {
					out.OmittedLinks++
					continue
				}
				cost += c
				lines = append(lines, line)
				out.Links = append(out.Links, l)
			}
			if out.OmittedLinks > 0 {
				lines = append(lines, omittedLinksLine(out.OmittedLinks))
			}
			linksSection = strings.Join(lines, "\n")
			remaining -= tokens.EstimateStringTokens("\n\n" + linksSection)
		}
	}

	// The subjects of the records shown, or a line saying they did not fit, paid from the room
	// reserved for it.
	subjectsSection := ""
	if subjects := shownSubjects(out.Candidates, out.Active); len(subjects) > 0 {
		remaining += subjectsNotice
		section := headingSubjects + " " + strings.Join(subjects, ", ")
		if cost := tokens.EstimateStringTokens("\n\n" + section); cost <= remaining {
			subjectsSection = section
			out.SubjectsShown = true
			remaining -= cost
		} else {
			subjectsSection = headingSubjects + " " + subjectsNotShown
			remaining -= subjectsNotice
		}
	}

	// The curated memories, each line while it fits, and a line saying how many did not, paid
	// from the room reserved for it; the cut range gets what remains after them.
	curatedSection := ""
	if curatedNotice > 0 {
		remaining += curatedNotice
		lines := []string{headingCurated}
		cost := tokens.EstimateStringTokens("\n\n" + headingCurated + "\n" + omittedCuratedLine(len(in.Curated)))
		for _, m := range in.Curated {
			line := curatedLine(m)
			c := tokens.EstimateStringTokens(line + "\n")
			if cost+c > remaining {
				out.OmittedCurated++
				continue
			}
			cost += c
			lines = append(lines, line)
			out.Curated = append(out.Curated, m)
		}
		if out.OmittedCurated > 0 {
			lines = append(lines, omittedCuratedLine(out.OmittedCurated))
		}
		curatedSection = strings.Join(lines, "\n")
		remaining -= tokens.EstimateStringTokens("\n\n" + curatedSection)
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
	for _, s := range []string{memorySection, linksSection, subjectsSection, curatedSection, instructions} {
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
// did not fit: with spans, the span lines that follow it list them with their memory queries;
// without, it says there was no room for those. The variant without spans is the longer, so
// the reserve takes it.
func omittedMemoryLine(records, reflections int, spans bool) string {
	line := fmt.Sprintf("omitted: %d older records and %d older reflections are not shown; they stay as they are and cannot be cited.", records, reflections)
	if spans {
		return line + " Each line below is a span of them: dates, count, first and last entry id, and the memory query that lists it."
	}
	return line + " There is no room for the spans and the memory queries that list them; the memory tool's notes and reflections sources return them after this compaction."
}

// linkLine is how the level-1 input lists a possible update.
func linkLine(l Link) string {
	return l.Later + " may update " + l.Earlier
}

// omittedLinksLine closes the possible updates when n of them did not fit.
func omittedLinksLine(n int) string {
	return fmt.Sprintf("omitted: %d more possible updates are not shown; they stay unsettled and cannot be settled here.", n)
}

// possibleUpdates are the possible updates (ledger.Observation.PossiblyUpdates) between records
// of shown, in order: a link whose earlier record is not among them cannot be settled here. With
// also, the links into also's records count too (the reserve's upper bound, before the shown
// records are chosen).
func possibleUpdates(shown, also []ledger.Observation) []Link {
	ids := map[string]bool{}
	for _, list := range [][]ledger.Observation{shown, also} {
		for _, o := range list {
			ids[o.ID] = true
		}
	}
	out := []Link{}
	for _, list := range [][]ledger.Observation{shown, also} {
		for _, o := range list {
			for _, earlier := range o.PossiblyUpdates {
				if ids[earlier] && earlier != o.ID {
					out = append(out, Link{Later: o.ID, Earlier: earlier})
				}
			}
		}
	}
	return out
}

// subjectsNotShown stands for the subjects section when it does not fit.
const subjectsNotShown = "(not shown: no room; reuse the subjects on the records above)"

// memoryChoice is the current memory shown under one budget.
type memoryChoice struct {
	active                             []ledger.Observation
	reflections                        []ledger.Reflection
	reflectionLines, recordLines       []string
	omittedRecords, omittedReflections int
	// cost is what the shown lines and their headings cost.
	cost int
}

// listed are the ids the omitted spans leave out: the memory shown, and every candidate (shown
// above, or announced by the omitted-candidates line).
func (m memoryChoice) listed(candidates []ledger.Observation) map[string]bool {
	out := map[string]bool{}
	for _, o := range candidates {
		out[o.ID] = true
	}
	for _, o := range m.active {
		out[o.ID] = true
	}
	for _, r := range m.reflections {
		out[r.ID] = true
	}
	return out
}

// chooseMemory shows the current memory under budget: reflections, then records, each newest
// first; the first of each that does not fit ends its list, so what is left out is the older
// part.
func chooseMemory(in PromptInput, budget int) memoryChoice {
	var m memoryChoice
	m.cost = tokens.EstimateStringTokens(headingReflections + "\n" + headingRecords + "\n")
	full := m.cost > budget
	for i := len(in.Reflections) - 1; i >= 0; i-- {
		line := ReflectionLine(in.Reflections[i])
		cost := tokens.EstimateStringTokens(line + "\n")
		if full || m.cost+cost > budget {
			full = true
			m.omittedReflections++
			continue
		}
		m.cost += cost
		m.reflectionLines = append([]string{line}, m.reflectionLines...)
		m.reflections = append([]ledger.Reflection{in.Reflections[i]}, m.reflections...)
	}
	// Records fill what the reflections left, newest first, stopping at their own first misfit.
	full = m.cost > budget
	for i := len(in.Active) - 1; i >= 0; i-- {
		line := RecordLine(in.Active[i])
		cost := tokens.EstimateStringTokens(line + "\n")
		if full || m.cost+cost > budget {
			full = true
			m.omittedRecords++
			continue
		}
		m.cost += cost
		m.recordLines = append([]string{line}, m.recordLines...)
		m.active = append([]ledger.Observation{in.Active[i]}, m.active...)
	}
	return m
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
