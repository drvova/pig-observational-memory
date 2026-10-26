package selection_test

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/extract"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/selection"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/snippettest"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// listTail is the end of a capped files or commits line: the snippets listing the whole list.
var listTail = regexp.MustCompile(` more, all \d+: (.+)$`)

// decodedSnippets are the snippets a tables summary prints, read back as a reader would: the
// omitted table's query cells decoded with the summary's codec, then the capped lists' tails.
func decodedSnippets(t *testing.T, codec tables.Codec, text string) []string {
	t.Helper()
	body, ok := ledger.TablesSummaryBody(text)
	if !ok {
		t.Fatalf("not a tables summary:\n%s", text)
	}
	decoded, err := codec.Decode(body)
	if err != nil {
		t.Fatalf("%s does not decode: %v\n%s", codec.Name(), err, body)
	}
	var out []string
	for _, table := range decoded {
		if table.Name != "omitted" {
			continue
		}
		for r := range table.Rows {
			q, _ := table.Cell(r, "query")
			out = append(out, q)
		}
	}
	_, lists, _ := strings.Cut(text, "\n\n---\n\n")
	for _, line := range strings.Split(lists, "\n") {
		if m := listTail.FindStringSubmatch(line); m != nil {
			out = append(out, strings.Split(m[1], "; ")...)
		}
	}
	return out
}

// Every snippet a summary prints, in every codec (rows, jsonl, a declared codec) and the lines
// format, decoded from its cell, runs as memory tool code in a namespace child, after a fork
// too, and lists exactly what the summary left out (with the shown records a folded span holds
// between), never cut by its limit; the built-in codecs print it verbatim.
func TestPrintedSnippetsRunInEveryCodec(t *testing.T) {
	entries := branch(70, func(i int, o *ledger.Observation) bool {
		switch {
		case i%7 == 3:
			o.Kind, o.Target, o.Content = ledger.KindFile, fmt.Sprintf("/w/f%02d.go", i%3), fmt.Sprintf("f%02d.go edited at step %d", i%3, i)
		case i%11 == 5:
			o.Kind, o.Target, o.Content = ledger.KindCommit, fmt.Sprintf("abc%04d", i), fmt.Sprintf("commit abc%04d step %d", i, i)
		default:
			o.Content = plain(i)
		}
		return true
	})
	entries = append(entries, testfix.ReflectionsRecordedEntry("refl", []ledger.Reflection{
		{ID: "bbbbbbbbbbb1", Content: "the team ships on fridays", SupportingObservationIDs: []string{recordID(1), recordID(2)}, TokenCount: 5},
	}, "m69"))
	declared, err := tables.Declared(tables.Format{Name: "semi", Header: tables.HeaderCounted, Delimiter: ';', Quote: '\'', Escape: '\\', Escapes: "nrt", Indent: 4})
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name     string
		codec    tables.Codec
		format   string
		verbatim bool
	}{
		{"rows", tables.Rows, ledger.FormatTables, true},
		{"jsonl", tables.JSONLines, ledger.FormatTables, true},
		{"declared", declared, ledger.FormatTables, false},
		{"lines", nil, ledger.FormatLines, true},
	}
	fixed := tokens.EstimateStringTokens(selection.TablesInstructions)
	for _, c := range cases {
		ran := false
		for _, budget := range []int{fixed + 150, fixed + 400, fixed + 900} {
			in := input(entries, budget)
			in.Codec, in.Format = c.codec, c.format
			r := selection.Render(in)
			if r.Text == "" || len(r.Snippets) == 0 {
				continue
			}
			ran = true
			printed := r.Snippets
			if c.format == ledger.FormatTables {
				printed = decodedSnippets(t, c.codec, r.Text)
				if !slices.Equal(printed, r.Snippets) {
					t.Fatalf("%s: decoded %q, printed %q", c.name, printed, r.Snippets)
				}
			}
			if c.verbatim {
				for _, q := range r.Snippets {
					if !strings.Contains(r.Text, q) {
						t.Fatalf("%s: %q is not in the summary verbatim:\n%s", c.name, q, r.Text)
					}
				}
			}
			listed := snippetsList(t, entries, in, r, printed)
			shown := shownIDs(r)
			dropped := map[string]bool{}
			for _, id := range extract.Drops(in.Observations, r.Observations, r.Reflections, in.Folded.ObservationsByID) {
				dropped[id] = true
			}
			for _, o := range in.Observations {
				if !shown[o.ID] && !dropped[o.ID] && !listed[o.ID] {
					t.Fatalf("%s budget %d: omitted %s is listed by no snippet:\n%s", c.name, budget, o.ID, r.Text)
				}
			}
			for id := range listed {
				if dropped[id] || (!shown[id] && !isActive(in, id)) {
					t.Fatalf("%s: a snippet lists %s, which the summary neither shows nor omits", c.name, id)
				}
			}
		}
		if !ran {
			t.Fatalf("%s: no budget left anything out", c.name)
		}
	}
}

func isActive(in selection.Input, id string) bool {
	for _, o := range in.Observations {
		if o.ID == id {
			return true
		}
	}
	for _, r := range in.Reflections {
		if r.ID == id {
			return true
		}
	}
	return false
}

// snippetsList runs each snippet over entries with the summary's drops applied and on a fork
// made after them, asserting both list the same ids and neither is cut, and returns every id
// listed.
func snippetsList(t *testing.T, entries []ledger.Entry, in selection.Input, r selection.Result, snippets []string) map[string]bool {
	t.Helper()
	if drops := extract.Drops(in.Observations, r.Observations, r.Reflections, in.Folded.ObservationsByID); len(drops) > 0 {
		entries = append(append([]ledger.Entry{}, entries...), testfix.ObservationsDroppedEntry("drop", drops, entries[len(entries)-1].ID))
	}
	fork := append(append([]ledger.Entry{}, entries...), testfix.RawMessage("forked", "a different direction"))
	out := map[string]bool{}
	for _, q := range snippets {
		res := snippettest.Run(t, entries, q)
		again := snippettest.Run(t, fork, q)
		if res.Cut() || !slices.Equal(res.IDs, again.IDs) {
			t.Fatalf("%q: %v (%d of %d) / after the fork %v", q, res.IDs, res.Showing, res.Matched, again.IDs)
		}
		for _, id := range res.IDs {
			out[id] = true
		}
	}
	return out
}
