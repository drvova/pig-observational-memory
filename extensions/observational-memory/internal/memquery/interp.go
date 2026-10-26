package memquery

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omhost"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/recall"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// Definer stores the queries define names. The curated store implements it by storing them
// (curated.Store.Define); registering each as a tool of its own is the per-model harness's
// (docs/memory-interface.md, section 6), which wraps the store.
type Definer interface {
	Define(d curated.Definition, source string) (curated.Definition, error)
}

// Proposer writes the rules a model proposes with propose (docs/memory-interface.md, section
// 7): the per-model harness checks the proposal against the remedy catalogue and stores it on
// probation for the calling model. args are the write's key=value arguments.
type Proposer interface {
	Propose(args map[string]string) (string, error)
}

// Layerer is the per-model harness's side of the layer (docs/memory-interface.md, section 8):
// the harness and rollback writes and the harness source. The harness implements it over the
// layer store: a definition is kernel-checked, refused when its text came from tool output on
// the branch, and held until a clean pass proves it. Each method returns the model's text;
// Define, Forget and Rollback also tell the user.
type Layerer interface {
	Define(kind, name, text string) (string, error)
	Forget(kind, name string) (string, error)
	// Rollback rolls model's layer back to generation n; model "" is the default model.
	Rollback(n int, model string) (string, error)
	// Listing is the layer as the harness source lists it (HarnessFields).
	Listing() ([]HarnessRow, error)
}

// HarnessRow is one row of the harness source: a definition of a model's layer, or a seed
// standing where the model defined nothing. It moved to internal/om as om.LayerRow
// (docs/code-tool.md, "Order of work", step 1).
type HarnessRow = om.LayerRow

// Env is what a query runs against.
type Env struct {
	// Entries are the current branch's entries.
	Entries []session.Entry
	// Codec encodes results; nil means the default codec.
	Codec tables.Codec
	// Saved is the curated store as seen from this project and session; nil when curated
	// memory is off, and saved, recall of a name and every write then fail.
	Saved *curated.Call
	// Definer stores definitions; nil means Saved's store.
	Definer Definer
	// DefinesTools reports that Definer also registers each definition as a tool of its name
	// (the per-model harness), so define's result says so.
	DefinesTools bool
	// Proposer stores proposed rules; nil refuses propose.
	Proposer Proposer
	// Layer is the model's layer; nil refuses the harness and rollback writes and the
	// harness source.
	Layer Layerer
	// DefinedTools are the names of the registered tools the agent defined. Their calls are
	// memory queries too, so history and the feedback quote check leave their calls and
	// results out as they leave out the memory tool's.
	DefinedTools []string
	// CitedBy makes recall of an observation list the reflections citing it
	// (FoldedLedger.CitedBy): memory v2's lineage (docs/memory-v2.md, "Memory tool"). The
	// per-model harness sets it on every memory tool call.
	CitedBy bool
	// Records, when set, are the memory the notes and reflections sources read instead of the
	// ledger folded from Entries: a compaction's projection for a summary query, or one
	// pass's notes for a detector query (docs/memory-interface.md, section 8).
	Records *Records
	// ToolCall runs the memory tool's own behaviour (docs/memory-v2.md, "Memory tool"): history
	// leaves out the current turn and harness markup, lists newest first, says how many entries
	// match each word when none matches them all, and a cut result leads with matched N,
	// showing M and, for history, the query for the next older page; recall pages a long text
	// (RecallPageChars) and leaves thinking out of an entry; history, notes and recall fall back
	// to SessionEntries when the current branch has nothing; saved shows each memory's state.
	// The per-model harness sets it on every memory tool and defined tool call; a detector's
	// or summary's query (Records set) leaves it unset and reads as the query engine always did.
	ToolCall bool
	// SessionEntries are every entry of the session file, in file order: memory v2's off-path
	// fallback (docs/memory-v2.md, "Branches"). Nil when the host cannot read them.
	SessionEntries []session.Entry

	// off marks the fallback read over SessionEntries (omhost.Reader.Fallback): rows on the
	// branch are left out, and the others are labelled OffPathLabel. Nil on the branch.
	off *omhost.OffPath
}

// OffPathLabel follows the id of a row or entry that is not on the current branch: a summary's
// off-path span ends, and memory v2's fallback rows (omhost.OffPathLabel).
const OffPathLabel = omhost.OffPathLabel

// Records are a memory given to a query directly (omhost.Records).
type Records = omhost.Records

// memoryTools are the tools whose calls are memory queries: the memory tool and the defined
// tools.
func (e Env) memoryTools() []string {
	return append([]string{ToolName}, e.DefinedTools...)
}

// reader is the read engine's view of env (omhost.Reader).
func (e Env) reader() omhost.Reader {
	return omhost.Reader{Entries: e.Entries, SessionEntries: e.SessionEntries, Records: e.Records, ToolCall: e.ToolCall, Skip: e.memoryTools(), Off: e.off}
}

// Details are a result's structured details, for the TUI renderer.
type Details struct {
	Query string `json:"query"`
	// Kind is the query's source or write.
	Kind string `json:"kind,omitempty"`
	// Rows is how many rows the result shows; Matched how many matched before the last cut.
	Rows    int  `json:"rows,omitempty"`
	Matched int  `json:"matched,omitempty"`
	Error   bool `json:"error,omitempty"`
	// Recall is recall's details when it read an observation or reflection id.
	Recall *recall.Details `json:"recall,omitempty"`
	// CitedBy are the reflections citing the observation recall read (FoldedLedger.CitedBy).
	CitedBy []string `json:"citedBy,omitempty"`
}

// Result is a query's outcome.
type Result struct {
	// Text is what the model sees.
	Text    string
	IsError bool
	// Notify, when set, is shown to the user: a save, edit, define or propose outlives the session and
	// reaches later system prompts, so the user sees each one, including any an injected
	// instruction asked for.
	Notify  string
	Details Details
	// ParseError marks a query that did not parse: a syntax error, as the per-model harness
	// counts it (docs/memory-interface.md, section 6).
	ParseError bool
	// Hits are the ids (notes, reflections) or names (saved) of the records the result shows,
	// or recall's id; none for history, counts and writes. The harness checks them against
	// what the current summary and index already show.
	Hits []string
}

// HistoryClipChars bounds a history row's content in a result: a window of this many
// characters around the first searched word. recall <id> reads the entry in full
// (om.HistoryClipChars).
const HistoryClipChars = om.HistoryClipChars

// Run parses and runs one query.
func Run(env Env, query string) Result {
	if env.Codec == nil {
		env.Codec = tables.Codecs()[0]
	}
	details := Details{Query: query}
	fail := func(err error) Result {
		details.Error = true
		return Result{Text: "Error: " + err.Error(), IsError: true, Details: details}
	}
	q, err := Parse(query)
	if err != nil {
		result := fail(err)
		result.ParseError = true
		return result
	}
	if q.Write != "" {
		details.Kind = q.Write
		text, notify, err := runWrite(env, q)
		if err != nil {
			return fail(err)
		}
		return Result{Text: text, Notify: notify, Details: details}
	}
	details.Kind = q.Source
	if q.Source == SourceRecall {
		text, recalled, cited, err := runRecall(env, q.ID)
		if err != nil && env.ToolCall && len(env.SessionEntries) > 0 {
			// Memory v2's fallback: an id not on the current branch is read from the session
			// file, labelled off-path.
			if offText, offRecalled, _, offErr := runRecall(env.fallback(), q.ID); offErr == nil {
				text, recalled, cited, err = strings.TrimSpace(OffPathLabel)+" "+offText, offRecalled, nil, nil
			}
		}
		if err != nil {
			return fail(err)
		}
		if env.ToolCall || q.Paged {
			if text, err = page(text, q); err != nil {
				return fail(err)
			}
		}
		details.Recall, details.CitedBy = recalled, cited
		return Result{Text: text, Details: details, Hits: []string{q.ID}}
	}
	out, err := runRead(env, q)
	if err != nil {
		return fail(err)
	}
	details.Rows, details.Matched = out.shown, out.matched
	return Result{Text: out.text, Details: details, Hits: out.hits}
}

// relation is the rows a read query carries from stage to stage.
type relation struct {
	name   string
	fields []string
	// rows hold every value in full; "" is absent.
	rows [][]string
	// from and to are each row's date range (YYYY-MM-DD, "" when undated); nil once count
	// has replaced the records.
	from, to []string
	// clip names the fields shortened for display (HistoryClipChars).
	clip map[string]bool
	// matched is the row count before the last limit that cut rows, 0 when none did; next is
	// the id of the first row that cut left out.
	matched int
	next    string
	// also maps a field to another whose value where also matches (the notes source's
	// subject to raw_subject), so a filter written against the stored value keeps matching.
	also map[string]string
}

func (r *relation) fieldIndex(field string, pos int) (int, error) {
	for i, f := range r.fields {
		if f == field {
			return i, nil
		}
	}
	return 0, &Error{Pos: pos, Msg: fmt.Sprintf("unknown field %q in %s", field, r.name), Hint: "fields are " + strings.Join(r.fields, ", ")}
}

// keep filters the rows (and their dates) by keepRow. A filter after a cut forgets the
// matched count: it saw only the rows the cut kept, so how many match the whole query is
// unknown.
func (r *relation) keep(keepRow func(i int) bool) {
	r.matched = 0
	rows := r.rows[:0:0]
	var from, to []string
	for i, row := range r.rows {
		if !keepRow(i) {
			continue
		}
		rows = append(rows, row)
		if r.from != nil {
			from, to = append(from, r.from[i]), append(to, r.to[i])
		}
	}
	r.rows = rows
	if r.from != nil {
		r.from, r.to = nonNilStrings(from), nonNilStrings(to)
	}
}

func (r *relation) cut(n int) {
	if len(r.rows) <= n {
		return
	}
	r.matched = len(r.rows)
	r.next = r.rows[n][0]
	r.rows = r.rows[:n]
	if r.from != nil {
		r.from, r.to = r.from[:n], r.to[:n]
	}
}

func nonNilStrings(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// readOutput is a read query's encoded result.
type readOutput struct {
	text           string
	shown, matched int
	// hits are the shown records' ids or names (Result.Hits).
	hits []string
}

// runRead runs a source and its stages. On a tool call a history or notes query that finds
// nothing on the branch is run again over the session file (fallback), and a history search
// that finds nothing there either says how many entries match each word (noMatchCounts).
func runRead(env Env, q *Query) (readOutput, error) {
	rel, words, group, err := evalRead(env, q, true)
	if err != nil {
		return readOutput{}, err
	}
	if env.ToolCall && len(rel.rows) == 0 && (q.Source == SourceHistory || q.Source == SourceNotes) && env.Records == nil {
		if len(env.SessionEntries) > 0 {
			if off, offWords, offGroup, err := evalRead(env.fallback(), q, true); err == nil && len(off.rows) > 0 {
				rel, words, group = off, offWords, offGroup
			}
		}
		if len(rel.rows) == 0 && q.Source == SourceHistory {
			if text, ok := noMatchCounts(env, q); ok {
				return readOutput{text: text}, nil
			}
		}
	}
	tablesOut := encodeRelation(rel, words, group)
	out := readOutput{text: env.Codec.Encode(tablesOut...), shown: len(rel.rows), matched: rel.matched}
	if rel.name == SourceNotes || rel.name == SourceReflections || rel.name == SourceSaved {
		for _, row := range rel.rows {
			out.hits = append(out.hits, strings.TrimSuffix(row[0], OffPathLabel))
		}
	}
	if rel.matched > len(rel.rows) {
		marker := fmt.Sprintf("matched %d, showing %d", rel.matched, len(rel.rows))
		if !env.ToolCall {
			out.text += "\n" + marker
			return out, nil
		}
		if next := nextPageQuery(env, q, rel); next != "" {
			marker += "; the next older page: " + next
		}
		out.text = marker + "\n" + out.text
	}
	return out, nil
}

// Selection is what a read query selected, as tables rather than text: what a summary query
// lays out and a detector query counts (docs/memory-interface.md, section 8).
type Selection struct {
	// Source is the query's source.
	Source string
	Tables []tables.Table
	// IDs are, per table, the id of the record each row shows; "" for a row that is no
	// record (a count).
	IDs [][]string
	// Count is the selection's size: a count stage's number, else the rows selected.
	Count int
}

// Select runs a read query and returns its tables. Unlike Run, no default limit cuts the
// rows: an explicit limit still does, and every other bound of the query engine holds. A
// write or a recall is refused: only a source and its stages select. sources limits the
// sources the caller allows (none means every read source).
func Select(env Env, query string, sources ...string) (Selection, error) {
	q, err := Parse(query)
	if err != nil {
		return Selection{}, err
	}
	if q.Write != "" || q.Source == SourceRecall {
		return Selection{}, &Error{Msg: "only a source and its stages select records here", Hint: "start the query with " + strings.Join(readSources(sources), ", ")}
	}
	if len(sources) > 0 && !contains(sources, q.Source) {
		return Selection{}, &Error{Msg: fmt.Sprintf("the source %s is not read here", q.Source), Hint: "start the query with " + strings.Join(sources, ", ")}
	}
	rel, words, group, err := evalRead(env, q, false)
	if err != nil {
		return Selection{}, err
	}
	sel := Selection{Source: q.Source, Tables: encodeRelation(rel, words, group), Count: len(rel.rows)}
	if rel.name == "count" && len(rel.rows) == 1 {
		sel.Count, _ = strconv.Atoi(rel.rows[0][0])
	}
	records := rel.name == SourceNotes || rel.name == SourceReflections
	for _, t := range sel.Tables {
		ids := make([]string, len(t.Rows))
		if records {
			for r, row := range t.Rows {
				ids[r] = row[0]
			}
		}
		sel.IDs = append(sel.IDs, ids)
	}
	return sel, nil
}

func readSources(allowed []string) []string {
	if len(allowed) > 0 {
		return allowed
	}
	return []string{SourceNotes, SourceReflections, SourceHistory, SourceSaved}
}

// evalRead runs a source and its stages over env; defaultLimit cuts an unlimited query at
// DefaultLimit, as the memory tool shows it.
func evalRead(env Env, q *Query, defaultLimit bool) (*relation, []string, *Stage, error) {
	for i, stage := range q.Stages {
		if stage.Kind == StageGroup && i != len(q.Stages)-1 {
			return nil, nil, nil, &Error{Pos: stage.pos, Msg: "group by must be the last stage", Hint: "move limit, sort and where before group by"}
		}
	}
	view, err := newPointInTime(env, q)
	if err != nil {
		return nil, nil, nil, err
	}
	rel, err := loadSource(env, q.Source, view)
	if err != nil {
		return nil, nil, nil, err
	}
	words := []string{}
	limited := false
	var group *Stage
	var anchors *rowAnchors
	for i := range q.Stages {
		stage := q.Stages[i]
		switch stage.Kind {
		case StageSearch:
			needles := strings.Fields(strings.ToLower(stage.Words))
			words = append(words, needles...)
			rel.keep(func(i int) bool {
				hay := strings.ToLower(strings.Join(rel.rows[i], "\n"))
				for _, w := range needles {
					if !strings.Contains(hay, w) {
						return false
					}
				}
				return true
			})
		case StageWhere:
			c, err := rel.fieldIndex(stage.Field, stage.fieldPos)
			if err != nil {
				return nil, nil, nil, err
			}
			value := stage.Value
			columns := []int{c}
			if other, ok := rel.also[stage.Field]; ok {
				if d, err := rel.fieldIndex(other, stage.fieldPos); err == nil {
					columns = append(columns, d)
				}
			}
			match := func(i int, test func(string) bool) bool {
				for _, col := range columns {
					if test(rel.rows[i][col]) {
						return true
					}
				}
				return false
			}
			if stage.Op == '~' {
				words = append(words, strings.Fields(strings.ToLower(value))...)
				needle := strings.ToLower(value)
				rel.keep(func(i int) bool {
					return match(i, func(v string) bool { return strings.Contains(strings.ToLower(v), needle) })
				})
			} else {
				rel.keep(func(i int) bool { return match(i, func(v string) bool { return strings.EqualFold(v, value) }) })
			}
		case StageSince, StageUntil:
			if rel.from == nil {
				return nil, nil, nil, &Error{Pos: stage.pos, Msg: stage.Kind + " filters records, not counts", Hint: "put " + stage.Kind + " before count"}
			}
			date := stage.Value
			from, to := func(i int) string { return rel.from[i] }, func(i int) string { return rel.to[i] }
			if stage.Event {
				c, ok := eventDateColumn(rel)
				if !ok {
					return nil, nil, nil, &Error{Pos: stage.pos, Msg: "event compares the event dates of notes", Hint: "use " + stage.Kind + " " + stage.Value + " without event on " + rel.name}
				}
				// A note without an event date compares by its entry time.
				from = func(i int) string { return eventOr(rel.rows[i][c], rel.from[i]) }
				to = func(i int) string { return eventOr(rel.rows[i][c], rel.to[i]) }
			}
			if stage.Kind == StageSince {
				rel.keep(func(i int) bool { return to(i) != "" && to(i) >= date })
			} else {
				rel.keep(func(i int) bool { return from(i) != "" && from(i) <= date })
			}
		case StageFrom, StageThrough:
			if rel.from == nil {
				return nil, nil, nil, &Error{Pos: stage.pos, Msg: stage.Kind + " filters records, not counts", Hint: "put " + stage.Kind + " before count"}
			}
			if anchors == nil {
				var err error
				if anchors, err = newRowAnchors(env, rel.name, stage); err != nil {
					return nil, nil, nil, err
				}
			}
			bound, ok := anchors.positions[stage.Value]
			if !ok {
				return nil, nil, nil, &Error{Pos: stage.pos, Msg: fmt.Sprintf("entry %s is not on this branch", stage.Value), Hint: "take entry ids from history or from a summary line on this branch"}
			}
			from := stage.Kind == StageFrom
			rel.keep(func(i int) bool {
				at, ok := anchors.of(strings.TrimSuffix(rel.rows[i][0], OffPathLabel))
				return ok && (from && at >= bound || !from && at <= bound)
			})
		case StageSort:
			c, err := rel.fieldIndex(stage.Field, stage.fieldPos)
			if err != nil {
				return nil, nil, nil, err
			}
			if e, ok := eventDateColumn(rel); ok && e == c {
				// A note without an event date sorts by its entry time.
				if when, err := rel.fieldIndex("when", stage.fieldPos); err == nil {
					sortRelationBy(rel, func(row []string) string { return eventOr(row[c], row[when]) }, stage.Desc)
					break
				}
			}
			sortRelation(rel, c, stage.Desc)
		case StageLimit:
			limited = true
			rel.cut(stage.N)
		case StageCount:
			if err := countRelation(rel, stage); err != nil {
				return nil, nil, nil, err
			}
		case StageGroup:
			if _, err := rel.fieldIndex(stage.Field, stage.fieldPos); err != nil {
				return nil, nil, nil, err
			}
			group = &q.Stages[i]
		}
	}
	if !limited && defaultLimit {
		rel.cut(DefaultLimit)
	}
	return rel, words, group, nil
}

// eventDateColumn is the notes source's event_date column, when rel is the notes source's rows.
func eventDateColumn(rel *relation) (int, bool) {
	if rel.name != SourceNotes {
		return 0, false
	}
	for i, f := range rel.fields {
		if f == FieldEventDate {
			return i, true
		}
	}
	return 0, false
}

// eventOr is a note's event date, else its entry time.
func eventOr(event, entry string) string {
	if event != "" {
		return event
	}
	return entry
}

// sortRelation orders the rows by field c, numerically when both values are whole numbers.
// The sort is stable, so equal values keep the source's order.
func sortRelation(rel *relation, c int, desc bool) {
	sortRelationBy(rel, func(row []string) string { return row[c] }, desc)
}

// sortRelationBy is sortRelation over the value key reads from each row.
func sortRelationBy(rel *relation, key func(row []string) string, desc bool) {
	order := make([]int, len(rel.rows))
	for i := range order {
		order[i] = i
	}
	less := func(a, b string) bool {
		x, errX := strconv.Atoi(a)
		y, errY := strconv.Atoi(b)
		if errX == nil && errY == nil {
			return x < y
		}
		return a < b
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := key(rel.rows[order[i]]), key(rel.rows[order[j]])
		if desc {
			return less(b, a)
		}
		return less(a, b)
	})
	rows := make([][]string, len(order))
	var from, to []string
	for i, k := range order {
		rows[i] = rel.rows[k]
		if rel.from != nil {
			from, to = append(from, rel.from[k]), append(to, rel.to[k])
		}
	}
	rel.rows = rows
	if rel.from != nil {
		rel.from, rel.to = nonNilStrings(from), nonNilStrings(to)
	}
}

// countRelation replaces the rows by their count, or by one row per value of the field with
// its count, most frequent first.
func countRelation(rel *relation, stage Stage) error {
	if stage.Field == "" {
		*rel = relation{name: "count", fields: []string{"count"}, rows: [][]string{{strconv.Itoa(len(rel.rows))}}}
		return nil
	}
	c, err := rel.fieldIndex(stage.Field, stage.fieldPos)
	if err != nil {
		return err
	}
	counts := map[string]int{}
	values := []string{}
	for _, row := range rel.rows {
		if _, seen := counts[row[c]]; !seen {
			values = append(values, row[c])
		}
		counts[row[c]]++
	}
	sort.SliceStable(values, func(i, j int) bool {
		if counts[values[i]] != counts[values[j]] {
			return counts[values[i]] > counts[values[j]]
		}
		return values[i] < values[j]
	})
	rows := make([][]string, len(values))
	for i, v := range values {
		rows[i] = []string{v, strconv.Itoa(counts[v])}
	}
	*rel = relation{name: "counts", fields: []string{stage.Field, "count"}, rows: rows}
	return nil
}

// encodeRelation turns the rows into tables: one, or one per value of the group field (named
// after the source and the value, in order of first appearance). Clipped fields are shortened
// around the first searched word.
func encodeRelation(rel *relation, words []string, group *Stage) []tables.Table {
	display := func(row []string) ([]string, []bool) {
		cells := make([]string, len(row))
		absent := make([]bool, len(row))
		for i, v := range row {
			if rel.clip[rel.fields[i]] {
				v = omhost.ClipAround(v, words, HistoryClipChars)
			}
			cells[i] = v
			// The first field (an id or a name) is always present; a single-field table
			// writes an empty cell as the empty string (tables.Table.Validate).
			absent[i] = i > 0 && v == ""
		}
		return cells, absent
	}
	if group == nil {
		t := tables.Table{Name: rel.name, Fields: rel.fields, Rows: [][]string{}}
		for _, row := range rel.rows {
			cells, absent := display(row)
			t.Rows, t.Absent = append(t.Rows, cells), append(t.Absent, absent)
		}
		return []tables.Table{t}
	}
	c := 0
	for i, f := range rel.fields {
		if f == group.Field {
			c = i
		}
	}
	out := []tables.Table{}
	index := map[string]int{}
	for _, row := range rel.rows {
		name := groupTableName(rel.name, row[c])
		k, ok := index[name]
		if !ok {
			k = len(out)
			index[name] = k
			out = append(out, tables.Table{Name: name, Fields: rel.fields})
		}
		cells, absent := display(row)
		out[k].Rows, out[k].Absent = append(out[k].Rows, cells), append(out[k].Absent, absent)
	}
	if len(out) == 0 {
		out = append(out, tables.Table{Name: rel.name, Fields: rel.fields})
	}
	return out
}

// groupTableName is base and the value lowercased, each run of characters a table name cannot
// hold replaced by "_"; base alone for an empty value.
func groupTableName(base, value string) string {
	var b strings.Builder
	pending := false
	for _, r := range strings.ToLower(value) {
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
		return base
	}
	return base + "." + name
}

// errCuratedOff is the failure of a query that needs the curated store when it is off.
var errCuratedOff = errors.New("curated memory is off (curatedMemory: false), or PiG's config directory could not be found; notes, reflections, history and recall of ids still work")

// rowAnchors are the branch positions the from and through stages compare (omhost.Anchors).
type rowAnchors struct {
	positions map[string]int
	of        func(id string) (int, bool)
}

// newRowAnchors reads the anchors of source's rows; stage is the from or through stage that
// asks, for the error when the rows have no branch position (a count, saved, harness).
func newRowAnchors(env Env, source string, stage Stage) (*rowAnchors, error) {
	kind := omhost.AnchorHistory
	switch source {
	case SourceHistory:
	case SourceNotes:
		kind = omhost.AnchorNotes
	case SourceReflections:
		kind = omhost.AnchorReflections
	default:
		return nil, &Error{Pos: stage.pos, Msg: stage.Kind + " filters notes, reflections and history by branch position", Hint: "put " + stage.Kind + " before count, on one of those sources"}
	}
	a := omhost.NewAnchors(env.Entries, env.Records, kind)
	return &rowAnchors{positions: a.Positions, of: a.Of}, nil
}

// pointInTime is the bound of the point-in-time view (docs/memory-v2.md, "Memory tool"): until
// DATE asof or through ID asof reads notes and reflections as the memory stood at the bound
// (omhost.PointInTime).
type pointInTime struct {
	view *omhost.PointInTime
}

// bound is the view the read engine reads at; nil for no view.
func (v *pointInTime) bound() *omhost.PointInTime {
	if v == nil {
		return nil
	}
	return v.view
}

// newPointInTime reads q's asof bound; nil when q has none. One asof per query, and only on
// notes and reflections, whose memory changes after it was recorded.
func newPointInTime(env Env, q *Query) (*pointInTime, error) {
	var view *pointInTime
	for _, stage := range q.Stages {
		if !stage.AsOf {
			continue
		}
		if view != nil {
			return nil, &Error{Pos: stage.pos, Msg: "one asof per query", Hint: "keep asof on the bound the memory is read at"}
		}
		if q.Source != SourceNotes && q.Source != SourceReflections {
			return nil, &Error{Pos: stage.pos, Msg: "asof reads notes and reflections as they stood at the bound", Hint: "use " + stage.Kind + " " + stage.Value + " without asof on " + q.Source}
		}
		if stage.Kind == StageThrough {
			at, ok := omhost.PointInTimeThrough(env.Entries, stage.Value)
			if !ok {
				return nil, &Error{Pos: stage.pos, Msg: fmt.Sprintf("entry %s is not on this branch", stage.Value), Hint: "take entry ids from history or from a summary line on this branch"}
			}
			view = &pointInTime{view: at}
			continue
		}
		view = &pointInTime{view: omhost.PointInTimeUntil(env.Entries, stage.Value)}
	}
	return view, nil
}
