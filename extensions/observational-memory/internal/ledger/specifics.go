package ledger

import (
	"regexp"
	"strings"
)

// The specifics of a memory line are the details a later question is most
// likely to need and a paraphrase most likely to lose: numbers (amounts,
// counts, prices, percentages), relative date and time phrases, and capitalized
// names. They are found by text patterns alone, never by a model's judgment, so
// the same line always yields the same specifics.

var (
	specificNumber = regexp.MustCompile(`\d[\d,]*(?:\.\d+)?`)
	specificTime   = regexp.MustCompile(`(?i)\b(?:today|tonight|yesterday|tomorrow|(?:last|next|this) (?:week|weekend|month|year|night|morning|evening)|(?:\d+|a|an|one|two|three|four|five|six|seven|eight|nine|ten|a few|several) (?:days?|weeks?|months?|years?) (?:ago|later|earlier))\b`)
	specificWord   = regexp.MustCompile(`[A-Z][\p{L}\p{N}'&.+-]*[\p{L}\p{N}]|[A-Z]`)
)

// genericCapitals are capitalized words that name no particular thing: the
// roles memory lines are written in, pronouns and common sentence openers.
var genericCapitals = map[string]bool{
	"User": true, "Users": true, "Assistant": true, "I": true, "The": true, "A": true, "An": true,
	"They": true, "He": true, "She": true, "It": true, "We": true, "You": true, "Their": true, "His": true, "Her": true,
	"This": true, "That": true, "These": true, "Those": true, "And": true, "But": true, "Or": true, "So": true,
	"If": true, "When": true, "After": true, "Before": true, "Later": true, "Also": true, "Then": true, "Now": true,
}

// Specifics returns the specifics of text, lowercased, in order, without
// duplicates. A capitalized word counts as a name only when it does not open a
// sentence, so ordinary sentence starts are not mistaken for names.
func Specifics(text string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, m := range specificNumber.FindAllString(text, -1) {
		add(strings.ReplaceAll(m, ",", ""))
	}
	for _, m := range specificTime.FindAllString(text, -1) {
		add(m)
	}
	for _, loc := range specificWord.FindAllStringIndex(text, -1) {
		word := strings.TrimRight(text[loc[0]:loc[1]], ".")
		if genericCapitals[word] || opensSentence(text, loc[0]) || (loc[0] > 0 && isWordChar(text[loc[0]-1])) {
			continue
		}
		add(word)
	}
	return out
}

// HasSpecifics reports whether text carries any specific.
func HasSpecifics(text string) bool { return len(Specifics(text)) > 0 }

// MissingSpecifics returns the specifics of text that none of the given texts
// contains (case-insensitive; thousands separators ignored).
func MissingSpecifics(text string, carriers []string) []string {
	joined := strings.ToLower(strings.ReplaceAll(strings.Join(carriers, "\n"), ",", ""))
	missing := []string{}
	for _, s := range Specifics(text) {
		if !strings.Contains(joined, s) {
			missing = append(missing, s)
		}
	}
	return missing
}

func isWordChar(b byte) bool {
	return b == '_' || b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}

// opensSentence reports whether the word at i starts the text or follows a
// sentence end (., !, ?, :, ;) or an opening bracket or quote.
func opensSentence(text string, i int) bool {
	j := i - 1
	for j >= 0 && (text[j] == ' ' || text[j] == '\t' || text[j] == '\n') {
		j--
	}
	if j < 0 {
		return true
	}
	switch text[j] {
	case '.', '!', '?', ':', ';', '(', '[', '"', '\'', '-':
		return true
	}
	return false
}
