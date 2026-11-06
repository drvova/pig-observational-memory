package secrets

import (
	"encoding/json"
	"strings"
)

// RedactJSON redacts every string value of a JSON text (object member names are kept), leaving
// every other byte as it is: a value that changes is written again as encoding/json writes a
// string. A text no rule's keyword occurs in is returned unchanged without being read.
func RedactJSON(text string) string {
	lower := asciiLower(text)
	hit := false
	for i := range Rules {
		if Rules[i].prefilter(lower) {
			hit = true
			break
		}
	}
	if !hit {
		return text
	}
	var b strings.Builder
	last := 0
	for i := 0; i < len(text); i++ {
		if text[i] != '"' {
			continue
		}
		end := stringEnd(text, i)
		if end < 0 {
			break
		}
		literal := text[i:end]
		if !isMemberName(text, end) {
			var value string
			if err := json.Unmarshal([]byte(literal), &value); err == nil {
				if redacted := Redact(value); redacted != value {
					if encoded, err := json.Marshal(redacted); err == nil {
						b.WriteString(text[last:i])
						b.Write(encoded)
						last = end
					}
				}
			}
		}
		i = end - 1
	}
	if last == 0 {
		return text
	}
	b.WriteString(text[last:])
	return b.String()
}

// stringEnd is the offset after the string literal opening at start, -1 when it is not closed.
func stringEnd(text string, start int) int {
	for i := start + 1; i < len(text); i++ {
		switch text[i] {
		case '\\':
			i++
		case '"':
			return i + 1
		}
	}
	return -1
}

// isMemberName reports a string literal ending at end that a colon follows: an object's member
// name.
func isMemberName(text string, end int) bool {
	for i := end; i < len(text); i++ {
		switch text[i] {
		case ' ', '\t', '\n', '\r':
			continue
		case ':':
			return true
		}
		return false
	}
	return false
}

// RedactData redacts the string values of data as JSON encodes it, for an entry about to be
// appended: data itself when nothing changes (so the host encodes it as it always would), else
// the redacted encoding as a json.RawMessage.
func RedactData(data any) any {
	encoded, err := json.Marshal(data)
	if err != nil {
		return data
	}
	redacted := RedactJSON(string(encoded))
	if redacted == string(encoded) {
		return data
	}
	return json.RawMessage(redacted)
}
