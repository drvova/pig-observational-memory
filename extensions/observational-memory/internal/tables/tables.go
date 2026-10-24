// Package tables is the memory interface's format layer (docs/memory-interface.md, section 1).
// Everything the model reads and writes is a Table of records with named fields; a Codec turns
// tables into text and back, so the text format can be switched (per model, too) without
// touching anything above this package.
//
// The default codec, Rows, is our own small format. It reads like TOON's tabular arrays but
// follows no external spec:
//
//	reflections[2]{id,dates,content}:
//	  a1b2c3d4e5f6,2023-05-20..2023-05-24,User bakes at home with a convection oven.
//	  b2c3d4e5f6a1,2023-05-26,"User hosts Indian dinners: tikka masala, naan, saag paneer."
//
// Every value is a string; a value can also be absent, which is distinct from the empty
// string. Decoding is strict and fails with an *Error that names the line, the column and the
// fix.
package tables

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Codec turns tables into text and back.
type Codec interface {
	// Name identifies the codec, e.g. "rows".
	Name() string
	// Encode renders tables as text, without a trailing newline.
	Encode(tables ...Table) string
	// Decode parses text holding only tables. A failure is an *Error naming the line and
	// the fix.
	Decode(text string) ([]Table, error)
	// Spec is a few lines a prompt can carry so a model can write the format.
	Spec() string
}

// codecs lists every codec in preference order; the first is the default.
var codecs = []Codec{Rows}

// Codecs returns every codec in preference order. The first is the default; a model that keeps
// failing to write one can be switched to the next.
func Codecs() []Codec {
	return append([]Codec(nil), codecs...)
}

// Lookup returns the codec named name.
func Lookup(name string) (Codec, bool) {
	for _, c := range codecs {
		if c.Name() == name {
			return c, true
		}
	}
	return nil, false
}

// Table is a named table of records: field names and rows holding one optional value per field.
type Table struct {
	Name   string
	Fields []string
	Rows   [][]string
	// Absent marks absent cells: Absent[r][c] true means row r has no value for field c. A nil
	// Absent, or a row or column beyond its bounds, means present. An absent cell's text is
	// ignored when encoding and is "" when decoded.
	Absent [][]bool
}

// IsAbsent reports whether the cell at row r, field index c is absent. An index out of range
// (including -1 from Index) reads as present.
func (t Table) IsAbsent(r, c int) bool {
	return r >= 0 && c >= 0 && r < len(t.Absent) && c < len(t.Absent[r]) && t.Absent[r][c]
}

// Index returns the position of field in t.Fields, or -1.
func (t Table) Index(field string) int {
	for i, f := range t.Fields {
		if f == field {
			return i
		}
	}
	return -1
}

// Cell returns row r's value for field and whether it is present. A missing field, row or
// cell reads as absent.
func (t Table) Cell(r int, field string) (string, bool) {
	c := t.Index(field)
	if c < 0 || r < 0 || r >= len(t.Rows) || c >= len(t.Rows[r]) || t.IsAbsent(r, c) {
		return "", false
	}
	return t.Rows[r][c], true
}

// Validate reports why t cannot be encoded so that it decodes back unchanged: an invalid name
// or field name, no fields, a duplicate field, a row whose width differs from the header, or
// an absent cell in a single-field table (whose row would be blank, and a blank last row does
// not survive trimming, so codecs write such a cell as the empty string).
func (t Table) Validate() error {
	if !ValidName(t.Name) {
		return fmt.Errorf("tables: invalid table name %q", t.Name)
	}
	if len(t.Fields) == 0 {
		return fmt.Errorf("tables: table %s has no fields", t.Name)
	}
	seen := make(map[string]bool, len(t.Fields))
	for _, f := range t.Fields {
		if !ValidName(f) {
			return fmt.Errorf("tables: table %s: invalid field name %q", t.Name, f)
		}
		if seen[f] {
			return fmt.Errorf("tables: table %s: duplicate field %q", t.Name, f)
		}
		seen[f] = true
	}
	for r, row := range t.Rows {
		if len(row) != len(t.Fields) {
			return fmt.Errorf("tables: table %s: row %d has %d values, header has %d fields", t.Name, r+1, len(row), len(t.Fields))
		}
		if len(t.Fields) == 1 && t.IsAbsent(r, 0) {
			return fmt.Errorf("tables: table %s: row %d is absent in a single-field table", t.Name, r+1)
		}
	}
	return nil
}

// ValidName reports whether s is a valid table name, field name or key: one or more letters,
// digits, `_`, `-` and `.`.
func ValidName(s string) bool {
	return s != "" && scanName(s, 0) == len(s)
}

// scanName returns the byte offset just past the run of name characters starting at p.
func scanName(s string, p int) int {
	for p < len(s) {
		r, n := utf8.DecodeRuneInString(s[p:])
		if !isNameRune(r) || (r == utf8.RuneError && n <= 1) {
			break
		}
		p += n
	}
	return p
}

func isNameRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '.'
}

// Error is a decoding failure: where it happened and how to fix it, short enough to hand back
// to a model as a tool result.
type Error struct {
	// Line and Column are 1-based; Column counts characters (runes), not bytes.
	Line, Column int
	// Msg says what is wrong; Hint says how to fix it.
	Msg, Hint string
}

// Error formats as "line L, column C: msg; hint".
func (e *Error) Error() string {
	s := fmt.Sprintf("line %d, column %d: %s", e.Line, e.Column, e.Msg)
	if e.Hint != "" {
		s += "; " + e.Hint
	}
	return s
}

// AsError returns err as an *Error when it is one.
func AsError(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}

// headerText renders a field list as it appears in a header, for messages.
func headerText(fields []string) string {
	return "{" + strings.Join(fields, ",") + "}"
}
