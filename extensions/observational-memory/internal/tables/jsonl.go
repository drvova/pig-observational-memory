package tables

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

// JSONLines is the second codec: JSON lines, one header object per table followed by one JSON
// array per row (docs/memory-interface.md, sections 1 and 6). A model that keeps failing to
// write rows is switched to it by the per-model harness:
//
//	{"table":"reflections","fields":["id","dates","content"]}
//	["a1b2c3d4e5f6","2023-05-20..2023-05-24","User bakes at home with a convection oven."]
//
// A row holds one string or null (absent) per field. Decoding is as strict as the rows codec's:
// every error names the line and the fix.
var JSONLines Codec = jsonLinesCodec{}

type jsonLinesCodec struct{}

func (jsonLinesCodec) Name() string { return "jsonl" }

// jsonlHeader is a table's header line.
type jsonlHeader struct {
	Table  string   `json:"table"`
	Fields []string `json:"fields"`
}

func marshalJSON(v any) string {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return strings.TrimSuffix(b.String(), "\n")
}

func (jsonLinesCodec) Encode(tables ...Table) string {
	lines := []string{}
	for _, t := range tables {
		fields := t.Fields
		if fields == nil {
			fields = []string{}
		}
		lines = append(lines, marshalJSON(jsonlHeader{Table: t.Name, Fields: fields}))
		for r, row := range t.Rows {
			cells := make([]any, len(t.Fields))
			for c := range t.Fields {
				if c < len(row) && !t.IsAbsent(r, c) {
					cells[c] = row[c]
				}
			}
			lines = append(lines, marshalJSON(cells))
		}
	}
	return strings.Join(lines, "\n")
}

const (
	jsonlHintHeader = `start each table with {"table":"name","fields":["field1","field2"]}`
	jsonlHintRow    = `write each row as a JSON array of strings or null, one per field, e.g. ["a","b"]`
)

func (jsonLinesCodec) Decode(text string) ([]Table, error) {
	lines := strings.Split(text, "\n")
	out := []Table{}
	var current *Table
	for i, raw := range lines {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" {
			continue
		}
		at := func(msg, hint string) error { return &Error{Line: i + 1, Column: 1, Msg: msg, Hint: hint} }
		switch line[0] {
		case '{':
			var header jsonlHeader
			dec := json.NewDecoder(strings.NewReader(line))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&header); err != nil || dec.More() {
				return nil, at("invalid table header", jsonlHintHeader)
			}
			if !ValidName(header.Table) {
				return nil, at("invalid table name "+quoteForMessage(header.Table), hintNames)
			}
			if len(header.Fields) == 0 {
				return nil, at("table "+header.Table+" has no fields", jsonlHintHeader)
			}
			seen := map[string]bool{}
			for _, f := range header.Fields {
				if !ValidName(f) {
					return nil, at("invalid field name "+quoteForMessage(f), hintNames)
				}
				if seen[f] {
					return nil, at("duplicate field "+quoteForMessage(f), "name each field once")
				}
				seen[f] = true
			}
			out = append(out, Table{Name: header.Table, Fields: header.Fields, Rows: [][]string{}})
			current = &out[len(out)-1]
		case '[':
			if current == nil {
				return nil, at("row outside a table", jsonlHintHeader+" above it")
			}
			var cells []*string
			dec := json.NewDecoder(strings.NewReader(line))
			if err := dec.Decode(&cells); err != nil || dec.More() {
				return nil, at("invalid row", jsonlHintRow)
			}
			if len(cells) != len(current.Fields) {
				return nil, at("row has "+strconv.Itoa(len(cells))+" values, header has "+strconv.Itoa(len(current.Fields))+" fields", "give one value per field of "+headerText(current.Fields)+", null for none")
			}
			row := make([]string, len(cells))
			absent := make([]bool, len(cells))
			anyAbsent := false
			for c, cell := range cells {
				if cell == nil {
					absent[c], anyAbsent = true, true
					continue
				}
				row[c] = *cell
			}
			current.Rows = append(current.Rows, row)
			if anyAbsent || current.Absent != nil {
				for len(current.Absent) < len(current.Rows)-1 {
					current.Absent = append(current.Absent, nil)
				}
				current.Absent = append(current.Absent, absent)
			}
		default:
			return nil, at("expected a table header or a row", jsonlHintHeader+"; "+jsonlHintRow)
		}
	}
	return out, nil
}

func (jsonLinesCodec) Spec() string {
	return `A table is a header line {"table":"name","fields":["field1","field2"]} followed by one line per row: a JSON array with one string per field, in header order, null for none.` + "\n" +
		`Every line is one complete JSON value; escape quotes and line breaks inside strings as JSON does (\" and \n).`
}

// LongSpec is Spec with examples, for a model that keeps making syntax errors.
func (c jsonLinesCodec) LongSpec() string {
	return c.Spec() + "\n" + `Example with two rows, the second without a scope:
{"table":"notes","fields":["id","scope","content"]}
["a1b2c3d4e5f6","on edX","User finished 3 courses: \"Python\", \"SQL\" and \"Stats\"."]
["b2c3d4e5f6a1",null,"User plans a trip to Lisbon."]
Common mistakes: a row with more or fewer values than fields; single quotes instead of double quotes; a value split across lines (write \n instead); a trailing comma.`
}

// LongSpec is the codec's longer spec with examples when it has one (a LongSpec method),
// else its Spec. The per-model harness carries it for a model that keeps making syntax
// errors (docs/memory-interface.md, section 6).
func LongSpec(c Codec) string {
	if long, ok := c.(interface{ LongSpec() string }); ok {
		return long.LongSpec()
	}
	return c.Spec()
}

func quoteForMessage(s string) string { return marshalJSON(s) }
