package om

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// The list types are concrete, since the interpreter cannot take a generic host type; their
// methods share these helpers. Every list method runs in the child on the rows it holds, so it
// costs no call.

// The columns each list's Table shows, in order.
var (
	entryFields   = []string{"id", "when", "role", "text"}
	memoryFields  = []string{"name", "type", "scope", "updated", "description", "state", "reason"}
	layerFields   = []string{"model", "kind", "name", "state", "generation", "origin", "stale", "engine"}
	retiredFields = []string{"name", "origin", "query", "snippet", "reason"}
)

func tableOf[R any](name string, fields []string, rows []R, field func(R, string) string) string {
	t := tables.Table{Name: name, Fields: fields}
	for _, r := range rows {
		row := make([]string, len(fields))
		for i, f := range fields {
			row[i] = field(r, f)
		}
		t.Rows = append(t.Rows, row)
	}
	return strings.TrimSuffix(tables.Rows.Encode(t), "\n")
}

func countOf[R any](rows []R, name string, field func(R, string) string) map[string]int {
	out := map[string]int{}
	for _, r := range rows {
		out[field(r, name)]++
	}
	return out
}

func groupOf[R any](rows []R, name string, field func(R, string) string) map[string][]R {
	out := map[string][]R{}
	for _, r := range rows {
		k := field(r, name)
		out[k] = append(out[k], r)
	}
	return out
}

func filterOf[R any](rows []R, keep func(R) bool) []R {
	var out []R
	for _, r := range rows {
		if keep(r) {
			out = append(out, r)
		}
	}
	return out
}

// listString is a list's table, then how many rows matched and the next page when it was cut.
func listString(table string, matched, showing int, next string) string {
	s := table
	if showing < matched {
		s += fmt.Sprintf("\nmatched %d, showing %d", matched, showing)
		if next != "" {
			s += "\nnext page: " + next
		}
	}
	return s
}

// NoteList is a page of records.
type NoteList struct {
	// Rows are the rows held.
	Rows []Note
	// Matched is how many rows matched, Showing how many are held.
	Matched, Showing int
	// Next is the snippet for the next page, "" when every match is shown.
	Next string
}

// Len is the number of rows held.
func (l NoteList) Len() int { return len(l.Rows) }

// IDs are the rows' record ids, in order.
func (l NoteList) IDs() []string {
	out := make([]string, len(l.Rows))
	for i, r := range l.Rows {
		out[i] = r.ID
	}
	return out
}

// Table is the rows in the rows codec, the same for every model.
func (l NoteList) Table() string { return tableOf("notes", NoteFields, l.Rows, Note.cell) }

// Count counts the rows by the value of a field.
func (l NoteList) Count(field string) map[string]int { return countOf(l.Rows, field, Note.field) }

// Group splits the rows by the value of a field.
func (l NoteList) Group(field string) map[string]NoteList {
	out := map[string]NoteList{}
	for k, rows := range groupOf(l.Rows, field, Note.field) {
		out[k] = NoteList{Rows: rows, Matched: len(rows), Showing: len(rows)}
	}
	return out
}

// Filter keeps the rows keep accepts.
func (l NoteList) Filter(keep func(Note) bool) NoteList {
	rows := filterOf(l.Rows, keep)
	return NoteList{Rows: rows, Matched: len(rows), Showing: len(rows)}
}

// String is the table, then "matched N, showing M" and the next page when it was cut.
func (l NoteList) String() string { return listString(l.Table(), l.Matched, l.Showing, l.Next) }

// ReflectionList is a page of standing reflections.
type ReflectionList struct {
	// Rows are the rows held.
	Rows []Reflection
	// Matched is how many rows matched, Showing how many are held.
	Matched, Showing int
	// Next is the snippet for the next page, "" when every match is shown.
	Next string
}

// Len is the number of rows held.
func (l ReflectionList) Len() int { return len(l.Rows) }

// IDs are the rows' reflection ids, in order.
func (l ReflectionList) IDs() []string {
	out := make([]string, len(l.Rows))
	for i, r := range l.Rows {
		out[i] = r.ID
	}
	return out
}

// Table is the rows in the rows codec.
func (l ReflectionList) Table() string {
	return tableOf("reflections", ReflectionFields, l.Rows, Reflection.cell)
}

// Count counts the rows by the value of a field.
func (l ReflectionList) Count(field string) map[string]int {
	return countOf(l.Rows, field, Reflection.field)
}

// Group splits the rows by the value of a field.
func (l ReflectionList) Group(field string) map[string]ReflectionList {
	out := map[string]ReflectionList{}
	for k, rows := range groupOf(l.Rows, field, Reflection.field) {
		out[k] = ReflectionList{Rows: rows, Matched: len(rows), Showing: len(rows)}
	}
	return out
}

// Filter keeps the rows keep accepts.
func (l ReflectionList) Filter(keep func(Reflection) bool) ReflectionList {
	rows := filterOf(l.Rows, keep)
	return ReflectionList{Rows: rows, Matched: len(rows), Showing: len(rows)}
}

// String is the table, then the counts and the next page when it was cut.
func (l ReflectionList) String() string {
	return listString(l.Table(), l.Matched, l.Showing, l.Next)
}

// EntryList is a page of history, newest first.
type EntryList struct {
	// Rows are the rows held.
	Rows []Entry
	// Matched is how many rows matched, Showing how many are held.
	Matched, Showing int
	// Next is the snippet for the next page, "" when every match is shown.
	Next string
	// Counts are, for a search with no match, each word's matches on the current branch.
	Counts map[string]int
}

// Len is the number of rows held.
func (l EntryList) Len() int { return len(l.Rows) }

// IDs are the rows' entry ids, in order.
func (l EntryList) IDs() []string {
	out := make([]string, len(l.Rows))
	for i, r := range l.Rows {
		out[i] = r.ID
	}
	return out
}

// Table is the rows in the rows codec.
func (l EntryList) Table() string { return tableOf("history", entryFields, l.Rows, Entry.cell) }

// Count counts the rows by the value of a field.
func (l EntryList) Count(field string) map[string]int { return countOf(l.Rows, field, Entry.field) }

// Group splits the rows by the value of a field.
func (l EntryList) Group(field string) map[string]EntryList {
	out := map[string]EntryList{}
	for k, rows := range groupOf(l.Rows, field, Entry.field) {
		out[k] = EntryList{Rows: rows, Matched: len(rows), Showing: len(rows)}
	}
	return out
}

// Filter keeps the rows keep accepts.
func (l EntryList) Filter(keep func(Entry) bool) EntryList {
	rows := filterOf(l.Rows, keep)
	return EntryList{Rows: rows, Matched: len(rows), Showing: len(rows)}
}

// String is the table, then the counts and the next page when it was cut, and each searched
// word's matches when nothing matched.
func (l EntryList) String() string {
	s := listString(l.Table(), l.Matched, l.Showing, l.Next)
	if len(l.Rows) == 0 && len(l.Counts) > 0 {
		var parts []string
		for _, w := range slices.Sorted(maps.Keys(l.Counts)) {
			parts = append(parts, fmt.Sprintf("%s: %d", w, l.Counts[w]))
		}
		s += "\nno entry matches every word; matches per word: " + strings.Join(parts, ", ")
	}
	return s
}

// MemoryList is a page of curated memories.
type MemoryList struct {
	// Rows are the rows held.
	Rows []Memory
	// Matched is how many rows matched, Showing how many are held.
	Matched, Showing int
	// Next is the snippet for the next page, "" when every match is shown.
	Next string
}

// Len is the number of rows held.
func (l MemoryList) Len() int { return len(l.Rows) }

// IDs are the rows' names, in order.
func (l MemoryList) IDs() []string {
	out := make([]string, len(l.Rows))
	for i, r := range l.Rows {
		out[i] = r.Name
	}
	return out
}

// Table is the rows in the rows codec.
func (l MemoryList) Table() string { return tableOf("saved", memoryFields, l.Rows, Memory.field) }

// Count counts the rows by the value of a field.
func (l MemoryList) Count(field string) map[string]int { return countOf(l.Rows, field, Memory.field) }

// Group splits the rows by the value of a field.
func (l MemoryList) Group(field string) map[string]MemoryList {
	out := map[string]MemoryList{}
	for k, rows := range groupOf(l.Rows, field, Memory.field) {
		out[k] = MemoryList{Rows: rows, Matched: len(rows), Showing: len(rows)}
	}
	return out
}

// Filter keeps the rows keep accepts.
func (l MemoryList) Filter(keep func(Memory) bool) MemoryList {
	rows := filterOf(l.Rows, keep)
	return MemoryList{Rows: rows, Matched: len(rows), Showing: len(rows)}
}

// String is the table, then the counts and the next page when it was cut.
func (l MemoryList) String() string { return listString(l.Table(), l.Matched, l.Showing, l.Next) }

// LayerList is a page of the layer listing, every model's rows labelled with their model.
type LayerList struct {
	// Rows are the rows held.
	Rows []LayerRow
	// Matched is how many rows matched, Showing how many are held.
	Matched, Showing int
	// Next is the snippet for the next page, "" when every match is shown.
	Next string
}

// Len is the number of rows held.
func (l LayerList) Len() int { return len(l.Rows) }

// IDs are the rows' names, in order.
func (l LayerList) IDs() []string {
	out := make([]string, len(l.Rows))
	for i, r := range l.Rows {
		out[i] = r.Name
	}
	return out
}

// Table is the rows in the rows codec.
func (l LayerList) Table() string { return tableOf("layer", layerFields, l.Rows, LayerRow.field) }

// Count counts the rows by the value of a field.
func (l LayerList) Count(field string) map[string]int {
	return countOf(l.Rows, field, LayerRow.field)
}

// Group splits the rows by the value of a field.
func (l LayerList) Group(field string) map[string]LayerList {
	out := map[string]LayerList{}
	for k, rows := range groupOf(l.Rows, field, LayerRow.field) {
		out[k] = LayerList{Rows: rows, Matched: len(rows), Showing: len(rows)}
	}
	return out
}

// Filter keeps the rows keep accepts.
func (l LayerList) Filter(keep func(LayerRow) bool) LayerList {
	rows := filterOf(l.Rows, keep)
	return LayerList{Rows: rows, Matched: len(rows), Showing: len(rows)}
}

// String is the table, then the counts and the next page when it was cut.
func (l LayerList) String() string { return listString(l.Table(), l.Matched, l.Showing, l.Next) }

// RetiredList is a page of retired defined queries.
type RetiredList struct {
	// Rows are the rows held.
	Rows []RetiredQuery
	// Matched is how many rows matched, Showing how many are held.
	Matched, Showing int
	// Next is the snippet for the next page, "" when every match is shown.
	Next string
}

// Len is the number of rows held.
func (l RetiredList) Len() int { return len(l.Rows) }

// IDs are the rows' names, in order.
func (l RetiredList) IDs() []string {
	out := make([]string, len(l.Rows))
	for i, r := range l.Rows {
		out[i] = r.Name
	}
	return out
}

// Table is the rows in the rows codec.
func (l RetiredList) Table() string {
	return tableOf("retired", retiredFields, l.Rows, RetiredQuery.field)
}

// Count counts the rows by the value of a field.
func (l RetiredList) Count(field string) map[string]int {
	return countOf(l.Rows, field, RetiredQuery.field)
}

// Group splits the rows by the value of a field.
func (l RetiredList) Group(field string) map[string]RetiredList {
	out := map[string]RetiredList{}
	for k, rows := range groupOf(l.Rows, field, RetiredQuery.field) {
		out[k] = RetiredList{Rows: rows, Matched: len(rows), Showing: len(rows)}
	}
	return out
}

// Filter keeps the rows keep accepts.
func (l RetiredList) Filter(keep func(RetiredQuery) bool) RetiredList {
	rows := filterOf(l.Rows, keep)
	return RetiredList{Rows: rows, Matched: len(rows), Showing: len(rows)}
}

// String is the table, then the counts and the next page when it was cut.
func (l RetiredList) String() string { return listString(l.Table(), l.Matched, l.Showing, l.Next) }
