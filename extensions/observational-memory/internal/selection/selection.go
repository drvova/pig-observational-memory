// Package selection is memory v2's level-0 summary (docs/memory-v2.md, "Selection"): it picks
// the ledger's records in ordered classes under the summary budget and renders them by code,
// with every line ending in the entry id it was picked from and everything left out written
// as a runnable memory query. The order is lexicographic over facts the ledger holds (kind,
// supersedes, outcome, specifics, citations); there are no weights, shares or caps, and every
// bound comes from the budget or from the memory tool's own limits (memquery.DefaultLimit,
// memquery.MaxLimit). Lines are picked, never reworded: a record's content is shown as
// recorded, with relative dates annotated, and only the split turn's opening request is
// clipped when it alone does not fit.
package selection

import (
	"slices"
	"sort"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/extract"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// Request is the split turn's opening request (the first user message of the turn PiG's cut
// falls inside): the summary shows it first, since the retained tail continues that turn.
type Request struct {
	EntryID, Text string
}

// Input is one summary to render.
type Input struct {
	// Reflections and Observations are the active records the compaction folds
	// (ledger.FullProjection), in ledger order.
	Reflections  []ledger.Reflection
	Observations []ledger.Observation
	// Folded is the branch's whole ledger: which records supersede which, and the sources of
	// the observations a reflection cites (dropped ones included).
	Folded ledger.FoldedLedger
	// Branch is the current branch, which places every record (ledger.ObservationAnchor).
	Branch []ledger.Entry
	// Request is the split turn's opening request, nil when the cut is between turns.
	Request *Request
	// Budget is the summary budget in estimated tokens; at 0 or less nothing renders.
	Budget int
	// Format is ledger.FormatLines or ledger.FormatTables ("" and unknown values mean tables).
	Format string
	// Aliases group the notes as the ledger does (ledger.Projection.Aliases).
	Aliases []ledger.SubjectAlias
	// ReflectionDates date the reflections (ledger.ReflectionDateRanges).
	ReflectionDates map[string]string
}

// Result is a rendered summary and what it shows.
type Result struct {
	// Text is the summary, "" when nothing fits or there is nothing to show.
	Text string
	// Reflections and Observations are the records the summary shows, in ledger order.
	Reflections  []ledger.Reflection
	Observations []ledger.Observation
	// Request reports whether the split turn's opening request is shown.
	Request bool
	// Omitted counts the records left out: in omitted spans and after a capped list's +N more.
	Omitted int
	// Spans counts the omitted spans, folded ones included; Folded the span rows that hold
	// more than one span.
	Spans, Folded int
	// Queries are every memory query the summary prints, in order.
	Queries []string
}

// role is what an item renders as.
type role int

const (
	roleRequest role = iota
	roleReflection
	roleNote
	roleFile
	roleCommit
)

// The groups that are not a notes table.
const (
	groupRequest     = "request"
	groupReflections = "reflections"
	groupFiles       = "files"
	groupCommits     = "commits"
	groupOmitted     = "omitted"
	groupNotes       = "notes"
)

// item is one record (or the request) as selection sees it.
type item struct {
	role  role
	index int
	obs   ledger.Observation
	ref   ledger.Reflection
	// text carries the item's specifics for class 4.
	text  string
	group string
	// anchorID is the first source entry id; anchored says it is on the branch, at anchor.
	anchorID string
	anchor   int
	anchored bool
	// from and to are the item's dates (YYYY-MM-DD, "" when undated).
	from, to string
	// status is the record's status as shown (statusOf); links the pointers its line ends
	// with before its source (linksOf).
	status, links string
	// cost is what the item adds to its group; cells or line render it.
	cost  int
	cells []string
	line  string
}

// source is the id the item's line ends with: its anchor when on the branch.
func (it *item) source() string {
	if it.anchored {
		return it.anchorID
	}
	return ""
}

// id is the record id (the request has none).
func (it *item) id() string {
	switch it.role {
	case roleReflection:
		return it.ref.ID
	case roleRequest:
		return ""
	}
	return it.obs.ID
}

// estimate is the host's token estimate (chars/4), the unit the budget is in.
func estimate(text string) int { return tokens.EstimateStringTokens(text) }

// Render picks and renders the summary of in (docs/memory-v2.md, "Selection"). Records are
// taken in ordered classes, newest first within a class, skipping a record that does not fit
// and continuing: the split turn's opening request; unsuperseded user corrections and
// preferences; open and failed attempts; records carrying specifics no selected line carries;
// reflections, each dropping the records it cites; everything else. Selecting a record also
// drops the records it supersedes, transitively, and the final choice drops exactly what the
// compaction's drop rule does (extract.Drops over the shown records). What is left out
// becomes omitted spans with runnable memory queries; capped lists (files, commits) end with
// +N more and the queries that list the whole list, each within memquery.MaxLimit rows; when
// the span rows do not fit, the oldest fold into as few rows as keep every query within that
// limit. The result's text is at most in.Budget tokens, or "" when nothing
// fits.
func Render(in Input) Result {
	if in.Budget <= 0 || len(in.Reflections)+len(in.Observations) == 0 {
		return Result{}
	}
	s := newState(in)
	fixed := s.l.fixedCost()
	reserve := 0
	var pick *selected
	var omitted omission
	// Each round reserves at least what the last round's omissions needed, so the reserve only
	// grows; it is bounded by the budget, and the bound on rounds is the items' count, since a
	// round that changes nothing ends the loop.
	for range len(s.items) + 2 {
		pick = s.choose(in.Budget - fixed - reserve)
		s.settleDrops(pick)
		var need int
		var ok bool
		omitted, need, ok = s.omissions(pick, in.Budget-fixed-pick.used)
		if ok || need <= reserve || need > in.Budget-fixed {
			break
		}
		reserve = need
	}
	// The costs bound the estimate from above, so the text normally fits as chosen; if it does
	// not, the item chosen last is let go until it does.
	for {
		text := s.l.render(s, pick, omitted)
		if estimate(text) <= in.Budget && pick.count() > 0 {
			return s.result(text, pick, omitted)
		}
		if !pick.popLast(s) {
			return Result{}
		}
		s.settleDrops(pick)
		omitted, _, _ = s.omissions(pick, in.Budget-fixed-pick.used)
	}
}

// state is one Render call's items and lookups.
type state struct {
	in        Input
	l         *layout
	items     []*item
	request   *item
	positions map[string]int
	// superseded are the active records some record of the ledger replaces, dropped
	// superseders included, so a record whose replacement left at an earlier compaction never
	// reads as unsuperseded.
	superseded map[string]bool
	byID       map[string]*item
	// records resolves supersede links through every record of the ledger (extract.DropClosure).
	records map[string]ledger.Observation
	// partners are, by record id, the active records a possibly_updates or conflicts link joins
	// it to, either way, in ledger order: taking one tries the others next.
	partners map[string][]*item
}

func newState(in Input) *state {
	s := &state{in: in, positions: ledger.EntryIndexByID(in.Branch), superseded: map[string]bool{}, byID: map[string]*item{}, records: map[string]ledger.Observation{}, partners: map[string][]*item{}}
	s.l = newLayout(in)
	for id, o := range in.Folded.ObservationsByID {
		s.records[id] = o
	}
	for _, o := range in.Observations {
		s.records[o.ID] = o
	}
	// Every record of the ledger, dropped ones included, in ledger order, then any active one
	// the fold does not hold.
	all := append([]ledger.Observation{}, in.Folded.Observations...)
	for _, o := range in.Observations {
		if _, folded := in.Folded.ObservationsByID[o.ID]; !folded {
			all = append(all, o)
		}
	}
	for _, o := range all {
		for _, id := range o.Supersedes {
			if id != o.ID {
				s.superseded[id] = true
			}
		}
	}
	for i, r := range in.Reflections {
		it := &item{role: roleReflection, index: i, ref: r, text: r.Content, group: groupReflections}
		it.anchorID, it.anchor, it.anchored = ledger.ReflectionAnchor(r, in.Folded.ObservationsByID, s.positions)
		from, to, ranged := strings.Cut(in.ReflectionDates[r.ID], " to ")
		if !ranged {
			to = from
		}
		it.from, it.to = from, to
		s.items = append(s.items, it)
		s.byID[r.ID] = it
	}
	canonical := ledger.ObservationSubjectsWithAliases(in.Observations, in.Aliases)
	for i, o := range in.Observations {
		it := &item{role: roleNote, index: i, obs: o, text: o.Content}
		switch o.Kind {
		case ledger.KindFile:
			it.role, it.group = roleFile, groupFiles
		case ledger.KindCommit:
			it.role, it.group = roleCommit, groupCommits
		default:
			it.group = ledger.SubjectTableName(canonical[o.Subject].Subject)
			if it.group == "" {
				it.group = groupNotes
			}
		}
		it.anchorID, it.anchor, it.anchored = ledger.ObservationAnchor(o, s.positions)
		it.from = recordDate(o.Timestamp)
		it.to = it.from
		s.items = append(s.items, it)
		s.byID[o.ID] = it
	}
	retracted := ledger.RetractedIDs(all)
	for _, it := range s.items {
		if it.role == roleNote {
			it.status = statusOf(it.obs, retracted)
		}
	}
	s.link()
	supersededBy := map[string][]string{}
	for _, o := range all {
		for _, id := range o.Supersedes {
			if id != o.ID && !contains(supersededBy[id], o.ID) {
				supersededBy[id] = append(supersededBy[id], o.ID)
			}
		}
	}
	for _, it := range s.items {
		s.l.price(it, canonical, supersededBy)
	}
	if in.Request != nil && strings.TrimSpace(in.Request.Text) != "" {
		text := strings.Join(strings.Fields(serialize.TruncateRecordContent(in.Request.Text)), " ")
		it := &item{role: roleRequest, text: text, group: groupRequest, anchorID: in.Request.EntryID}
		_, it.anchored = s.positions[in.Request.EntryID]
		s.l.price(it, canonical, supersededBy)
		s.request = it
	}
	return s
}

// statusOf is a record's status as the summary shows it (docs/memory-v2.md, "Selection"):
// retracted for a record a retraction withdrew (ledger.RetractedIDs), else its own status, so a
// plan or a condition is never shown as done.
func statusOf(o ledger.Observation, retracted map[string]bool) string {
	if retracted[o.ID] {
		return ledger.StatusRetracted
	}
	return o.Status
}

// link fills each note's pointers (the recall pointers to what it supersedes, "was"; its
// unsettled possible updates of active unsuperseded records, "may update"; its conflicts with
// active records, "conflicts") and the partners each link joins. The pointers depend only on the
// input, so an item's cost does not change with the choice.
func (s *state) link() {
	join := func(a, b *item) {
		if !slices.Contains(s.partners[a.id()], b) {
			s.partners[a.id()] = append(s.partners[a.id()], b)
		}
	}
	for _, it := range s.items {
		if it.role != roleNote {
			continue
		}
		var parts []string
		var was []string
		for _, id := range it.obs.Supersedes {
			if id != it.obs.ID && !slices.Contains(was, id) {
				was = append(was, id)
			}
		}
		if len(was) > 0 {
			parts = append(parts, "(was "+strings.Join(was, " ")+")")
		}
		pointer := func(label string, ids []string, live func(*item) bool) {
			var shown []string
			for _, id := range ids {
				other := s.byID[id]
				if other == nil || other == it || other.role == roleReflection || !live(other) {
					continue
				}
				join(it, other)
				join(other, it)
				shown = append(shown, id)
			}
			if len(shown) > 0 {
				parts = append(parts, "("+label+" "+strings.Join(shown, " ")+")")
			}
		}
		pointer("may update", it.obs.PossiblyUpdates, func(other *item) bool { return !s.superseded[other.obs.ID] })
		pointer("conflicts", it.obs.Conflicts, func(*item) bool { return true })
		it.links = strings.Join(parts, " ")
	}
	for id, items := range s.partners {
		sort.SliceStable(items, func(a, b int) bool { return items[a].index < items[b].index })
		s.partners[id] = items
	}
}

// selected is one choice of items under a budget.
type selected struct {
	on     map[*item]bool
	order  []*item
	opened map[string]int
	// dropped are the record ids a selected record supersedes or a selected reflection cites.
	dropped map[string]bool
	used    int
	carried strings.Builder
	// requestText is the request as shown (clipped when it alone did not fit).
	requestText string
}

func (p *selected) count() int {
	n := 0
	for _, on := range p.on {
		if on {
			n++
		}
	}
	return n
}

// popLast lets the item chosen last go, refunding its cost; false when none is left.
func (p *selected) popLast(s *state) bool {
	for len(p.order) > 0 {
		it := p.order[len(p.order)-1]
		p.order = p.order[:len(p.order)-1]
		if !p.on[it] {
			continue
		}
		p.unselect(s, it)
		return true
	}
	return false
}

func (p *selected) unselect(s *state, it *item) {
	p.on[it] = false
	if it.role == roleRequest {
		p.requestText = ""
	}
	p.used -= it.cost
	p.opened[it.group]--
	if p.opened[it.group] == 0 {
		p.used -= s.l.headerCost(it.group)
		delete(p.opened, it.group)
	}
}

// choose selects items under budget in the ordered classes.
func (s *state) choose(budget int) *selected {
	p := &selected{on: map[*item]bool{}, opened: map[string]int{}, dropped: map[string]bool{}}
	try := func(it *item) bool {
		if p.on[it] || p.dropped[it.id()] {
			return false
		}
		cost := it.cost
		if p.opened[it.group] == 0 {
			cost += s.l.headerCost(it.group)
		}
		if p.used+cost > budget {
			return false
		}
		p.on[it] = true
		p.order = append(p.order, it)
		p.used += cost
		p.opened[it.group]++
		p.carried.WriteString(it.text)
		p.carried.WriteByte('\n')
		// What the item replaces leaves the summary, and frees its room when it was shown.
		var replaced []string
		if it.role == roleReflection {
			replaced = it.ref.SupportingObservationIDs
		} else {
			replaced = it.obs.Supersedes
		}
		// The drop rule is transitive (extract.Drops): what a replaced record supersedes leaves
		// with it.
		for id := range extract.DropClosure(replaced, s.in.Observations, s.records) {
			if id == it.id() {
				continue
			}
			p.dropped[id] = true
			if other := s.byID[id]; other != nil && other.role != roleReflection && p.on[other] {
				p.unselect(s, other)
			}
		}
		return true
	}
	// 1. The split turn's opening request, clipped to what fits when whole it does not.
	if s.request != nil {
		if try(s.request) {
			p.requestText = s.request.text
		} else if text, ok := s.clippedRequest(budget); ok {
			clipped := *s.request
			clipped.text = text
			s.l.price(&clipped, nil, nil)
			if try(&clipped) {
				p.requestText = text
			}
		}
	}
	records := s.items
	// Taking a record tries the records its links join it to next, in the same class, which may
	// still not fit; a class a record may not hold refuses it as a partner too.
	newestFirst := func(eligible func(*item) bool, roles ...role) {
		for i := len(records) - 1; i >= 0; i-- {
			it := records[i]
			if !containsRole(roles, it.role) || !eligible(it) || !try(it) {
				continue
			}
			for _, other := range s.partners[it.id()] {
				if !holdsNoValue(other) || eligible(other) {
					try(other)
				}
			}
		}
	}
	observations := []role{roleNote, roleFile, roleCommit}
	// 2. Unsuperseded user corrections and preferences.
	newestFirst(func(it *item) bool {
		return ledger.UserPriority(it.obs) && !s.superseded[it.obs.ID] && it.status != ledger.StatusRetracted
	}, observations...)
	// 3. Open and failed attempts not resolved by a later one.
	newestFirst(func(it *item) bool {
		return it.obs.Kind == ledger.KindAttempt && (it.obs.Outcome == ledger.OutcomeOpen || it.obs.Outcome == ledger.OutcomeFailed) && !s.superseded[it.obs.ID]
	}, observations...)
	// 4. Records carrying specifics no selected line carries.
	newestFirst(func(it *item) bool {
		return it.status != ledger.StatusRetracted && len(ledger.MissingSpecifics(it.text, []string{p.carried.String()})) > 0
	}, observations...)
	// 5. Reflections, which drop the records they cite.
	newestFirst(func(*item) bool { return true }, roleReflection)
	// 6. Everything else.
	newestFirst(func(*item) bool { return true }, observations...)
	return p
}

// settleDrops sets p's drops to what the compaction drops for p's final choice (extract.Drops
// over the records and reflections it shows), so a record a pick dropped and a later pick or
// popLast let go again is listed as omitted, never left neither shown, omitted nor dropped.
func (s *state) settleDrops(p *selected) {
	var observations []ledger.Observation
	var reflections []ledger.Reflection
	for _, it := range s.items {
		if !p.on[it] {
			continue
		}
		if it.role == roleReflection {
			reflections = append(reflections, it.ref)
		} else {
			observations = append(observations, it.obs)
		}
	}
	p.dropped = map[string]bool{}
	for _, id := range extract.Drops(s.in.Observations, observations, reflections, s.records) {
		p.dropped[id] = true
	}
}

// clippedRequest is the longest prefix of the request whose item fits budget alone.
func (s *state) clippedRequest(budget int) (string, bool) {
	runes := []rune(s.request.text)
	fits := func(n int) bool {
		it := *s.request
		it.text = clipRunes(runes, n)
		s.l.price(&it, nil, nil)
		return it.cost+s.l.headerCost(groupRequest) <= budget
	}
	lo, hi := 0, len(runes)
	for lo < hi {
		mid := (lo + hi + 1) / 2
		if fits(mid) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	if lo == 0 {
		return "", false
	}
	return clipRunes(runes, lo), true
}

// clipRunes is the first n runes, marked "…" when cut.
func clipRunes(runes []rune, n int) string {
	if n >= len(runes) {
		return string(runes)
	}
	return strings.TrimSpace(string(runes[:n])) + "…"
}

// result is the Result of a rendered choice.
func (s *state) result(text string, p *selected, o omission) Result {
	r := Result{Text: text, Reflections: []ledger.Reflection{}, Observations: []ledger.Observation{}, Omitted: o.records, Spans: o.spans, Folded: o.folded, Queries: o.queries()}
	for _, it := range s.items {
		if !p.on[it] {
			continue
		}
		if it.role == roleReflection {
			r.Reflections = append(r.Reflections, it.ref)
		} else {
			r.Observations = append(r.Observations, it.obs)
		}
	}
	r.Request = p.requestText != ""
	return r
}

// recordDate is the YYYY-MM-DD a timestamp starts with, "" when it does not start with one.
func recordDate(timestamp string) string {
	if len(timestamp) < 10 || !validDate(timestamp[:10]) {
		return ""
	}
	return timestamp[:10]
}

func validDate(s string) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}
	for i, r := range s {
		if i != 4 && i != 7 && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// holdsNoValue reports a record classes 2 and 4 may not hold: a retraction, or a record one
// withdrew.
func holdsNoValue(it *item) bool { return it.status == ledger.StatusRetracted }

func containsRole(roles []role, r role) bool {
	for _, x := range roles {
		if x == r {
			return true
		}
	}
	return false
}

// sortedAnchors are the anchors of items, ascending, for counting what a range query returns.
func sortedAnchors(items []*item) []int {
	out := []int{}
	for _, it := range items {
		if it.anchored {
			out = append(out, it.anchor)
		}
	}
	sort.Ints(out)
	return out
}

// countIn counts the anchors in [lo, hi].
func countIn(anchors []int, lo, hi int) int {
	return sort.SearchInts(anchors, hi+1) - sort.SearchInts(anchors, lo)
}
