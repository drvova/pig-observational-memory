package ledger

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
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
	items := specifics(text)
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = s.text
	}
	return out
}

// specific is one item Specifics finds, with whether it is a name (a capitalized word) rather
// than a number or a time phrase.
type specific struct {
	text string
	name bool
}

// specifics is Specifics with each item's type.
func specifics(text string) []specific {
	seen := map[string]bool{}
	out := []specific{}
	add := func(s string, name bool) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s != "" && !seen[s] {
			seen[s] = true
			out = append(out, specific{text: s, name: name})
		}
	}
	for _, m := range specificNumber.FindAllString(text, -1) {
		add(strings.ReplaceAll(m, ",", ""), false)
	}
	for _, m := range specificTime.FindAllString(text, -1) {
		add(m, false)
	}
	for _, loc := range specificWord.FindAllStringIndex(text, -1) {
		word := strings.TrimRight(text[loc[0]:loc[1]], ".")
		if genericCapitals[word] || opensSentence(text, loc[0]) || (loc[0] > 0 && isWordChar(text[loc[0]-1])) {
			continue
		}
		add(word, true)
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

// specificToken is one word or number of a carrier text, the unit Given compares a specific
// against.
var specificToken = regexp.MustCompile(`[\p{L}\p{N}][\p{L}\p{N}'&.+-]*[\p{L}\p{N}]|[\p{L}\p{N}]`)

// Given is the set of specifics some carrier texts already give, which AddedSpecifics subtracts
// from a text's own. Build it once (NewGiven) to subtract the same carriers from many texts.
type Given struct {
	// items are the carriers' numbers and time phrases (Specifics), lowercased.
	items map[string]bool
	// names are the carriers' names: their Specifics names and every capitalized whole word,
	// lowercased.
	names map[string]bool
}

// NewGiven is the specifics carriers give: each carrier's Specifics, plus each of its
// capitalized whole words (so a name that opens a carrier's sentence, which Specifics skips
// there, still counts as given). A lowercase word gives nothing: "us", "nice" or "may" in a
// carrier never gives the names "US", "Nice" or "May".
func NewGiven(carriers []string) Given {
	g := Given{items: map[string]bool{}, names: map[string]bool{}}
	for _, c := range carriers {
		for _, s := range specifics(c) {
			if s.name {
				g.names[s.text] = true
			} else {
				g.items[s.text] = true
			}
		}
		for _, w := range specificToken.FindAllString(c, -1) {
			if r, _ := utf8.DecodeRuneInString(w); unicode.IsUpper(r) {
				g.names[strings.ToLower(strings.TrimRight(w, "."))] = true
			}
		}
	}
	return g
}

// has reports whether g gives s: exactly, on whole items, never substrings (so "8" is not given
// by "18"); a name also by its bare form without a possessive ("Lisbon's" by "Lisbon"), and by
// its plural or singular when the carrier's form is a name too ("Sunday" by "Sundays", one
// trailing "s").
func (g Given) has(s specific) bool {
	if g.items[s.text] || g.names[s.text] {
		return true
	}
	if !s.name {
		return false
	}
	bare := strings.TrimSuffix(s.text, "'s")
	if g.names[bare] || g.names[bare+"s"] {
		return true
	}
	singular, plural := strings.CutSuffix(bare, "s")
	return plural && g.names[singular]
}

// Added returns the specifics of text that none of givens gives, in order (AddedSpecifics).
func Added(text string, givens ...Given) []string {
	added := []string{}
	for _, s := range specifics(text) {
		given := false
		for _, g := range givens {
			if g.has(s) {
				given = true
				break
			}
		}
		if !given {
			added = append(added, s.text)
		}
	}
	return added
}

// AddedSpecifics returns the specifics of text that the carriers did not already give, in
// order: the set difference of Specifics(text) and what the carriers give (NewGiven). Matching
// is on whole items, never substrings; case is ignored for a carrier's names and its capitalized
// words only, so a lowercase common word never cancels a name; thousands separators are dropped
// as Specifics drops them; a possessive is given by its bare name, and a name by its plural or
// singular name (Given).
func AddedSpecifics(text string, carriers []string) []string {
	return Added(text, NewGiven(carriers))
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

// phraseSpecifics are the specifics of a phrase quoted out of a sentence (the X of "not X"),
// with a capitalized word counted where it opens the phrase too: the phrase does not open a
// sentence, so its first word is not a sentence start.
func phraseSpecifics(phrase string) []specific {
	// A lowercase word with no specifics in front keeps the phrase's first word from reading as
	// a sentence start.
	return specifics("of " + phrase)
}

// PhraseSpecifics returns the specifics of a phrase quoted out of a sentence (the X a
// correction names in "not X" or "instead of X"): its numbers, time phrases and names,
// lowercased, in order, without duplicates. Unlike Specifics, a name opening the phrase counts.
func PhraseSpecifics(phrase string) []string {
	items := phraseSpecifics(phrase)
	out := make([]string, len(items))
	for i, s := range items {
		out[i] = s.text
	}
	return out
}

// CarriesPhrase reports whether text carries every specific of phrase (PhraseSpecifics) as a
// specific of its own, matched as Given matches; false when phrase has none. A text that only
// contains the phrase's words ("make sure" for "sure") does not carry it.
func CarriesPhrase(text, phrase string) bool {
	items := phraseSpecifics(phrase)
	if len(items) == 0 {
		return false
	}
	g := NewGiven([]string{text})
	for _, s := range items {
		if !g.has(s) {
			return false
		}
	}
	return true
}

// CoveredByPhrase reports whether every specific of text is one of phrase's (PhraseSpecifics):
// replacing the phrase replaces everything specific text says.
func CoveredByPhrase(text, phrase string) bool {
	given := Given{items: map[string]bool{}, names: map[string]bool{}}.with(phraseSpecifics(phrase))
	return len(Added(text, given)) == 0
}

// with is g giving items too.
func (g Given) with(items []specific) Given {
	for _, s := range items {
		if s.name {
			g.names[s.text] = true
		} else {
			g.items[s.text] = true
		}
	}
	return g
}

// Restates reports whether text gives a new value of a specific earlier carries: for one type
// of specific (names, or numbers and time phrases), both carry one and text carries one earlier
// does not give. A correction that names no phrase it replaces ("Correction: the budget is
// 6000 USD") restates only such a record ("the budget is 5000 USD").
func Restates(text, earlier string) bool {
	mine, theirs := specifics(text), specifics(earlier)
	given := NewGiven([]string{earlier})
	for _, name := range []bool{true, false} {
		has := false
		for _, s := range theirs {
			if s.name == name {
				has = true
				break
			}
		}
		if !has {
			continue
		}
		for _, s := range mine {
			if s.name == name && !given.has(s) {
				return true
			}
		}
	}
	return false
}
