package ledger

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Subject canonicalization (docs/memory-interface.md, sections 2, 3 and 5). The observer names
// a note's subject freely, and one model writes "running", "running gear" and "running shoes"
// for notes about one topic. Every view that groups or queries notes by subject reads the
// canonical subject instead, derived here from the subjects present; the stored records are
// never rewritten, so what the observer wrote stays readable (the notes source's raw_subject).
//
// The rule is containment, not similarity: a subject whose words contain every word of a
// shorter subject present belongs to it, and the words it adds become scope. Nothing is
// scored, so there is no threshold to tune, the result does not depend on the order the
// subjects arrive in, and canonicalizing the canonical subjects again changes nothing. It is
// kernel-owned (kernel.Owns names it), so no definition in the model-built layer can replace
// it: every model and every layer groups the same way.

// The names under which the kernel owns subject canonicalization: no definition may take
// them, and no note schema may add a field named like one of the notes source's subject
// fields.
const (
	// SubjectKeyName names SubjectKey.
	SubjectKeyName = "subject-key"
	// CanonicalSubjectsName names CanonicalSubjects.
	CanonicalSubjectsName = "canonical-subjects"
	// RawSubjectField is the notes source's field holding the subject the observer wrote,
	// beside the canonical subject.
	RawSubjectField = "raw_subject"
	// SubjectScopeField is the notes source's field holding the words the written subject
	// adds to the canonical subject (Canonical.ExtraScope), beside the stored scope.
	SubjectScopeField = "subject_scope"
)

// SubjectCanonicalizationNames are the names the kernel owns for subject canonicalization.
var SubjectCanonicalizationNames = []string{SubjectKeyName, CanonicalSubjectsName, RawSubjectField, SubjectScopeField}

// Canonical is the subject a note groups under and the qualifier its own subject adds.
type Canonical struct {
	// Subject is the canonical subject: the shortest present subject the note's subject
	// contains, or the note's subject itself.
	Subject string
	// ExtraScope is the words the note's subject adds to Subject, lowercased, in their
	// original order ("" when it adds none).
	ExtraScope string
}

// inWordSymbol reports whether r stays inside a word: an apostrophe ("user's", "o'brien") or
// one of + # & ("c++", "c#", "r&d"), so a symbol never splits a word into unrelated ones.
func inWordSymbol(r rune) bool {
	return r == '\'' || r == '+' || r == '#' || r == '&'
}

// subjectWords splits a subject into its lowercase words. A word is a run of letters, digits
// and the in-word symbols (inWordSymbol); every other character separates words. A typographic
// apostrophe reads as ', apostrophes at a word's edges are trimmed, a possessive "'s" ending
// is dropped ("user's" is "user"), and a run without a letter or digit ("&", "+") is no word.
func subjectWords(subject string) []string {
	subject = strings.ReplaceAll(strings.ToLower(subject), "’", "'")
	fields := strings.FieldsFunc(subject, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r) && !inWordSymbol(r)
	})
	words := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, "'")
		f = strings.TrimSuffix(f, "'s")
		if strings.IndexFunc(f, func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) }) < 0 {
			continue
		}
		words = append(words, f)
	}
	return words
}

// singularCandidates are the words word may be the plural of, most specific first: "ies" to
// "y" ("stories"), "ses" to "sis" ("analyses"), "es" after s, x, z, ch or sh ("boxes",
// "statuses", "dishes"), else a final "s" not after "s" ("shoes"). Every candidate has at
// least three letters, and one made by dropping a bare "s" at least four, so "news" is never
// read as "new" nor "gas" as "ga". They are only candidates: CanonicalSubjects folds a word
// into one only when it is a word of some subject present.
func singularCandidates(word string) []string {
	var out []string
	add := func(c string) {
		if utf8.RuneCountInString(c) >= 3 && c != word {
			out = append(out, c)
		}
	}
	if stem, ok := strings.CutSuffix(word, "ies"); ok {
		add(stem + "y")
	}
	if stem, ok := strings.CutSuffix(word, "ses"); ok {
		add(stem + "sis")
	}
	if stem, ok := strings.CutSuffix(word, "es"); ok {
		for _, end := range []string{"s", "x", "z", "ch", "sh"} {
			if strings.HasSuffix(stem, end) {
				add(stem)
				break
			}
		}
	}
	if stem, ok := strings.CutSuffix(word, "s"); ok && !strings.HasSuffix(stem, "s") && utf8.RuneCountInString(stem) >= 4 {
		add(stem)
	}
	return out
}

// SubjectKey is a subject's spelling key: its words (subjectWords) joined by one space, so
// case, punctuation between words and a possessive "'s" do not matter ("Running-Shoes" and
// "running shoes"), while symbols inside a word do ("c", "c++" and "c#" differ). It is "" for
// a subject with no letters or digits. It does not depend on other subjects; folding a plural
// into a singular is CanonicalSubjects' work, against the words present. It is for matching
// only and is never stored or shown.
func SubjectKey(subject string) string {
	return strings.Join(subjectWords(subject), " ")
}

// subjectGroup is the subjects that share one word set, with that set.
type subjectGroup struct {
	// subject is the group's spelling: the lexicographically smallest subject in it.
	subject string
	// key is the group's sorted, distinct words joined by one space.
	key   string
	words map[string]bool
}

// CanonicalSubjects maps each subject present (empty ones aside) to its Canonical over the
// whole set. Each subject is read as the set of its words (subjectWords), a word that is a
// plural of another word present (singularCandidates) read as that word, so "shoes" matches
// "shoe" only when some subject says "shoe". Subjects with one word set are one subject,
// whatever their order, repetition, case or punctuation ("marathon training" and "training
// marathon"), spelled as the lexicographically smallest of them, with no extra scope. A
// subject whose word set contains all the words of a smaller present set groups under the
// smallest such set, fewest words first, then the lexicographically smallest key, and the
// words it adds become ExtraScope. A subject that contains no smaller set stays itself. The
// result depends only on the set of subjects, and CanonicalSubjects of the canonical subjects
// maps each to itself.
func CanonicalSubjects(subjects []string) map[string]Canonical {
	out := map[string]Canonical{}
	vocabulary := map[string]bool{}
	for _, s := range subjects {
		if s == "" {
			continue
		}
		if _, seen := out[s]; seen {
			continue
		}
		out[s] = Canonical{Subject: s}
		for _, w := range subjectWords(s) {
			vocabulary[w] = true
		}
	}
	// match is the form a word is compared in: the first of its singular candidates that is a
	// word present, else the word.
	match := func(w string) string {
		for _, c := range singularCandidates(w) {
			if vocabulary[c] {
				return c
			}
		}
		return w
	}
	wordSet := func(s string) (string, map[string]bool) {
		set := map[string]bool{}
		for _, w := range subjectWords(s) {
			set[match(w)] = true
		}
		words := make([]string, 0, len(set))
		for w := range set {
			words = append(words, w)
		}
		sort.Strings(words)
		return strings.Join(words, " "), set
	}
	byKey := map[string]*subjectGroup{}
	keyOf := map[string]string{}
	for s := range out {
		key, set := wordSet(s)
		if key == "" {
			continue
		}
		keyOf[s] = key
		if g := byKey[key]; g == nil {
			byKey[key] = &subjectGroup{subject: s, key: key, words: set}
		} else if s < g.subject {
			g.subject = s
		}
	}
	groups := make([]*subjectGroup, 0, len(byKey))
	for _, g := range byKey {
		groups = append(groups, g)
	}
	// Candidates in tie-break order: fewest words, then key.
	sort.Slice(groups, func(a, b int) bool {
		if len(groups[a].words) != len(groups[b].words) {
			return len(groups[a].words) < len(groups[b].words)
		}
		return groups[a].key < groups[b].key
	})
	target := map[string]*subjectGroup{}
	for _, g := range groups {
		target[g.key] = g
		for _, c := range groups {
			if len(c.words) >= len(g.words) {
				break
			}
			if containsWords(g.words, c.words) {
				target[g.key] = c
				break
			}
		}
	}
	for s, key := range keyOf {
		t := target[key]
		extra := []string{}
		if t.key != key {
			for _, w := range subjectWords(s) {
				if !t.words[match(w)] {
					extra = append(extra, w)
				}
			}
		}
		out[s] = Canonical{Subject: t.subject, ExtraScope: strings.Join(extra, " ")}
	}
	return out
}

// containsWords reports whether every word of part is in whole.
func containsWords(whole, part map[string]bool) bool {
	for w := range part {
		if !whole[w] {
			return false
		}
	}
	return true
}

// ObservationSubjects is CanonicalSubjects over the subjects of observations.
func ObservationSubjects(observations []Observation) map[string]Canonical {
	subjects := make([]string, 0, len(observations))
	for _, o := range observations {
		subjects = append(subjects, o.Subject)
	}
	return CanonicalSubjects(subjects)
}

// CanonicalScope is a note's scope with the qualifier its subject adds to its canonical
// subject: scope, then ExtraScope, joined by "; " when both are set.
func CanonicalScope(scope string, c Canonical) string {
	switch {
	case c.ExtraScope == "":
		return scope
	case scope == "":
		return c.ExtraScope
	}
	return scope + "; " + c.ExtraScope
}

// SubjectCount is a canonical subject with the number of notes grouped under it.
type SubjectCount struct {
	Subject string
	Count   int
}

// Line renders "subject: count", the form the observer's prompt lists.
func (s SubjectCount) Line() string {
	return s.Subject + ": " + strconv.Itoa(s.Count)
}

// CountCanonicalSubjects counts observations per canonical subject, most used first, then by
// subject; observations without a subject are not counted.
func CountCanonicalSubjects(observations []Observation) []SubjectCount {
	canonical := ObservationSubjects(observations)
	counts := map[string]int{}
	for _, o := range observations {
		if c, ok := canonical[o.Subject]; ok {
			counts[c.Subject]++
		}
	}
	out := make([]SubjectCount, 0, len(counts))
	for s, n := range counts {
		out = append(out, SubjectCount{Subject: s, Count: n})
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Count != out[b].Count {
			return out[a].Count > out[b].Count
		}
		return out[a].Subject < out[b].Subject
	})
	return out
}
