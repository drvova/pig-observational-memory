package tables

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A table format declared as data (docs/memory-interface.md, section 8, the format kind): the
// model declares a delimiter, quote and escape characters, the escapes its quotes understand,
// the characters that force quoting, and a header form from a closed set, and Declared
// interprets the declaration as a strict codec. A declaration is data, never code: nothing in
// it is run, and the codec it yields is the same generic interpreter for every declaration.

// The header forms a format declaration can name.
const (
	// HeaderCounted is `name[N]{f1<d>f2...}:` followed by exactly N rows, each indented by the
	// declared number of spaces: the rows codec's form with the declared characters.
	HeaderCounted = "counted"
	// HeaderJSONLines is the jsonl codec's form: a JSON header object per table and a JSON
	// array per row. JSON fixes its own delimiter, quote and escape, so a declaration of this
	// form must name them (`,`, `"`, `\`).
	HeaderJSONLines = "jsonl"
)

// HeaderForms lists the header forms.
var HeaderForms = []string{HeaderCounted, HeaderJSONLines}

// FormatTable is the table a format declaration holds, one row of FormatFields.
const FormatTable = "format"

// FormatFields are a format declaration's columns.
var FormatFields = []string{"header", "delimiter", "quote", "escape", "escapes", "special", "indent"}

// MaxIndent bounds a counted declaration's row indentation.
const MaxIndent = 8

// Format is one parsed format declaration.
type Format struct {
	// Name is the codec name the declaration defines (the definition's name).
	Name string
	// Header is one of HeaderForms.
	Header string
	// Delimiter separates values; Quote encloses a quoted value; Escape starts an escape
	// inside quotes.
	Delimiter, Quote, Escape rune
	// Escapes are the letters, among "nrt", the escape character introduces besides the quote
	// and itself: n a line feed, r a carriage return, t a tab. n and r are required, since a
	// value with a line break must survive a line-based format.
	Escapes string
	// Special are further characters that force a value to be quoted.
	Special string
	// Indent is the number of spaces before each row of a counted table.
	Indent int
}

// ParseFormat reads the format declaration text (one FormatTable row in the rows codec) as
// the codec named name, and checks it (Format.Validate).
func ParseFormat(name, text string) (Format, error) {
	decoded, err := Rows.Decode(strings.TrimSpace(text))
	if err != nil {
		return Format{}, fmt.Errorf("a format declaration is one %s row in the rows codec: %w", FormatTable, err)
	}
	if len(decoded) != 1 || decoded[0].Name != FormatTable || len(decoded[0].Rows) != 1 {
		return Format{}, fmt.Errorf("a format declaration is one %s row with the fields %s", FormatTable, headerText(FormatFields))
	}
	t := decoded[0]
	for _, field := range t.Fields {
		if !containsName(FormatFields, field) {
			return Format{}, fmt.Errorf("unknown format field %q; the fields are %s", field, strings.Join(FormatFields, ", "))
		}
	}
	cell := func(field string) string { v, _ := t.Cell(0, field); return v }
	char := func(field string) (rune, error) {
		v := cell(field)
		if utf8.RuneCountInString(v) != 1 {
			return 0, fmt.Errorf("format %s %q must be exactly one character", field, v)
		}
		r, _ := utf8.DecodeRuneInString(v)
		return r, nil
	}
	f := Format{Name: name, Header: cell("header"), Escapes: cell("escapes"), Special: cell("special")}
	if f.Delimiter, err = char("delimiter"); err != nil {
		return Format{}, err
	}
	if f.Quote, err = char("quote"); err != nil {
		return Format{}, err
	}
	if f.Escape, err = char("escape"); err != nil {
		return Format{}, err
	}
	if v := cell("indent"); v != "" {
		if f.Indent, err = strconv.Atoi(v); err != nil {
			return Format{}, fmt.Errorf("format indent %q must be a whole number of spaces", v)
		}
	}
	if err := f.Validate(); err != nil {
		return Format{}, err
	}
	return f, nil
}

// Declaration is f as the declaration text ParseFormat reads.
func (f Format) Declaration() string {
	row := []string{f.Header, string(f.Delimiter), string(f.Quote), string(f.Escape), f.Escapes, f.Special, ""}
	if f.Indent > 0 {
		row[6] = strconv.Itoa(f.Indent)
	}
	absent := make([]bool, len(row))
	for i, v := range row {
		absent[i] = v == ""
	}
	return Rows.Encode(Table{Name: FormatTable, Fields: FormatFields, Rows: [][]string{row}, Absent: [][]bool{absent}})
}

// structural are the characters a counted header is built from; a delimiter, quote or escape
// among them would make the header ambiguous.
const structural = "[]{}:"

// Validate reports why f cannot be interpreted as a strict codec.
func (f Format) Validate() error {
	if !ValidName(f.Name) {
		return fmt.Errorf("format name %q is not a valid codec name", f.Name)
	}
	switch f.Header {
	case HeaderJSONLines:
		if f.Delimiter != ',' || f.Quote != '"' || f.Escape != '\\' {
			return errors.New(`the jsonl header form fixes JSON's delimiter ",", quote "\"" and escape "\\"`)
		}
		return nil
	case HeaderCounted:
	default:
		return fmt.Errorf("format header %q is not a header form; the forms are %s", f.Header, strings.Join(HeaderForms, ", "))
	}
	for _, c := range []struct {
		field string
		r     rune
	}{{"delimiter", f.Delimiter}, {"quote", f.Quote}, {"escape", f.Escape}} {
		if !(unicode.IsPunct(c.r) || unicode.IsSymbol(c.r)) || isNameRune(c.r) || strings.ContainsRune(structural, c.r) {
			return fmt.Errorf("format %s %q must be a punctuation or symbol character other than _ - . and %s", c.field, c.r, structural)
		}
	}
	if f.Delimiter == f.Quote || f.Delimiter == f.Escape || f.Quote == f.Escape {
		return errors.New("format delimiter, quote and escape must be three different characters")
	}
	for _, r := range f.Escapes {
		if !strings.ContainsRune("nrt", r) || strings.Count(f.Escapes, string(r)) > 1 {
			return fmt.Errorf("format escapes %q may hold only n, r and t, each once", f.Escapes)
		}
	}
	if !strings.ContainsRune(f.Escapes, 'n') || !strings.ContainsRune(f.Escapes, 'r') {
		return fmt.Errorf("format escapes %q must include n and r, so a value with a line break survives", f.Escapes)
	}
	for _, r := range f.Special {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsSpace(r) || unicode.IsControl(r) {
			return fmt.Errorf("format special %q may hold only punctuation and symbols", f.Special)
		}
	}
	if f.Indent < 1 || f.Indent > MaxIndent {
		return fmt.Errorf("format indent %d must be 1 to %d spaces", f.Indent, MaxIndent)
	}
	return nil
}

// Declared is the strict codec f declares. A declaration that does not validate yields an
// error; one that does still has to round-trip the kernel's test tables before the layer
// accepts it (kernel.AcceptCodec).
func Declared(f Format) (Codec, error) {
	if err := f.Validate(); err != nil {
		return nil, err
	}
	return declaredCodec{f: f}, nil
}

type declaredCodec struct{ f Format }

func (c declaredCodec) Name() string { return c.f.Name }

// Format is the declaration the codec interprets.
func (c declaredCodec) Format() Format { return c.f }

func (c declaredCodec) Encode(ts ...Table) string {
	if c.f.Header == HeaderJSONLines {
		return JSONLines.Encode(ts...)
	}
	parts := make([]string, 0, len(ts))
	for _, t := range ts {
		parts = append(parts, c.encodeTable(t))
	}
	return strings.Join(parts, "\n")
}

func (c declaredCodec) needsQuote(s string) bool {
	if s == "" {
		return true
	}
	if r, _ := utf8.DecodeRuneInString(s); unicode.IsSpace(r) {
		return true
	}
	if r, _ := utf8.DecodeLastRuneInString(s); unicode.IsSpace(r) {
		return true
	}
	return strings.ContainsRune(s, c.f.Delimiter) || strings.ContainsRune(s, c.f.Quote) || strings.ContainsRune(s, c.f.Escape) ||
		strings.ContainsAny(s, "\n\r") || (c.f.Special != "" && strings.ContainsAny(s, c.f.Special))
}

func (c declaredCodec) quoteValue(s string) string {
	if !c.needsQuote(s) {
		return s
	}
	var b strings.Builder
	b.WriteRune(c.f.Quote)
	for _, r := range s {
		switch {
		case r == c.f.Quote || r == c.f.Escape:
			b.WriteRune(c.f.Escape)
			b.WriteRune(r)
		case r == '\n':
			b.WriteRune(c.f.Escape)
			b.WriteByte('n')
		case r == '\r':
			b.WriteRune(c.f.Escape)
			b.WriteByte('r')
		case r == '\t' && strings.ContainsRune(c.f.Escapes, 't'):
			b.WriteRune(c.f.Escape)
			b.WriteByte('t')
		default:
			b.WriteRune(r)
		}
	}
	b.WriteRune(c.f.Quote)
	return b.String()
}

func (c declaredCodec) encodeTable(t Table) string {
	var b strings.Builder
	b.WriteString(t.Name)
	b.WriteString("[" + strconv.Itoa(len(t.Rows)) + "]{")
	for i, f := range t.Fields {
		if i > 0 {
			b.WriteRune(c.f.Delimiter)
		}
		b.WriteString(f)
	}
	b.WriteString("}:")
	indent := strings.Repeat(" ", c.f.Indent)
	for r, row := range t.Rows {
		b.WriteString("\n" + indent)
		for i := range t.Fields {
			if i > 0 {
				b.WriteRune(c.f.Delimiter)
			}
			switch {
			case i < len(row) && !t.IsAbsent(r, i):
				b.WriteString(c.quoteValue(row[i]))
			case len(t.Fields) == 1:
				b.WriteRune(c.f.Quote)
				b.WriteRune(c.f.Quote)
			}
		}
	}
	return b.String()
}

func (c declaredCodec) Decode(text string) ([]Table, error) {
	if c.f.Header == HeaderJSONLines {
		return JSONLines.Decode(text)
	}
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	indent := strings.Repeat(" ", c.f.Indent)
	isRow := func(line string) bool { return line != "" && (line[0] == ' ' || line[0] == '\t') }
	out := []Table{}
	for i := 0; i < len(lines); {
		if lines[i] == "" {
			i++
			continue
		}
		if isRow(lines[i]) {
			return nil, errAt(lines, i, 0, "row outside a table", "put a header name[N]"+c.fieldsHint()+": above it, or remove the indentation")
		}
		t, n, open, err := c.decodeHeader(lines, i)
		if err != nil {
			return nil, err
		}
		header := i
		i++
		for ; i < len(lines) && isRow(lines[i]); i++ {
			line := lines[i]
			if len(t.Rows) == n {
				return nil, errAt(lines, i, 0, fmt.Sprintf("table %s has more rows than its header says [%d]", t.Name, n), "set the count to the number of rows or remove the extra rows")
			}
			if !strings.HasPrefix(line, indent) || (len(line) > len(indent) && (line[len(indent)] == ' ' || line[len(indent)] == '\t')) {
				return nil, errAt(lines, i, 0, "bad indentation", fmt.Sprintf("indent rows exactly %d spaces and start every other line at column 1", c.f.Indent))
			}
			values, absent, err := c.decodeRow(lines, i, len(indent))
			if err != nil {
				return nil, err
			}
			if len(values) != len(t.Fields) {
				return nil, errAt(lines, i, len(line), fmt.Sprintf("row %d has %d fields, header %s has %d", len(t.Rows)+1, len(values), c.headerFields(t.Fields), len(t.Fields)),
					fmt.Sprintf("quote values that contain %q, and give every field a value or an empty slot", c.f.Delimiter))
			}
			t.Rows = append(t.Rows, values)
			t.Absent = append(t.Absent, absent)
		}
		if len(t.Rows) < n {
			return nil, errAt(lines, header, open+1, fmt.Sprintf("table %s has %d rows, header says [%d]", t.Name, len(t.Rows), n),
				fmt.Sprintf("set the count to [%d] or add the missing rows, indented %d spaces", len(t.Rows), c.f.Indent))
		}
		out = append(out, t)
	}
	return out, nil
}

func (c declaredCodec) fieldsHint() string {
	return "{field1" + string(c.f.Delimiter) + "field2}"
}

func (c declaredCodec) headerFields(fields []string) string {
	return "{" + strings.Join(fields, string(c.f.Delimiter)) + "}"
}

// decodeHeader parses the counted header at lines[i]: the table, its count, and the byte
// offset of its '['.
func (c declaredCodec) decodeHeader(lines []string, i int) (Table, int, int, error) {
	line := lines[i]
	hint := "write a table as name[N]" + c.fieldsHint() + ": followed by its rows"
	p := scanName(line, 0)
	if p == 0 {
		return Table{}, 0, 0, errAt(lines, i, 0, "expected a table name", hint)
	}
	name := line[:p]
	if p == len(line) || line[p] != '[' {
		return Table{}, 0, 0, errAt(lines, i, p, fmt.Sprintf("expected '[' after the table name %q", name), hint)
	}
	open := p
	p++
	q := p
	for q < len(line) && line[q] >= '0' && line[q] <= '9' {
		q++
	}
	if q == p || q == len(line) || line[q] != ']' {
		return Table{}, 0, 0, errAt(lines, i, p, "expected a count in [N]", "write the number of rows with digits only, e.g. "+name+"[3]")
	}
	n, err := strconv.Atoi(line[p:q])
	if err != nil {
		return Table{}, 0, 0, errAt(lines, i, p, "count too large", "write the actual number of rows")
	}
	p = q + 1
	if p == len(line) || line[p] != '{' {
		return Table{}, 0, 0, errAt(lines, i, p, "expected '{' after the count", hint)
	}
	end := strings.IndexByte(line[p:], '}')
	if end < 0 {
		return Table{}, 0, 0, errAt(lines, i, len(line), "unterminated field list", "close the field list with }:")
	}
	end += p
	t := Table{Name: name, Rows: [][]string{}}
	seen := map[string]bool{}
	start := p + 1
	for _, f := range strings.Split(line[p+1:end], string(c.f.Delimiter)) {
		if !ValidName(f) {
			return Table{}, 0, 0, errAt(lines, i, start, fmt.Sprintf("bad field name %q", f), hintNames+fmt.Sprintf(", separated by %q", c.f.Delimiter))
		}
		if seen[f] {
			return Table{}, 0, 0, errAt(lines, i, start, fmt.Sprintf("duplicate field %q", f), "give every field a distinct name")
		}
		seen[f] = true
		t.Fields = append(t.Fields, f)
		start += len(f) + utf8.RuneLen(c.f.Delimiter)
	}
	if end+1 != len(line)-1 || line[end+1] != ':' {
		return Table{}, 0, 0, errAt(lines, i, end+1, "expected ':' to end the header", "end the header with }: and put rows on the following lines")
	}
	return t, n, open, nil
}

// decodeRow parses the values of the row at lines[i], starting at byte offset from.
func (c declaredCodec) decodeRow(lines []string, i, from int) ([]string, []bool, error) {
	line := lines[i]
	var values []string
	var absent []bool
	p := from
	for {
		if r, n := utf8.DecodeRuneInString(line[p:]); p < len(line) && r == c.f.Quote {
			value, next, err := c.decodeQuoted(lines, i, p+n)
			if err != nil {
				return nil, nil, err
			}
			values, absent = append(values, value), append(absent, false)
			p = next
		} else {
			end := len(line)
			if k := strings.IndexRune(line[p:], c.f.Delimiter); k >= 0 {
				end = p + k
			}
			value := line[p:end]
			if k := strings.IndexRune(value, c.f.Quote); k >= 0 {
				return nil, nil, errAt(lines, i, p+k, "quote inside an unquoted value", fmt.Sprintf("quote the whole value and escape inner quotes as %c%c", c.f.Escape, c.f.Quote))
			}
			if value != strings.TrimSpace(value) {
				return nil, nil, errAt(lines, i, p, "space around an unquoted value", fmt.Sprintf("remove spaces after %q, or quote values that start or end with a space", c.f.Delimiter))
			}
			values, absent = append(values, value), append(absent, value == "")
			p = end
		}
		if p == len(line) {
			return values, absent, nil
		}
		r, n := utf8.DecodeRuneInString(line[p:])
		if r != c.f.Delimiter {
			return nil, nil, errAt(lines, i, p, "text after a closing quote", fmt.Sprintf("put %q right after the closing quote", c.f.Delimiter))
		}
		p += n
	}
}

// decodeQuoted parses a quoted value whose text starts at byte offset p (just after the
// opening quote) and returns it with the offset just past its closing quote.
func (c declaredCodec) decodeQuoted(lines []string, i, p int) (string, int, error) {
	line := lines[i]
	var b strings.Builder
	for p < len(line) {
		r, n := utf8.DecodeRuneInString(line[p:])
		switch r {
		case c.f.Quote:
			return b.String(), p + n, nil
		case c.f.Escape:
			next, m := utf8.DecodeRuneInString(line[p+n:])
			switch {
			case p+n >= len(line):
				return "", 0, errAt(lines, i, p, "escape at the end of the line", c.escapeHint())
			case next == c.f.Quote || next == c.f.Escape:
				b.WriteRune(next)
			case next == 'n' && strings.ContainsRune(c.f.Escapes, 'n'):
				b.WriteByte('\n')
			case next == 'r' && strings.ContainsRune(c.f.Escapes, 'r'):
				b.WriteByte('\r')
			case next == 't' && strings.ContainsRune(c.f.Escapes, 't'):
				b.WriteByte('\t')
			default:
				return "", 0, errAt(lines, i, p, fmt.Sprintf("unknown escape %c%c", c.f.Escape, next), c.escapeHint())
			}
			p += n + m
		default:
			b.WriteRune(r)
			p += n
		}
	}
	return "", 0, errAt(lines, i, len(line), "unterminated quote", fmt.Sprintf("close the value with %c, and write a line break inside it as %cn", c.f.Quote, c.f.Escape))
}

// escapeList is the escapes the codec's quotes understand, as a reader writes them.
func (c declaredCodec) escapeList() string {
	e := string(c.f.Escape)
	out := []string{e + string(c.f.Quote), e + e}
	for _, r := range c.f.Escapes {
		out = append(out, e+string(r))
	}
	return strings.Join(out, " ")
}

func (c declaredCodec) escapeHint() string { return "inside quotes use " + c.escapeList() }

func (c declaredCodec) Spec() string {
	if c.f.Header == HeaderJSONLines {
		return JSONLines.Spec()
	}
	d := string(c.f.Delimiter)
	forcing := []string{d, string(c.f.Quote), string(c.f.Escape)}
	for _, r := range c.f.Special {
		forcing = append(forcing, string(r))
	}
	return fmt.Sprintf("A table is name[N]{field1%sfield2%s...}: followed by exactly N rows, each on its own line indented %d spaces, values separated by %s in header order with no space after it.\n", d, d, c.f.Indent, d) +
		fmt.Sprintf("Leave a value empty for none. Quote a value with %c when it is empty, starts or ends with a space, or has %s or a line break; inside quotes write %s.", c.f.Quote, strings.Join(forcing, " "), c.escapeList())
}

// LongSpec is Spec with an example written in the declared format itself.
func (c declaredCodec) LongSpec() string {
	if c.f.Header == HeaderJSONLines {
		return LongSpec(JSONLines)
	}
	example := c.Encode(Table{Name: "notes", Fields: []string{"id", "scope", "content"}, Rows: [][]string{
		{"a1b2c3d4e5f6", "on edX", "User finished three courses."},
		{"b2c3d4e5f6a1", "", "User bakes" + string(c.f.Delimiter) + " sourdough and rye."},
	}, Absent: [][]bool{nil, {false, true, false}}})
	return c.Spec() + "\nExample with two rows, the second without a scope and with a quoted value:\n" + example +
		fmt.Sprintf("\nCommon mistakes: N not equal to the number of rows; a row with more or fewer values than fields (quote values that contain %q); a space after a delimiter; rows not indented exactly %d spaces.", c.f.Delimiter, c.f.Indent)
}

// DeclaredFormat is the declaration a codec Declared returned interprets; false for any other
// codec.
func DeclaredFormat(c Codec) (Format, bool) {
	d, ok := c.(declaredCodec)
	return d.f, ok
}

func containsName(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
