// Package legacyquery is the frozen recognizer of the memory tool's removed query language
// (docs/code-tool.md, "Migration"): a copy of internal/memquery's lexer, parser and vocabulary
// with no interpreter. It validates stored definitions written in the old grammar (summary
// declarations, query detectors, defined queries), reads old memory tool calls in session
// entries, and supplies the kernel's retired vocabulary. It never runs anything.
//
// It is frozen: no new words, sources, stages or writes are added, so every definition it
// accepted once keeps loading, and its tests are copied from the parser's.
package legacyquery

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// tokenKind classifies a token.
type tokenKind int

const (
	tokEOF tokenKind = iota
	tokWord
	tokString
	tokPipe
	tokEq
	tokTilde
)

func (k tokenKind) String() string {
	switch k {
	case tokWord:
		return "word"
	case tokString:
		return "quoted text"
	case tokPipe:
		return "|"
	case tokEq:
		return "="
	case tokTilde:
		return "~"
	}
	return "end of query"
}

// token is one lexeme: its kind, its text (a string's unescaped value), its 1-based character
// position, and its byte span in the query.
type token struct {
	kind       tokenKind
	text       string
	pos        int
	start, end int
}

// describe names a token in an error message.
func (t token) describe() string {
	switch t.kind {
	case tokWord:
		return fmt.Sprintf("%q", t.text)
	case tokString:
		return fmt.Sprintf("quoted text %q", clipRunes(t.text, 30))
	case tokEOF:
		return "the end of the query"
	}
	return fmt.Sprintf("%q", t.kind.String())
}

// Error is a query that cannot run: where it went wrong and how to fix it.
type Error struct {
	// Pos is the 1-based character position of the offending token, 0 when the error is not
	// tied to one.
	Pos int
	// Msg says what is wrong; Hint says how to fix it.
	Msg, Hint string
}

// Error formats as "at character P: msg; hint".
func (e *Error) Error() string {
	s := e.Msg
	if e.Pos > 0 {
		s = fmt.Sprintf("at character %d: %s", e.Pos, e.Msg)
	}
	if e.Hint != "" {
		s += "; " + e.Hint
	}
	return s
}

func errAt(t token, hint, format string, args ...any) *Error {
	return &Error{Pos: t.pos, Msg: fmt.Sprintf(format, args...), Hint: hint}
}

// isWordRune reports whether r can be part of a bare word: anything but space, a quote and the
// operators.
func isWordRune(r rune) bool {
	return !unicode.IsSpace(r) && r != '"' && r != '|' && r != '=' && r != '~'
}

// lex splits a query into tokens, ending with one tokEOF.
func lex(query string) ([]token, error) {
	tokens := []token{}
	pos := 1
	for i := 0; i < len(query); {
		r, n := utf8.DecodeRuneInString(query[i:])
		switch {
		case unicode.IsSpace(r):
			i += n
			pos++
		case r == '|' || r == '=' || r == '~':
			kind := map[rune]tokenKind{'|': tokPipe, '=': tokEq, '~': tokTilde}[r]
			tokens = append(tokens, token{kind: kind, text: string(r), pos: pos, start: i, end: i + n})
			i += n
			pos++
		case r == '"':
			t, next, nextPos, err := lexString(query, i, pos)
			if err != nil {
				return nil, err
			}
			tokens = append(tokens, t)
			i, pos = next, nextPos
		default:
			start, startPos := i, pos
			for i < len(query) {
				r, n := utf8.DecodeRuneInString(query[i:])
				if !isWordRune(r) {
					break
				}
				i += n
				pos++
			}
			tokens = append(tokens, token{kind: tokWord, text: query[start:i], pos: startPos, start: start, end: i})
		}
	}
	return append(tokens, token{kind: tokEOF, pos: pos, start: len(query), end: len(query)}), nil
}

// lexString reads the quoted string opening at byte i (character pos): `\"` `\\` `\n` `\t`
// escape, any other backslash is kept as written.
func lexString(query string, i, pos int) (token, int, int, error) {
	start, startPos := i, pos
	i++
	pos++
	var b strings.Builder
	for i < len(query) {
		r, n := utf8.DecodeRuneInString(query[i:])
		switch r {
		case '"':
			return token{kind: tokString, text: b.String(), pos: startPos, start: start, end: i + n}, i + n, pos + 1, nil
		case '\\':
			if i+n < len(query) {
				next, m := utf8.DecodeRuneInString(query[i+n:])
				switch next {
				case '"', '\\':
					b.WriteRune(next)
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				default:
					b.WriteRune(r)
					b.WriteRune(next)
				}
				i += n + m
				pos += 2
				continue
			}
		}
		b.WriteRune(r)
		i += n
		pos++
	}
	return token{}, 0, 0, &Error{Pos: startPos, Msg: "unterminated quote", Hint: `close the text with " (write \" for a quote inside it)`}
}

// clipRunes keeps the first n characters of s, marking a cut with "…".
func clipRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}
