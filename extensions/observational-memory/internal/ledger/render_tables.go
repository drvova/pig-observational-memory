package ledger

import (
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// TablesUsageInstructions opens every memory summary rendered in the tables
// format with the rows codec (TablesUsageInstructionsFor gives another codec's).
// It starts with TablesSummaryIntro, and its last paragraph,
// TablesRecallParagraph, points at the memory tool.
const TablesUsageInstructions = TablesSummaryIntro + rowsFormatLine + tablesUsageBody

// TablesSummaryIntro starts the instructions of every tables summary, whatever
// its codec, so a reader can find a summary's tables without knowing the codec.
const TablesSummaryIntro = "These are condensed memories from earlier in this session, as tables"

// rowsFormatLine ends the instructions' first sentence for the rows codec.
const rowsFormatLine = ": name[rows]{fields}: then one record per line, values in field order; an empty value means none."

const tablesUsageBody = `

- reflections: stable facts about the user, project, decisions, and constraints; dates is when their observations were recorded.
- notes.<subject>: observations about one subject, oldest first. kind is fact, event, preference, plan, correction or assistant (something the assistant produced); scope tells two figures apart; superseded_by is the id of the record that replaced this one.
- notes: observations without a subject, oldest first.
- when is the date recorded; [=date] is the date a relative phrase refers to; omitted lists records left out to fit: date range, count, first id.

Treat these as past records. When records conflict, the most recent reflects the latest known state. Work described as completed should not be redone unless the user explicitly asks.

` + TablesRecallParagraph

// TablesUsageInstructionsFor is the instructions that open a tables summary
// encoded with codec: TablesUsageInstructions for the native rows codec (or
// nil), else the same text with its first sentence naming the codec and
// carrying the first line of the codec's spec, so the instructions describe the
// format below them. A format a model declared under the name rows is described
// by its own spec, never by the native wording.
func TablesUsageInstructionsFor(codec tables.Codec) string {
	if nativeRows(codec) {
		return TablesUsageInstructions
	}
	format, _, _ := strings.Cut(codec.Spec(), "\n")
	return TablesSummaryIntro + " in the " + codec.Name() + " format. " + strings.TrimSpace(format) + tablesUsageBody
}

// nativeRows reports whether codec is the kernel's native rows codec (nil
// stands for it), the one whose syntax the fixed wording states. A declared
// codec (tables.Declared) is never native, whatever its name.
func nativeRows(codec tables.Codec) bool {
	if codec == nil {
		return true
	}
	if _, declared := tables.DeclaredFormat(codec); declared {
		return false
	}
	return codec.Name() == tables.Rows.Name()
}

// TablesSummaryBody is the encoded tables of a tables summary in any codec:
// the text after its instructions, up to any appended section ("\n\n---\n\n").
// False when summary is not a tables summary.
func TablesSummaryBody(summary string) (string, bool) {
	if !strings.HasPrefix(summary, TablesSummaryIntro) {
		return "", false
	}
	_, body, ok := strings.Cut(summary, TablesRecallParagraph)
	if !ok {
		return "", false
	}
	body, _, _ = strings.Cut(body, "\n\n---\n\n")
	return strings.TrimSpace(body), true
}

// TablesRecallParagraph is the last paragraph of TablesUsageInstructions. It
// names the tool the extension registers and its queries (memquery.ToolName,
// which this package cannot import; memquery's tests check the name).
const TablesRecallParagraph = `The memory tool reads any record back with its exact source, omitted ones included (recall <id>), and searches what these tables condensed (history | search "words"); use it when precision needs the source.`

// Table names and fields of the tables format.
const (
	reflectionsTable = "reflections"
	notesTable       = "notes"
	omittedTable     = "omitted"
)

var (
	reflectionFields  = []string{"id", "dates", "content"}
	subjectNoteFields = []string{"id", "when", "kind", "scope", "content", "superseded_by"}
	plainNoteFields   = []string{"id", "when", "relevance", "content"}
	omittedFields     = []string{"from", "to", "count", "first_id"}
)

// SubjectTableName is the table an observation with subject is listed in:
// "notes." and the subject lowercased, each run of characters a table name
// cannot hold (see tables.ValidName) replaced by "_". It is "" for a subject
// with no letters or digits.
func SubjectTableName(subject string) string {
	var b strings.Builder
	pending := false
	for _, r := range strings.ToLower(subject) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '.' {
			if pending && b.Len() > 0 {
				b.WriteByte('_')
			}
			pending = false
			b.WriteRune(r)
			continue
		}
		pending = true
	}
	name := strings.Trim(b.String(), "-.")
	if name == "" {
		return ""
	}
	return notesTable + "." + name
}

// recordDate is the calendar date of a record timestamp ("YYYY-MM-DD HH:MM"
// or ISO), or the timestamp itself when it does not start with one.
func recordDate(timestamp string) string {
	if t := recordAnchor(timestamp); !t.IsZero() {
		return timestamp[:10]
	}
	return timestamp
}

// recordAnchor parses the date a timestamp starts with, or the zero time.
func recordAnchor(timestamp string) time.Time {
	if len(timestamp) < 10 {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02", timestamp[:10])
	if err != nil {
		return time.Time{}
	}
	return t
}

// tablesLayout holds what the tables format derives from the records once:
// each observation's table and row, each reflection's row, and the costs.
type tablesLayout struct {
	codec        tables.Codec
	reflections  []Reflection
	observations []Observation
	dates        map[string]string
	// groups[i] is observation i's table name; fields maps a table name to
	// its fields.
	groups []string
	fields map[string][]string
	// rows and absent are observation i's cells.
	rows    [][]string
	absent  [][]bool
	costs   observationCosts
	headers map[string]int
	// emptyTokens caches the estimate of each table encoded without rows.
	emptyTokens map[string]int
}

// newTablesLayout prices every row with the codec: a row costs what it adds to
// its table's encoding, plus one token, and a table header costs its encoding
// plus two tokens (its separating newline and a row count with more digits).
// Each cost is then at least the row's or header's own estimate, so the
// summed costs bound the estimate of the whole text.
func newTablesLayout(reflections []Reflection, observations []Observation, options RenderOptions) *tablesLayout {
	codec := options.Codec
	if codec == nil {
		codec = tables.Rows
	}
	l := &tablesLayout{codec: codec, reflections: reflections, observations: observations, dates: map[string]string{}, fields: map[string][]string{}, headers: map[string]int{}, emptyTokens: map[string]int{}}
	for id, date := range options.ReflectionDates {
		l.dates[id] = strings.Replace(date, " to ", "..", 1)
	}
	supersededBy := map[string][]string{}
	for _, observation := range observations {
		for _, id := range observation.Supersedes {
			if id != observation.ID && !containsString(supersededBy[id], observation.ID) {
				supersededBy[id] = append(supersededBy[id], observation.ID)
			}
		}
	}
	plainFields := plainNoteFields
	// Notes group under their canonical subject over every record of the memory, shown or
	// not, so selection never changes which table a record is in.
	canonical := ObservationSubjectsWithAliases(observations, options.Aliases)
	l.groups = make([]string, len(observations))
	for i, observation := range observations {
		l.groups[i] = SubjectTableName(canonical[observation.Subject].Subject)
		if l.groups[i] == "" {
			l.groups[i] = notesTable
			if len(supersededBy[observation.ID]) > 0 {
				plainFields = append(append([]string{}, plainNoteFields...), "superseded_by")
			}
		}
	}
	l.rows = make([][]string, len(observations))
	l.absent = make([][]bool, len(observations))
	l.costs = observationCosts{rows: make([]int, len(observations)), groups: l.groups, headers: l.headers, specifics: observationSpecifics(observations)}
	for i, observation := range observations {
		content := ResolveRelativeDates(observation.Content, recordAnchor(observation.Timestamp))
		replacedBy := strings.Join(supersededBy[observation.ID], " ")
		var cells []string
		if l.groups[i] == notesTable {
			l.fields[notesTable] = plainFields
			cells = []string{observation.ID, recordDate(observation.Timestamp), string(observation.Relevance), content}
			if len(plainFields) > len(plainNoteFields) {
				cells = append(cells, replacedBy)
			}
		} else {
			l.fields[l.groups[i]] = subjectNoteFields
			scope := CanonicalScope(observation.Scope, canonical[observation.Subject])
			cells = []string{observation.ID, recordDate(observation.Timestamp), string(observation.Kind), scope, content, replacedBy}
		}
		l.rows[i], l.absent[i] = cells, absentEmpty(cells)
		if _, ok := l.headers[l.groups[i]]; !ok {
			l.headers[l.groups[i]] = l.headerCost(l.groups[i], l.fields[l.groups[i]])
		}
	}
	for i := range observations {
		l.costs.rows[i] = l.rowCost(l.groups[i], l.fields[l.groups[i]], l.rows[i], l.absent[i])
	}
	return l
}

func containsString(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// absentEmpty marks the empty cells absent, except the first (an id).
func absentEmpty(cells []string) []bool {
	absent := make([]bool, len(cells))
	for i, cell := range cells {
		absent[i] = i > 0 && cell == ""
	}
	return absent
}

// empty is the estimate of table name encoded without rows. A table's fields
// are fixed by its name within one render.
func (l *tablesLayout) empty(name string, fields []string) int {
	tokens, ok := l.emptyTokens[name]
	if !ok {
		tokens = estimateTokens(l.codec.Encode(tables.Table{Name: name, Fields: fields}))
		l.emptyTokens[name] = tokens
	}
	return tokens
}

func (l *tablesLayout) headerCost(name string, fields []string) int {
	return l.empty(name, fields) + 2
}

func (l *tablesLayout) rowCost(name string, fields []string, row []string, absent []bool) int {
	one := tables.Table{Name: name, Fields: fields, Rows: [][]string{row}, Absent: [][]bool{absent}}
	return estimateTokens(l.codec.Encode(one)) - l.empty(name, fields) + 1
}

func (l *tablesLayout) reflectionRow(reflection Reflection) ([]string, []bool) {
	cells := []string{reflection.ID, l.dates[reflection.ID], reflection.Content}
	return cells, absentEmpty(cells)
}

func (l *tablesLayout) reflectionCost(reflection Reflection) int {
	row, absent := l.reflectionRow(reflection)
	return l.rowCost(reflectionsTable, reflectionFields, row, absent)
}

// omittedRows lists the spans of records not kept: runs of consecutive
// omitted reflections, then of consecutive omitted observations, each with
// its date range, count and first id.
func (l *tablesLayout) omittedRows(keptReflections, keptObservations map[string]bool) [][]string {
	return omittedSpans(l.reflections, l.observations, l.dates, keptReflections, keptObservations)
}

// omittedSpans lists the spans of records not kept: runs of consecutive
// omitted reflections, then of consecutive omitted observations, each with
// its date range, count and first id. dates are the reflections' date spans
// ("from..to" or one date).
func omittedSpans(reflections []Reflection, observations []Observation, dates map[string]string, keptReflections, keptObservations map[string]bool) [][]string {
	rows := [][]string{}
	span := func(n int, kept func(int) bool, from, to func(int) string, id func(int) string) {
		for i := 0; i < n; {
			if kept(i) {
				i++
				continue
			}
			j := i
			first, last := "", ""
			for ; j < n && !kept(j); j++ {
				if f := from(j); f != "" && (first == "" || f < first) {
					first = f
				}
				if t := to(j); t > last {
					last = t
				}
			}
			rows = append(rows, []string{first, last, strconv.Itoa(j - i), id(i)})
			i = j
		}
	}
	reflectionRange := func(i int) (string, string) {
		from, to, _ := strings.Cut(dates[reflections[i].ID], "..")
		if to == "" {
			to = from
		}
		return from, to
	}
	span(len(reflections),
		func(i int) bool { return keptReflections[reflections[i].ID] },
		func(i int) string { from, _ := reflectionRange(i); return from },
		func(i int) string { _, to := reflectionRange(i); return to },
		func(i int) string { return reflections[i].ID })
	observationDate := func(i int) string {
		if date := recordDate(observations[i].Timestamp); len(date) == 10 {
			return date
		}
		return ""
	}
	span(len(observations),
		func(i int) bool { return keptObservations[observations[i].ID] },
		observationDate, observationDate,
		func(i int) string { return observations[i].ID })
	return rows
}

func omittedTableOf(rows [][]string) tables.Table {
	absent := make([][]bool, len(rows))
	for i, row := range rows {
		absent[i] = absentEmpty(row)
	}
	return tables.Table{Name: omittedTable, Fields: omittedFields, Rows: rows, Absent: absent}
}

// omittedCost is what the omitted table adds to the summary, 0 when empty.
func (l *tablesLayout) omittedCost(rows [][]string) int {
	if len(rows) == 0 {
		return 0
	}
	cost := l.headerCost(omittedTable, omittedFields)
	for _, row := range rows {
		cost += l.rowCost(omittedTable, omittedFields, row, absentEmpty(row))
	}
	return cost
}

// idSet is the set of ids of records.
func idSet[T any](records []T, id func(T) string) map[string]bool {
	set := make(map[string]bool, len(records))
	for _, record := range records {
		set[id(record)] = true
	}
	return set
}

// renderTablesSummary renders the tables format (docs/memory-interface.md,
// section 3): a reflections table, notes.<subject> tables ordered by their
// newest record, a notes table for records without a subject and an omitted
// table for the spans left out. Selection is the lines format's (observations
// keep at least half, recency, specifics, then the rest), with each record
// costed by its encoded row and each table's header paid once. The omitted
// table's cost is reserved before selection: a selection that omits records is
// redone with the budget less the table it produced, until the table fits what
// was reserved. When the whole summary's estimate fits maxTokens, every record
// is rendered: the per-record costs bound the estimate from above, so selection
// runs only when the full render does not fit.
func renderTablesSummary(reflections []Reflection, observations []Observation, maxTokens int, options RenderOptions) RenderedSummary {
	if len(reflections) == 0 && len(observations) == 0 {
		return RenderedSummary{Reflections: []Reflection{}, Observations: []Observation{}}
	}
	l := newTablesLayout(reflections, observations, options)
	if maxTokens <= 0 {
		return l.render(reflections, observations)
	}
	if full := l.render(reflections, observations); estimateTokens(full.Text) <= maxTokens {
		return full
	}
	var keptReflections []Reflection
	var keptObservations []Observation
	fixed := estimateTokens(TablesUsageInstructionsFor(l.codec)) + 1
	reflectionHeader := l.headerCost(reflectionsTable, reflectionFields)
	selectWith := func(reserve int) ([]Reflection, []Observation) {
		budget := max(0, maxTokens-fixed-reserve)
		_, reservedTokens := selectCostedObservations(observations, int(float64(budget)*SummaryObservationsMinShare), l.costs)
		pickedReflections, reflectionTokens := selectCostedWithinBudget(reflections, l.reflectionCost, budget-reservedTokens-reflectionHeader)
		if len(pickedReflections) > 0 {
			reflectionTokens += reflectionHeader
		} else {
			reflectionTokens = 0
		}
		pickedObservations, _ := selectCostedObservations(observations, budget-reflectionTokens, l.costs)
		return pickedReflections, pickedObservations
	}
	reserve := 0
	for range 32 {
		keptReflections, keptObservations = selectWith(reserve)
		need := l.omittedCost(l.omittedRows(idSet(keptReflections, func(r Reflection) string { return r.ID }), idSet(keptObservations, func(o Observation) string { return o.ID })))
		if need <= reserve {
			break
		}
		reserve = need
	}
	return l.render(keptReflections, keptObservations)
}

// render encodes the kept records as the tables format, with an omitted table
// for the records of the layout left out.
func (l *tablesLayout) render(keptReflections []Reflection, keptObservations []Observation) RenderedSummary {
	reflections, observations := l.reflections, l.observations
	keptReflectionIDs := idSet(keptReflections, func(r Reflection) string { return r.ID })
	keptObservationIDs := idSet(keptObservations, func(o Observation) string { return o.ID })
	out := []tables.Table{}
	if len(keptReflections) > 0 {
		t := tables.Table{Name: reflectionsTable, Fields: reflectionFields}
		for _, reflection := range keptReflections {
			row, absent := l.reflectionRow(reflection)
			t.Rows, t.Absent = append(t.Rows, row), append(t.Absent, absent)
		}
		out = append(out, t)
	}
	byGroup := map[string]*tables.Table{}
	newest := map[string]int{}
	for i, observation := range observations {
		if !keptObservationIDs[observation.ID] {
			continue
		}
		group := l.groups[i]
		t := byGroup[group]
		if t == nil {
			t = &tables.Table{Name: group, Fields: l.fields[group]}
			byGroup[group] = t
		}
		t.Rows, t.Absent = append(t.Rows, l.rows[i]), append(t.Absent, l.absent[i])
		newest[group] = i
	}
	subjects := []string{}
	for group := range byGroup {
		if group != notesTable {
			subjects = append(subjects, group)
		}
	}
	sort.Slice(subjects, func(a, b int) bool { return newest[subjects[a]] < newest[subjects[b]] })
	for _, group := range subjects {
		out = append(out, *byGroup[group])
	}
	if t := byGroup[notesTable]; t != nil {
		out = append(out, *t)
	}
	omittedReflections := len(reflections) - len(keptReflections)
	omittedObservations := len(observations) - len(keptObservations)
	if omittedReflections+omittedObservations > 0 {
		out = append(out, omittedTableOf(l.omittedRows(keptReflectionIDs, keptObservationIDs)))
	}
	if keptReflections == nil {
		keptReflections = []Reflection{}
	}
	if keptObservations == nil {
		keptObservations = []Observation{}
	}
	text := TablesUsageInstructionsFor(l.codec)
	if len(out) > 0 {
		text += "\n\n" + l.codec.Encode(out...)
	}
	return RenderedSummary{
		Text:                text,
		Reflections:         keptReflections,
		Observations:        keptObservations,
		OmittedReflections:  omittedReflections,
		OmittedObservations: omittedObservations,
	}
}
