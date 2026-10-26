package codestore

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// Problem is a loadable unit the live namespace does not hold for a reason the store cannot
// see: a load error the child reported, a dependent of one, or a unit quarantined for crashing
// the child at replay (the supervisor's state).
type Problem struct {
	Units []string
	Msg   string
}

// PageArg reports whether a kept() argument is a page number rather than a unit name: names are
// Go identifiers (a method Recv.Name), which never start with a digit, so kept("2") is the
// second page.
func PageArg(arg string) (int, bool) {
	n, err := strconv.Atoi(arg)
	if err != nil || n < 1 || arg != strconv.Itoa(n) {
		return 0, false
	}
	return n, true
}

// Kept is kept()'s answer (docs/code-tool.md, "kept() and forget()"). With no argument, or a page
// number, it lists the stored units PageRows to a page in name order, each with its kind and
// state: kept, refused by the load-time check (with the rule), a load error or quarantined
// (problems, from the supervisor), or different from the live namespace (seen: the digests the
// live namespace loaded or kept since it started), since kept() reads the store and another
// process may have changed it. With names, it returns each unit's source, refused units
// included, so a unit can be read back whole before it is forgotten or redefined.
func Kept(l Loaded, problems []Problem, seen map[string]string, args ...string) string {
	page := 1
	var names []string
	for _, a := range args {
		if n, ok := PageArg(a); ok {
			page = n
		} else {
			names = append(names, a)
		}
	}
	if len(names) > 0 {
		return sources(l, names)
	}
	return listPage(l, problems, seen, page)
}

// sources returns each named unit's source.
func sources(l Loaded, names []string) string {
	var b strings.Builder
	for i, n := range names {
		if i > 0 {
			b.WriteString("\n\n")
		}
		if u, ok := l.unit(n); ok {
			fmt.Fprintf(&b, "// %s (%s, kept %s)\n%s", n, u.Kind, u.Written, u.Src)
			continue
		}
		if r, ok := l.refused(n); ok {
			fmt.Fprintf(&b, "// %s is stored but refused at load: %s", n, r.Reason)
			if src, ok := l.raw[n]; ok {
				b.WriteString("\n" + src)
			}
			continue
		}
		fmt.Fprintf(&b, "// no kept unit is named %s", n)
	}
	return b.String()
}

// row is one listed unit.
type row struct{ name, kind, state string }

// listPage lists one page of the stored units.
func listPage(l Loaded, problems []Problem, seen map[string]string, page int) string {
	problem := map[string]string{}
	for _, p := range problems {
		for _, n := range p.Units {
			problem[n] = p.Msg
		}
	}
	var rows []row
	ui, ri := 0, 0
	for ui < len(l.Units) || ri < len(l.Refused) {
		if ri >= len(l.Refused) || (ui < len(l.Units) && l.Units[ui].Name < l.Refused[ri].Name) {
			u := l.Units[ui]
			ui++
			state := "kept"
			switch d, ok := seen[u.Name]; {
			case problem[u.Name] != "":
				state = "not loaded: " + problem[u.Name]
			case seen != nil && !ok:
				state = "kept by another process since this namespace started: it loads at the next restart"
			case seen != nil && d != u.Digest:
				state = "changed by another process since this namespace started: the live namespace holds the earlier definition until its next restart"
			}
			rows = append(rows, row{u.Name, string(u.Kind), state})
			continue
		}
		r := l.Refused[ri]
		ri++
		rows = append(rows, row{r.Name, "?", "refused at load: " + r.Reason})
	}
	var gone []string
	for n := range seen {
		if _, ok := l.unit(n); ok {
			continue
		}
		if _, ok := l.refused(n); ok {
			continue
		}
		gone = append(gone, n)
	}
	var b strings.Builder
	if len(rows) == 0 {
		b.WriteString("no definitions are kept")
	} else {
		pages := (len(rows) + PageRows - 1) / PageRows
		page = min(page, pages)
		from := (page - 1) * PageRows
		to := min(from+PageRows, len(rows))
		fmt.Fprintf(&b, "kept units: %d (%d loadable, %d refused), showing %d-%d", len(rows), len(l.Units), len(l.Refused), from+1, to)
		for _, r := range rows[from:to] {
			fmt.Fprintf(&b, "\n%s\t%s\t%s", r.name, r.kind, r.state)
		}
		if to < len(rows) {
			fmt.Fprintf(&b, "\nnext page: kept(\"%d\")", page+1)
		}
	}
	if len(gone) > 0 {
		slices.Sort(gone)
		b.WriteString("\nforgotten by another process, still live in this namespace until its next restart: " + strings.Join(gone, ", "))
	}
	return b.String()
}

func (l Loaded) unit(name string) (Loadable, bool) {
	for _, u := range l.Units {
		if u.Name == name {
			return u, true
		}
	}
	return Loadable{}, false
}

func (l Loaded) refused(name string) (Refused, bool) {
	for _, r := range l.Refused {
		if r.Name == name {
			return r, true
		}
	}
	return Refused{}, false
}
