package legacyquery

import (
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
)

// The strict legacy-to-snippet hint (docs/code-tool.md, "Errors" and "Migration"): a read query
// of the removed language translated, as text only, into the om call that lists the same rows.
// The translation goes through om's one snippet formatter in its strict mode, since the query's
// words and ids are model-written: ids must match the generated id formats, dates and limits
// their formats, words are quoted with strconv.Quote, and the text is parsed back and must be one
// read call with literal options. Nothing here runs anything, and no code generated from model
// words is ever run by the extension.

// sourceFuncs are the om reads each source translates to.
var sourceFuncs = map[string]string{
	SourceNotes:       omwire.MNotes,
	SourceReflections: omwire.MReflections,
	SourceHistory:     omwire.MHistory,
	SourceSaved:       omwire.MSaved,
	SourceHarness:     omwire.MLayer,
	SourceRecall:      omwire.MRecall,
}

// writeEquivalents name the om write that replaces each write of the language.
var writeEquivalents = map[string]string{
	WriteSave:     "om.Save",
	WriteEdit:     "om.Edit",
	WriteForget:   "om.ForgetMemory",
	WriteFeedback: "om.Feedback",
	WriteDefine:   "a kept func (om.Define only says defined tools are retired)",
	WritePropose:  "nothing: rules are proposed inside the compaction's consolidation request (om.Propose says so)",
	WriteHarness:  "om.LayerForget (definitions are proposed inside the compaction's consolidation request)",
	WriteRollback: "om.LayerRollback",
}

// Hint is the snippet equivalent to query, a read of the removed language, or "" and why there
// is none: a write (with the om write that replaces it), a query that does not parse, one that
// takes $input, or a form with no exact equivalent.
func Hint(query string) (snippet, reason string) {
	if strings.Contains(query, "$"+InputParameter) {
		return "", "it takes $" + InputParameter + ": keep a func with a parameter instead"
	}
	q, err := Parse(query)
	if err != nil {
		return "", "it does not parse: " + err.Error()
	}
	return HintOf(q)
}

// HintOf is Hint for a parsed query.
func HintOf(q *Query) (snippet, reason string) {
	if q.Write != "" {
		return "", fmt.Sprintf("it is a write (%s); its replacement is %s", q.Write, writeEquivalents[q.Write])
	}
	fn := sourceFuncs[q.Source]
	var o omwire.Options
	if q.Source == SourceRecall {
		if q.Paged {
			n := q.Offset
			o.Offset = &n
		}
		s, err := om.FormatReadStrict(fn, q.ID, o, "")
		if err != nil {
			return "", "no snippet: " + err.Error()
		}
		return s, ""
	}
	// The om options merge: filters commute, and the sort and limit apply after them. So a
	// query translates exactly only when its filters come first, then at most one sort, then at
	// most one limit, then at most a plain count, and when no bound is given twice.
	const (
		filters = iota
		sorted
		limited
		counted
	)
	phase := filters
	tail := ""
	seen := map[string]bool{}
	event := map[string]bool{}
	asOf := false
	field := func(f string) string {
		if q.Source == SourceHistory && f == "content" {
			return "text"
		}
		return f
	}
	for _, s := range q.Stages {
		switch s.Kind {
		case StageSearch, StageWhere, StageSince, StageUntil, StageFrom, StageThrough:
			if phase != filters {
				return "", "no exact equivalent: a filter after sort, limit or count"
			}
		case StageSort:
			if phase != filters {
				return "", "no exact equivalent: sort after limit or count"
			}
			phase = sorted
		case StageLimit:
			if phase > sorted {
				return "", "no exact equivalent: limit after count"
			}
			phase = limited
		case StageCount:
			if s.Field != "" {
				return "", "no exact equivalent in one call: count by reads every row; read with om.Limit(200) and call .Count(\"" + s.Field + "\") on the list"
			}
			if phase == limited {
				return "", "no exact equivalent: count after limit"
			}
			phase, tail = counted, "Matched"
		case StageGroup:
			return "", "no exact equivalent in one call: group by reads every row; read with om.Limit(200) and call .Group(\"" + s.Field + "\") on the list"
		}
		switch s.Kind {
		case StageSince, StageUntil, StageFrom, StageThrough, StageSort, StageLimit:
			if seen[s.Kind] {
				return "", "no exact equivalent: " + s.Kind + " given twice"
			}
			seen[s.Kind] = true
		}
		switch s.Kind {
		case StageSearch:
			o.Search = append(o.Search, s.Words)
		case StageWhere:
			p := omwire.Pair{Field: field(s.Field), Value: s.Value}
			if s.Op == '~' {
				o.Contains = append(o.Contains, p)
			} else {
				o.Where = append(o.Where, p)
			}
		case StageSince:
			o.Since, event[s.Kind] = s.Value, s.Event
		case StageUntil:
			o.Until, event[s.Kind] = s.Value, s.Event
			asOf = asOf || s.AsOf
		case StageFrom:
			o.From = s.Value
		case StageThrough:
			o.Through = s.Value
			asOf = asOf || s.AsOf
		case StageSort:
			o.SortField, o.SortDesc = field(s.Field), s.Desc
		case StageLimit:
			n := s.N
			o.Limit = &n
		}
	}
	if seen[StageSince] && seen[StageUntil] && event[StageSince] != event[StageUntil] {
		return "", "no exact equivalent: event on only one of since and until"
	}
	o.ByEvent = event[StageSince] || event[StageUntil]
	if asOf && seen[StageUntil] && seen[StageThrough] {
		return "", "no exact equivalent: asof with both until and through"
	}
	o.AsOf = asOf
	s, err := om.FormatReadStrict(fn, "", o, tail)
	if err != nil {
		return "", "no snippet: " + err.Error()
	}
	return s, ""
}
