package codestore

import (
	"fmt"
	"slices"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/codepolicy"
)

// Kind is what a stored unit defines, read from its source.
type Kind = codepolicy.UnitKind

// Loaded is the store as one load read and checked it.
type Loaded struct {
	// Units are the units that passed the load-time check, sorted by name.
	Units []Loadable
	// Refused are the stored units that failed it, sorted by name, each with the rule it
	// broke: never sent to the child, kept in the store until forgotten.
	Refused []Refused
	// Components are Units grouped into strongly connected components in dependency order
	// (docs/code-tool.md, "Reload"): each component's dependencies come before it.
	Components []Component

	// raw are the refused units' sources, so kept(name) can show one before it is forgotten.
	raw map[string]string
}

// Loadable is a stored unit that passed the load-time check.
type Loadable struct {
	Unit
	Kind Kind
	// Deps are the names of the other loadable units it depends on: the stored units its free
	// identifiers name, its receiver type for a method, and every method of a type it names
	// (a caller of b.Twice needs Box.Twice defined, though it names only Box).
	Deps []string
}

// Component is a strongly connected set of loadable units, the smallest unit of isolation at
// replay: a forward reference, a method stored before its type and mutual recursion cannot load
// one unit at a time.
type Component struct {
	// Units are the component's units, sorted by name.
	Units []Unit
	// Deps are the indexes of the earlier components this one depends on, ascending.
	Deps []int
}

// Digests are the loadable units' digests by name: what a namespace started from this load
// holds, the seen map Keep and Page compare against.
func (l Loaded) Digests() map[string]string {
	out := make(map[string]string, len(l.Units))
	for _, u := range l.Units {
		out[u.Name] = u.Digest
	}
	return out
}

// Load reads the store and runs the load-time check on every unit (docs/code-tool.md,
// "Load-time check"): it must parse as exactly one function, method, type or constant spec
// bound to the name it is stored under and pass the whole code policy (codepolicy.CheckUnit,
// which holds the unit bound), match its digest, be stored once, fit the store bound
// (MaxUnits, in name order), and refer to no name outside the loadable units, the seeded and
// owned names and the allow-list. A unit that depends on a refused one is refused too ("depends
// on"). Provenance is not re-checked: it needs a branch. An unreadable store is an error.
func (s Store) Load() (Loaded, error) {
	stored, err := s.read()
	if err != nil {
		return Loaded{}, err
	}
	return check(stored), nil
}

// check runs the load-time check over stored units.
func check(stored []Unit) Loaded {
	var out Loaded
	sorted := slices.Clone(stored)
	slices.SortStableFunc(sorted, func(a, b Unit) int { return strings.Compare(a.Name, b.Name) })
	reason := map[string]string{}
	var names []string
	for i, u := range sorted {
		if i > 0 && sorted[i-1].Name == u.Name {
			// A later entry under the same name: only the first is judged.
			continue
		}
		names = append(names, u.Name)
		switch {
		case u.Name == "":
			reason[u.Name] = "the unit has no name"
		case i+1 < len(sorted) && sorted[i+1].Name == u.Name:
			reason[u.Name] = "the unit is stored more than once: forget it and keep it again"
		case u.Digest != Digest(u.Src):
			reason[u.Name] = "its digest does not match its source: the store was changed outside the memory tool"
		default:
			if err := codepolicy.CheckUnit(u.Name, u.Src); err != nil {
				reason[u.Name] = err.Error()
			} else if len(names) > MaxUnits {
				reason[u.Name] = fmt.Sprintf("the store holds more than %d units, the most kept() can list: forget one to load it", MaxUnits)
			}
		}
	}
	byName := map[string]Unit{}
	for _, u := range sorted {
		if _, ok := byName[u.Name]; !ok {
			byName[u.Name] = u
		}
	}
	free := map[string][]string{}
	for _, n := range names {
		if reason[n] != "" {
			continue
		}
		free[n] = codepolicy.FreeNames(byName[n].Src)
		for _, f := range free[n] {
			if _, ok := byName[f]; !ok && !codepolicy.Seeded(f) {
				reason[n] = fmt.Sprintf("refers to %s, which no kept unit, seeded name or allowed package declares", f)
				break
			}
		}
	}
	// Units naming a refused unit cannot load either.
	for changed := true; changed; {
		changed = false
		for _, n := range names {
			if reason[n] != "" {
				continue
			}
			for _, d := range dependsOn(codepolicy.Unit{Name: n}, free[n]) {
				if r, ok := reason[d]; ok && r != "" && d != n {
					reason[n], changed = "depends on "+d+", which does not load", true
					break
				}
			}
		}
	}
	methods := map[string][]string{}
	for _, n := range names {
		if reason[n] == "" {
			if recv := codepolicy.ReceiverType(n); recv != "" {
				methods[recv] = append(methods[recv], n)
			}
		}
	}
	for _, n := range names {
		if r := reason[n]; r != "" {
			out.Refused = append(out.Refused, Refused{Name: n, Reason: r})
			if out.raw == nil {
				out.raw = map[string]string{}
			}
			out.raw[n] = byName[n].Src
			continue
		}
		u := byName[n]
		l := Loadable{Unit: u, Kind: kindOf(u)}
		for _, d := range dependsOn(codepolicy.Unit{Name: n}, free[n]) {
			if _, ok := byName[d]; ok && d != n {
				l.Deps = append(l.Deps, d)
				for _, m := range methods[d] {
					if m != n {
						l.Deps = append(l.Deps, m)
					}
				}
			}
		}
		slices.Sort(l.Deps)
		l.Deps = slices.Compact(l.Deps)
		out.Units = append(out.Units, l)
	}
	out.Components = components(out.Units)
	return out
}

// kindOf is what a stored unit that passed CheckUnit defines.
func kindOf(u Unit) Kind {
	p, err := codepolicy.Split(u.Src)
	if err != nil {
		return ""
	}
	units, _ := p.Units()
	if len(units) != 1 {
		return ""
	}
	return units[0].Kind
}

// components groups units into strongly connected components (Tarjan's algorithm, which emits
// a component only after every component it reaches), visiting units in name order so the
// result does not depend on the file's order.
func components(units []Loadable) []Component {
	index := map[string]int{}
	for i, u := range units {
		index[u.Name] = i
	}
	var (
		next    int
		order   = make([]int, len(units))
		low     = make([]int, len(units))
		onStack = make([]bool, len(units))
		stack   []int
		compOf  = make([]int, len(units))
		comps   [][]int
	)
	for i := range order {
		order[i] = -1
	}
	var visit func(v int)
	visit = func(v int) {
		order[v], low[v] = next, next
		next++
		stack = append(stack, v)
		onStack[v] = true
		for _, d := range units[v].Deps {
			w, ok := index[d]
			if !ok {
				continue
			}
			if order[w] < 0 {
				visit(w)
				low[v] = min(low[v], low[w])
			} else if onStack[w] {
				low[v] = min(low[v], order[w])
			}
		}
		if low[v] == order[v] {
			var comp []int
			for {
				w := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				onStack[w] = false
				compOf[w] = len(comps)
				comp = append(comp, w)
				if w == v {
					break
				}
			}
			slices.Sort(comp)
			comps = append(comps, comp)
		}
	}
	for v := range units {
		if order[v] < 0 {
			visit(v)
		}
	}
	out := make([]Component, len(comps))
	for ci, comp := range comps {
		for _, v := range comp {
			out[ci].Units = append(out[ci].Units, units[v].Unit)
			for _, d := range units[v].Deps {
				if w, ok := index[d]; ok && compOf[w] != ci && !slices.Contains(out[ci].Deps, compOf[w]) {
					out[ci].Deps = append(out[ci].Deps, compOf[w])
				}
			}
		}
		slices.Sort(out[ci].Deps)
	}
	return out
}
