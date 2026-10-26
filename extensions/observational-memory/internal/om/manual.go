package om

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// The manual (docs/code-tool.md, "Manual"): what empty code and man() print. Its fixed part is
// built from the generated doc table and the bounds below, so it is the same for every model;
// only the kept definitions' part, which the extension supplies, varies with the store. The
// whole manual fits one result (RecallPageChars): a store part too long for the room left is cut
// on a line boundary with a pointer to kept().

// ManualGroup is one group of the API the manual lists, each entry with its signature and doc.
type ManualGroup struct {
	// Title is the group's section title.
	Title string
	// Names are the om names it lists, in order.
	Names []string
}

// The value and list types, which the manual names on one line each: describe(x) shows a
// value's fields and methods.
var (
	valueTypes = []string{"Note", "Reflection", "Entry", "Memory", "Text", "LayerRow", "StatusInfo", "ModelStatus", "RetiredQuery"}
	listTypes  = []string{"NoteList", "ReflectionList", "EntryList", "MemoryList", "LayerList", "RetiredList"}
)

// ManualGroups are the API's entry points as the manual lists them: every name of the
// interpreter's om table (the sandbox's symgen.OMNames, which its test compares).
var ManualGroups = []ManualGroup{
	{"Reads", []string{"Notes", "Reflections", "History", "Recall", "NoteByID", "ReflectionByID", "Files", "Commits", "Attempts", "Dropped", "Subjects", "Saved", "Info", "Layer", "Retired", "Str", "ErrNoCall"}},
	{"Options (each is also an Opt method, so they chain: om.From(`e1`).Through(`e9`).Limit(50))", []string{"Opt", "Search", "From", "Through", "Since", "Until", "ByEvent", "AsOf", "Where", "Contains", "Kind", "Subject", "WithStatus", "SortBy", "Limit", "Offset", "Failed", "Worked", "Open"}},
	{"Writes", []string{"Save", "SaveOpt", "Body", "Scope", "Quoting", "Edit", "EditOpt", "SetType", "SetDescription", "SetBody", "ForgetMemory", "Feedback", "LayerForget", "LayerRollback", "Propose", "Define"}},
	{"Values (describe(x) lists a value's fields and methods with their docs)", append(append([]string{}, valueTypes...), listTypes...)},
}

// manualSection is one titled section of the manual.
type manualSection struct {
	title string
	body  string
}

// apiLine is one entry point: its signature and its doc, the doc's leading name dropped.
func apiLine(name string) string {
	sig, doc, _ := Doc(name)
	line := "  om." + sig
	if strings.HasPrefix(sig, "type ") {
		line = "  type om." + strings.TrimPrefix(sig, "type ")
	}
	doc = strings.TrimPrefix(strings.TrimPrefix(doc, name+" "), "is ")
	if doc != "" {
		line += ": " + doc
	}
	return line
}

func manualSections() []manualSection {
	out := []manualSection{
		{"The memory tool", `Runs Go code (yaegi, in a child process) over this session's memory: definitions and
statements in any order, no package clause or imports. The result is what the code printed, the
value of its last expression, and a line per definition kept or refused. Long text goes in the
strings argument, read with om.Str("name"). Empty code prints this manual.`},
		{"Seeded functions", `  describe(x any) string: the type of x and its fields and methods, with docs
  man() string: this manual
  kept(name ...string) string: the kept definitions, 20 a page (kept("2") is page 2), with load
      errors and quarantined ones; kept("name") is one definition's source
  forget(name string) string: forgets a kept definition (om.ForgetMemory forgets a memory)`},
	}
	for _, g := range ManualGroups {
		values := strings.HasPrefix(g.Title, "Values")
		var lines []string
		for _, n := range g.Names {
			if !values {
				lines = append(lines, apiLine(n))
			}
		}
		if values {
			lines = append(lines, "  type om."+strings.Join(valueTypes, ", om.")+": the records, texts and rows the reads return, plain data the code may change freely", "  type om."+strings.Join(listTypes, ", om.")+": a page of rows, with Rows, Matched, Showing and Next (the next page's snippet), and Len, IDs, Table, Count(field), Group(field), Filter(keep) and String, which run on the rows held and cost no call")
		}
		out = append(out, manualSection{g.Title, strings.Join(lines, "\n")})
	}
	return append(out,
		manualSection{"Snippets", `Summaries, the index and paged results print snippets that run as code here, such as
` + SnippetRecall + ` and ` + SnippetNotesSpan + `. Plain Until and Through filter the current memory;
AsOf() reads the memory as it stood. The old query language is removed and never runs: a query
gets this notice and, for a read, its snippet.`},
		manualSection{"Bounds", fmt.Sprintf(`One result (output, last value and kept lines): %d characters, then "[cut: N more
characters]"; the call's input (code and strings): %d. Rows per list: %d, at most %d (Limit). A
history row: %d characters around the first searched word; Recall pages %d. One kept
definition: %d characters; %d per project (forget one first). One om call's arguments: %d.`, RecallPageChars, RecallPageChars, DefaultLimit, MaxLimit, HistoryClipChars, RecallPageChars, RecallPageChars, MaxLimit, RecallPageChars)},
		manualSection{"Persistence", `Top-level functions, methods, types and constants are kept once they compile: stored per
project, re-checked on every load, replayed into every new namespace (also beside a statement
that panics). A failed compile keeps nothing and restores the earlier definition. Never kept:
statements, variables, a definition referring to a variable (pass it as a parameter or make it
a constant), one quoting six words of tool output. Variables last this conversation; a new
session, a cancel or a crash restarts the namespace from the kept definitions. A forgotten
function is a stub that panics "was forgotten" until the next restart; forgetting a method, type
or constant restarts the namespace when the call returns.
Refused, with the reason: go statements, a top-level defer, func init and main, rebinding a
predeclared, package or seeded name, assigning through a package, other imports, and a type
redefined with another source (forget it first).`},
		manualSection{"Language level", `About Go 1.20 with generics. Missing: range over an int or a func, the builtins min, max and
clear (define them as functions), the iter package, slices.Sort and slices.Sorted (use sort.Slice
or sort.Strings), APIs after Go 1.22 (strings.Lines); generic inference has gaps
(Map[int, string](...) works where Map(...) fails). Packages: fmt, strings, strconv, sort, slices,
maps, math, unicode, unicode/utf8, bytes, errors, regexp, cmp, text/tabwriter, time (without
its sleeping, timer and zone-loading functions). Loops run about 200 times slower than Go and
each om call is a round trip: let om reads filter and count.`},
		manualSection{"Limits not set, and reach", `No time limit: an endless loop or a blocking receive runs until the call is cancelled (the
user's abort, the session's end), and a headless run can hang, as with bash. A deep recursion
briefly needs about 1.5 GB. On Linux the child is the out-of-memory killer's first pick; on macOS
and Windows it is not. A crash kills only the child. The symbol table is the wall: the child has
no environment, inherited descriptor or path, but is no OS sandbox and runs as your user. Other
extensions' inits run in it. The tool never makes a model request.`},
	)
}

func contains(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

// keptHeading opens the manual's store part.
const keptHeading = "── Kept definitions ──\n"

// fixedManual is the manual without its store part.
func fixedManual() string {
	var b strings.Builder
	for _, s := range manualSections() {
		b.WriteString("── " + s.title + " ──\n" + s.body + "\n\n")
	}
	return b.String()
}

// Manual is the manual: the fixed sections, then the kept definitions as store describes them
// (the kept() list with load errors and quarantined units), cut on a line boundary to the room
// one result leaves.
func Manual(store string) string {
	fixed := fixedManual() + keptHeading
	store = strings.TrimRight(store, "\n")
	if strings.TrimSpace(store) == "" {
		return fixed + "none"
	}
	room := RecallPageChars - utf8.RuneCountInString(fixed)
	if utf8.RuneCountInString(store) <= room {
		return fixed + store
	}
	const more = "\n… kept() lists them all"
	used := utf8.RuneCountInString(more)
	var kept []string
	for _, l := range strings.Split(store, "\n") {
		n := utf8.RuneCountInString(l) + 1
		if used+n > room {
			break
		}
		kept, used = append(kept, l), used+n
	}
	return fixed + strings.Join(kept, "\n") + more
}

// ManualSection is the manual section that lists the om symbol name (a read, an option, a
// write or a value), or the language-level section for min, max and clear; "" for any other
// name. A compile error naming the symbol is answered with it.
func ManualSection(name string) string {
	for _, s := range manualSections() {
		listed := strings.Contains(s.body, "om."+name+"(") || strings.Contains(s.body, "type om."+name+":") ||
			strings.Contains(s.body, "type om."+name+"\n") || strings.Contains(s.body, "om."+name+",") || strings.Contains(s.body, "om."+name+":") || strings.HasSuffix(s.body, "type om."+name)
		if name == "min" || name == "max" || name == "clear" {
			listed = s.title == "Language level"
		}
		if listed {
			return "── " + s.title + " ──\n" + s.body
		}
	}
	return ""
}
