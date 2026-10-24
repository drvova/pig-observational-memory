package memquery

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
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
	// DefinedTools are the names of the registered tools the agent defined. Their calls are
	// memory queries too, so history and the feedback quote check leave their calls and
	// results out as they leave out the memory tool's.
	DefinedTools []string
}

// memoryTools are the tools whose calls are memory queries: the memory tool and the defined
// tools.
func (e Env) memoryTools() []string {
	return append([]string{ToolName}, e.DefinedTools...)
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
// characters around the first searched word. recall <id> reads the entry in full.
const HistoryClipChars = 400

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
		text, recalled, err := runRecall(env, q.ID)
		if err != nil {
			return fail(err)
		}
		details.Recall = recalled
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
	// matched is the row count before the last limit that cut rows, 0 when none did.
	matched int
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

// runRead runs a source and its stages.
func runRead(env Env, q *Query) (readOutput, error) {
	for i, stage := range q.Stages {
		if stage.Kind == StageGroup && i != len(q.Stages)-1 {
			return readOutput{}, &Error{Pos: stage.pos, Msg: "group by must be the last stage", Hint: "move limit, sort and where before group by"}
		}
	}
	rel, err := loadSource(env, q.Source)
	if err != nil {
		return readOutput{}, err
	}
	words := []string{}
	limited := false
	var group *Stage
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
				return readOutput{}, err
			}
			value := stage.Value
			if stage.Op == '~' {
				words = append(words, strings.Fields(strings.ToLower(value))...)
				needle := strings.ToLower(value)
				rel.keep(func(i int) bool { return strings.Contains(strings.ToLower(rel.rows[i][c]), needle) })
			} else {
				rel.keep(func(i int) bool { return strings.EqualFold(rel.rows[i][c], value) })
			}
		case StageSince, StageUntil:
			if rel.from == nil {
				return readOutput{}, &Error{Pos: stage.pos, Msg: stage.Kind + " filters records, not counts", Hint: "put " + stage.Kind + " before count"}
			}
			date := stage.Value
			if stage.Kind == StageSince {
				rel.keep(func(i int) bool { return rel.to[i] != "" && rel.to[i] >= date })
			} else {
				rel.keep(func(i int) bool { return rel.from[i] != "" && rel.from[i] <= date })
			}
		case StageSort:
			c, err := rel.fieldIndex(stage.Field, stage.fieldPos)
			if err != nil {
				return readOutput{}, err
			}
			sortRelation(rel, c, stage.Desc)
		case StageLimit:
			limited = true
			rel.cut(stage.N)
		case StageCount:
			if err := countRelation(rel, stage); err != nil {
				return readOutput{}, err
			}
		case StageGroup:
			if _, err := rel.fieldIndex(stage.Field, stage.fieldPos); err != nil {
				return readOutput{}, err
			}
			group = &q.Stages[i]
		}
	}
	if !limited {
		rel.cut(DefaultLimit)
	}
	tablesOut := encodeRelation(rel, words, group)
	out := readOutput{text: env.Codec.Encode(tablesOut...), shown: len(rel.rows), matched: rel.matched}
	if rel.name == SourceNotes || rel.name == SourceReflections || rel.name == SourceSaved {
		for _, row := range rel.rows {
			out.hits = append(out.hits, row[0])
		}
	}
	if rel.matched > len(rel.rows) {
		out.text += fmt.Sprintf("\nmatched %d, showing %d", rel.matched, len(rel.rows))
	}
	return out, nil
}

// sortRelation orders the rows by field c, numerically when both values are whole numbers.
// The sort is stable, so equal values keep the source's order.
func sortRelation(rel *relation, c int, desc bool) {
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
		a, b := rel.rows[order[i]][c], rel.rows[order[j]][c]
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
				v = clipAround(v, words, HistoryClipChars)
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

// clipAround keeps n characters of s: a window starting a quarter of n before the first
// occurrence of any of words (case-insensitive), else the start. A cut end is marked "…".
func clipAround(s string, words []string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	lower := make([]rune, len(runes))
	for i, r := range runes {
		lower[i] = unicode.ToLower(r)
	}
	first := -1
	for _, w := range words {
		if at := runeIndex(lower, []rune(w)); at >= 0 && (first < 0 || at < first) {
			first = at
		}
	}
	start := 0
	if first > 0 {
		start = max(0, min(first-n/4, len(runes)-n))
	}
	end := start + n
	text := string(runes[start:end])
	if start > 0 {
		text = "…" + text
	}
	if end < len(runes) {
		text += "…"
	}
	return text
}

func runeIndex(hay, needle []rune) int {
	if len(needle) == 0 {
		return -1
	}
outer:
	for i := 0; i+len(needle) <= len(hay); i++ {
		for j, r := range needle {
			if hay[i+j] != r {
				continue outer
			}
		}
		return i
	}
	return -1
}

// errCuratedOff is the failure of a query that needs the curated store when it is off.
var errCuratedOff = errors.New("curated memory is off (curatedMemory: false), or PiG's config directory could not be found; notes, reflections, history and recall of ids still work")
