package ledger

import (
	"regexp"
	"strings"
)

// Memory v2's level-0 links between records (docs/memory-v2.md, "Possible updates" and
// "Negation conflicts"): facts code reads from the text alone, which never drop or supersede a
// record. The word lists are fixed grammar, not tuned.

var (
	// typedISODate is an absolute date, as written or as ResolveRelativeDates annotates one.
	typedISODate = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}\b`)
	// typedAmount is a number with a currency: a symbol before it, or a currency word after.
	typedAmount = regexp.MustCompile(`(?i)[$€£¥]\s?\d[\d,]*(?:\.\d+)?|\b\d[\d,]*(?:\.\d+)?\s?(?:usd|eur|gbp|jpy|dollars?|euros?|pounds?|bucks)\b`)
	// typedPath is a path: words joined by at least one slash.
	typedPath = regexp.MustCompile(`(?:~|\.{1,2})?/?[\w.-]+(?:/[\w.-]+)+/?`)
	// annotationOpen follows a relative phrase ResolveRelativeDates already resolved.
	annotationOpen = regexp.MustCompile(`^\s*\[=`)
	// digitsComma is a thousands separator.
	digitsComma = regexp.MustCompile(`(\d),(\d)`)
	// clauseBreak ends a clause for negation: punctuation, a sentence end, or "but".
	clauseBreak = regexp.MustCompile(`(?i)[,;:!?]|\.(?:\s|$)|\bbut\b`)
)

// negationWords negate the first specific after them in their clause.
var negationWords = map[string]bool{
	"not": true, "never": true, "no": true, "cannot": true,
	"didn't": true, "don't": true, "doesn't": true, "isn't": true, "wasn't": true, "weren't": true,
	"won't": true, "can't": true, "shouldn't": true, "haven't": true, "hasn't": true, "aren't": true,
}

// The types of specific a possible update compares (typedSpecifics).
const (
	specificTypeDate   = "date"
	specificTypeAmount = "amount"
	specificTypePath   = "path"
	specificTypeNumber = "number"
)

// typedSpecifics are text's values by type: dates (absolute dates, and time phrases no
// annotation resolved), amounts, paths, and the numbers that are none of those. Values are
// lowercased, with thousands separators dropped.
func typedSpecifics(text string) map[string]map[string]bool {
	out := map[string]map[string]bool{}
	add := func(kind, value string) {
		value = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), ",", ""))
		if value == "" {
			return
		}
		if out[kind] == nil {
			out[kind] = map[string]bool{}
		}
		out[kind][value] = true
	}
	rest := []byte(text)
	blank := func(loc []int) {
		for i := loc[0]; i < loc[1]; i++ {
			rest[i] = ' '
		}
	}
	for _, loc := range typedISODate.FindAllStringIndex(text, -1) {
		add(specificTypeDate, text[loc[0]:loc[1]])
		blank(loc)
	}
	for _, loc := range specificTime.FindAllStringIndex(text, -1) {
		if !annotationOpen.MatchString(text[loc[1]:]) {
			add(specificTypeDate, text[loc[0]:loc[1]])
		}
		blank(loc)
	}
	for _, loc := range typedAmount.FindAllStringIndex(string(rest), -1) {
		add(specificTypeAmount, strings.Join(specificNumber.FindAllString(string(rest[loc[0]:loc[1]]), -1), ""))
		blank(loc)
	}
	for _, loc := range typedPath.FindAllStringIndex(string(rest), -1) {
		add(specificTypePath, strings.TrimRight(string(rest[loc[0]:loc[1]]), "."))
		blank(loc)
	}
	for _, m := range specificNumber.FindAllString(string(rest), -1) {
		add(specificTypeNumber, m)
	}
	return out
}

// UpdatesSpecific reports whether later gives a different value of a specific type earlier
// gives (docs/memory-v2.md, "Possible updates"): for one type (a date, an amount, a path, a
// number), each carries a value of it the other does not. A later text that only adds a value
// beside the earlier ones updates nothing.
func UpdatesSpecific(later, earlier string) bool {
	mine, theirs := typedSpecifics(later), typedSpecifics(earlier)
	for kind, values := range mine {
		other := theirs[kind]
		if len(other) == 0 {
			continue
		}
		if hasOutside(values, other) && hasOutside(other, values) {
			return true
		}
	}
	return false
}

// UpdatedSpecifics are the specifics of earlier (Specifics items) that later's update replaces:
// the parts of earlier's values of every type later updates (UpdatesSpecific) that later does
// not give. Memory v2's level 1 excuses exactly these when a confirmed possible update
// supersedes earlier (docs/memory-v2.md, "Possible updates"); every other specific of earlier
// must still be carried.
func UpdatedSpecifics(later, earlier string) map[string]bool {
	mine, theirs := typedSpecifics(later), typedSpecifics(earlier)
	parts := map[string]bool{}
	for kind, values := range theirs {
		other := mine[kind]
		if len(other) == 0 || !hasOutside(values, other) || !hasOutside(other, values) {
			continue
		}
		for value := range values {
			if other[value] {
				continue
			}
			parts[value] = true
			for _, n := range specificNumber.FindAllString(value, -1) {
				parts[strings.ReplaceAll(n, ",", "")] = true
			}
			for _, w := range specificToken.FindAllString(value, -1) {
				parts[strings.ToLower(strings.TrimRight(w, "."))] = true
			}
		}
	}
	out := map[string]bool{}
	for _, s := range Specifics(earlier) {
		if parts[s] {
			out[s] = true
		}
	}
	return out
}

// hasOutside reports a value of a that b does not hold.
func hasOutside(a, b map[string]bool) bool {
	for v := range a {
		if !b[v] {
			return true
		}
	}
	return false
}

// NegatedSpecifics are the specifics of text (Specifics, single words and numbers) an explicit
// negation is attached to: the first specific after a negation word in the same clause, so in
// "use Redis, not Memcached" only memcached is negated, and in "never deploy Friday or Monday"
// only friday.
func NegatedSpecifics(text string) map[string]bool {
	out := map[string]bool{}
	items := map[string]bool{}
	for _, s := range Specifics(text) {
		items[s] = true
	}
	if len(items) == 0 {
		return out
	}
	normalized := strings.ReplaceAll(text, "’", "'")
	for digitsComma.MatchString(normalized) {
		normalized = digitsComma.ReplaceAllString(normalized, "$1$2")
	}
	start := 0
	clauses := []string{}
	for _, loc := range clauseBreak.FindAllStringIndex(normalized, -1) {
		clauses = append(clauses, normalized[start:loc[0]])
		start = loc[1]
	}
	clauses = append(clauses, normalized[start:])
	for _, clause := range clauses {
		pending := false
		for _, token := range specificToken.FindAllString(clause, -1) {
			word := strings.ToLower(strings.TrimRight(token, "."))
			switch {
			case negationWords[word]:
				pending = true
			case items[word]:
				if pending {
					out[word] = true
				}
				pending = false
			}
		}
	}
	return out
}

// NegationConflict reports whether a and b share a specific that exactly one of them negates
// (NegatedSpecifics; docs/memory-v2.md, "Negation conflicts"): "the release ships Friday" and
// "the release does not ship Friday".
func NegationConflict(a, b string) bool {
	negatedA, negatedB := NegatedSpecifics(a), NegatedSpecifics(b)
	shared := map[string]bool{}
	for _, s := range Specifics(a) {
		shared[s] = true
	}
	for _, s := range Specifics(b) {
		if shared[s] && negatedA[s] != negatedB[s] {
			return true
		}
	}
	return false
}

// RetractedIDs are the ids of the records a retraction (StatusRetracted) among observations
// supersedes: withdrawn records, which read as retracted wherever they are shown.
func RetractedIDs(observations []Observation) map[string]bool {
	out := map[string]bool{}
	for _, o := range observations {
		if o.Status != StatusRetracted {
			continue
		}
		for _, id := range o.Supersedes {
			if id != o.ID {
				out[id] = true
			}
		}
	}
	return out
}

// MaySupersede reports whether later's status lets it replace earlier (docs/memory-v2.md,
// "Status by explicit construction"): a record stated as fact may replace any record; a plan or
// a condition (proposed, hypothetical) only a record of its own status, a revised plan or
// condition, never a fact, so nothing not done ever stands for what is; a retraction only a plan
// or a condition, never a statement of fact, a preference or a correction.
func MaySupersede(later, earlier Observation) bool {
	switch later.Status {
	case "":
		return true
	case StatusRetracted:
		return earlier.Status == StatusProposed || earlier.Status == StatusHypothetical
	}
	return earlier.Status == later.Status
}
