package tables

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// needsQuote reports whether s must be quoted to survive decoding: it is empty, has leading
// or trailing white space, or contains `,` `:` `"` `\` `|` or a line break.
func needsQuote(s string) bool {
	if s == "" {
		return true
	}
	if r, _ := utf8.DecodeRuneInString(s); unicode.IsSpace(r) {
		return true
	}
	if r, _ := utf8.DecodeLastRuneInString(s); unicode.IsSpace(r) {
		return true
	}
	return strings.ContainsAny(s, ",:\"\\|\n\r")
}

// quoteValue returns s as a present value: verbatim when needsQuote is false, otherwise in
// double quotes with `"`, `\`, newline, carriage return and tab escaped.
func quoteValue(s string) string {
	if !needsQuote(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for i := 0; i < len(s); i++ {
		switch c := s[i]; c {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			b.WriteByte(c)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// encodeTable renders t without a trailing newline. The count is len(t.Rows); a row shorter
// than the header is padded with absent values and a longer one is truncated. In a
// single-field table an absent cell is written as "" so no row is blank and the text survives
// trimming. The result decodes back to t when t.Validate returns nil.
func encodeTable(t Table) string {
	var b strings.Builder
	b.WriteString(t.Name)
	b.WriteByte('[')
	b.WriteString(strconv.Itoa(len(t.Rows)))
	b.WriteString("]")
	b.WriteString(headerText(t.Fields))
	b.WriteByte(':')
	for r, row := range t.Rows {
		b.WriteString("\n  ")
		for c := range t.Fields {
			if c > 0 {
				b.WriteByte(',')
			}
			switch {
			case c < len(row) && !t.IsAbsent(r, c):
				b.WriteString(quoteValue(row[c]))
			case len(t.Fields) == 1:
				b.WriteString(`""`)
			}
		}
	}
	return b.String()
}

// encodeScalar renders s as `key: value`, or `key:` when the value is absent.
func encodeScalar(s scalar) string {
	if s.Absent {
		return s.Key + ":"
	}
	return s.Key + ": " + quoteValue(s.Value)
}

// encodeList renders l as `key[N]: a,b,c`, or `key[0]:` when empty.
func encodeList(l list) string {
	var b strings.Builder
	b.WriteString(l.Key)
	b.WriteByte('[')
	b.WriteString(strconv.Itoa(len(l.Items)))
	b.WriteString("]:")
	if len(l.Items) > 0 {
		b.WriteByte(' ')
	}
	for i, v := range l.Items {
		if i > 0 {
			b.WriteByte(',')
		}
		if !l.IsAbsent(i) {
			b.WriteString(quoteValue(v))
		}
	}
	return b.String()
}

// encodeBlocks renders blocks one after another, separated by newlines, without a trailing
// newline. A nil block is skipped.
func encodeBlocks(blocks ...block) string {
	parts := make([]string, 0, len(blocks))
	for _, blk := range blocks {
		switch v := blk.(type) {
		case Table:
			parts = append(parts, encodeTable(v))
		case scalar:
			parts = append(parts, encodeScalar(v))
		case list:
			parts = append(parts, encodeList(v))
		}
	}
	return strings.Join(parts, "\n")
}
