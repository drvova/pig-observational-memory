package extract

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
)

// textBlocks is the text of a message's content: a string, or its text blocks in order. Thinking,
// tool calls and images are not text.
func textBlocks(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		parts := []string{}
		for _, raw := range c {
			block, _ := raw.(map[string]any)
			if block == nil || block["type"] != "text" {
				continue
			}
			if text, ok := block["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// withoutMarkup is a user message's text without harness markup (serialize.WithoutMarkup).
func withoutMarkup(text string) string { return serialize.WithoutMarkup(text) }

// markupOnly is a line that is a markup tag alone, such as an unclosed wrapper's opening tag.
var markupOnly = regexp.MustCompile(`^</?[A-Za-z][\w-]*(?:\s[^<>]*)?/?>$`)

// isMarkupOnly reports a sentence that is a markup tag and nothing else.
func isMarkupOnly(sentence string) bool { return markupOnly.MatchString(strings.TrimSpace(sentence)) }

// oneLine folds runs of whitespace, newlines included, into one space.
func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

// clip is text on one line, cut to n runes with an ellipsis when longer.
func clip(text string, n int) string {
	line := []rune(oneLine(text))
	if len(line) <= n {
		return string(line)
	}
	if n <= 1 {
		return "…"
	}
	return string(line[:n-1]) + "…"
}

// listMarker opens a list item: a bullet ("-", "*", "+", "•") or a number ("1.", "2)") followed
// by whitespace. "**bold**" and "1.5 km" are not markers: a marker is always followed by a space.
var listMarker = regexp.MustCompile(`^(?:[-*+•]|\d{1,3}[.)])\s+`)

// bareMarker is a piece of text that is a list marker and nothing else, such as the "2." a
// sentence split leaves of an inline list.
var bareMarker = regexp.MustCompile(`^(?:[-*+•]|\d{1,3}[.)])$`)

// blockPrefix is a heading's or a quote's leading markup, which says nothing itself.
var blockPrefix = regexp.MustCompile(`^(?:>\s*)+|^#{1,6}\s+`)

// unit is one statement of a message: a line's sentences, or a list, whose items (their markers
// removed, each on one line) stay in order.
type unit struct {
	sentences []string
	items     []string
}

// isList reports a list unit.
func (u unit) isList() bool { return len(u.items) > 0 }

// joinItems is list items joined in order, one statement, as consecutive records merge (a
// space between them): nothing is added and nothing reworded.
func joinItems(items []string) string { return strings.Join(items, " ") }

// thematicBreak is a markdown horizontal rule: three or more of one of "-", "*" or "_", spaces
// allowed between them. It says nothing.
var thematicBreak = regexp.MustCompile(`^(?:(?:-[ \t]*){3,}|(?:\*[ \t]*){3,}|(?:_[ \t]*){3,})$`)

// numberedMarker reads a numbered list marker's number.
var numberedMarker = regexp.MustCompile(`^(\d{1,3})[.)]`)

// units splits text into the statements a record may keep: fenced code is not a statement and is
// skipped (a fence indented inside a list item leaves the list open); consecutive list items
// (blank lines between them allowed, an indented line continuing the item before it) are one
// list unit, but after a blank line an item of the other marker type (bullet or number), or a
// numbering that starts again, opens a new list; every other line is split at its sentence ends.
// Heading and quote markup and horizontal rules are trimmed, list markers are removed and never
// a statement of their own, and nothing is reworded.
func units(text string) []unit {
	out := []unit{}
	fenced, blank := false, false
	var list *unit
	numbered, lastNumber := false, 0
	closeList := func() {
		if list != nil {
			out = append(out, *list)
			list = nil
		}
	}
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		indented := line != strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			if list == nil || !indented {
				closeList()
			}
			continue
		}
		if fenced {
			continue
		}
		if trimmed == "" {
			blank = true
			continue
		}
		afterBlank := blank
		blank = false
		trimmed = strings.TrimSpace(blockPrefix.ReplaceAllString(trimmed, ""))
		if thematicBreak.MatchString(trimmed) {
			continue
		}
		if m := listMarker.FindString(trimmed); m != "" {
			if item := oneLine(trimmed[len(m):]); item != "" && !bareMarker.MatchString(item) {
				number, isNumber := 0, false
				if n := numberedMarker.FindStringSubmatch(m); n != nil {
					number, _ = strconv.Atoi(n[1])
					isNumber = true
				}
				if list != nil && afterBlank && (numbered != isNumber || isNumber && number <= lastNumber) {
					closeList()
				}
				if list == nil {
					list = &unit{}
				}
				list.items = append(list.items, item)
				numbered, lastNumber = isNumber, number
			}
			continue
		}
		if bareMarker.MatchString(trimmed) {
			continue
		}
		if list != nil && indented {
			list.items[len(list.items)-1] += " " + oneLine(trimmed)
			continue
		}
		closeList()
		u := unit{}
		for _, s := range splitLine(trimmed) {
			if s = oneLine(s); s != "" {
				u.sentences = append(u.sentences, s)
			}
		}
		if len(u.sentences) > 0 {
			out = append(out, u)
		}
	}
	closeList()
	return out
}

// sentences splits text into the sentences a record may keep, each on one line (units): a list
// item is split like any line, so a sentence is never a list marker alone. Nothing is reworded.
func sentences(text string) []string {
	out := []string{}
	for _, u := range units(text) {
		if !u.isList() {
			out = append(out, u.sentences...)
			continue
		}
		for _, item := range u.items {
			for _, s := range splitLine(item) {
				if s = oneLine(s); s != "" {
					out = append(out, s)
				}
			}
		}
	}
	return out
}

// splitLine splits one line at its sentence ends (., ! or ? followed by whitespace and a capital,
// a digit, a quote or a bracket). A piece that is a list marker alone ("2." of "1. Run it. 2.
// Check it.") is joined to the piece after it, and dropped at the end of the line.
func splitLine(line string) []string {
	pieces := []string{}
	start := 0
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case '.', '!', '?':
		default:
			continue
		}
		j := i + 1
		for j < len(line) && (line[j] == '.' || line[j] == '!' || line[j] == '?' || line[j] == '"' || line[j] == '\'' || line[j] == ')') {
			j++
		}
		k := j
		for k < len(line) && (line[k] == ' ' || line[k] == '\t') {
			k++
		}
		if k == j || k >= len(line) {
			continue
		}
		r, _ := utf8.DecodeRuneInString(line[k:])
		if !unicode.IsUpper(r) && !unicode.IsDigit(r) && r != '"' && r != '\'' && r != '(' && r != '[' {
			continue
		}
		pieces = append(pieces, line[start:j])
		start, i = k, k-1
	}
	pieces = append(pieces, line[start:])
	out := make([]string, 0, len(pieces))
	pending := ""
	for _, piece := range pieces {
		if bareMarker.MatchString(strings.TrimSpace(piece)) {
			pending += piece + " "
			continue
		}
		out = append(out, pending+piece)
		pending = ""
	}
	// An inline list's first marker can close the text before it ("Do these: 1. Stretch ... 2.
	// Ice ..."): a piece ending in a number marker whose next number opens a later piece gives
	// that marker to the piece after it.
	for n := 0; n+1 < len(out); n++ {
		m := trailingMarker.FindStringSubmatchIndex(out[n])
		if m == nil {
			continue
		}
		number, _ := strconv.Atoi(out[n][m[2]:m[3]])
		next := strconv.Itoa(number+1) + "."
		opens := false
		for _, later := range out[n+1:] {
			if strings.HasPrefix(later, next+" ") {
				opens = true
				break
			}
		}
		if !opens {
			continue
		}
		out[n+1] = out[n][m[2]:m[1]] + " " + out[n+1]
		out[n] = strings.TrimRight(out[n][:m[0]], " \t")
	}
	return out
}

// trailingMarker is a number marker closing a piece of text after whitespace ("Do these: 1.").
var trailingMarker = regexp.MustCompile(`\s(\d{1,3})\.$`)

// ackWords are the words of a bare acknowledgement: a sentence made of these alone carries
// nothing to remember.
var ackWords = map[string]bool{
	"ok": true, "okay": true, "k": true, "yes": true, "yeah": true, "yep": true, "no": true, "nope": true,
	"thanks": true, "thank": true, "you": true, "thx": true, "sure": true, "great": true, "good": true,
	"got": true, "it": true, "sounds": true, "cool": true, "nice": true, "perfect": true, "right": true,
	"alright": true, "done": true, "fine": true, "awesome": true, "excellent": true, "so": true, "much": true,
	"please": true, "go": true, "ahead": true, "continue": true, "proceed": true, "lgtm": true, "agreed": true,
	"actually": true, "correct": true, "exactly": true, "understood": true, "noted": true, "hi": true, "hello": true,
}

// isBareAck reports a sentence made only of acknowledgement words.
func isBareAck(sentence string) bool {
	words := strings.FieldsFunc(strings.ToLower(sentence), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '\'' })
	if len(words) == 0 {
		return true
	}
	for _, w := range words {
		if !ackWords[strings.Trim(w, "'")] {
			return false
		}
	}
	return true
}

// isQuestion reports a sentence that asks rather than states.
func isQuestion(sentence string) bool {
	return strings.HasSuffix(strings.TrimRight(sentence, `"') `), "?")
}

// normalized is text compared for echoes: one line, lowercased.
func normalized(text string) string { return strings.ToLower(oneLine(text)) }
