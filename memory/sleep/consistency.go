package sleep

import (
	"sort"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/ledger"
)

// The joint-consistency check's bounds (docs/memory-v2.md, "Sleep"; the quantum report's change
// 8): the largest cluster it reads, the steps it may spend on one cluster, and the most
// contradictions one pass writes. A cluster over either bound is counted in SleepData.Unchecked,
// never guessed at.
const (
	MaxClusterRecords = 16
	MaxCheckSteps     = 4096
	MaxContradictions = 16
)

// Preference is one substitution a record asserts: Over is to be used rather than Under.
type Preference struct{ Over, Under string }

// span is one backtick-quoted value of a record's text: its bytes between the backticks, and
// where the opening and closing backticks are.
type span struct {
	value      string
	open, shut int
}

// spans are text's backtick-quoted values, in order: each from a backtick to the next on the
// same line, the value trimmed, an empty one skipped.
func spans(text string) []span {
	var out []span
	for i := 0; i < len(text); i++ {
		if text[i] != '`' {
			continue
		}
		j := strings.IndexAny(text[i+1:], "`\n")
		if j < 0 {
			break
		}
		j += i + 1
		if text[j] == '\n' {
			i = j
			continue
		}
		if v := strings.TrimSpace(text[i+1 : j]); v != "" {
			out = append(out, span{value: v, open: i, shut: j})
		}
		i = j
	}
	return out
}

// words are text's ASCII words lowered: runs of letters and apostrophes, every other byte a
// separator.
func words(text string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c >= 'A' && c <= 'Z':
			b.WriteByte(c + 'a' - 'A')
		case c >= 'a' && c <= 'z', c == '\'':
			b.WriteByte(c)
		default:
			flush()
		}
	}
	flush()
	return out
}

// forwardGaps are the words between two values that put the first over the second ("use `a`,
// not `b`", "`a` instead of `b`", "prefer `a` over `b`").
var forwardGaps = map[string]bool{"not": true, "instead of": true, "rather than": true, "over": true, "but not": true, "and not": true}

// negations open a clause that turns the first value down; with "use" or "instead" between
// the values, the second is put over the first ("Never run `a` here; always use `b` instead").
var negations = map[string]bool{"never": true, "don't": true, "dont": true, "not": true, "avoid": true, "stop": true}

// Substitutions are the preferences text asserts, read from each pair of adjacent backtick-quoted
// values (contracts/sleep.jsonl; lisp/src/sleep.lisp, sleep-substitutions): the words between
// them, or a negation opening the first value's clause. A pair of one value twice asserts none.
func Substitutions(text string) []Preference {
	ss := spans(text)
	var out []Preference
	add := func(p Preference) {
		if p.Over == p.Under {
			return
		}
		for _, q := range out {
			if q == p {
				return
			}
		}
		out = append(out, p)
	}
	for i := 0; i+1 < len(ss); i++ {
		a, b := ss[i], ss[i+1]
		gap := words(text[a.shut+1 : b.open])
		if forwardGaps[strings.Join(gap, " ")] {
			add(Preference{Over: a.value, Under: b.value})
			continue
		}
		start := strings.LastIndexAny(text[:a.open], ".;!?\n") + 1
		negated := false
		for _, w := range words(text[start:a.open]) {
			negated = negated || negations[w]
		}
		turns := false
		for _, w := range gap {
			turns = turns || w == "use" || w == "instead"
		}
		if negated && turns {
			add(Preference{Over: b.value, Under: a.value})
		}
	}
	return out
}

// substitutable is a record whose text can state a rule of use: not a tool's output, a host
// summary or a forget.
func substitutable(o ledger.Observation) bool {
	switch o.Kind {
	case ledger.KindAttempt, ledger.KindEvidence, ledger.KindValue, ledger.KindFile, ledger.KindCommit, ledger.KindForget, ledger.KindPushback:
		return false
	}
	return o.Origin != ledger.OriginHost
}

// consistencyScope is the scope a record's claims hold in: its scope and its repository. Records
// of different scopes or repositories never contradict each other (decision 8).
func consistencyScope(o ledger.Observation) string {
	return o.Scope + "\x00" + o.Frame.RepoID
}

// contradictions is the joint-consistency check (the quantum report's change 8): the believed
// records the pass does not hide, grouped in clusters that share a value or a key within one
// scope; in each cluster the preferences its records assert must order the values without a
// cycle (a strict order exists exactly when the graph is acyclic), so a cycle, odd ones of three
// or more included, is a contradiction no pair shows. Each cluster gives at most its shortest
// cycle, the records on it a minimal conflicting set; nothing is resolved. A cluster over
// MaxClusterRecords or MaxCheckSteps is counted unchecked.
func contradictions(records []ledger.Observation, labels map[string]string, hidden map[string]bool, st ledger.SleepState) (out []ledger.SleepContradiction, unchecked int) {
	type member struct {
		o     ledger.Observation
		prefs []Preference
		at    int
	}
	var members []member
	for i, o := range records {
		if labels[o.ID] != ledger.LabelBelieved || hidden[o.ID] || !substitutable(o) {
			continue
		}
		if prefs := Substitutions(o.Content); len(prefs) > 0 {
			members = append(members, member{o: o, prefs: prefs, at: i})
		}
	}
	// Union-find over the members: one cluster per scope and shared value or key.
	parent := make([]int, len(members))
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	first := map[string]int{}
	for i, m := range members {
		scope := consistencyScope(m.o)
		var keys []string
		for _, p := range m.prefs {
			keys = append(keys, "value:"+p.Over, "value:"+p.Under)
		}
		keys = append(keys, keyValues(m.o.Keys)...)
		for _, k := range keys {
			k = scope + "\x00" + k
			if j, ok := first[k]; ok {
				if a, b := find(i), find(j); a != b {
					if a < b {
						parent[b] = a
					} else {
						parent[a] = b
					}
				}
			} else {
				first[k] = i
			}
		}
	}
	clusters := map[int][]member{}
	var roots []int
	for i, m := range members {
		r := find(i)
		if _, ok := clusters[r]; !ok {
			roots = append(roots, r)
		}
		clusters[r] = append(clusters[r], m)
	}
	for _, r := range roots {
		cluster := clusters[r]
		if len(cluster) > MaxClusterRecords {
			unchecked++
			continue
		}
		// The graph: an edge from each preferred value to the one it is preferred over, kept with
		// the first record (in ledger order) that asserts it.
		edge := map[[2]string]string{}
		next := map[string][]string{}
		nodes := map[string]bool{}
		for _, m := range cluster {
			for _, p := range m.prefs {
				nodes[p.Over], nodes[p.Under] = true, true
				e := [2]string{p.Over, p.Under}
				if _, ok := edge[e]; !ok {
					edge[e] = m.o.ID
					next[p.Over] = append(next[p.Over], p.Under)
				}
			}
		}
		sorted := make([]string, 0, len(nodes))
		for n := range nodes {
			sorted = append(sorted, n)
		}
		sort.Strings(sorted)
		for _, n := range sorted {
			sort.Strings(next[n])
		}
		cycle, steps, ok := shortestCycle(sorted, next)
		if !ok {
			unchecked++
			continue
		}
		if len(cycle) == 0 {
			continue
		}
		ids := map[string]bool{}
		for i, v := range cycle {
			ids[edge[[2]string{v, cycle[(i+1)%len(cycle)]}]] = true
		}
		c := ledger.SleepContradiction{Cycle: cycle, Steps: steps}
		for _, m := range cluster {
			if ids[m.o.ID] {
				c.IDs = append(c.IDs, m.o.ID)
			}
		}
		c.Scope = cluster[0].o.Scope
		if st.Contradictions[ledger.ContradictionKey(c)] || len(out) >= MaxContradictions {
			continue
		}
		out = append(out, c)
	}
	return out, unchecked
}

// shortestCycle is the shortest directed cycle of the graph (nodes sorted, each node's next
// values sorted): a breadth-first search from each node in order, the first shortest found kept,
// starting at its smallest node's search. steps counts the edges examined; ok is false when they
// pass MaxCheckSteps. No cycle is (nil, steps, true).
func shortestCycle(nodes []string, next map[string][]string) (cycle []string, steps int, ok bool) {
	for _, s := range nodes {
		parent := map[string]string{s: ""}
		dist := map[string]int{s: 0}
		queue := []string{s}
		found := false
		for len(queue) > 0 && !found {
			u := queue[0]
			queue = queue[1:]
			if cycle != nil && dist[u]+1 >= len(cycle) {
				break
			}
			for _, v := range next[u] {
				steps++
				if steps > MaxCheckSteps {
					return nil, steps, false
				}
				if v == s {
					var path []string
					for n := u; n != ""; n = parent[n] {
						path = append([]string{n}, path...)
					}
					cycle, found = path, true
					break
				}
				if _, seen := dist[v]; !seen {
					dist[v], parent[v] = dist[u]+1, u
					queue = append(queue, v)
				}
			}
		}
	}
	return cycle, steps, true
}
