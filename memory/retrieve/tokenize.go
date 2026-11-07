// Package retrieve is the search behind the memory tool's reads (docs/protocol.md, "Retrieval";
// docs/brain-memory.md, Phase 4): the tokenizer contract, an exact key index, integer BM25 over
// the tokens, two-level reciprocal rank fusion, status tags and neighbour merging. It is pure and
// harness-neutral; lisp/src/retrieve.lisp is its twin, and contracts/tokenize.jsonl and
// contracts/retrieval.jsonl are the cases both engines check byte for byte.
package retrieve

import "strings"

// The tokenizer reads ASCII classes byte by byte, as extract/output.go's scanners do; a byte at or
// above 0x80 is a word byte, so a UTF-8 word stays whole and both engines cut it the same way.

// isWordByte is a byte a raw token is made of: an ASCII letter or digit, a joiner (_ - . / :) or
// any non-ASCII byte.
func isWordByte(c byte) bool {
	return isAlnum(c) || isJoiner(c) || c >= 0x80
}

// isJoiner is a byte that joins an identifier's parts: snake, kebab, dotted, path and
// qualified separators.
func isJoiner(c byte) bool {
	return c == '_' || c == '-' || c == '.' || c == '/' || c == ':'
}

// isEdge is a byte trimmed off the end of a raw token (sentence punctuation, a path's trailing
// slash); isLeadingEdge one trimmed off its start (a flag's dashes). A leading dot or slash is kept
// (.golangci.yml, ./cmd, /etc/hosts), and an underscore anywhere: it is part of a name.
func isEdge(c byte) bool {
	return c == '.' || c == '-' || c == '/' || c == ':'
}

func isLeadingEdge(c byte) bool { return c == '-' || c == ':' }

func isAlnum(c byte) bool { return isDigit(c) || isUpper(c) || isLower(c) }
func isDigit(c byte) bool { return c >= '0' && c <= '9' }
func isUpper(c byte) bool { return c >= 'A' && c <= 'Z' }
func isLower(c byte) bool { return c >= 'a' && c <= 'z' }
func isHex(c byte) bool   { return isDigit(c) || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F' }

// lowerASCII lowercases the ASCII letters of s only.
func lowerASCII(s string) string {
	for i := 0; i < len(s); i++ {
		if isUpper(s[i]) {
			b := []byte(s)
			for j := i; j < len(b); j++ {
				if isUpper(b[j]) {
					b[j] += 'a' - 'A'
				}
			}
			return string(b)
		}
	}
	return s
}

// RawTokens are the maximal runs of word bytes in text, each trimmed of its edge bytes, in
// order; a run left empty, or holding no letter, digit or non-ASCII byte, is left out.
func RawTokens(text string) []string {
	var out []string
	for i := 0; i < len(text); {
		if !isWordByte(text[i]) {
			i++
			continue
		}
		j := i
		for j < len(text) && isWordByte(text[j]) {
			j++
		}
		if t := trimEdges(text[i:j]); hasWordCore(t) {
			out = append(out, t)
		}
		i = j
	}
	return out
}

func hasWordCore(t string) bool {
	for i := 0; i < len(t); i++ {
		if isAlnum(t[i]) || t[i] >= 0x80 {
			return true
		}
	}
	return false
}

func trimEdges(s string) string {
	start, end := 0, len(s)
	for start < end && isLeadingEdge(s[start]) {
		start++
	}
	for end > start && isEdge(s[end-1]) {
		end--
	}
	return s[start:end]
}

// IsVersion reports a version: an optional v or V, digits, one or more ".digits", then any
// number of "-" or "." suffixes of letters and digits (1.22, v2.6.10, 1.0.0-rc.1).
func IsVersion(t string) bool {
	i := 0
	if i < len(t) && (t[i] == 'v' || t[i] == 'V') {
		i++
	}
	digits := func() bool {
		start := i
		for i < len(t) && isDigit(t[i]) {
			i++
		}
		return i > start
	}
	if !digits() {
		return false
	}
	dotted := 0
	for i+1 < len(t) && t[i] == '.' && isDigit(t[i+1]) {
		i++
		digits()
		dotted++
	}
	if dotted == 0 {
		return false
	}
	for i < len(t) {
		if t[i] != '-' && t[i] != '.' {
			return false
		}
		i++
		start := i
		for i < len(t) && isAlnum(t[i]) {
			i++
		}
		if i == start {
			return false
		}
	}
	return true
}

// IsSHA reports a commit or content hash: 7 to 64 hex bytes holding at least one digit and at
// least one letter.
func IsSHA(t string) bool {
	if len(t) < 7 || len(t) > 64 {
		return false
	}
	digit, letter := false, false
	for i := 0; i < len(t); i++ {
		c := t[i]
		if !isHex(c) {
			return false
		}
		if isDigit(c) {
			digit = true
		} else {
			letter = true
		}
	}
	return digit && letter
}

// IsDotNumber reports a token that starts with a digit and holds a dot: a decimal, a duration,
// an address (0.02s, 10.0.0.1).
func IsDotNumber(t string) bool { return t != "" && isDigit(t[0]) && strings.IndexByte(t, '.') >= 0 }

// ExactOnly reports a raw token that goes to the exact key index only, never to the lexical
// terms: a version, a SHA or a dotted number, whose numerals a split would lose.
func ExactOnly(t string) bool { return IsVersion(t) || IsSHA(t) || IsDotNumber(t) }

// MinTermBytes is the shortest lexical term.
const MinTermBytes = 2

// TokenTerms are a raw token's lexical terms, lowercased (ASCII), each once, in this order: the
// whole token; its segments split at the joiners (_ - . / :); then, for a segment of more than
// one camelCase part, its parts. A term shorter than MinTermBytes is left out. An exact-only
// token has none.
func TokenTerms(t string) []string {
	if ExactOnly(t) {
		return nil
	}
	var out []string
	add := func(s string) {
		if len(s) < MinTermBytes {
			return
		}
		s = lowerASCII(s)
		for _, have := range out {
			if have == s {
				return
			}
		}
		out = append(out, s)
	}
	add(t)
	segments := splitJoiners(t)
	for _, s := range segments {
		add(s)
	}
	for _, s := range segments {
		if parts := camelParts(s); len(parts) > 1 {
			for _, p := range parts {
				add(p)
			}
		}
	}
	return out
}

func splitJoiners(t string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(t); i++ {
		if i == len(t) || isJoiner(t[i]) {
			if i > start {
				out = append(out, t[start:i])
			}
			start = i + 1
		}
	}
	return out
}

// camelParts cuts a segment before an uppercase letter that follows a lowercase letter or a
// digit (parseConfig, utf8Decode), and before the last uppercase letter of a run that a
// lowercase letter follows (HTTPServer: HTTP, Server). Digits stay with the part before them.
func camelParts(s string) []string {
	var out []string
	start := 0
	for i := 1; i < len(s); i++ {
		c, prev := s[i], s[i-1]
		if !isUpper(c) {
			continue
		}
		if isLower(prev) || isDigit(prev) || isUpper(prev) && i+1 < len(s) && isLower(s[i+1]) {
			out = append(out, s[start:i])
			start = i
		}
	}
	return append(out, s[start:])
}

// Tokens are a text's lexical terms, a term once per raw token that gives it (so a document's
// term counts are its term frequencies), and its exact-only tokens, byte for byte, each once.
func Tokens(text string) (terms, exact []string) {
	for _, t := range RawTokens(text) {
		if ExactOnly(t) {
			exact = appendOnce(exact, t)
			continue
		}
		terms = append(terms, TokenTerms(t)...)
	}
	return terms, exact
}

func appendOnce(list []string, s string) []string {
	for _, have := range list {
		if have == s {
			return list
		}
	}
	return append(list, s)
}

// errorMarkers are the substrings, matched case-sensitively, that make a line an error line.
var errorMarkers = []string{"error", "Error", "ERROR", "FAIL", "panic", "fatal", "Fatal", "FATAL", "exception", "Exception", "Traceback"}

// IsErrorLine reports a line holding an error marker.
func IsErrorLine(line string) bool {
	for _, m := range errorMarkers {
		if strings.Contains(line, m) {
			return true
		}
	}
	return false
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// NormalizeLine is a line as the exact index compares it: ASCII white space runs as one space,
// trimmed, then any leading locations ("file.go:42: ", "main.go:3:7: ") cut off, so a compiler's
// or test runner's error line equals the message a person quotes.
func NormalizeLine(line string) string {
	var b strings.Builder
	space := false
	for i := 0; i < len(line); i++ {
		c := line[i]
		if isSpace(c) {
			space = b.Len() > 0
			continue
		}
		if space {
			b.WriteByte(' ')
			space = false
		}
		b.WriteByte(c)
	}
	s := b.String()
	for {
		rest, ok := cutLocation(s)
		if !ok {
			return s
		}
		s = rest
	}
}

// cutLocation cuts one leading "<name>:<digits>[:<digits>]: " off s, where name holds no space
// or colon; ok is false when s does not start with one, or nothing would be left.
func cutLocation(s string) (string, bool) {
	i := 0
	for i < len(s) && s[i] != ':' && s[i] != ' ' {
		i++
	}
	if i == 0 || i >= len(s) || s[i] != ':' {
		return s, false
	}
	for n := 0; n < 2; n++ {
		j := i + 1
		for j < len(s) && isDigit(s[j]) {
			j++
		}
		if j == i+1 || j >= len(s) || s[j] != ':' {
			if n == 0 {
				return s, false
			}
			break
		}
		i = j
	}
	if i+2 > len(s) || s[i+1] != ' ' || i+2 == len(s) {
		return s, false
	}
	return s[i+2:], true
}

// ErrorLines are text's error lines (IsErrorLine; every line when all is true), normalized
// (NormalizeLine), each once, in order; a normalized line shorter than MinErrorLineBytes is left
// out.
func ErrorLines(text string, all bool) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		if !all && !IsErrorLine(line) {
			continue
		}
		if n := NormalizeLine(line); len(n) >= MinErrorLineBytes {
			out = appendOnce(out, n)
		}
	}
	return out
}

// MinErrorLineBytes is the shortest error line the exact index keeps.
const MinErrorLineBytes = 8
