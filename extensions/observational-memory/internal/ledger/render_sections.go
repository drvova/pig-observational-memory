package ledger

import (
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// The kernel's layout for a summary a model's own summary query selected (docs/memory-
// interface.md, section 8): the query decides which records the tables show and how they are
// grouped; the kernel prices every row in the codec, keeps what fits the room budget, and
// always appends the omitted table, which discloses every record of the memory the summary does
// not show, whether the query left it out or it did not fit. A summary query cannot suppress
// that table: it is computed here, from the records, after the query ran.

// OmittedTableName is the table that discloses the records a summary left out. No summary
// query may produce a table of this name.
const OmittedTableName = omittedTable

// SummarySection is one table a summary query produced, with the record id each row shows
// ("" for a row that is no record, such as a count).
type SummarySection struct {
	Table tables.Table
	IDs   []string
	// Sorted is true when the query ordered the rows itself (a sort stage): under budget
	// pressure its first rows are kept first. Otherwise the rows are in their source's order,
	// oldest first, and the newest are kept first.
	Sorted bool
}

const customSummaryBody = `

These tables were selected and grouped by this model's own summary query; each table is named after what it selected. omitted lists the records of the memory these tables do not show, whether the query left them out or they did not fit: date range, count, first id.

Treat these as past records. When records conflict, the most recent reflects the latest known state. Work described as completed should not be redone unless the user explicitly asks.

` + TablesRecallParagraph

// CustomSummaryInstructionsFor is the instructions that open a summary a model's own summary
// query selected, encoded with codec: TablesSummaryIntro, the format, what the tables are and
// TablesRecallParagraph, so TablesSummaryBody reads it as any tables summary.
func CustomSummaryInstructionsFor(codec tables.Codec) string {
	if nativeRows(codec) {
		return TablesSummaryIntro + rowsFormatLine + customSummaryBody
	}
	format, _, _ := strings.Cut(codec.Spec(), "\n")
	return TablesSummaryIntro + " in the " + codec.Name() + " format. " + strings.TrimSpace(format) + customSummaryBody
}

// RenderSectionsSummary lays out the sections a summary query produced under maxTokens (0 or
// less renders every row). When the whole render fits, every row is kept. Otherwise rows are
// kept section by section, in the query's order, newest row first (a source lists its records
// oldest first) or, for a query that sorted its rows, first row first, each costed by its encoded row and each table's header paid once, skipping a
// row that does not fit; the omitted table's cost is reserved first, as the tables layout
// reserves it. Every reflection and observation whose id no kept row shows (with its content:
// ContentField) is disclosed in the omitted table. The kept rows keep their order, and tables left without rows are dropped.
func RenderSectionsSummary(sections []SummarySection, reflections []Reflection, observations []Observation, maxTokens int, options RenderOptions) RenderedSummary {
	if len(reflections) == 0 && len(observations) == 0 {
		return RenderedSummary{Reflections: []Reflection{}, Observations: []Observation{}}
	}
	codec := options.Codec
	if codec == nil {
		codec = tables.Rows
	}
	dates := map[string]string{}
	for id, date := range options.ReflectionDates {
		dates[id] = strings.Replace(date, " to ", "..", 1)
	}
	l := sectionsLayout{codec: codec, sections: sections, reflections: reflections, observations: observations, dates: dates, empty: map[string]int{}}
	all := make([][]bool, len(sections))
	for s, section := range sections {
		all[s] = make([]bool, len(section.Table.Rows))
		for r := range all[s] {
			all[s][r] = true
		}
	}
	full := l.render(all)
	if maxTokens <= 0 || estimateTokens(full.Text) <= maxTokens {
		return full
	}
	fixed := estimateTokens(CustomSummaryInstructionsFor(codec)) + 1
	var kept [][]bool
	reserve := 0
	for range 32 {
		kept = l.selectWithin(max(0, maxTokens-fixed-reserve))
		need := l.omittedCost(l.omitted(kept))
		if need <= reserve {
			break
		}
		reserve = need
	}
	return l.render(kept)
}

type sectionsLayout struct {
	codec        tables.Codec
	sections     []SummarySection
	reflections  []Reflection
	observations []Observation
	dates        map[string]string
	empty        map[string]int
}

func (l *sectionsLayout) emptyCost(t tables.Table) int {
	key := t.Name + "\x00" + strings.Join(t.Fields, "\x00")
	tokens, ok := l.empty[key]
	if !ok {
		tokens = estimateTokens(l.codec.Encode(tables.Table{Name: t.Name, Fields: t.Fields}))
		l.empty[key] = tokens
	}
	return tokens
}

func (l *sectionsLayout) rowCost(t tables.Table, r int) int {
	one := tables.Table{Name: t.Name, Fields: t.Fields, Rows: [][]string{t.Rows[r]}}
	if r < len(t.Absent) {
		one.Absent = [][]bool{t.Absent[r]}
	}
	return estimateTokens(l.codec.Encode(one)) - l.emptyCost(t) + 1
}

// selectWithin keeps rows within budget: sections in order, each newest row first, or first
// row first when the query sorted them.
func (l *sectionsLayout) selectWithin(budget int) [][]bool {
	kept := make([][]bool, len(l.sections))
	used := 0
	for s, section := range l.sections {
		t := section.Table
		kept[s] = make([]bool, len(t.Rows))
		started := false
		for i := range t.Rows {
			r := len(t.Rows) - 1 - i
			if section.Sorted {
				r = i
			}
			cost := l.rowCost(t, r)
			if !started {
				cost += l.emptyCost(t) + 2
			}
			if used+cost > budget {
				continue
			}
			used += cost
			kept[s][r] = true
			started = true
		}
	}
	return kept
}

// ContentField is the field a summary table must carry for its rows to show their records: a
// row that shows an id without the record's content discloses nothing of it, so the record
// stays in the omitted table.
const ContentField = "content"

// shown is the set of record ids the kept rows show: rows of tables that carry ContentField.
func (l *sectionsLayout) shown(kept [][]bool) map[string]bool {
	out := map[string]bool{}
	for s, section := range l.sections {
		if section.Table.Index(ContentField) < 0 {
			continue
		}
		for r, id := range section.IDs {
			if id != "" && r < len(kept[s]) && kept[s][r] {
				out[id] = true
			}
		}
	}
	return out
}

func (l *sectionsLayout) omitted(kept [][]bool) [][]string {
	shown := l.shown(kept)
	return omittedSpans(l.reflections, l.observations, l.dates, shown, shown)
}

func (l *sectionsLayout) omittedCost(rows [][]string) int {
	if len(rows) == 0 {
		return 0
	}
	t := omittedTableOf(rows)
	cost := l.emptyCost(t) + 2
	for r := range rows {
		cost += l.rowCost(t, r)
	}
	return cost
}

func (l *sectionsLayout) render(kept [][]bool) RenderedSummary {
	out := []tables.Table{}
	for s, section := range l.sections {
		t := section.Table
		k := tables.Table{Name: t.Name, Fields: t.Fields}
		for r := range t.Rows {
			if !kept[s][r] {
				continue
			}
			k.Rows = append(k.Rows, t.Rows[r])
			var absent []bool
			if r < len(t.Absent) {
				absent = t.Absent[r]
			}
			k.Absent = append(k.Absent, absent)
		}
		if len(k.Rows) > 0 {
			out = append(out, k)
		}
	}
	shown := l.shown(kept)
	omitted := omittedSpans(l.reflections, l.observations, l.dates, shown, shown)
	if len(omitted) > 0 {
		out = append(out, omittedTableOf(omitted))
	}
	result := RenderedSummary{Reflections: []Reflection{}, Observations: []Observation{}}
	for _, r := range l.reflections {
		if shown[r.ID] {
			result.Reflections = append(result.Reflections, r)
		}
	}
	for _, o := range l.observations {
		if shown[o.ID] {
			result.Observations = append(result.Observations, o)
		}
	}
	result.OmittedReflections = len(l.reflections) - len(result.Reflections)
	result.OmittedObservations = len(l.observations) - len(result.Observations)
	result.Text = CustomSummaryInstructionsFor(l.codec)
	if len(out) > 0 {
		result.Text += "\n\n" + l.codec.Encode(out...)
	}
	return result
}
