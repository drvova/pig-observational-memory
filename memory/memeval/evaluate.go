package memeval

import (
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/om"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/tables"
)

// Outcome is where a probe stands after a compaction.
type Outcome string

// The outcomes, best first.
const (
	Shown     Outcome = "shown"
	Reachable Outcome = "reachable"
	Lost      Outcome = "lost"
)

// Lister runs snippet, an om read returning a list, over branch and returns the ids it lists.
type Lister interface {
	List(branch []session.Entry, snippet string) ([]string, error)
}

// Cell is one probe after one compaction that had removed it: N counts the compactions since
// the probe's turn left the context (1 at the compaction that removed it).
type Cell struct {
	Probe   string
	Class   Class
	N       int
	Outcome Outcome
	// FalseCurrent is set for a superseded value whose old value a summary row shows as current.
	FalseCurrent bool
}

// Check is one hard invariant on one compaction.
type Check struct {
	Name   string
	N      int
	OK     bool
	Detail string
}

// Result is one evaluated run.
type Result struct {
	Seed     int64
	Cells    []Cell
	Checks   []Check
	Snippets int
	// SnippetErrors are snippets that did not run.
	SnippetErrors []string
}

// The invariants every compaction must keep.
const (
	InvSupersession = "supersession pointers"
	InvStatusRule   = "status rule (MaySupersede)"
	InvAnchors      = "anchors resolve to entries holding the specifics"
	InvExtraction   = "nothing removed without extraction"
)

// Invariants is every invariant, in report order.
var Invariants = []string{InvSupersession, InvStatusRule, InvAnchors, InvExtraction}

// Evaluate classifies every gold probe after every compaction of run and checks the
// invariants. gold is normally run.Script.Probes; the shuffled-gold control passes another
// script's. lister runs the printed snippets; nil leaves every unshown probe lost.
func Evaluate(run Run, gold []Probe, lister Lister) Result {
	res := Result{Seed: run.Script.Seed}
	// ages counts, per probe, the compactions that removed its turn so far: a cell's N.
	ages := map[string]int{}
	for _, snap := range run.Snapshots {
		rows := SummaryRows(snap.Summary)
		folded := ledger.FoldLedger(snap.Branch, "")
		var listed map[string]bool
		listedOnce := false
		listIDs := func() map[string]bool {
			if listedOnce {
				return listed
			}
			listedOnce = true
			listed = map[string]bool{}
			if lister == nil {
				return listed
			}
			for _, snippet := range PrintedSnippets(snap.Summary) {
				res.Snippets++
				ids, err := lister.List(snap.Branch, snippet)
				if err != nil {
					res.SnippetErrors = append(res.SnippetErrors, fmt.Sprintf("compaction %d: %s: %v", snap.N, snippet, err))
					continue
				}
				for _, id := range ids {
					listed[id] = true
				}
			}
			return listed
		}
		for _, p := range gold {
			if p.Turn >= snap.Turn {
				continue // still in the kept context: no compaction has removed it
			}
			ages[p.Name]++
			cell := Cell{Probe: p.Name, Class: p.Class, N: ages[p.Name]}
			switch {
			case anyRowHolds(rows, p):
				cell.Outcome = Shown
			case reachable(folded, listIDs(), p):
				cell.Outcome = Reachable
			default:
				cell.Outcome = Lost
			}
			if p.Class == ClassSuperseded {
				cell.FalseCurrent = showsOldAsCurrent(rows, p)
			}
			res.Cells = append(res.Cells, cell)
		}
		res.Checks = append(res.Checks, checkSupersession(snap, folded, gold), checkStatusRule(snap, folded), checkAnchors(snap, folded, rows), checkExtraction(snap))
	}
	return res
}

// Row is one summary row: a decoded table row's cells by field, or a line outside the tables.
type Row struct {
	Table  string
	Cells  map[string]string
	Text   string
	Source string
}

// SummaryRows are the summary's rows: its tables decoded with the native codec, and the
// lines outside them (the files and commits lines).
func SummaryRows(summary string) []Row {
	var out []Row
	body, ok := ledger.TablesSummaryBody(summary)
	decoded := []tables.Table(nil)
	if ok {
		if d, err := tables.Rows.Decode(body); err == nil {
			decoded = d
		}
	}
	if decoded == nil {
		for _, line := range strings.Split(summary, "\n") {
			out = append(out, Row{Text: line})
		}
		return out
	}
	for _, t := range decoded {
		for r, cells := range t.Rows {
			row := Row{Table: t.Name, Cells: map[string]string{}}
			parts := make([]string, 0, len(cells))
			for c, field := range t.Fields {
				if c < len(cells) {
					row.Cells[field] = cells[c]
					parts = append(parts, cells[c])
				}
			}
			row.Text = strings.Join(parts, "\t")
			row.Source, _ = t.Cell(r, "source")
			out = append(out, row)
		}
	}
	if _, trailer, found := strings.Cut(summary, "\n\n---\n\n"); found {
		for _, line := range strings.Split(trailer, "\n") {
			out = append(out, Row{Text: line})
		}
	}
	return out
}

// anyRowHolds reports a row picked from the probe's turn that holds every needle.
func anyRowHolds(rows []Row, p Probe) bool {
	for _, r := range rows {
		if r.Table == "omitted" || !InTurn(r.Source, p.Turn) {
			continue
		}
		if holdsAll(r.Text, p.Needles) {
			return true
		}
	}
	return false
}

// InTurn reports whether entry id is one of turn's entries (TurnEntries' ids).
func InTurn(id string, turn int) bool {
	if len(id) < 4 {
		return false
	}
	return id[1:4] == fmt.Sprintf("%03d", turn) && (len(id) == 4 || id[4] == '-')
}

func holdsAll(text string, needles []string) bool {
	for _, n := range needles {
		if !strings.Contains(text, n) {
			return false
		}
	}
	return len(needles) > 0
}

// reachable reports a listed record picked from the probe's turn that holds every needle.
func reachable(folded ledger.FoldedLedger, listed map[string]bool, p Probe) bool {
	for id := range listed {
		if o, ok := folded.ObservationsByID[id]; ok && len(o.SourceEntryIDs) > 0 && InTurn(o.SourceEntryIDs[0], p.Turn) && holdsAll(o.Content, p.Needles) {
			return true
		}
	}
	return false
}

// showsOldAsCurrent reports a row that gives the superseded value without the current one and
// without marking it replaced or withdrawn.
func showsOldAsCurrent(rows []Row, p Probe) bool {
	for _, r := range rows {
		if r.Table == "omitted" || !strings.Contains(r.Text, p.Old) || !strings.Contains(r.Text, p.Subject) || holdsAll(r.Text, p.Needles) {
			continue
		}
		if r.Cells["superseded_by"] != "" || r.Cells["status"] == ledger.StatusRetracted {
			continue
		}
		return true
	}
	return false
}

// snippetStart finds the start of an om call.
var snippetStart = regexp.MustCompile(`om\.[A-Z][A-Za-z]*\(`)

// PrintedSnippets are the runnable om snippets the summary prints (the omitted rows' queries
// and the capped lists' tails), without the fixed texts' placeholders, in order, once each.
func PrintedSnippets(summary string) []string {
	var out []string
	seen := map[string]bool{}
	for _, loc := range snippetStart.FindAllStringIndex(summary, -1) {
		if loc[0] > 0 && (summary[loc[0]-1] == '(' || summary[loc[0]-1] == '.') {
			continue // nested inside another snippet
		}
		end := snippetEnd(summary, loc[0])
		if end < 0 {
			continue
		}
		s := summary[loc[0]:end]
		if om.IsPlaceholder(s) || strings.Contains(s, "<") || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// snippetEnd is the end of the call chain starting at start: balanced parentheses, then any
// .Method(...) continuation; -1 when unbalanced.
func snippetEnd(s string, start int) int {
	i := start
	for {
		open := strings.IndexByte(s[i:], '(')
		if open < 0 {
			return -1
		}
		i += open
		depth, inQuote := 0, false
		for ; i < len(s); i++ {
			switch c := s[i]; {
			case c == '`':
				inQuote = !inQuote
			case inQuote:
			case c == '(':
				depth++
			case c == ')':
				depth--
			}
			if depth == 0 && !inQuote {
				break
			}
		}
		if i >= len(s) {
			return -1
		}
		i++ // past ')'
		if i+1 < len(s) && s[i] == '.' && s[i+1] >= 'A' && s[i+1] <= 'Z' {
			continue
		}
		return i
	}
}

// checkSupersession: every superseded probe removed by this compaction has a correction record
// that supersedes the old value's record, and the old record is never the one standing.
func checkSupersession(snap Snapshot, folded ledger.FoldedLedger, gold []Probe) Check {
	c := Check{Name: InvSupersession, N: snap.N, OK: true}
	for _, p := range gold {
		if p.Class != ClassSuperseded || p.Turn >= snap.Turn {
			continue
		}
		newRec, oldRec := recordFrom(folded, UserID(p.Turn), p.Needles[0]), recordFrom(folded, UserID(p.OldTurn), p.Old)
		switch {
		case newRec == nil || oldRec == nil:
			c.OK, c.Detail = false, fmt.Sprintf("%s: no record for the old (%v) or the new value (%v)", p.Name, oldRec != nil, newRec != nil)
		case !slices.Contains(newRec.Supersedes, oldRec.ID):
			c.OK, c.Detail = false, fmt.Sprintf("%s: %s does not supersede %s", p.Name, newRec.ID, oldRec.ID)
		case ledger.Standing(oldRec.ID, folded.ReplacedBy, nil) == oldRec.ID && !isSuperseded(folded, oldRec.ID):
			c.OK, c.Detail = false, fmt.Sprintf("%s: %s still stands", p.Name, oldRec.ID)
		}
		if !c.OK {
			return c
		}
	}
	return c
}

func isSuperseded(folded ledger.FoldedLedger, id string) bool {
	for _, o := range folded.Observations {
		if slices.Contains(o.Supersedes, id) {
			return true
		}
	}
	return false
}

// recordFrom is the record picked from entry whose content holds needle.
func recordFrom(folded ledger.FoldedLedger, entry, needle string) *ledger.Observation {
	for i := range folded.Observations {
		o := folded.Observations[i]
		if len(o.SourceEntryIDs) > 0 && o.SourceEntryIDs[0] == entry && strings.Contains(o.Content, needle) {
			return &o
		}
	}
	return nil
}

// checkStatusRule: every supersedes link joins records the status rule allows.
func checkStatusRule(snap Snapshot, folded ledger.FoldedLedger) Check {
	c := Check{Name: InvStatusRule, N: snap.N, OK: true}
	for _, o := range folded.Observations {
		for _, id := range o.Supersedes {
			earlier, ok := folded.ObservationsByID[id]
			if ok && !ledger.MaySupersede(o, earlier) {
				return Check{Name: InvStatusRule, N: snap.N, Detail: fmt.Sprintf("%s (%q) supersedes %s (%q)", o.ID, o.Status, id, earlier.Status)}
			}
		}
	}
	return c
}

// annotation is a date annotation level 0 adds; links are the pointers after a record's text.
var (
	annotation = regexp.MustCompile(`\s*\[=[^\]]*\]`)
	pointer    = regexp.MustCompile(`\s*\((was|may update|conflicts) [0-9a-f]+\)`)
)

// checkAnchors: every shown record's row names a source entry on the branch, and every
// specific the record carries is in one of its source entries.
func checkAnchors(snap Snapshot, folded ledger.FoldedLedger, rows []Row) Check {
	c := Check{Name: InvAnchors, N: snap.N, OK: true}
	byID := map[string]session.Entry{}
	for _, e := range snap.Branch {
		byID[e.ID] = e
	}
	for _, r := range rows {
		if r.Source == "" {
			continue
		}
		if _, ok := byID[r.Source]; !ok {
			return Check{Name: InvAnchors, N: snap.N, Detail: fmt.Sprintf("row %q: source %s is not on the branch", clip(r.Text), r.Source)}
		}
	}
	for _, id := range snap.Result.Details.Shown {
		o, ok := folded.ObservationsByID[id]
		if !ok {
			continue
		}
		if len(o.SourceEntryIDs) == 0 {
			return Check{Name: InvAnchors, N: snap.N, Detail: id + ": no source"}
		}
		if _, on := byID[o.SourceEntryIDs[0]]; !on {
			return Check{Name: InvAnchors, N: snap.N, Detail: fmt.Sprintf("%s: anchor %s is not on the branch", id, o.SourceEntryIDs[0])}
		}
		var source strings.Builder
		for _, sid := range o.SourceEntryIDs {
			if e, ok := byID[sid]; ok {
				collectStrings(&source, e.Message)
			}
		}
		text := pointer.ReplaceAllString(annotation.ReplaceAllString(o.Content, ""), "")
		if o.Kind == ledger.KindAttempt || o.Kind == ledger.KindFile || o.Kind == ledger.KindCommit {
			// Code builds these from the call's name, arguments and outcome; their specifics
			// are the path or command, which the call carries.
			text = attemptPayload(text)
		}
		for _, s := range ledger.Specifics(text) {
			if !strings.Contains(strings.ToLower(source.String()), strings.ToLower(s)) {
				return Check{Name: InvAnchors, N: snap.N, Detail: fmt.Sprintf("%s: specific %q not in its source entries %v", id, s, o.SourceEntryIDs)}
			}
		}
	}
	return c
}

// attemptBracket is the code-built part of an attempt record: [tool "arg" outcome: first line].
var attemptBracket = regexp.MustCompile(`\[(\S+) "((?:[^"\\]|\\.)*)" (worked|failed|open)(?:: ([^\]]*))?\]`)

// attemptPayload is the text of an attempt record whose specifics must be in its sources: the
// model's note, the call's argument and the result's first line, without code's own words.
func attemptPayload(text string) string {
	return attemptBracket.ReplaceAllStringFunc(text, func(m string) string {
		parts := attemptBracket.FindStringSubmatch(m)
		return strings.ReplaceAll(parts[2], `\"`, `"`) + " " + parts[4]
	})
}

func collectStrings(b *strings.Builder, v any) {
	switch x := v.(type) {
	case string:
		b.WriteString(x)
		b.WriteByte('\n')
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			collectStrings(b, x[k])
		}
	case []any:
		for _, item := range x {
			collectStrings(b, item)
		}
	}
}

// checkExtraction: extraction covers every entry the compaction removed.
func checkExtraction(snap Snapshot) Check {
	cut := ledger.EntryIndexForID(snap.Branch, snap.FirstKept)
	progress := ledger.ExtractionProgressIndex(snap.Branch)
	if cut < 0 || progress < cut-1 {
		return Check{Name: InvExtraction, N: snap.N, Detail: fmt.Sprintf("cut at %d, extraction through %d", cut, progress)}
	}
	return Check{Name: InvExtraction, N: snap.N, OK: true}
}

func clip(s string) string {
	if len(s) > 80 {
		return s[:80] + "…"
	}
	return s
}
