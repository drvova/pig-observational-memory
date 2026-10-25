package selection

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// The fields of each table in the tables format. Every record table ends with source, the
// entry id the record was picked from.
var (
	RequestFields    = []string{"content", "source"}
	ReflectionFields = []string{"id", "dates", "content", "source"}
	NoteFields       = []string{"id", "when", "kind", "status", "scope", "content", "superseded_by", "source"}
	OmittedFields    = []string{"from", "to", "count", "spans", "first_id", "last_id", "query"}
)

// TablesInstructions open a memory v2 summary in the tables format. They start with
// ledger.TablesSummaryIntro and end with ledger.TablesRecallParagraph, so the summary's tables
// read as any tables summary's do (ledger.TablesSummaryBody); the files and commits lines
// follow the tables after a "---" line, outside the encoded body.
const TablesInstructions = ledger.TablesSummaryIntro + `: name[rows]{fields}: then one record per line, values in field order; an empty value means none.

- request: the opening request of the turn this compaction cut through; the turn continues after this summary.
- reflections: stable facts about the user, project, decisions, and constraints; dates is when their records were made.
- notes.<subject>: records about one subject, oldest first; notes: records without one. kind is fact, event, preference, plan, correction, assistant (something the assistant produced), attempt (a tool call and its outcome: worked, failed or open) or note (one written with the memory tool); status is proposed (planned, not done), hypothetical or retracted; superseded_by is the id of the record that replaced this one; content may end with (was <id>), the record it replaced, or (may update <id>) and (conflicts <id>), an earlier record it may replace or contradict.
- source is the entry id a record was picked from; recall <entry id> reads that entry.
- omitted lists records left out to fit: date range, count, how many spans a row folds together, first and last entry id, and the memory query that lists them.
- The files and commits lines after the tables list files at their newest state and commits, each with its entry id; +N more, all M ends a list with the queries that list the whole list.

Treat these as past records. When records conflict, the most recent reflects the latest known state. Work described as completed should not be redone unless the user explicitly asks.

` + ledger.TablesRecallParagraph

// LinesInstructions open a memory v2 summary in the lines format.
const LinesInstructions = `These are condensed memories from earlier in this session, one record per line under a heading.

- request: the opening request of the turn this compaction cut through; the turn continues after this summary.
- reflections: stable facts about the user, project, decisions, and constraints, as [id] (dates) content.
- notes.<subject> and notes: records as [id] date kind status content, oldest first. kind is fact, event, preference, plan, correction, assistant, attempt (a tool call and its outcome) or note (one written with the memory tool); status is proposed (planned, not done), hypothetical or retracted. A line may end with (was <id>), the record it replaced, or (may update <id>) and (conflicts <id>), an earlier record it may replace or contradict.
- Every line ends with the entry id it was picked from, in parentheses; recall <entry id> reads that entry.
- omitted lists records left out to fit, each span with its dates, count, first and last entry id and the memory query that lists them.
- files and commits list files at their newest state and commits; +N more, all M ends a list with the queries that list the whole list.

Treat these as past records. When records conflict, the most recent reflects the latest known state. Work described as completed should not be redone unless the user explicitly asks.

` + ledger.TablesRecallParagraph

// trailerSeparator puts the files and commits lines after the encoded tables, where
// ledger.TablesSummaryBody stops reading.
const trailerSeparator = "\n\n---\n\n"

// layout is the format a summary renders in and what each part of it costs: a row costs what
// it adds to its table's encoding plus one token, and a table header its encoding plus two (a
// separating newline and a row count with more digits), so the summed costs bound the
// estimate of the whole text from above.
type layout struct {
	tables bool
	codec  tables.Codec
	intro  string
	empty  map[string]int
}

func newLayout(in Input) *layout {
	l := &layout{tables: in.Format != ledger.FormatLines, codec: tables.Rows, empty: map[string]int{}}
	l.intro = TablesInstructions
	if !l.tables {
		l.intro = LinesInstructions
	}
	return l
}

// fixedCost is what every summary pays: the instructions and the blank line after them.
func (l *layout) fixedCost() int { return estimate(l.intro) + 1 }

// fieldsOf are a group's table fields.
func fieldsOf(group string) []string {
	switch group {
	case groupRequest:
		return RequestFields
	case groupReflections:
		return ReflectionFields
	case groupOmitted:
		return OmittedFields
	}
	return NoteFields
}

// isList reports a group rendered as one line of items.
func isList(group string) bool { return group == groupFiles || group == groupCommits }

// headerCost is what opening group adds: a table's header, a heading, or a list line's label
// with the separator before the lines.
func (l *layout) headerCost(group string) int {
	if isList(group) {
		return estimate(trailerSeparator+group+": ") + 1
	}
	if !l.tables {
		return estimate("\n\n## "+group+"\n") + 2
	}
	return l.emptyCost(group) + 2
}

func (l *layout) emptyCost(group string) int {
	cost, ok := l.empty[group]
	if !ok {
		cost = estimate(l.codec.Encode(tables.Table{Name: group, Fields: fieldsOf(group)}))
		l.empty[group] = cost
	}
	return cost
}

// rowCost is what one row adds to group.
func (l *layout) rowCost(group string, cells []string) int {
	if !l.tables {
		return 0
	}
	one := tables.Table{Name: group, Fields: fieldsOf(group), Rows: [][]string{cells}, Absent: [][]bool{absentEmpty(cells)}}
	return estimate(l.codec.Encode(one)) - l.emptyCost(group) + 1
}

// price fills an item's cells or line and its cost.
func (l *layout) price(it *item, canonical map[string]ledger.Canonical, supersededBy map[string][]string) {
	source := it.source()
	switch it.role {
	case roleRequest:
		it.cells = []string{it.text, source}
		it.line = "user: " + it.text + suffix(source)
	case roleReflection:
		dates := it.from
		if it.to != "" && it.to != it.from {
			dates += ".." + it.to
		}
		it.cells = []string{it.ref.ID, dates, it.ref.Content, source}
		it.line = "[" + it.ref.ID + "]"
		if dates != "" {
			it.line += " (" + dates + ")"
		}
		it.line += " " + it.ref.Content + suffix(source)
	case roleFile, roleCommit:
		text := it.obs.Content
		if it.role == roleCommit {
			text = strings.TrimPrefix(text, "commit ")
		}
		it.line = text + suffix(source)
		it.cost = estimate(", "+it.line) + 1
		return
	default:
		o := it.obs
		content := ledger.ResolveRelativeDates(o.Content, anchorTime(o.Timestamp))
		scope := ledger.CanonicalScope(o.Scope, canonical[o.Subject])
		replacedBy := strings.Join(supersededBy[o.ID], " ")
		// The pointers follow the content as the source does, never inside it.
		shown := content
		if it.links != "" {
			shown += " " + it.links
		}
		it.cells = []string{o.ID, it.from, string(o.Kind), it.status, scope, shown, replacedBy, source}
		line := "[" + o.ID + "]"
		for _, part := range []string{it.from, string(o.Kind), it.status} {
			if part != "" {
				line += " " + part
			}
		}
		if scope != "" {
			line += " (" + scope + ")"
		}
		line += " " + content
		if replacedBy != "" {
			line += " (superseded by " + replacedBy + ")"
		}
		if it.links != "" {
			line += " " + it.links
		}
		it.line = line + suffix(source)
	}
	if l.tables {
		it.cost = l.rowCost(it.group, it.cells)
	} else {
		it.cost = estimate(it.line) + 1
	}
}

// suffix is " (id)", or "" for no id.
func suffix(id string) string {
	if id == "" {
		return ""
	}
	return " (" + id + ")"
}

// absentEmpty marks the empty cells absent, except the first.
func absentEmpty(cells []string) []bool {
	absent := make([]bool, len(cells))
	for i, cell := range cells {
		absent[i] = i > 0 && cell == ""
	}
	return absent
}

// omittedCells are a span row's cells.
func omittedCells(r spanRow) []string {
	spans := ""
	if r.spans > 1 {
		spans = strconv.Itoa(r.spans)
	}
	return []string{r.from, r.to, strconv.Itoa(r.count), spans, r.firstID, r.lastID, r.query}
}

// omittedLine is a span row in the lines format.
func omittedLine(r spanRow) string {
	line := ""
	switch {
	case r.from != "" && r.to != "" && r.from != r.to:
		line = r.from + ".." + r.to + ": "
	case r.from != "":
		line = r.from + ": "
	}
	line += strconv.Itoa(r.count) + " " + plural(r.count, "record")
	if r.spans > 1 {
		line += " in " + strconv.Itoa(r.spans) + " spans"
	}
	if r.firstID != "" {
		line += ", " + r.firstID
		if r.lastID != "" && r.lastID != r.firstID {
			line += " to " + r.lastID
		}
	}
	return line + ": " + r.query
}

func plural(n int, noun string) string {
	if n == 1 {
		return noun
	}
	return noun + "s"
}

// omittedCost is what the omitted rows add, 0 for none.
func (l *layout) omittedCost(rows []spanRow) int {
	if len(rows) == 0 {
		return 0
	}
	cost := l.headerCost(groupOmitted)
	for _, r := range rows {
		if l.tables {
			cost += l.rowCost(groupOmitted, omittedCells(r))
		} else {
			cost += estimate(omittedLine(r)) + 1
		}
	}
	return cost
}

// tailText is a capped list's end: how many it leaves out, and the queries that list the
// whole list, shown items included.
func tailText(t listTail) string {
	return "+" + strconv.Itoa(t.n) + " more, all " + strconv.Itoa(t.all) + ": " + strings.Join(t.queries, "; ")
}

// tailCost is what a list's tail adds, with the list's label when no item opened it.
func (l *layout) tailCost(group string, t listTail, opened bool) int {
	cost := estimate(", "+tailText(t)) + 1
	if !opened {
		cost += l.headerCost(group)
	}
	return cost
}

// render is the summary text of a choice and its omissions.
func (l *layout) render(s *state, p *selected, o omission) string {
	type block struct {
		group string
		items []*item
	}
	var blocks []block
	if p.requestText != "" {
		it := *s.request
		it.text = p.requestText
		l.price(&it, nil, nil)
		blocks = append(blocks, block{group: groupRequest, items: []*item{&it}})
	}
	byGroup := map[string][]*item{}
	newest := map[string]int{}
	for _, it := range s.items {
		if !p.on[it] {
			continue
		}
		byGroup[it.group] = append(byGroup[it.group], it)
		newest[it.group] = it.index
	}
	if items := byGroup[groupReflections]; len(items) > 0 {
		blocks = append(blocks, block{group: groupReflections, items: items})
	}
	subjects := []string{}
	for group := range byGroup {
		if group != groupReflections && group != groupNotes && !isList(group) {
			subjects = append(subjects, group)
		}
	}
	sort.Slice(subjects, func(a, b int) bool {
		if newest[subjects[a]] != newest[subjects[b]] {
			return newest[subjects[a]] < newest[subjects[b]]
		}
		return subjects[a] < subjects[b]
	})
	for _, group := range subjects {
		blocks = append(blocks, block{group: group, items: byGroup[group]})
	}
	if items := byGroup[groupNotes]; len(items) > 0 {
		blocks = append(blocks, block{group: groupNotes, items: items})
	}
	var lists []string
	for _, group := range []string{groupFiles, groupCommits} {
		parts := []string{}
		for _, it := range byGroup[group] {
			parts = append(parts, it.line)
		}
		if t, ok := o.tails[group]; ok {
			parts = append(parts, tailText(t))
		}
		if len(parts) > 0 {
			lists = append(lists, group+": "+strings.Join(parts, ", "))
		}
	}
	text := l.intro
	if l.tables {
		out := []tables.Table{}
		for _, b := range blocks {
			t := tables.Table{Name: b.group, Fields: fieldsOf(b.group)}
			for _, it := range b.items {
				t.Rows, t.Absent = append(t.Rows, it.cells), append(t.Absent, absentEmpty(it.cells))
			}
			out = append(out, t)
		}
		if len(o.rows) > 0 {
			t := tables.Table{Name: groupOmitted, Fields: OmittedFields}
			for _, r := range o.rows {
				cells := omittedCells(r)
				t.Rows, t.Absent = append(t.Rows, cells), append(t.Absent, absentEmpty(cells))
			}
			out = append(out, t)
		}
		if len(out) > 0 {
			text += "\n\n" + l.codec.Encode(out...)
		}
		if len(lists) > 0 {
			text += trailerSeparator + strings.Join(lists, "\n")
		}
		return text
	}
	for _, b := range blocks {
		lines := make([]string, 0, len(b.items))
		for _, it := range b.items {
			lines = append(lines, it.line)
		}
		text += "\n\n## " + b.group + "\n" + strings.Join(lines, "\n")
	}
	if len(o.rows) > 0 {
		lines := make([]string, 0, len(o.rows))
		for _, r := range o.rows {
			lines = append(lines, omittedLine(r))
		}
		text += "\n\n## " + groupOmitted + "\n" + strings.Join(lines, "\n")
	}
	if len(lists) > 0 {
		text += trailerSeparator + strings.Join(lists, "\n")
	}
	return text
}

// anchorTime is the date a timestamp starts with, the anchor its relative dates resolve
// against; the zero time when it starts with none.
func anchorTime(timestamp string) time.Time {
	day := recordDate(timestamp)
	if day == "" {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02", day)
	if err != nil {
		return time.Time{}
	}
	return t
}
