package ledger

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/memory/ids"
	"github.com/drvova/pig-observational-memory/memory/secrets"
	"github.com/drvova/pig-observational-memory/memory/tables"
)

// The promise syndrome (docs/memory-v2.md, "The promise syndrome"; the quantum report's change
// 7): each records summary promises a set of protected facts, shown or listed by a printed
// snippet, and its compaction's details carry a digest over that set (PromiseSyndrome). At the
// next compaction and in the sleep pass the set is worked out again from the ledger and read back
// from the summary's text (CheckPromise): a protected record the ledger says the summary promised
// that the text neither shows nor lists is a located loss, an erasure named by id. It is a digest
// over the promised facts, not a checksum of the summary's bytes, which change every time.

// PromiseVersion is the version of the promise syndrome's data.
const PromiseVersion = 1

// The protected classes a summary promises (PromisedRecord.Class).
const (
	PromiseRule      = "rule"
	PromiseDecision  = "decision"
	PromisePlan      = "plan"
	PromiseContested = "contested"
	PromiseHeld      = "held"
)

// The states a summary promises a record in.
const (
	PromiseShown  = "shown"
	PromiseListed = "listed"
	// PromiseHeldBack is a held record's state: never shown, listed in /om:status.
	PromiseHeldBack = "held"
	promiseAbsent   = ""
)

// PromiseSyndrome is what a records summary promises, stored in its compaction's details: how
// many protected records it promises, how many it shows and holds back, and Digest, the first
// twelve hex characters of the SHA-256 of the sorted lines "<id> <class> <state> <keys>"
// (PromiseDigest). Check is the previous compaction's syndrome rechecked at this one.
type PromiseSyndrome struct {
	Version int           `json:"version"`
	Records int           `json:"records"`
	Shown   int           `json:"shown"`
	Held    int           `json:"held,omitempty"`
	Digest  string        `json:"digest"`
	Check   *PromiseCheck `json:"check,omitempty"`
}

// PromiseCheck is one compaction's syndrome checked against the ledger and the summary's text:
// Lost are the protected records the ledger says it promised that its text neither shows nor
// lists, or a shown one it only lists; Leaked the held records it shows; Mismatch is set when
// the digest worked out from the ledger is not the stored one (the promised set moved); Unread
// when the text could not be read (a lines summary or a declared codec), which is never a loss.
type PromiseCheck struct {
	Compaction string   `json:"compaction"`
	Lost       []string `json:"lost,omitempty"`
	Leaked     []string `json:"leaked,omitempty"`
	Mismatch   bool     `json:"mismatch,omitempty"`
	Unread     bool     `json:"unread,omitempty"`
}

// Clean reports a check that found nothing.
func (c PromiseCheck) Clean() bool {
	return len(c.Lost) == 0 && len(c.Leaked) == 0 && !c.Mismatch
}

// PromisedRecord is a protected record a summary promises, with its class.
type PromisedRecord struct {
	Observation
	Class string
}

// PromiseClass is o's protected class among the summary's records, "" for none: held when it
// holds a preference that weakens security (HeldRule); contested when it is a claim standing
// contested or a pushback answering one; a rule when it is the user's (UserPriority: rules,
// prohibitions, constraints, scope exclusions, corrections); a decision; a plan standing open (status
// proposed, closed by no receipt, superseded by no record of all).
func PromiseClass(o Observation, contested map[string]bool, closed map[string]Closure, superseded map[string]bool) string {
	switch {
	case HeldRule(o) != "":
		return PromiseHeld
	case contested[o.ID]:
		return PromiseContested
	case UserPriority(o):
		return PromiseRule
	case o.Kind == KindDecision:
		return PromiseDecision
	case o.Status == StatusProposed && !superseded[o.ID]:
		if _, done := closed[o.ID]; !done {
			return PromisePlan
		}
	}
	return ""
}

// Promised are the protected records among records (a summary's records, in ledger order), all
// being every record of the ledger (FoldedLedger.Observations) that the contests, receipts and
// supersedes are read from.
func Promised(records, all []Observation) []PromisedRecord {
	contested := map[string]bool{}
	for id, by := range Contested(all) {
		contested[id] = true
		for _, p := range by {
			contested[p] = true
		}
	}
	closed := ClosedBy(all)
	superseded := map[string]bool{}
	for _, o := range all {
		for _, id := range o.Supersedes {
			if id != o.ID {
				superseded[id] = true
			}
		}
	}
	var out []PromisedRecord
	for _, o := range records {
		if class := PromiseClass(o, contested, closed, superseded); class != "" {
			out = append(out, PromisedRecord{Observation: o, Class: class})
		}
	}
	return out
}

// promiseState is what a summary showing shown promises of p.
func promiseState(p PromisedRecord, shown map[string]bool) string {
	switch {
	case p.Class == PromiseHeld:
		return PromiseHeldBack
	case shown[p.ID]:
		return PromiseShown
	}
	return PromiseListed
}

// PromiseDigest is the syndrome's digest of promised, shown being the ids the summary shows.
func PromiseDigest(promised []PromisedRecord, shown map[string]bool) string {
	lines := make([]string, 0, len(promised))
	for _, p := range promised {
		var keys []string
		for _, kind := range KeyKinds {
			for _, v := range p.Keys.Get(kind) {
				keys = append(keys, kind+":"+v)
			}
		}
		sort.Strings(keys)
		lines = append(lines, p.ID+" "+p.Class+" "+promiseState(p, shown)+" "+strings.Join(keys, ","))
	}
	sort.Strings(lines)
	return ids.HashID(strings.Join(lines, "\n"))
}

// NewPromiseSyndrome is the syndrome of a summary promising promised and showing the ids shown.
func NewPromiseSyndrome(promised []PromisedRecord, shown []string) *PromiseSyndrome {
	on := map[string]bool{}
	for _, id := range shown {
		on[id] = true
	}
	s := &PromiseSyndrome{Version: PromiseVersion, Records: len(promised), Digest: PromiseDigest(promised, on)}
	for _, p := range promised {
		switch promiseState(p, on) {
		case PromiseShown:
			s.Shown++
		case PromiseHeldBack:
			s.Held++
		}
	}
	return s
}

// PromiseSyndromeOf is the syndrome stored in a compaction's details, nil when there is none.
func PromiseSyndromeOf(details any) *PromiseSyndrome {
	m, ok := details.(map[string]any)
	if !ok {
		return nil
	}
	p, ok := m["promise"].(map[string]any)
	if !ok {
		return nil
	}
	version, _ := p["version"].(float64)
	digest, _ := p["digest"].(string)
	if version != PromiseVersion || digest == "" {
		return nil
	}
	records, _ := p["records"].(float64)
	shown, _ := p["shown"].(float64)
	held, _ := p["held"].(float64)
	s := &PromiseSyndrome{Version: PromiseVersion, Records: int(records), Shown: int(shown), Held: int(held), Digest: digest}
	if c, ok := p["check"].(map[string]any); ok {
		s.Check = promiseCheckFrom(c)
	}
	return s
}

func promiseCheckFrom(m map[string]any) *PromiseCheck {
	id, _ := m["compaction"].(string)
	if id == "" {
		return nil
	}
	c := &PromiseCheck{Compaction: id}
	c.Lost, c.Leaked = stringList(m["lost"]), stringList(m["leaked"])
	c.Mismatch, _ = m["mismatch"].(bool)
	c.Unread, _ = m["unread"].(bool)
	return c
}

func stringList(v any) []string {
	list, _ := v.([]any)
	var out []string
	for _, x := range list {
		if s, ok := x.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// PromisedAt are the protected records the compaction at branch index at promised, worked out
// from the ledger before it: its full projection through its firstKeptEntryId, every record of
// the fold.
func PromisedAt(branch []Entry, at int) []PromisedRecord {
	prefix := branch[:at]
	return Promised(FullProjection(prefix, branch[at].FirstKeptEntryID).Observations, FoldLedger(prefix, "").Observations)
}

// CheckPromise checks the compaction at branch index at against the ledger before it and its own
// summary; ok is false when the compaction carries no syndrome.
func CheckPromise(branch []Entry, at int) (PromiseCheck, bool) {
	if at < 0 || at >= len(branch) || branch[at].Type != "compaction" {
		return PromiseCheck{}, false
	}
	e := branch[at]
	syndrome := PromiseSyndromeOf(e.Details)
	details, okDetails := MemoryDetailsFrom(e.Details)
	if syndrome == nil || !okDetails {
		return PromiseCheck{}, false
	}
	shown := map[string]bool{}
	for _, id := range details.Shown {
		shown[id] = true
	}
	promised := PromisedAt(branch, at)
	c := PromiseCheck{Compaction: e.ID, Mismatch: PromiseDigest(promised, shown) != syndrome.Digest}
	summary, _ := e.Summary.(string)
	text, ok := readPromises(summary, details.SummaryCodec, details.SummaryFormat)
	if !ok {
		c.Unread = true
		return c, true
	}
	positions := EntryIndexByID(branch[:at])
	labels := FoldLedger(branch[:at], "").Labels()
	for _, p := range promised {
		want := promiseState(p, shown)
		got := text.state(p.Observation, positions, labels)
		switch {
		case want == PromiseHeldBack:
			if got == PromiseShown {
				c.Leaked = append(c.Leaked, p.ID)
			}
		case want == PromiseShown && got != PromiseShown, want == PromiseListed && got == promiseAbsent:
			c.Lost = append(c.Lost, p.ID)
		}
	}
	return c, true
}

// promiseText is what a summary's text shows and lists, as CheckPromise reads it back: the ids
// of its record tables, its constraints rows, its omitted rows' first and last entry ids and the
// working-memory slots its also: line lists.
type promiseText struct {
	ids                  map[string]bool
	constraints          [][2]string
	spans                [][2]string
	alsoRules, alsoPlans bool
	// alsoContested and alsoOlder are the also: line's contested pairs and older values, listed
	// by every record labelled contested or superseded.
	alsoContested, alsoOlder bool
}

// The summary's frozen names the check reads (selection's layout).
const (
	promiseConstraintsTable = "constraints"
	promiseOmittedTable     = "omitted"
	promiseAlsoPrefix       = "also: "
	promiseAlsoRules        = " more constraints: "
	promiseAlsoPlans        = " more done: "
	promiseAlsoContested    = " more contested pairs: "
	promiseAlsoOlder        = " more older values: "
	promiseOffPath          = " (off-path)"
)

// readPromises reads a tables summary in the rows or jsonl codec; false for any other.
func readPromises(summary, codecName, format string) (promiseText, bool) {
	body, ok := TablesSummaryBody(summary)
	if !ok || format != "" {
		return promiseText{}, false
	}
	var codec tables.Codec
	switch codecName {
	case "", tables.Rows.Name():
		codec = tables.Rows
	case tables.JSONLines.Name():
		codec = tables.JSONLines
	default:
		return promiseText{}, false
	}
	decoded, err := codec.Decode(body)
	if err != nil {
		return promiseText{}, false
	}
	out := promiseText{ids: map[string]bool{}}
	for _, t := range decoded {
		col := func(name string) int { return slices.Index(t.Fields, name) }
		switch t.Name {
		case promiseConstraintsTable:
			content, source := col("content"), col("source")
			if content < 0 || source < 0 {
				continue
			}
			for _, row := range t.Rows {
				out.constraints = append(out.constraints, [2]string{row[content], row[source]})
			}
		case promiseOmittedTable:
			first, last := col("first_id"), col("last_id")
			if first < 0 || last < 0 {
				continue
			}
			for _, row := range t.Rows {
				out.spans = append(out.spans, [2]string{row[first], row[last]})
			}
		default:
			if id := col("id"); id >= 0 {
				for _, row := range t.Rows {
					out.ids[row[id]] = true
				}
			}
		}
	}
	for _, line := range strings.Split(summary, "\n") {
		if rest, ok := strings.CutPrefix(line, promiseAlsoPrefix); ok {
			out.alsoRules = out.alsoRules || strings.Contains(rest, promiseAlsoRules)
			out.alsoPlans = out.alsoPlans || strings.Contains(rest, promiseAlsoPlans)
			out.alsoContested = out.alsoContested || strings.Contains(rest, promiseAlsoContested)
			out.alsoOlder = out.alsoOlder || strings.Contains(rest, promiseAlsoOlder)
		}
	}
	return out, true
}

// state is how the text promises o: shown in a record table by its id or in the constraints
// registry by its text and source; listed by an omitted row whose span holds its anchor (an
// off-path row for a record off the branch) or by the also: line's slot or label (labels, the
// fold's); else absent.
func (t promiseText) state(o Observation, positions map[string]int, labels map[string]string) string {
	if t.ids[o.ID] {
		return PromiseShown
	}
	anchor, at, anchored := ObservationAnchor(o, positions)
	source := ""
	if anchored {
		source = anchor
	}
	content := ResolveRelativeDates(o.Content, promiseAnchorDay(o.Timestamp))
	for _, row := range t.constraints {
		if row[1] == source && (strings.HasPrefix(row[0], content) || strings.HasPrefix(row[0], secrets.Redact(content))) {
			return PromiseShown
		}
	}
	for _, span := range t.spans {
		lo, okLo := positions[span[0]]
		hi, okHi := positions[span[1]]
		if anchored && okLo && okHi && lo <= at && at <= hi {
			return PromiseListed
		}
		if !anchored && (strings.HasSuffix(span[0], promiseOffPath) || strings.HasSuffix(span[1], promiseOffPath)) {
			return PromiseListed
		}
	}
	if t.alsoRules && o.Kind == "preference" || t.alsoPlans && o.Status == StatusProposed ||
		t.alsoContested && labels[o.ID] == LabelContested || t.alsoOlder && labels[o.ID] == LabelSuperseded {
		return PromiseListed
	}
	return promiseAbsent
}

// promiseAnchorDay is the day a record's relative dates resolve against (selection's
// anchorTime): the day its timestamp starts with, zero when none.
func promiseAnchorDay(timestamp string) time.Time {
	if len(timestamp) < 10 {
		return time.Time{}
	}
	t, err := time.Parse("2006-01-02", timestamp[:10])
	if err != nil {
		return time.Time{}
	}
	return t
}

// MaxPromiseChecks bounds the compactions one sleep pass checks: the newest it has not checked.
const MaxPromiseChecks = 32

// SleepPromiseChecks are the checks a sleep pass over entries writes: each compaction carrying a
// syndrome that no earlier sleep record checked, the newest MaxPromiseChecks of them, in branch
// order.
func SleepPromiseChecks(entries []Entry, st SleepState) []PromiseCheck {
	var at []int
	for i, e := range entries {
		if e.Type == "compaction" && !st.PromiseChecked[e.ID] && PromiseSyndromeOf(e.Details) != nil {
			at = append(at, i)
		}
	}
	if len(at) > MaxPromiseChecks {
		at = at[len(at)-MaxPromiseChecks:]
	}
	var out []PromiseCheck
	for _, i := range at {
		if c, ok := CheckPromise(entries, i); ok {
			out = append(out, c)
		}
	}
	return out
}

// PromiseChecks are every promise check on entries, in branch order: those a compaction's
// details hold for the one before it and those sleep records hold, one per compaction, the lost
// and leaked records of both joined.
func PromiseChecks(entries []Entry) []PromiseCheck {
	var out []PromiseCheck
	at := map[string]int{}
	add := func(c PromiseCheck) {
		i, ok := at[c.Compaction]
		if !ok {
			at[c.Compaction] = len(out)
			out = append(out, c)
			return
		}
		p := &out[i]
		for _, id := range c.Lost {
			if !slices.Contains(p.Lost, id) {
				p.Lost = append(p.Lost, id)
			}
		}
		for _, id := range c.Leaked {
			if !slices.Contains(p.Leaked, id) {
				p.Leaked = append(p.Leaked, id)
			}
		}
		p.Mismatch = p.Mismatch || c.Mismatch
		p.Unread = p.Unread && c.Unread
	}
	for _, e := range entries {
		switch {
		case e.Type == "compaction":
			if s := PromiseSyndromeOf(e.Details); s != nil && s.Check != nil {
				add(*s.Check)
			}
		default:
			if d, ok := SleepEntry(e); ok {
				for _, c := range d.Promises {
					add(c)
				}
			}
		}
	}
	return out
}

// PreviousPromiseIndex is the branch index of the newest compaction on entries carrying a
// syndrome, -1 when none does.
func PreviousPromiseIndex(entries []Entry) int {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == "compaction" && PromiseSyndromeOf(entries[i].Details) != nil {
			return i
		}
	}
	return -1
}
