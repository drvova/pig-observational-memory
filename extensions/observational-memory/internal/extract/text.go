package extract

import (
	"regexp"
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

// sentences splits text into the sentences a record may keep, each on one line: fenced code is
// not a statement and is skipped, every remaining line is split at a sentence end (., ! or ?
// followed by whitespace and a capital, a digit, a quote or a bracket), and list markers are
// trimmed. Nothing is reworded.
func sentences(text string) []string {
	out := []string{}
	fenced := false
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced || trimmed == "" {
			continue
		}
		trimmed = strings.TrimSpace(strings.TrimLeft(trimmed, "-*•>#"))
		for _, s := range splitLine(trimmed) {
			if s = oneLine(s); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// splitLine splits one line at its sentence ends.
func splitLine(line string) []string {
	out := []string{}
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
		out = append(out, line[start:j])
		start, i = k, k-1
	}
	return append(out, line[start:])
}

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
