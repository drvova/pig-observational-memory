package tables

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	hintLineStart = "start a line with name: value, name[N]: values or name[N]{fields}:"
	hintNames     = "names use letters, digits, _, - and ., without spaces"
	hintIndent    = "indent rows exactly two spaces and start every other line at column 1"
	hintQuote     = `quote the whole value and escape inner quotes as \"`
	hintEscape    = `inside quotes use \" \\ \n \r or \t`
	hintSpace     = "remove spaces after commas, or quote values that start with a space"
)

// decode parses a rows text into its blocks and each block's 1-based starting line. Lines
// are separated by "\n" (a trailing "\r" is dropped); empty lines between blocks are ignored.
// Any deviation from the format fails with an *Error.
func decode(text string) ([]block, []int, error) {
	lines := strings.Split(text, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimSuffix(l, "\r")
	}
	var blocks []block
	var starts []int
	for i := 0; i < len(lines); {
		line := lines[i]
		if line == "" {
			i++
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if isRowLine(line) {
				return nil, nil, errAt(lines, i, 0, "row outside a table", "put a header name[N]{fields}: above it, or remove the indentation")
			}
			return nil, nil, errAt(lines, i, indentEnd(line), "bad indentation", hintIndent)
		}
		blk, next, err := decodeBlock(lines, i)
		if err != nil {
			return nil, nil, err
		}
		blocks = append(blocks, blk)
		starts = append(starts, i+1)
		i = next
	}
	return blocks, starts, nil
}

// decodeTables parses a rows text that holds only tables and returns each table's starting
// line.
func decodeTables(text string) ([]Table, []int, error) {
	blocks, starts, err := decode(text)
	if err != nil {
		return nil, nil, err
	}
	tables := make([]Table, 0, len(blocks))
	for k, blk := range blocks {
		t, ok := blk.(Table)
		if !ok {
			found := "a scalar"
			if _, isList := blk.(list); isList {
				found = "a list"
			}
			return nil, nil, &Error{Line: starts[k], Column: 1, Msg: "expected a table, found " + found, Hint: "write a table as name[N]{fields}: followed by its rows"}
		}
		tables = append(tables, t)
	}
	return tables, starts, nil
}

// decodeBlock parses the block starting at lines[i] and returns the index of the next line.
func decodeBlock(lines []string, i int) (block, int, error) {
	line := lines[i]
	p := scanName(line, 0)
	if p == 0 {
		return nil, 0, errAt(lines, i, 0, "expected a name", hintLineStart)
	}
	name := line[:p]
	if p == len(line) {
		return nil, 0, errAt(lines, i, p, fmt.Sprintf("expected ':' or '[' after %q", name), hintLineStart)
	}
	switch line[p] {
	case ':':
		s, err := decodeScalar(lines, i, name, p+1)
		return s, i + 1, err
	case '[':
	default:
		r, _ := utf8.DecodeRuneInString(line[p:])
		return nil, 0, errAt(lines, i, p, fmt.Sprintf("unexpected %q after name %q", r, name), hintNames)
	}

	// Count: [N].
	open := p
	p++
	q := p
	for q < len(line) && line[q] >= '0' && line[q] <= '9' {
		q++
	}
	if q == p {
		return nil, 0, errAt(lines, i, p, "expected a count in [N]", "write the number of rows or items, e.g. "+name+"[3]")
	}
	if q == len(line) || line[q] != ']' {
		return nil, 0, errAt(lines, i, q, "expected ']' after the count", "write the count as [N] with digits only")
	}
	n, err := strconv.Atoi(line[p:q])
	if err != nil {
		return nil, 0, errAt(lines, i, p, "count too large", "write the actual number of rows or items")
	}
	p = q + 1
	if p == len(line) {
		return nil, 0, errAt(lines, i, p, "expected ':' or '{' after the count", "write name[N]: values or name[N]{fields}:")
	}
	switch line[p] {
	case ':':
		l, err := decodeList(lines, i, name, n, open, p+1)
		return l, i + 1, err
	case '{':
	default:
		return nil, 0, errAt(lines, i, p, "expected ':' or '{' after the count", "write name[N]: values or name[N]{fields}:")
	}

	// Fields: {f1,f2,...}.
	end := strings.IndexByte(line[p:], '}')
	if end < 0 {
		return nil, 0, errAt(lines, i, len(line), "unterminated field list", "close the field list with }:")
	}
	end += p
	var fields []string
	seen := map[string]bool{}
	start := p + 1
	for _, f := range strings.Split(line[p+1:end], ",") {
		if !ValidName(f) {
			msg := fmt.Sprintf("bad field name %q", f)
			if f == "" {
				msg = "empty field name"
			}
			return nil, 0, errAt(lines, i, start, msg, hintNames)
		}
		if seen[f] {
			return nil, 0, errAt(lines, i, start, fmt.Sprintf("duplicate field %q", f), "give every field a distinct name")
		}
		seen[f] = true
		fields = append(fields, f)
		start += len(f) + 1
	}
	if end+1 == len(line) || line[end+1] != ':' {
		return nil, 0, errAt(lines, i, end+1, "expected ':' after the field list", "end the header with }:")
	}
	if end+2 != len(line) {
		return nil, 0, errAt(lines, i, end+2, "unexpected text after the table header", "put rows on the following lines, indented two spaces")
	}

	t := Table{Name: name, Fields: fields}
	header := headerText(fields)
	anyAbsent := false
	var absent [][]bool
	j := i + 1
	for ; j < len(lines) && lines[j] != "" && (lines[j][0] == ' ' || lines[j][0] == '\t'); j++ {
		row := lines[j]
		if !isRowLine(row) {
			return nil, 0, errAt(lines, j, indentEnd(row), "bad indentation", hintIndent)
		}
		if len(t.Rows) == n {
			total := n
			for k := j; k < len(lines) && isRowLine(lines[k]); k++ {
				total++
			}
			return nil, 0, errAt(lines, j, 0, fmt.Sprintf("table %s has %d rows, header says [%d]", name, total, n), fmt.Sprintf("set the count to [%d] or remove the extra rows", total))
		}
		vals, abs, starts, spaceErr, err := decodeFields(lines, j, 2)
		if err != nil {
			return nil, 0, err
		}
		if len(vals) != len(fields) {
			col := len(row)
			if len(vals) > len(fields) {
				col = starts[len(fields)]
			}
			return nil, 0, errAt(lines, j, col,
				fmt.Sprintf("row %d has %d fields, header %s has %d", len(t.Rows)+1, len(vals), header, len(fields)),
				"quote values that contain commas, and give every field a value or an empty slot")
		}
		if spaceErr != nil {
			return nil, 0, spaceErr
		}
		for _, a := range abs {
			anyAbsent = anyAbsent || a
		}
		t.Rows = append(t.Rows, vals)
		absent = append(absent, abs)
	}
	if len(t.Rows) < n {
		return nil, 0, errAt(lines, i, open+1, fmt.Sprintf("table %s has %d rows, header says [%d]", name, len(t.Rows), n),
			fmt.Sprintf("set the count to [%d] or add the missing rows, indented two spaces", len(t.Rows)))
	}
	if anyAbsent {
		t.Absent = absent
	}
	return t, j, nil
}

func decodeScalar(lines []string, i int, key string, p int) (scalar, error) {
	line := lines[i]
	if p == len(line) {
		return scalar{Key: key, Absent: true}, nil
	}
	if line[p] != ' ' {
		return scalar{}, errAt(lines, i, p, "expected a space after ':'", "write key: value")
	}
	p++
	if p == len(line) {
		return scalar{Key: key, Absent: true}, nil
	}
	if line[p] == '"' {
		v, end, err := decodeQuoted(lines, i, p)
		if err != nil {
			return scalar{}, err
		}
		if end != len(line) {
			return scalar{}, errAt(lines, i, end, "text after the closing quote", hintQuote)
		}
		return scalar{Key: key, Value: v}, nil
	}
	if startsWithSpace(line[p:]) {
		return scalar{}, errAt(lines, i, p, "value starts with a space", "write key: value with one space, or quote a value that starts with a space")
	}
	if k := strings.IndexByte(line[p:], '"'); k >= 0 {
		return scalar{}, errAt(lines, i, p+k, "quote inside an unquoted value", hintQuote)
	}
	return scalar{Key: key, Value: line[p:]}, nil
}

func decodeList(lines []string, i int, key string, n, open, p int) (list, error) {
	line := lines[i]
	l := list{Key: key}
	if p < len(line) {
		if line[p] != ' ' {
			return list{}, errAt(lines, i, p, "expected a space after ':'", "write key[N]: a,b,c")
		}
		p++
	}
	if p == len(line) && n == 0 {
		return l, nil
	}
	vals, abs, starts, spaceErr, err := decodeFields(lines, i, p)
	if err != nil {
		return list{}, err
	}
	if len(vals) != n {
		col := open + 1
		if len(vals) > n && n > 0 {
			col = starts[n]
		}
		return list{}, errAt(lines, i, col, fmt.Sprintf("list %s has %d items, header says [%d]", key, len(vals), n),
			fmt.Sprintf("set the count to [%d], and quote items that contain commas", len(vals)))
	}
	if spaceErr != nil {
		return list{}, spaceErr
	}
	l.Items = vals
	for _, a := range abs {
		if a {
			l.Absent = abs
			break
		}
	}
	return l, nil
}

// decodeFields splits lines[i] from byte p into comma-separated values. It returns the values,
// which of them are absent, and each value's starting byte offset. An unquoted value may not
// start with white space: the encoder quotes such values, so in hand-written text the space
// is a typo (`a, b`), not part of the value. The first such value is returned as spaceErr
// rather than err, so the caller can report a wrong value count first: `x,tikka, naan` is
// missing quotes, not just a space.
func decodeFields(lines []string, i, p int) (vals []string, absent []bool, starts []int, spaceErr, err error) {
	line := lines[i]
	first := p
	for {
		starts = append(starts, p)
		if p < len(line) && line[p] == '"' {
			v, end, err := decodeQuoted(lines, i, p)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			vals = append(vals, v)
			absent = append(absent, false)
			if end == len(line) {
				return vals, absent, starts, spaceErr, nil
			}
			if line[end] != ',' {
				return nil, nil, nil, nil, errAt(lines, i, end, "text after the closing quote", hintQuote)
			}
			p = end + 1
			continue
		}
		k := strings.IndexByte(line[p:], ',')
		f := line[p:]
		if k >= 0 {
			f = line[p : p+k]
		}
		if startsWithSpace(f) {
			if spaceErr == nil {
				msg := "space after comma"
				if p == first {
					msg = "value starts with a space"
				}
				spaceErr = errAt(lines, i, p, msg, hintSpace)
			}
		} else if q := strings.IndexByte(f, '"'); q >= 0 {
			return nil, nil, nil, nil, errAt(lines, i, p+q, "quote inside an unquoted value", hintQuote)
		}
		vals = append(vals, f)
		absent = append(absent, f == "")
		if k < 0 {
			return vals, absent, starts, spaceErr, nil
		}
		p += k + 1
	}
}

// decodeQuoted parses the quoted value whose opening quote is at lines[i][p] and returns it
// with the byte offset just past the closing quote.
func decodeQuoted(lines []string, i, p int) (string, int, error) {
	line := lines[i]
	var b strings.Builder
	for k := p + 1; k < len(line); k++ {
		c := line[k]
		if c == '"' {
			return b.String(), k + 1, nil
		}
		if c != '\\' {
			b.WriteByte(c)
			continue
		}
		if k+1 == len(line) {
			break
		}
		k++
		switch line[k] {
		case '"':
			b.WriteByte('"')
		case '\\':
			b.WriteByte('\\')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 't':
			b.WriteByte('\t')
		default:
			r, _ := utf8.DecodeRuneInString(line[k:])
			return "", 0, errAt(lines, i, k-1, fmt.Sprintf("unknown escape \\%c", r), hintEscape)
		}
	}
	return "", 0, errAt(lines, i, p, "unterminated quote", `close the value with " on the same line; write line breaks as \n`)
}

// startsWithSpace reports whether s begins with Unicode white space.
func startsWithSpace(s string) bool {
	r, _ := utf8.DecodeRuneInString(s)
	return s != "" && unicode.IsSpace(r)
}

// isRowLine reports whether line is indented exactly two spaces.
func isRowLine(line string) bool {
	return strings.HasPrefix(line, "  ") && (len(line) == 2 || (line[2] != ' ' && line[2] != '\t'))
}

// indentEnd returns the byte offset of the first character after line's leading spaces and
// tabs, or 0 when the line is blank.
func indentEnd(line string) int {
	k := len(line) - len(strings.TrimLeft(line, " \t"))
	if k == len(line) {
		return 0
	}
	return k
}

// errAt builds an *Error at byte offset p of lines[i].
func errAt(lines []string, i, p int, msg, hint string) *Error {
	line := lines[i]
	if p > len(line) {
		p = len(line)
	}
	return &Error{Line: i + 1, Column: utf8.RuneCountInString(line[:p]) + 1, Msg: msg, Hint: hint}
}
