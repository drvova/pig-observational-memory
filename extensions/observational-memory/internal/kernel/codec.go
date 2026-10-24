package kernel

import (
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// The kernel's fixed test tables (docs/memory-interface.md, section 8): a format the model
// declares is accepted only when the codec it yields round-trips every one of them, so a
// declaration that would lose or garble a value the memory writes (an absent cell, an empty
// string, a delimiter, a quote, an escape, a line break, a value with edge spaces, a table of
// one field or of no rows) never reaches a worker or a summary. They are kernel data: no
// definition can change them.

// TestTables are the kernel's fixed test tables, as groups encoded together.
func TestTables() [][]tables.Table {
	tricky := []string{
		"plain", "", " leading", "trailing ", "a,b", "a;b", "a|b", "a\tb", `say "hi"`, "it's", `back\slash`,
		"line\nbreak", "carriage\rreturn", "colon: here", "{braces}", "[brackets]", "#hash", "~tilde", "naïve café ✓", "100%", "a=b", "x/y",
	}
	values := tables.Table{Name: "values", Fields: []string{"id", "value"}}
	for i, v := range tricky {
		values.Rows = append(values.Rows, []string{fmt.Sprintf("v%02d", i), v})
		values.Absent = append(values.Absent, []bool{false, false})
	}
	return [][]tables.Table{
		{{
			Name:   "notes",
			Fields: []string{"id", "when", "kind", "scope", "content", "superseded_by"},
			Rows: [][]string{
				{"a1b2c3d4e5f6", "2023-05-20", "fact", "on edX", "User finished three courses.", ""},
				{"b2c3d4e5f6a1", "2023-05-26", "", "", "User hosts Indian dinners: tikka masala, naan, saag paneer.", "c3d4e5f6a1b2"},
			},
			Absent: [][]bool{{false, false, false, false, false, true}, {false, false, true, true, false, false}},
		}},
		{values},
		{
			{Name: "one", Fields: []string{"only"}, Rows: [][]string{{""}, {"x"}, {"a,b"}}},
			{Name: "empty", Fields: []string{"id", "content"}, Rows: [][]string{}},
			{Name: "omitted", Fields: []string{"from", "to", "count", "first_id"}, Rows: [][]string{{"2023-05-01", "2023-05-09", "4", "d4e5f6a1b2c3"}, {"", "", "1", "e5f6a1b2c3d4"}},
				Absent: [][]bool{nil, {true, true, false, false}}},
		},
	}
}

// AcceptCodec is the round trip a declared codec must pass before the layer accepts its
// format: every group of TestTables, and every table alone, decodes back to what was encoded
// (names, fields, values, and which cells are absent). The refusal names the first table and
// cell that did not survive.
func AcceptCodec(c tables.Codec) error {
	if c == nil {
		return fmt.Errorf("refused: no codec to test")
	}
	groups := TestTables()
	for _, group := range groups {
		if err := roundTrip(c, group); err != nil {
			return err
		}
		if len(group) > 1 {
			for _, t := range group {
				if err := roundTrip(c, []tables.Table{t}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func roundTrip(c tables.Codec, want []tables.Table) error {
	refuse := func(format string, args ...any) error {
		return fmt.Errorf("refused: the format %s does not round-trip the kernel's test tables: %s", c.Name(), fmt.Sprintf(format, args...))
	}
	text := c.Encode(want...)
	got, err := c.Decode(text)
	if err != nil {
		return refuse("its own encoding does not decode (%v)", err)
	}
	if len(got) != len(want) {
		return refuse("%d tables encoded, %d decoded", len(want), len(got))
	}
	for i := range want {
		w, g := want[i], got[i]
		if w.Name != g.Name || strings.Join(w.Fields, "\x00") != strings.Join(g.Fields, "\x00") {
			return refuse("table %s{%s} came back as %s{%s}", w.Name, strings.Join(w.Fields, ","), g.Name, strings.Join(g.Fields, ","))
		}
		if len(w.Rows) != len(g.Rows) {
			return refuse("table %s has %d rows, %d came back", w.Name, len(w.Rows), len(g.Rows))
		}
		for r := range w.Rows {
			if len(g.Rows[r]) != len(w.Fields) {
				return refuse("table %s row %d came back with %d values", w.Name, r+1, len(g.Rows[r]))
			}
			for f := range w.Fields {
				wa, ga := w.IsAbsent(r, f), g.IsAbsent(r, f)
				// A single-field table writes an absent cell as the empty string.
				if len(w.Fields) == 1 && wa {
					wa = false
				}
				if wa != ga || (!wa && w.Rows[r][f] != g.Rows[r][f]) {
					return refuse("table %s row %d field %s: %q (absent %v) came back as %q (absent %v)", w.Name, r+1, w.Fields[f], w.Rows[r][f], wa, g.Rows[r][f], ga)
				}
			}
		}
	}
	return nil
}
