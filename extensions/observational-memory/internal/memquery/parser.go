package memquery

import (
	"strconv"
	"strings"
	"time"
)

// The sources a read query starts from.
const (
	SourceNotes       = "notes"
	SourceReflections = "reflections"
	SourceHistory     = "history"
	SourceSaved       = "saved"
	SourceRecall      = "recall"
)

// Sources lists the sources in the order the grammar names them.
var Sources = []string{SourceNotes, SourceReflections, SourceHistory, SourceSaved, SourceRecall}

// The stages of a read query.
const (
	StageSearch = "search"
	StageWhere  = "where"
	StageSince  = "since"
	StageUntil  = "until"
	StageSort   = "sort"
	StageLimit  = "limit"
	StageCount  = "count"
	StageGroup  = "group"
)

// Stages lists the stages in the order the grammar names them.
var Stages = []string{StageSearch, StageWhere, StageSince, StageUntil, StageSort, StageLimit, StageCount, StageGroup}

// The writes.
const (
	WriteSave     = "save"
	WriteEdit     = "edit"
	WriteForget   = "forget"
	WriteFeedback = "feedback"
	WriteDefine   = "define"
	WritePropose  = "propose"
)

// Writes lists the writes in the order the grammar names them.
var Writes = []string{WriteSave, WriteEdit, WriteForget, WriteFeedback, WriteDefine, WritePropose}

// The limit stage's bounds: a query without one shows DefaultLimit rows.
const (
	DefaultLimit = 20
	MaxLimit     = 200
)

// writeArgs are each key=value write's keys, required ones first.
var writeArgs = map[string]struct{ required, optional []string }{
	WriteSave:     {required: []string{"name", "type", "description"}, optional: []string{"body", "scope"}},
	WriteEdit:     {required: []string{"name"}, optional: []string{"type", "description", "body"}},
	WriteForget:   {required: []string{"name"}},
	WriteFeedback: {required: []string{"name", "outcome", "quote"}},
	WritePropose:  {required: []string{"detector", "remedy"}, optional: []string{"text", "kind", "fullness"}},
}

// Query is a parsed query: a read (Source and Stages) or a write.
type Query struct {
	// Source is the read's source, "" for a write.
	Source string
	// ID is recall's id or name.
	ID     string
	Stages []Stage
	// Write is the write's name, "" for a read.
	Write string
	// Args are a key=value write's arguments.
	Args map[string]string
	// Define is define's definition.
	Define *Definition
	pos    int
}

// Stage is one parsed stage.
type Stage struct {
	Kind string
	// Words is search's text; Value is where's value or since/until's date.
	Words, Value string
	// Field is where's, sort's, count by's or group by's field.
	Field string
	// Op is where's operator: '=' (equals) or '~' (contains).
	Op byte
	// Desc is sort's direction.
	Desc bool
	// N is limit's count.
	N int
	// pos and fieldPos are the stage's and its field's character positions.
	pos, fieldPos int
}

// Definition is a parsed define write: the name, the read query it names (as written) and
// what it returns.
type Definition struct {
	Name, Query, Description string
}

// parser walks the token list.
type parser struct {
	query  string
	tokens []token
	i      int
}

func (p *parser) peek() token { return p.tokens[p.i] }

func (p *parser) next() token {
	t := p.tokens[p.i]
	if t.kind != tokEOF {
		p.i++
	}
	return t
}

// Parse parses a query.
func Parse(query string) (*Query, error) {
	tokens, err := lex(query)
	if err != nil {
		return nil, err
	}
	p := &parser{query: query, tokens: tokens}
	first := p.peek()
	if first.kind == tokEOF {
		return nil, &Error{Msg: "empty query", Hint: "start with a source (" + strings.Join(Sources, ", ") + ") or a write (" + strings.Join(Writes, ", ") + ")"}
	}
	if first.kind == tokWord {
		switch first.text {
		case WriteDefine:
			return p.parseDefine()
		case WriteSave, WriteEdit, WriteForget, WriteFeedback, WritePropose:
			return p.parseWrite()
		}
	}
	q, err := p.parseRead(false)
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != tokEOF {
		return nil, errAt(t, "separate stages with |", "unexpected %s", t.describe())
	}
	return q, nil
}

// parseRead parses a source and its stages. In a define, a quoted text after a complete stage
// ends the query (it is the description) instead of being an error.
func (p *parser) parseRead(inDefine bool) (*Query, error) {
	t := p.next()
	if t.kind != tokWord || !contains(Sources, t.text) {
		hint := "start with a source: " + strings.Join(Sources, ", ") + "; or a write: " + strings.Join(Writes, ", ")
		if t.kind == tokWord && contains(Stages, t.text) {
			hint = "a stage needs a source first, e.g. notes | " + t.text + " ..."
		}
		return nil, errAt(t, hint, "unknown source %s", t.describe())
	}
	q := &Query{Source: t.text, pos: t.pos}
	if q.Source == SourceRecall {
		id := p.next()
		if id.kind != tokWord && id.kind != tokString || strings.TrimSpace(id.text) == "" {
			return nil, errAt(id, "write recall <id>: a note or reflection id, an entry id or a saved memory name", "recall needs an id, got %s", id.describe())
		}
		q.ID = strings.TrimSpace(id.text)
		if next := p.peek(); next.kind == tokPipe {
			return nil, errAt(next, "run recall on its own; to filter records use notes, reflections, history or saved", "recall takes no stages")
		}
		return q, nil
	}
	for {
		t := p.peek()
		if t.kind == tokEOF || inDefine && t.kind == tokString {
			return q, nil
		}
		if t.kind != tokPipe {
			return nil, errAt(t, "separate stages with |", "unexpected %s", t.describe())
		}
		p.next()
		stage, err := p.parseStage()
		if err != nil {
			return nil, err
		}
		q.Stages = append(q.Stages, stage)
	}
}

// field reads a field name for stage.
func (p *parser) field(stage string) (token, error) {
	t := p.next()
	if t.kind != tokWord {
		return t, errAt(t, "name a field of the source, e.g. "+stage+" id", "%s needs a field, got %s", stage, t.describe())
	}
	return t, nil
}

func (p *parser) parseStage() (Stage, error) {
	t := p.next()
	if t.kind != tokWord || !contains(Stages, t.text) {
		hint := "stages are: " + strings.Join(Stages, ", ")
		if t.kind == tokWord && contains(Sources, t.text) {
			hint = "a query has one source, at its start"
		}
		return Stage{}, errAt(t, hint, "unknown stage %s", t.describe())
	}
	s := Stage{Kind: t.text, pos: t.pos}
	switch s.Kind {
	case StageSearch:
		w := p.next()
		if w.kind != tokString && w.kind != tokWord || strings.TrimSpace(w.text) == "" {
			return s, errAt(w, `write search "words": every word must appear`, "search needs words, got %s", w.describe())
		}
		s.Words = w.text
	case StageWhere:
		f, err := p.field(s.Kind)
		if err != nil {
			return s, err
		}
		s.Field, s.fieldPos = f.text, f.pos
		op := p.next()
		if op.kind != tokEq && op.kind != tokTilde {
			return s, errAt(op, `write where field=value (equals) or where field~"text" (contains)`, "where needs = or ~ after %s, got %s", f.text, op.describe())
		}
		s.Op = op.text[0]
		v := p.next()
		if v.kind != tokWord && v.kind != tokString {
			return s, errAt(v, `quote a value with spaces: where field~"some text"`, "where %s%s needs a value, got %s", f.text, op.text, v.describe())
		}
		s.Value = v.text
	case StageSince, StageUntil:
		d := p.next()
		if d.kind != tokWord && d.kind != tokString || !validDate(d.text) {
			return s, errAt(d, "write the date as YYYY-MM-DD, e.g. "+s.Kind+" 2023-05-20", "%s needs a date, got %s", s.Kind, d.describe())
		}
		s.Value = d.text
	case StageSort:
		f, err := p.field(s.Kind)
		if err != nil {
			return s, err
		}
		s.Field, s.fieldPos = f.text, f.pos
		if d := p.peek(); d.kind == tokWord && (d.text == "desc" || d.text == "asc") {
			p.next()
			s.Desc = d.text == "desc"
		}
	case StageLimit:
		n := p.next()
		value, err := strconv.Atoi(n.text)
		if n.kind != tokWord || err != nil || value < 1 || value > MaxLimit {
			return s, errAt(n, "write limit N with N from 1 to "+strconv.Itoa(MaxLimit)+"; narrow with search or where for fewer rows", "limit needs a whole number from 1 to %d, got %s", MaxLimit, n.describe())
		}
		s.N = value
	case StageCount:
		if by := p.peek(); by.kind == tokWord && by.text == "by" {
			p.next()
			f, err := p.field("count by")
			if err != nil {
				return s, err
			}
			s.Field, s.fieldPos = f.text, f.pos
		}
	case StageGroup:
		by := p.next()
		if by.kind != tokWord || by.text != "by" {
			return s, errAt(by, "write group by field", "group needs by, got %s", by.describe())
		}
		f, err := p.field("group by")
		if err != nil {
			return s, err
		}
		s.Field, s.fieldPos = f.text, f.pos
	}
	return s, nil
}

// parseWrite parses save, edit, forget, feedback and propose: the write's name, then key=value
// arguments, each key once.
func (p *parser) parseWrite() (*Query, error) {
	t := p.next()
	spec := writeArgs[t.text]
	allowed := append(append([]string{}, spec.required...), spec.optional...)
	usage := writeUsage(t.text)
	q := &Query{Write: t.text, Args: map[string]string{}, pos: t.pos}
	for {
		k := p.next()
		if k.kind == tokEOF {
			break
		}
		if k.kind != tokWord {
			return nil, errAt(k, "write "+usage, "expected key=value, got %s", k.describe())
		}
		if !contains(allowed, k.text) {
			return nil, errAt(k, t.text+" takes "+strings.Join(allowed, ", ")+": "+usage, "unknown key %q", k.text)
		}
		if _, dup := q.Args[k.text]; dup {
			return nil, errAt(k, "give each key once", "%s given twice", k.text)
		}
		if eq := p.next(); eq.kind != tokEq {
			return nil, errAt(eq, "write "+k.text+"=value, quoting a value with spaces", "%s needs = and a value, got %s", k.text, eq.describe())
		}
		v := p.next()
		if v.kind != tokWord && v.kind != tokString {
			return nil, errAt(v, `quote a value with spaces: `+k.text+`="some text"`, "%s= needs a value, got %s", k.text, v.describe())
		}
		q.Args[k.text] = v.text
	}
	for _, key := range spec.required {
		if _, ok := q.Args[key]; !ok {
			return nil, errAt(t, "write "+usage, "%s needs %s=", t.text, key)
		}
	}
	return q, nil
}

// parseDefine parses define <name> = <read query> "description".
func (p *parser) parseDefine() (*Query, error) {
	t := p.next()
	usage := writeUsage(WriteDefine)
	name := p.next()
	if name.kind != tokWord {
		return nil, errAt(name, "write "+usage, "define needs a name, got %s", name.describe())
	}
	if eq := p.next(); eq.kind != tokEq {
		return nil, errAt(eq, "write "+usage, "define %s needs = and a query, got %s", name.text, eq.describe())
	}
	body := p.peek()
	if body.kind == tokWord && contains(Writes, body.text) {
		return nil, errAt(body, "define a read query: a source and its stages", "a defined query cannot write")
	}
	if _, err := p.parseRead(true); err != nil {
		return nil, err
	}
	end := p.tokens[p.i-1].end
	desc := p.next()
	if desc.kind != tokString || strings.TrimSpace(desc.text) == "" {
		return nil, errAt(desc, "end with what it returns in quotes: "+usage, "define needs a description")
	}
	if rest := p.peek(); rest.kind != tokEOF {
		return nil, errAt(rest, "the description ends the define", "unexpected %s", rest.describe())
	}
	return &Query{Write: WriteDefine, Define: &Definition{Name: name.text, Query: strings.TrimSpace(p.query[body.start:end]), Description: desc.text}, pos: t.pos}, nil
}

// writeUsage is a write's syntax, for hints.
func writeUsage(write string) string {
	switch write {
	case WriteSave:
		return `save name=<name> type=user|feedback|project|reference|lesson description="one sentence" [body="why and how to apply it"] [scope=global|project]`
	case WriteEdit:
		return `edit name=<name> [type=...] [description="..."] [body="..."]`
	case WriteForget:
		return `forget name=<name>`
	case WriteFeedback:
		return `feedback name=<name> outcome=helped|harmed quote="words from this conversation that show it"`
	case WriteDefine:
		return `define <name> = <query> "what it returns"`
	case WritePropose:
		return `propose detector=<detector> remedy=<remedy> [text="one line for your worker prompt"] [kind=<kind>] [fullness=<number>]`
	}
	return write
}

// validDate reports whether s is a YYYY-MM-DD date.
func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
