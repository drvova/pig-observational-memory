package legacyquery

import (
	"fmt"
	"unicode/utf8"
)

// MaxDefinitionQueryChars and MaxDefinitionLiteralChars bound a defined query, in characters,
// and each quoted text in it. The query was shown in a tool's description in every later
// session, so its text was bounded like the description's: a whole query of one short line,
// whose quoted texts are a few search words or a field value, not sentences. The code tool
// removes the bounds from what the agent writes (docs/code-tool.md, "Limits"); they stay as the
// load checks of stored defined queries (docs/code-tool.md, "Migration").
const (
	MaxDefinitionQueryChars   = 200
	MaxDefinitionLiteralChars = 40
)

// CheckDefinitionBounds refuses a query over MaxDefinitionQueryChars characters or holding a
// quoted text over MaxDefinitionLiteralChars. It is checked when a definition is stored and again
// when one is read, so a file written by hand or by an earlier version never reaches a tool
// description unbounded.
func CheckDefinitionBounds(query string) error {
	if n := utf8.RuneCountInString(query); n > MaxDefinitionQueryChars {
		return fmt.Errorf("a defined query is at most %d characters (this one has %d): keep it to a source and a few stages", MaxDefinitionQueryChars, n)
	}
	for _, literal := range QuotedTexts(query) {
		if n := utf8.RuneCountInString(literal); n > MaxDefinitionLiteralChars {
			return fmt.Errorf("a quoted text in a defined query is at most %d characters (%q has %d): search for a few words, or use $input for the rest", MaxDefinitionLiteralChars, literal, n)
		}
	}
	return nil
}

// QuotedTexts are the "..." texts of a query, escapes kept as written; an unterminated one runs
// to the end. Unlike the lexer it never fails, so the bound is checked on any stored text.
func QuotedTexts(query string) []string {
	out := []string{}
	start := -1
	for i := 0; i < len(query); i++ {
		switch {
		case start >= 0 && query[i] == '\\':
			i++
		case query[i] == '"' && start < 0:
			start = i + 1
		case query[i] == '"':
			out = append(out, query[start:i])
			start = -1
		}
	}
	if start >= 0 {
		out = append(out, query[start:])
	}
	return out
}
