package declare

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/legacyquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// SummaryTable is the table a summary declaration holds: one row per section, each a memory
// query whose tables the summary shows, in order.
const SummaryTable = "summary"

// SummaryColumns are a summary declaration's columns: the section's table name, its query,
// and optionally the fields it shows (space-separated, in order; none shows every field).
var SummaryColumns = []string{"table", "query", "fields"}

// Summary bounds: a summary is a few sections, each one short query.
const (
	MaxSummarySections = 8
	MaxSummaryQuery    = 200
)

// summarySources are the sources a summary query may read: the memory the compaction folds.
var summarySources = []string{legacyquery.SourceNotes, legacyquery.SourceReflections}

// SummarySection is one section of a summary declaration.
type SummarySection struct {
	// Table names the section's tables: the query's tables are named after its source, and
	// the section's name replaces the source in them (a grouped "notes.baking" of a section
	// named "facts" is "facts.baking").
	Table  string
	Query  string
	Fields []string
	// source is the query's source.
	source string
	// sorted is true when the query orders its rows (a sort stage), so its first rows are
	// the ones it puts first.
	sorted bool
}

// Summary is one parsed summary declaration.
type Summary struct {
	Sections []SummarySection
}

// ParseSummary reads a summary declaration: a SummaryTable in the rows codec, or a single
// query (one section named after its source). Each query must parse in the frozen recognizer
// (legacyquery: summary queries are validated, never run, under memory v2), read notes or
// reflections, and stay within MaxSummaryQuery characters; a section's fields must be fields
// of its source (for notes, also fields a note schema adds), start with id and include
// content, so every shown record can be read back and the omitted ones disclosed (a query that
// counts shows its count fields and takes none); no section may be named like the kernel's omitted table.
func ParseSummary(text string) (Summary, error) {
	text = strings.TrimSpace(text)
	var rows [][3]string
	if decoded, err := tables.Rows.Decode(text); err == nil && len(decoded) == 1 && decoded[0].Name == SummaryTable {
		t := decoded[0]
		for _, c := range t.Fields {
			if !containsString(SummaryColumns, c) {
				return Summary{}, fmt.Errorf("unknown summary column %q; the columns are %s", c, strings.Join(SummaryColumns, ","))
			}
		}
		for r := range t.Rows {
			table, _ := t.Cell(r, "table")
			query, _ := t.Cell(r, "query")
			fields, _ := t.Cell(r, "fields")
			rows = append(rows, [3]string{table, query, fields})
		}
	} else if strings.HasPrefix(text, SummaryTable+"[") {
		return Summary{}, fmt.Errorf("a summary declaration is one %s table with the columns %s, or a single query: %v", SummaryTable, strings.Join(SummaryColumns, ","), err)
	} else {
		rows = append(rows, [3]string{"", text, ""})
	}
	if len(rows) == 0 || len(rows) > MaxSummarySections {
		return Summary{}, fmt.Errorf("a summary has 1 to %d sections", MaxSummarySections)
	}
	s := Summary{}
	seen := map[string]bool{}
	for _, row := range rows {
		section, err := parseSection(row[0], row[1], row[2])
		if err != nil {
			return Summary{}, err
		}
		if seen[section.Table] {
			return Summary{}, fmt.Errorf("summary section %s is named twice", section.Table)
		}
		seen[section.Table] = true
		s.Sections = append(s.Sections, section)
	}
	return s, nil
}

func parseSection(table, query, fields string) (SummarySection, error) {
	query = strings.TrimSpace(query)
	if n := utf8.RuneCountInString(query); n == 0 || n > MaxSummaryQuery {
		return SummarySection{}, fmt.Errorf("a summary query is 1 to %d characters (this one %d)", MaxSummaryQuery, n)
	}
	q, err := legacyquery.Parse(query)
	if err != nil {
		return SummarySection{}, fmt.Errorf("summary query %q: %w", query, err)
	}
	if q.Write != "" || !containsString(summarySources, q.Source) {
		return SummarySection{}, fmt.Errorf("summary query %q must read %s", query, strings.Join(summarySources, " or "))
	}
	counted, sorted := false, false
	for _, stage := range q.Stages {
		counted = counted || stage.Kind == legacyquery.StageCount
		sorted = sorted || stage.Kind == legacyquery.StageSort
	}
	if table == "" {
		table = q.Source
	}
	if !tables.ValidName(table) || strings.Contains(table, ".") {
		return SummarySection{}, fmt.Errorf("summary section name %q must be letters, digits, _ and -", table)
	}
	if table == ledger.OmittedTableName {
		return SummarySection{}, fmt.Errorf("refused: %s is the kernel's disclosure of the records a summary leaves out; no summary section can take its name", ledger.OmittedTableName)
	}
	section := SummarySection{Table: table, Query: query, Fields: strings.Fields(fields), source: q.Source, sorted: sorted}
	if len(section.Fields) > 0 {
		if counted {
			return SummarySection{}, fmt.Errorf("summary query %q counts; it shows its count fields, so leave fields empty", query)
		}
		known := om.NoteFields
		if q.Source == legacyquery.SourceReflections {
			known = om.ReflectionFields
		}
		for _, f := range section.Fields {
			if q.Source == legacyquery.SourceNotes && ledger.SchemaAddedFieldName(f) {
				// A field a note schema adds; notes without it show it as absent.
				continue
			}
			if !containsString(known, f) {
				return SummarySection{}, fmt.Errorf("summary section %s: %q is not a field of %s (%s)", table, f, q.Source, strings.Join(known, ", "))
			}
		}
		if section.Fields[0] != "id" {
			return SummarySection{}, fmt.Errorf("summary section %s must show id first, so its records can be read back (recall <id>) and the omitted ones disclosed", table)
		}
		if !containsString(section.Fields, ledger.ContentField) {
			return SummarySection{}, fmt.Errorf("summary section %s must show %s: a row without its record's content shows nothing of it, and the kernel discloses that record as omitted", table, ledger.ContentField)
		}
	}
	return section, nil
}
