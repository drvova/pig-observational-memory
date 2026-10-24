package ledger

import (
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
)

// Subject aliases (docs/memory-interface.md, sections 2 and 3). Word containment cannot group
// synonyms: "half marathon training" and "running" share no word. The reflector, which sees
// every subject, supplies that meaning as an alias record (om.subjects.aliased): "from names
// the same topic as to". The kernel applies it the same way for every model and never rewrites
// a stored record: CanonicalSubjectsWithAliases applies the aliases in force
// (CheckSubjectAliases) after path roots and containment.

// MaxSubjectAliasesPerEntry bounds the aliases one om.subjects.aliased entry carries, and so
// one reflector run's alias_subjects calls: a payload bound, not a tuned number. An entry read
// with more keeps its first MaxSubjectAliasesPerEntry.
const MaxSubjectAliasesPerEntry = 16

// SubjectAlias declares that notes under From are about the same topic as notes under To.
type SubjectAlias struct {
	From string `json:"from"`
	To   string `json:"to"`
	// Reason is the reflector's short justification, kept for the reader; it changes nothing.
	Reason string `json:"reason,omitempty"`
}

// SubjectsAliasedData is an om.subjects.aliased payload. It has no coversUpToId and never
// moves a coverage marker: an alias is a claim about subjects already recorded, not about
// source entries. ObservationsThroughID says which recorded subjects: the observations'
// coverage when the aliases were checked, so the alias folds with the observations it groups
// (foldProjection), wherever the entry itself sits.
type SubjectsAliasedData struct {
	Aliases []SubjectAlias `json:"aliases"`
	// ObservationsThroughID is the observation coverage marker's id (LatestCoverageMarkerID of
	// om.observations.recorded) on the branch the aliases were checked against; "" in an entry
	// written without one, which folds by its own position.
	ObservationsThroughID string `json:"observationsThroughId,omitempty"`
}

// subjectAliasFrom reads one decoded alias: from and to are normalized subjects
// (NormalizeSubject), the reason has its whitespace folded.
func subjectAliasFrom(v any) (SubjectAlias, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return SubjectAlias{}, false
	}
	from, _ := m["from"].(string)
	to, _ := m["to"].(string)
	reason, _ := m["reason"].(string)
	alias := SubjectAlias{From: NormalizeSubject(from), To: NormalizeSubject(to), Reason: strings.Join(strings.Fields(reason), " ")}
	if alias.From == "" || alias.To == "" {
		return SubjectAlias{}, false
	}
	return alias, true
}

// subjectAliasList reads a decoded alias list leniently: malformed items are skipped and at
// most MaxSubjectAliasesPerEntry are kept. nil when none is valid.
func subjectAliasList(v any) []SubjectAlias {
	list, _ := v.([]any)
	var out []SubjectAlias
	for _, item := range list {
		if len(out) == MaxSubjectAliasesPerEntry {
			break
		}
		if alias, ok := subjectAliasFrom(item); ok {
			out = append(out, alias)
		}
	}
	return out
}

// SubjectsAliasedDataFrom validates a decoded om.subjects.aliased payload: it needs at least
// one well-formed alias. Whether an alias applies is decided when it is read
// (CheckSubjectAliases), against the subjects present then.
func SubjectsAliasedDataFrom(v any) (SubjectsAliasedData, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return SubjectsAliasedData{}, false
	}
	aliases := subjectAliasList(m["aliases"])
	if len(aliases) == 0 {
		return SubjectsAliasedData{}, false
	}
	through, _ := m["observationsThroughId"].(string)
	return SubjectsAliasedData{Aliases: aliases, ObservationsThroughID: through}, true
}

// SubjectsAliasedEntry reads a valid om.subjects.aliased entry.
func SubjectsAliasedEntry(entry Entry) (SubjectsAliasedData, bool) {
	if entry.Type != "custom" || entry.CustomType != OMSubjectsAliased {
		return SubjectsAliasedData{}, false
	}
	return SubjectsAliasedDataFrom(entry.Data)
}

// BuildSubjectsAliasedData builds a payload of at most MaxSubjectAliasesPerEntry aliases about
// the observations covered through observationsThroughID, or nil when it would be empty.
func BuildSubjectsAliasedData(aliases []SubjectAlias, observationsThroughID string) *SubjectsAliasedData {
	if len(aliases) == 0 {
		return nil
	}
	if len(aliases) > MaxSubjectAliasesPerEntry {
		aliases = aliases[:MaxSubjectAliasesPerEntry]
	}
	return &SubjectsAliasedData{Aliases: append([]SubjectAlias(nil), aliases...), ObservationsThroughID: observationsThroughID}
}

// The reasons CheckSubjectAliases refuses an alias, as the reflector's receipt states them.
const (
	AliasRefusedEmpty     = "from or to has no letters or digits"
	AliasRefusedPath      = "from and to must be topics, not subject paths"
	AliasRefusedSelf      = "from and to are the same subject, and from is not aliased"
	AliasRefusedKernel    = "names a field of the notes source"
	AliasRefusedAbsent    = "from or to is not a subject present in the memory"
	AliasRefusedRepeat    = "the alias is already recorded"
	AliasRefusedContained = "to already contains every word of from, so from's notes would read as about to; containment already groups to under from"
	AliasRefusedCycle     = "would close a cycle of aliases"
)

// AliasRefusal is an alias CheckSubjectAliases refused, with why.
type AliasRefusal struct {
	Alias  SubjectAlias
	Reason string
}

// IsRetraction reports whether a is a retraction: from and to are one subject (by
// SubjectKey), which undoes the alias in force for from, if any.
func (a SubjectAlias) IsRetraction() bool {
	k := SubjectKey(a.From)
	return k != "" && k == SubjectKey(a.To)
}

// aliasGraph is the aliases in force over a set of subjects, applied in ledger order.
type aliasGraph struct {
	set baseSet
	// edges maps a from key to its to key.
	edges map[string]string
	// inForce are the aliases behind edges, in the ledger order they were accepted in.
	inForce []SubjectAlias
}

// newAliasGraph is an empty aliasGraph over set.
func newAliasGraph(set baseSet) *aliasGraph {
	return &aliasGraph{set: set, edges: map[string]string{}}
}

// resolve is where k ends: its alias's target, or its containment subject when it has no
// alias, followed until a key with neither. The kernel never accepts an alias that would make
// this loop (refusal), so it ends; the bound only guards it.
func (g *aliasGraph) resolve(k string) string {
	for steps := 0; steps <= 2*len(g.edges)+1; steps++ {
		if next, ok := g.edges[k]; ok {
			k = next
			continue
		}
		if p := g.set.parent(k); p != k {
			k = p
			continue
		}
		break
	}
	return k
}

// refusal is why a is refused given the aliases in force, or "".
func (g *aliasGraph) refusal(a SubjectAlias) string {
	from, to := SubjectKey(a.From), SubjectKey(a.To)
	switch {
	case from == "" || to == "":
		return AliasRefusedEmpty
	case strings.Contains(a.From, SubjectPathSeparator) || strings.Contains(a.To, SubjectPathSeparator):
		return AliasRefusedPath
	}
	// Only the notes source's own subject fields are refused: any other name is a plain
	// subject the observer may write, so it may be aliased too.
	for _, side := range []string{a.From, a.To} {
		if owner, owned := kernel.OwnsForEveryKind(side); owned && owner == kernel.OwnerSubjects {
			return AliasRefusedKernel
		}
	}
	if !g.set.present(from) || !g.set.present(to) {
		return AliasRefusedAbsent
	}
	if from == to {
		if _, aliased := g.edges[from]; !aliased {
			return AliasRefusedSelf
		}
		return ""
	}
	if g.edges[from] == to {
		return AliasRefusedRepeat
	}
	if containsSubjectWords(a.To, a.From) {
		return AliasRefusedContained
	}
	// Following to through the aliases and containment must not come back to from.
	for k, steps := to, 0; steps <= 2*len(g.edges)+1; steps++ {
		if k == from {
			return AliasRefusedCycle
		}
		if next, ok := g.edges[k]; ok {
			k = next
			continue
		}
		p := g.set.parent(k)
		if p == k {
			break
		}
		k = p
	}
	return ""
}

// apply checks a against the aliases in force and, when it is accepted, puts it in force: it
// replaces the alias in force for its from, if any, or, as a retraction (IsRetraction), removes
// it. It returns why a was refused, or "", and the alias it replaced or retracted (ok false
// when none).
func (g *aliasGraph) apply(a SubjectAlias) (reason string, replaced SubjectAlias, ok bool) {
	if reason := g.refusal(a); reason != "" {
		return reason, SubjectAlias{}, false
	}
	from := SubjectKey(a.From)
	if _, had := g.edges[from]; had {
		for i, in := range g.inForce {
			if SubjectKey(in.From) == from {
				replaced, ok = in, true
				g.inForce = append(g.inForce[:i:i], g.inForce[i+1:]...)
				break
			}
		}
		delete(g.edges, from)
	}
	if !a.IsRetraction() {
		g.edges[from] = SubjectKey(a.To)
		g.inForce = append(g.inForce, a)
	}
	return "", replaced, ok
}

// containsSubjectWords reports whether every word of part is a word of whole, or a plural of
// one (singularCandidates): "running shoes" contains "running" and "shoe".
func containsSubjectWords(whole, part string) bool {
	words := map[string]bool{}
	for _, w := range subjectWords(whole) {
		words[w] = true
		for _, c := range singularCandidates(w) {
			words[c] = true
		}
	}
	for _, w := range subjectWords(part) {
		if !words[w] {
			return false
		}
	}
	return true
}

// CheckSubjectAliases is the kernel's check of aliases over the subjects present, applied when
// an alias is recorded and again whenever it is read; it returns the aliases in force, in the
// ledger order they were accepted in, and the refused ones. aliases are in ledger order,
// earliest first, and each is checked against those in force before it. An alias is refused
// when from or to has no words, is a subject path, names one of the notes source's subject
// fields (SubjectCanonicalizationNames) or is not the base of a subject present (by SubjectKey;
// a path subject is present by its root); when it repeats the alias in force for from; when
// to already contains every word of from (containment relates them the other way); or when it
// would close a cycle through the aliases in force and containment. An accepted alias replaces
// the one in force for its from: the latest alias that applies wins, so a wrong alias is
// corrected by a later one, and an earlier alias that stopped applying never comes back over
// a later one. An alias from a subject to itself is a retraction: it removes the alias in
// force for from, and is refused when there is none. The aliases in force form a forest, and
// the result depends on the subjects only as a set and on the aliases only through the ledger
// order of those with one from.
func CheckSubjectAliases(subjects []string, aliases []SubjectAlias) (inForce []SubjectAlias, refused []AliasRefusal) {
	g := newAliasGraph(readBases(subjects))
	for _, a := range aliases {
		if reason, _, _ := g.apply(a); reason != "" {
			refused = append(refused, AliasRefusal{Alias: a, Reason: reason})
		}
	}
	return g.inForce, refused
}

// AcceptNewSubjectAliases is the record-time check: the proposed aliases the kernel accepts
// after the aliases already recorded (earliest first, each checked as CheckSubjectAliases
// checks it), over the subjects present, with the refused proposals. A proposal that repeats
// the alias in force for its from is refused, so nothing is recorded twice; one a later
// proposal replaces or retracts is left out of accepted, since recording the later one alone
// leaves the same aliases in force.
func AcceptNewSubjectAliases(subjects []string, recorded, proposed []SubjectAlias) (accepted []SubjectAlias, refused []AliasRefusal) {
	g := newAliasGraph(readBases(subjects))
	for _, a := range recorded {
		g.apply(a)
	}
	for _, a := range proposed {
		reason, replaced, ok := g.apply(a)
		if reason != "" {
			refused = append(refused, AliasRefusal{Alias: a, Reason: reason})
			continue
		}
		if ok {
			for i, prev := range accepted {
				if prev == replaced {
					accepted = append(accepted[:i:i], accepted[i+1:]...)
					break
				}
			}
		}
		accepted = append(accepted, a)
	}
	return accepted, refused
}

// SubjectsOf lists the subjects of observations, as CanonicalSubjects and CheckSubjectAliases
// read them.
func SubjectsOf(observations []Observation) []string {
	subjects := make([]string, 0, len(observations))
	for _, o := range observations {
		subjects = append(subjects, o.Subject)
	}
	return subjects
}
