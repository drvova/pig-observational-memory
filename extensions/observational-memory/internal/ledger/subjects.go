package ledger

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
)

// Subject canonicalization (docs/memory-interface.md, sections 2, 3 and 5). The observer names
// a note's subject freely, and one model writes "running", "running gear" and "running shoes"
// for notes about one topic. Every view that groups or queries notes by subject reads the
// canonical subject instead, derived here from the subjects present; the stored records are
// never rewritten, so what the observer wrote stays readable (the notes source's raw_subject).
//
// Two rules let the model supply meaning that words cannot: a subject written as a path
// ("running/half marathon") groups under its root, and an alias the reflector recorded
// (om.subjects.aliased, aliases.go) groups one subject, and what nests under it, under another
// (CanonicalSubjectsWithAliases). Between them is containment, not similarity: a subject whose
// words contain every word of a shorter subject present belongs to it, and the words it adds
// become scope. Nothing is
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
	// Subject is the canonical subject: the note's subject resolved through its path root, the
	// shortest present subject it contains, and any alias of either
	// (CanonicalSubjectsWithAliases); the note's subject itself when none applies.
	Subject string
	// ExtraScope is the words the note's subject adds to Subject, lowercased, in their
	// original order: an aliased subject's words, then the containment words, then the path
	// segments after the root ("" when it adds none).
	ExtraScope string
}

// SubjectPathSeparator separates the segments of a subject written as a path, root topic first
// ("running/half marathon").
const SubjectPathSeparator = "/"

// MaxSubjectPathDepth is the most segments a subject path may have: a root topic and two
// qualifiers ("running/half marathon/pace"). Deeper nesting is what scope is for.
const MaxSubjectPathDepth = 3

// SubjectPath reads subject as a path: its segments, root first, each normalized
// (NormalizeSubjectPath), empty ones dropped. A subject without a separator is one segment. ok
// is false when the kernel refuses the path: more than MaxSubjectPathDepth segments, or two or
// more leading segments that together spell a kernel-owned name (kernel.OwnsForEveryKind
// ignores the separator, so "om/folded" spells om.folded and "raw/subject" raw_subject). A
// root alone never refuses a path: the plain subject "search" is accepted, so "search/indexing"
// is too. A refused path reads as one plain subject, its segments its words.
func SubjectPath(subject string) (segments []string, ok bool) {
	normalized := NormalizeSubjectPath(subject)
	if normalized == "" {
		return nil, true
	}
	segments = strings.Split(normalized, SubjectPathSeparator)
	if len(segments) == 1 {
		return segments, true
	}
	if len(segments) > MaxSubjectPathDepth {
		return segments, false
	}
	for i := 2; i <= len(segments); i++ {
		if _, owned := kernel.OwnsForEveryKind(strings.Join(segments[:i], SubjectPathSeparator)); owned {
			return segments, false
		}
	}
	return segments, true
}

// RecordableSubject is the subject the observer's record stores for a written subject: the
// normalized subject (NormalizeSubjectPath), or, when the kernel refuses it as a path
// (SubjectPath), its segments joined by spaces, with refused true so the receipt can say so.
// Either way the words written are kept.
func RecordableSubject(subject string) (stored string, refused bool) {
	normalized := NormalizeSubjectPath(subject)
	if segments, ok := SubjectPath(normalized); !ok {
		return strings.Join(segments, " "), true
	}
	return normalized, false
}

// pathBase is a subject's base for canonicalization: the root of a subject path the kernel
// accepts, with the words of the rest, else the subject itself.
type pathBase struct {
	root, rest string
}

// subjectBase is subject's pathBase.
func subjectBase(subject string) pathBase {
	segments, ok := SubjectPath(subject)
	if !ok || len(segments) < 2 {
		return pathBase{root: subject}
	}
	return pathBase{root: segments[0], rest: SubjectKey(strings.Join(segments[1:], " "))}
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

// CanonicalSubjects is CanonicalSubjectsWithAliases with no aliases: subject paths, then
// containment.
func CanonicalSubjects(subjects []string) map[string]Canonical {
	return CanonicalSubjectsWithAliases(subjects, nil)
}

// CanonicalSubjectsWithAliases maps each subject present (empty ones aside) to its Canonical
// over the whole set, resolving in a fixed order:
//
//  1. Base: a subject path the kernel accepts (SubjectPath) reads as its root, the remaining
//     segments' words becoming scope; any other subject is its own base.
//  2. Containment: every base, aliased or not, is grouped by containmentSubjects.
//  3. Alias: a base an accepted alias maps (CheckSubjectAliases over subjects), or whose
//     containment subject one maps, reads as the alias's final target: the aliases are
//     followed transitively, and a target that nests under a shorter subject goes on to it
//     (and on through that subject's alias, if it has one). The words of the aliased subject
//     that the canonical subject lacks become scope, so nothing written is lost; a base that
//     moved with its containment subject keeps its containment words after them, so the
//     subjects grouped under an aliased subject follow it to its target.
//
// ExtraScope joins the alias's words, the containment words and the path words, in that order.
// With no path and no alias the result is containmentSubjects' exactly. The result depends on
// the subjects only as a set and on the aliases only through CheckSubjectAliases, and
// canonicalizing the canonical subjects again (with the same aliases) maps each to itself: a
// canonical subject is never a path, and no alias's from is canonical.
func CanonicalSubjectsWithAliases(subjects []string, aliases []SubjectAlias) map[string]Canonical {
	set := readBases(subjects)
	g := newAliasGraph(set)
	for _, a := range aliases {
		g.apply(a)
	}
	out := make(map[string]Canonical, len(set.bases))
	for s, b := range set.bases {
		k := SubjectKey(b.root)
		c := set.contained[b.root]
		aliased, carried := "", ""
		if _, ok := g.edges[k]; ok {
			aliased = b.root
		} else if p := set.parent(k); p != k {
			if _, ok := g.edges[p]; ok {
				aliased, carried = c.Subject, c.ExtraScope
			}
		}
		if aliased == "" {
			out[s] = Canonical{Subject: c.Subject, ExtraScope: joinWords(c.ExtraScope, b.rest)}
			continue
		}
		target := set.contained[set.spelling[g.resolve(k)]].Subject
		have := map[string]bool{}
		for _, w := range subjectWords(target) {
			have[w] = true
		}
		kept := []string{}
		for _, w := range subjectWords(aliased) {
			if !have[w] {
				kept = append(kept, w)
			}
		}
		out[s] = Canonical{Subject: target, ExtraScope: joinWords(strings.Join(kept, " "), carried, b.rest)}
	}
	return out
}

// baseSet is a set of subjects read for canonicalization: each subject's base, the spelling of
// each base key, and every base root's containment Canonical over all the roots.
type baseSet struct {
	// bases maps each non-empty subject to its pathBase.
	bases map[string]pathBase
	// spelling maps each base key (SubjectKey of a root) to the smallest root with it.
	spelling map[string]string
	// contained is containmentSubjects over every root, aliased ones included.
	contained map[string]Canonical
}

// readBases reads subjects' bases (empty subjects aside).
func readBases(subjects []string) baseSet {
	set := baseSet{bases: map[string]pathBase{}, spelling: map[string]string{}}
	roots := []string{}
	for _, s := range subjects {
		if s == "" {
			continue
		}
		if _, seen := set.bases[s]; seen {
			continue
		}
		b := subjectBase(s)
		set.bases[s] = b
		roots = append(roots, b.root)
		k := SubjectKey(b.root)
		if current, ok := set.spelling[k]; k != "" && (!ok || b.root < current) {
			set.spelling[k] = b.root
		}
	}
	set.contained = containmentSubjects(roots)
	return set
}

// present reports whether k is the key of a base in the set.
func (set baseSet) present(k string) bool {
	_, ok := set.spelling[k]
	return ok
}

// parent is the key of the containment subject of the base keyed k: k itself when that base
// nests under no shorter subject, or when k is no base key. A parent is its own parent.
func (set baseSet) parent(k string) string {
	root, ok := set.spelling[k]
	if !ok {
		return k
	}
	if p := SubjectKey(set.contained[root].Subject); p != "" {
		return p
	}
	return k
}

// joinWords joins the non-empty parts with one space.
func joinWords(parts ...string) string {
	kept := parts[:0:0]
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, " ")
}

// containmentSubjects maps each subject present (empty ones aside) to its Canonical over the
// whole set by word containment. Each subject is read as the set of its words (subjectWords), a word that is a
// plural of another word present (singularCandidates) read as that word, so "shoes" matches
// "shoe" only when some subject says "shoe". Subjects with one word set are one subject,
// whatever their order, repetition, case or punctuation ("marathon training" and "training
// marathon"), spelled as the lexicographically smallest of them, with no extra scope. A
// subject whose word set contains all the words of a smaller present set groups under the
// smallest such set, fewest words first, then the lexicographically smallest key, and the
// words it adds become ExtraScope. A subject that contains no smaller set stays itself. The
// result depends only on the set of subjects, and containmentSubjects of the canonical
// subjects maps each to itself.
func containmentSubjects(subjects []string) map[string]Canonical {
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
	return ObservationSubjectsWithAliases(observations, nil)
}

// ObservationSubjectsWithAliases is CanonicalSubjectsWithAliases over the subjects of
// observations.
func ObservationSubjectsWithAliases(observations []Observation, aliases []SubjectAlias) map[string]Canonical {
	return CanonicalSubjectsWithAliases(SubjectsOf(observations), aliases)
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
	return CountCanonicalSubjectsWithAliases(observations, nil)
}

// CountCanonicalSubjectsWithAliases is CountCanonicalSubjects with the subjects canonical
// under aliases (CanonicalSubjectsWithAliases).
func CountCanonicalSubjectsWithAliases(observations []Observation, aliases []SubjectAlias) []SubjectCount {
	canonical := ObservationSubjectsWithAliases(observations, aliases)
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
