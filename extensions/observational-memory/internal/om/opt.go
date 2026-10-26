package om

import (
	"slices"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
)

// Opt is one or more options of a read. Every option is a function and also a method, so
// options chain without commas: om.From(`e1`).Through(`e9`).Limit(50). Options given to one
// read are merged in order: a later scalar wins, and searches and field filters accumulate.
type Opt struct{ w omwire.Options }

// with returns a copy of o with its slices copied, so a chained option never changes the Opt it
// was chained on.
func (o Opt) with(set func(*omwire.Options)) Opt {
	o.w.Search = slices.Clone(o.w.Search)
	o.w.Where = slices.Clone(o.w.Where)
	o.w.Contains = slices.Clone(o.w.Contains)
	set(&o.w)
	return o
}

// Search keeps rows matching every literal word, case-insensitive, over every column.
func Search(words string) Opt { return Opt{}.Search(words) }

// From keeps rows at or after this entry id (an anchor or a record id).
func From(id string) Opt { return Opt{}.From(id) }

// Through keeps rows at or before this entry id.
func Through(id string) Opt { return Opt{}.Through(id) }

// Since keeps rows on or after this date (YYYY-MM-DD, entry time).
func Since(date string) Opt { return Opt{}.Since(date) }

// Until keeps rows on or before this date (YYYY-MM-DD, entry time).
func Until(date string) Opt { return Opt{}.Until(date) }

// ByEvent makes Since and Until compare a record's event date when it has one.
func ByEvent() Opt { return Opt{}.ByEvent() }

// AsOf reads notes and reflections as they stood at the Until or Through bound.
func AsOf() Opt { return Opt{}.AsOf() }

// Where keeps rows whose field equals value (case-insensitive).
func Where(field, value string) Opt { return Opt{}.Where(field, value) }

// Contains keeps rows whose field contains value.
func Contains(field, value string) Opt { return Opt{}.Contains(field, value) }

// Kind keeps records of this kind: Where("kind", k).
func Kind(k string) Opt { return Opt{}.Kind(k) }

// Subject keeps records whose canonical or raw subject equals s.
func Subject(s string) Opt { return Opt{}.Subject(s) }

// WithStatus keeps records with this status: proposed, hypothetical or retracted.
func WithStatus(s string) Opt { return Opt{}.WithStatus(s) }

// SortBy orders rows by field, descending when desc is true.
func SortBy(field string, desc bool) Opt { return Opt{}.SortBy(field, desc) }

// Limit shows at most n rows: 1 to 200, 20 when not given.
func Limit(n int) Opt { return Opt{}.Limit(n) }

// Offset is the character offset of the page Recall shows.
func Offset(n int) Opt { return Opt{}.Offset(n) }

// Failed keeps attempts that failed.
func Failed() Opt { return Opt{}.Failed() }

// Worked keeps attempts that worked.
func Worked() Opt { return Opt{}.Worked() }

// Open keeps attempts with no outcome yet.
func Open() Opt { return Opt{}.Open() }

// Search adds a search for every literal word.
func (o Opt) Search(words string) Opt {
	return o.with(func(w *omwire.Options) { w.Search = append(w.Search, words) })
}

// From sets the first entry id.
func (o Opt) From(id string) Opt { return o.with(func(w *omwire.Options) { w.From = id }) }

// Through sets the last entry id.
func (o Opt) Through(id string) Opt { return o.with(func(w *omwire.Options) { w.Through = id }) }

// Since sets the first date.
func (o Opt) Since(date string) Opt { return o.with(func(w *omwire.Options) { w.Since = date }) }

// Until sets the last date.
func (o Opt) Until(date string) Opt { return o.with(func(w *omwire.Options) { w.Until = date }) }

// ByEvent compares event dates.
func (o Opt) ByEvent() Opt { return o.with(func(w *omwire.Options) { w.ByEvent = true }) }

// AsOf asks for the point-in-time view.
func (o Opt) AsOf() Opt { return o.with(func(w *omwire.Options) { w.AsOf = true }) }

// Where adds a field equality filter.
func (o Opt) Where(field, value string) Opt {
	return o.with(func(w *omwire.Options) { w.Where = append(w.Where, omwire.Pair{Field: field, Value: value}) })
}

// Contains adds a field substring filter.
func (o Opt) Contains(field, value string) Opt {
	return o.with(func(w *omwire.Options) {
		w.Contains = append(w.Contains, omwire.Pair{Field: field, Value: value})
	})
}

// Kind adds Where("kind", k).
func (o Opt) Kind(k string) Opt { return o.Where("kind", k) }

// Subject sets the subject.
func (o Opt) Subject(s string) Opt { return o.with(func(w *omwire.Options) { w.Subject = s }) }

// WithStatus sets the status.
func (o Opt) WithStatus(s string) Opt { return o.with(func(w *omwire.Options) { w.Status = s }) }

// SortBy sets the order.
func (o Opt) SortBy(field string, desc bool) Opt {
	return o.with(func(w *omwire.Options) { w.SortField, w.SortDesc = field, desc })
}

// Limit sets the row limit.
func (o Opt) Limit(n int) Opt { return o.with(func(w *omwire.Options) { w.Limit = &n }) }

// Offset sets Recall's page offset.
func (o Opt) Offset(n int) Opt { return o.with(func(w *omwire.Options) { w.Offset = &n }) }

// Failed keeps failed attempts.
func (o Opt) Failed() Opt { return o.with(func(w *omwire.Options) { w.Failed = true }) }

// Worked keeps attempts that worked.
func (o Opt) Worked() Opt { return o.with(func(w *omwire.Options) { w.Worked = true }) }

// Open keeps open attempts.
func (o Opt) Open() Opt { return o.with(func(w *omwire.Options) { w.Open = true }) }

// mergeOpts merges a read's options in order into what crosses the pipe.
func mergeOpts(opts []Opt) omwire.Options {
	var m omwire.Options
	for _, o := range opts {
		w := o.w
		m.Search = append(m.Search, w.Search...)
		m.Where = append(m.Where, w.Where...)
		m.Contains = append(m.Contains, w.Contains...)
		for _, s := range []struct {
			dst *string
			src string
		}{{&m.From, w.From}, {&m.Through, w.Through}, {&m.Since, w.Since}, {&m.Until, w.Until}, {&m.Subject, w.Subject}, {&m.Status, w.Status}} {
			if s.src != "" {
				*s.dst = s.src
			}
		}
		if w.SortField != "" {
			m.SortField, m.SortDesc = w.SortField, w.SortDesc
		}
		if w.Limit != nil {
			n := *w.Limit
			m.Limit = &n
		}
		if w.Offset != nil {
			n := *w.Offset
			m.Offset = &n
		}
		m.ByEvent = m.ByEvent || w.ByEvent
		m.AsOf = m.AsOf || w.AsOf
		m.Failed = m.Failed || w.Failed
		m.Worked = m.Worked || w.Worked
		m.Open = m.Open || w.Open
	}
	return m
}

// SaveOpt is an option of Save.
type SaveOpt struct{ w omwire.SaveOptions }

// Body sets the memory's body.
func Body(text string) SaveOpt { return SaveOpt{omwire.SaveOptions{Body: &text}} }

// Scope sets where the memory applies: "global" or "project".
func Scope(scope string) SaveOpt { return SaveOpt{omwire.SaveOptions{Scope: &scope}} }

// Quoting gives the user's words that confirm the memory, which saves it live.
func Quoting(text string) SaveOpt { return SaveOpt{omwire.SaveOptions{Quoting: &text}} }

func mergeSave(opts []SaveOpt) omwire.SaveOptions {
	var m omwire.SaveOptions
	for _, o := range opts {
		if o.w.Body != nil {
			m.Body = o.w.Body
		}
		if o.w.Scope != nil {
			m.Scope = o.w.Scope
		}
		if o.w.Quoting != nil {
			m.Quoting = o.w.Quoting
		}
	}
	return m
}

// EditOpt is an option of Edit.
type EditOpt struct{ w omwire.EditOptions }

// SetType changes the memory's type.
func SetType(typ string) EditOpt { return EditOpt{omwire.EditOptions{Type: &typ}} }

// SetDescription changes the memory's description.
func SetDescription(text string) EditOpt { return EditOpt{omwire.EditOptions{Description: &text}} }

// SetBody changes the memory's body.
func SetBody(text string) EditOpt { return EditOpt{omwire.EditOptions{Body: &text}} }

func mergeEdit(opts []EditOpt) omwire.EditOptions {
	var m omwire.EditOptions
	for _, o := range opts {
		if o.w.Type != nil {
			m.Type = o.w.Type
		}
		if o.w.Description != nil {
			m.Description = o.w.Description
		}
		if o.w.Body != nil {
			m.Body = o.w.Body
		}
	}
	return m
}
