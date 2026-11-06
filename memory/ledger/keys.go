package ledger

import (
	"regexp"
	"strings"
)

// The keys of a record are the exact strings a rule, a scope exclusion or a decision is about:
// the code spans it quotes (a path, a tool, a command) and the bare paths it names. Specifics
// (Specifics) are numbers, dates and capitalized names, so "Don't touch `migrations/`" has none;
// its key is "migrations/". Level 1 never lets a record standing for such a record drop one of
// its keys (consolidate's drop check), so the rule cannot be merged or summarized away into
// words that no longer name what it is about. Keys are read from the text by these patterns
// alone, the same in every engine.
var (
	keySpan = regexp.MustCompile("`([^`\n]+)`")
	// keyPath is a bare path: a token with a slash between or after path characters. It is read
	// from the text with its code spans taken out.
	keyPath = regexp.MustCompile(`(?:^|[\s(\["'])((?:~|\.{1,2})?/?[A-Za-z0-9_.-]+(?:/[A-Za-z0-9_.-]*)+)`)
)

// keyPathWords are slash pairs that name no path.
var keyPathWords = map[string]bool{"and/or": true, "either/or": true}

// RecordKeys are text's keys (see keySpan, keyPath), in order, without duplicates: each code
// span's text, trimmed, then each bare path outside the code spans, without the punctuation
// that closes a sentence or a clause after it. A bare path needs a letter and at least four
// characters.
func RecordKeys(text string) []string {
	seen := map[string]bool{}
	out := []string{}
	add := func(k string) {
		if k != "" && !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	for _, m := range keySpan.FindAllStringSubmatch(text, -1) {
		add(strings.TrimSpace(m[1]))
	}
	bare := keySpan.ReplaceAllString(text, " ")
	for _, m := range keyPath.FindAllStringSubmatch(bare, -1) {
		k := strings.TrimRight(m[1], ".,;:")
		if len(k) >= 4 && strings.ContainsAny(strings.ToLower(k), "abcdefghijklmnopqrstuvwxyz") && !keyPathWords[strings.ToLower(k)] {
			add(k)
		}
	}
	return out
}

// MissingKeys are the keys of text (RecordKeys) that replacement does not hold byte for byte.
func MissingKeys(text, replacement string) []string {
	out := []string{}
	for _, k := range RecordKeys(text) {
		if !strings.Contains(replacement, k) {
			out = append(out, k)
		}
	}
	return out
}

// KeepsKeys reports whether level 1 must keep o's keys (RecordKeys) in any record standing for
// it: o stands for the user's own rule or correction (UserPriority) or is a decision
// (KindDecision).
func KeepsKeys(o Observation) bool {
	return UserPriority(o) || o.Kind == KindDecision
}
