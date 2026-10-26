package selection

import (
	"sort"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omhost"
)

// OffPathLabel follows an entry id that is not on the current branch (docs/memory-v2.md,
// "Branches"): the id names where the record came from, but no range snippet can start there.
const OffPathLabel = omhost.OffPathLabel

// The om reads a span's snippet calls (docs/code-tool.md, "The summary's snippets and their
// test"): once printed into a summary on a branch, these names are frozen.
const (
	readNotes       = omwire.MNotes
	readReflections = omwire.MReflections
	readCommits     = omwire.MCommits
)

// read is the om read a row's snippet calls, with the options every snippet of it carries.
type read struct {
	fn   string
	opts omwire.Options
}

// fileKind is the option a capped files list's snippet reads with: om.Notes(om.Kind(`file`))
// lists every active file record, where om.Files() keeps only each path's newest state, which
// would leave out older records of a path the summary also leaves out.
var fileKind = read{fn: readNotes, opts: omwire.Options{Where: []omwire.Pair{{Field: "kind", Value: string(ledger.KindFile)}}}}

// spanRow is one row of the omitted table: a span of records left out (or several, folded),
// with its date range, record count, first and last entry id and the query that lists it.
type spanRow struct {
	from, to        string
	count, spans    int
	firstID         string
	lastID          string
	query           string
	source          read
	firstAt, lastAt int
	// positional says the query bounds the span by branch position (from/through), so spans
	// of it can fold into one positional row.
	positional bool
	inRange    func(lo, hi int) int
}

// listTail ends a capped list: how many of its records the list leaves out, how many it holds
// in all, and the queries that list the whole list (one when it fits om.MaxLimit rows,
// else one per positional chunk of at most that many), so each left-out record is returned by
// one of them.
type listTail struct {
	n, all  int
	queries []string
}

// omission is everything a choice leaves out, as the summary prints it.
type omission struct {
	rows  []spanRow
	tails map[string]listTail
	// records, spans and folded are Result.Omitted, Result.Spans and Result.Folded.
	records, spans, folded int
}

// snippets are every snippet the omission prints: the rows', then the tails' in list order.
func (o omission) snippets() []string {
	out := []string{}
	for _, r := range o.rows {
		out = append(out, r.query)
	}
	for _, group := range []string{groupFiles, groupCommits} {
		if t, ok := o.tails[group]; ok {
			out = append(out, t.queries...)
		}
	}
	return out
}

// omissions lays out what p leaves out within room tokens: the span rows, folding the oldest
// when they do not fit, and the capped lists' tails. ok is false when even the smallest layout
// does not fit; need is then what that layout costs.
func (s *state) omissions(p *selected, room int) (omission, int, bool) {
	live := func(it *item) bool { return !p.dropped[it.id()] }
	var notes, reflections, offNotes, offReflections []*item
	var noteAnchors, reflectionAnchors []*item
	files, commits := 0, 0
	var fileItems, commitItems []*item
	for _, it := range s.items {
		if !live(it) {
			continue
		}
		switch it.role {
		case roleReflection:
			reflectionAnchors = append(reflectionAnchors, it)
			if p.on[it] {
				continue
			}
			if it.anchored {
				reflections = append(reflections, it)
			} else {
				offReflections = append(offReflections, it)
			}
			continue
		case roleFile:
			fileItems = append(fileItems, it)
			if !p.on[it] {
				files++
			}
		case roleCommit:
			commitItems = append(commitItems, it)
			if !p.on[it] {
				commits++
			}
		}
		noteAnchors = append(noteAnchors, it)
		if it.role != roleNote || p.on[it] {
			continue
		}
		if it.anchored {
			notes = append(notes, it)
		} else {
			offNotes = append(offNotes, it)
		}
	}
	o := omission{tails: map[string]listTail{}}
	if files > 0 {
		o.tails[groupFiles] = listTail{n: files, all: len(fileItems), queries: s.listQueries(fileKind, fileItems, p)}
		o.records += files
	}
	if commits > 0 {
		o.tails[groupCommits] = listTail{n: commits, all: len(commitItems), queries: s.listQueries(read{fn: readCommits}, commitItems, p)}
		o.records += commits
	}
	allNotes := sortedAnchors(noteAnchors)
	allReflections := sortedAnchors(reflectionAnchors)
	// A shown record breaks a span: the runs are over every live record of the source in
	// anchor order, files and commits passing through (their lists show them).
	breaks := func(it *item) bool { return p.on[it] && (it.role == roleNote || it.role == roleReflection) }
	noteRows := s.spans(read{fn: readNotes}, noteAnchors, notes, allNotes, breaks)
	reflectionRows := s.spans(read{fn: readReflections}, reflectionAnchors, reflections, allReflections, breaks)
	var offRows []spanRow
	if len(offReflections) > 0 {
		offRows = append(offRows, s.offPathRow(read{fn: readReflections}, offReflections, reflectionAnchors))
	}
	if len(offNotes) > 0 {
		offRows = append(offRows, s.offPathRow(read{fn: readNotes}, offNotes, noteAnchors))
	}
	for _, rows := range [][]spanRow{noteRows, reflectionRows, offRows} {
		for _, r := range rows {
			o.records += r.count
			o.spans += r.spans
		}
	}
	tailCost := 0
	for group, t := range o.tails {
		tailCost += s.l.tailCost(group, t, p.opened[group] > 0)
	}
	layout := func(noteFold, reflectionFold int) []spanRow {
		rows := append(fold(reflectionRows, reflectionFold), fold(noteRows, noteFold)...)
		return append(rows, offRows...)
	}
	try := func(rows []spanRow) (omission, int, bool) {
		cost := tailCost + s.l.omittedCost(rows)
		out := o
		out.rows = rows
		out.folded = 0
		for _, r := range rows {
			if r.spans > 1 {
				out.folded++
			}
		}
		return out, cost, cost <= room
	}
	// Unfolded first; then the fewest oldest note spans folded that fit; then every note span
	// folded and the fewest oldest reflection spans. Folding more spans never adds a row, so
	// the fewest that fit is found by halving.
	if out, _, ok := try(layout(0, 0)); ok {
		return out, 0, true
	}
	fewest := func(n int, fits func(k int) bool) (int, bool) {
		if n < 2 || !fits(n) {
			return 0, false
		}
		lo, hi := 2, n
		for lo < hi {
			mid := (lo + hi) / 2
			if fits(mid) {
				hi = mid
			} else {
				lo = mid + 1
			}
		}
		return lo, true
	}
	if k, ok := fewest(len(noteRows), func(k int) bool { _, _, ok := try(layout(k, 0)); return ok }); ok {
		out, _, _ := try(layout(k, 0))
		return out, 0, true
	}
	if k, ok := fewest(len(reflectionRows), func(k int) bool { _, _, ok := try(layout(len(noteRows), k)); return ok }); ok {
		out, _, _ := try(layout(len(noteRows), k))
		return out, 0, true
	}
	return try(layout(len(noteRows), len(reflectionRows)))
}

// listQueries are the queries that list a capped list's records, all of them live: one over
// the whole list when it fits om.MaxLimit rows; else one positional query per chunk of at
// most that many anchored records holding a left-out one, and one for the off-path records left
// out (by date, as offPathRow).
func (s *state) listQueries(base read, all []*item, p *selected) []string {
	if len(all) <= om.MaxLimit {
		return []string{snippet(base, omwire.Options{}, len(all))}
	}
	var left, offLeft []*item
	for _, it := range all {
		if p.on[it] {
			continue
		}
		if it.anchored {
			left = append(left, it)
		} else {
			offLeft = append(offLeft, it)
		}
	}
	queries := []string{}
	// No shown item breaks a chunk: the query lists the whole list, shown items included.
	for _, r := range s.spans(base, all, left, sortedAnchors(all), func(*item) bool { return false }) {
		queries = append(queries, r.query)
	}
	if len(offLeft) > 0 {
		queries = append(queries, s.offPathRow(base, offLeft, all).query)
	}
	return queries
}

// spans are the runs of left-out records of one source in anchor order, each bounded by the
// anchors of its first and last record, so its positional query (from/through) lists it. A
// run is cut before its query would return more than om.MaxLimit rows, so every query
// the summary prints lists all of its span.
func (s *state) spans(source read, all, left []*item, anchors []int, breaks func(*item) bool) []spanRow {
	if len(left) == 0 {
		return nil
	}
	order := make([]*item, 0, len(all))
	for _, it := range all {
		if it.anchored {
			order = append(order, it)
		}
	}
	sort.SliceStable(order, func(a, b int) bool {
		if order[a].anchor != order[b].anchor {
			return order[a].anchor < order[b].anchor
		}
		return order[a].index < order[b].index
	})
	isLeft := map[*item]bool{}
	for _, it := range left {
		isLeft[it] = true
	}
	inRange := func(lo, hi int) int { return countIn(anchors, lo, hi) }
	rows := []spanRow{}
	var run []*item
	flush := func() {
		if len(run) == 0 {
			return
		}
		rows = append(rows, s.positionalRow(source, run, inRange))
		run = nil
	}
	for _, it := range order {
		if breaks(it) {
			flush()
			continue
		}
		if !isLeft[it] {
			continue
		}
		if len(run) > 0 && inRange(run[0].anchor, it.anchor) > om.MaxLimit {
			flush()
		}
		run = append(run, it)
	}
	flush()
	return rows
}

// positionalRow is the row of one run of anchored records.
func (s *state) positionalRow(source read, run []*item, inRange func(lo, hi int) int) spanRow {
	first, last := run[0], run[len(run)-1]
	r := spanRow{count: len(run), spans: 1, firstID: first.anchorID, lastID: last.anchorID, source: source, firstAt: first.anchor, lastAt: last.anchor, positional: true, inRange: inRange}
	r.from, r.to = dateRange(run)
	r.query = positionalQuery(source, r.firstID, r.lastID, inRange(r.firstAt, r.lastAt))
	return r
}

// offPathRow is the one row of a source's records whose first source is not on the branch:
// their ids are labelled off-path, and the query bounds them by date when every one is dated,
// else lists the whole source.
func (s *state) offPathRow(source read, left, all []*item) spanRow {
	r := spanRow{count: len(left), spans: 1, source: source}
	r.from, r.to = dateRange(left)
	if id := left[0].anchorID; id != "" {
		r.firstID = id + OffPathLabel
	}
	if id := left[len(left)-1].anchorID; id != "" {
		r.lastID = id + OffPathLabel
	}
	dated := true
	for _, it := range left {
		if it.from == "" {
			dated = false
		}
	}
	if dated && r.from != "" {
		n := 0
		for _, it := range all {
			if it.from != "" && it.to >= r.from && it.from <= r.to {
				n++
			}
		}
		r.query = snippet(source, omwire.Options{Since: r.from, Until: r.to}, n)
	} else {
		r.query = snippet(source, omwire.Options{}, len(all))
	}
	return r
}

// fold joins the oldest k rows (k < 2 folds nothing) into as few rows as keep every query
// listing all of its rows: consecutive rows join while the joined range returns at most
// om.MaxLimit records (shown ones between the spans included), so a folded query is never
// cut by its limit. A joined row has the rows' date range, total count, span count, the first
// row's first id, the last row's last id, and one positional query over them.
func fold(rows []spanRow, k int) []spanRow {
	if k < 2 || len(rows) < 2 {
		return rows
	}
	k = min(k, len(rows))
	out := []spanRow{}
	var joined *spanRow
	for _, r := range rows[:k] {
		if joined != nil && joined.inRange(joined.firstAt, r.lastAt) <= om.MaxLimit {
			joined.lastID, joined.lastAt = r.lastID, r.lastAt
			joined.count += r.count
			joined.spans += r.spans
			if r.from != "" && (joined.from == "" || r.from < joined.from) {
				joined.from = r.from
			}
			if r.to > joined.to {
				joined.to = r.to
			}
			continue
		}
		if joined != nil {
			out = append(out, *joined)
		}
		next := r
		joined = &next
	}
	out = append(out, *joined)
	for i := range out {
		if out[i].spans > 1 {
			out[i].query = positionalQuery(out[i].source, out[i].firstID, out[i].lastID, out[i].inRange(out[i].firstAt, out[i].lastAt))
		}
	}
	return append(out, rows[k:]...)
}

// dateRange is the earliest and latest date of items.
func dateRange(items []*item) (string, string) {
	from, to := "", ""
	for _, it := range items {
		if it.from != "" && (from == "" || it.from < from) {
			from = it.from
		}
		if it.to > to {
			to = it.to
		}
	}
	return from, to
}

// positionalQuery is the snippet listing the records of read fn anchored from first through
// last (om.Notes(om.From(`A`).Through(`B`))); n is how many it returns, so a limit is written
// only when the default would cut them.
func positionalQuery(src read, first, last string, n int) string {
	return snippet(src, omwire.Options{From: first, Through: last}, n)
}

// snippet is the read src with its options and the bounds o, through om's one snippet
// formatter, with a Limit when n rows exceed om.DefaultLimit, at most om.MaxLimit (the list
// then says "matched N, showing M"). It holds no comma, double quote or |, so it survives every
// codec as printed.
func snippet(src read, o omwire.Options, n int) string {
	o.Where = src.opts.Where
	if n > om.DefaultLimit {
		limit := min(n, om.MaxLimit)
		o.Limit = &limit
	}
	return om.FormatRead(src.fn, "", o)
}

// SpansInput is a memory some of whose records are listed elsewhere (the level-1 input's
// current memory), for OmittedSpans to write the rest as omitted spans.
type SpansInput struct {
	// Reflections and Observations are every active record of the memory, in ledger order: the
	// records a span's query counts.
	Reflections  []ledger.Reflection
	Observations []ledger.Observation
	// Listed are the ids of the records shown, or accounted for elsewhere: they break spans and
	// are never counted as omitted.
	Listed map[string]bool
	// Folded is the branch's whole ledger and Branch the current branch, which place every
	// record (ledger.ObservationAnchor).
	Folded ledger.FoldedLedger
	Branch []ledger.Entry
	// Room is what the spans may cost, in estimated tokens.
	Room int
}

// Spans are the omitted spans of a memory, one line each.
type Spans struct {
	// Lines are the span rows as the lines format prints them (date range, count, first and last
	// entry id, query), then the capped files and commits lists' ends (+N more and the queries
	// that list the whole list).
	Lines []string
	// Snippets are every om snippet Lines print, in order.
	Snippets []string
	// Records counts the records left out.
	Records int
}

// OmittedSpans writes the records of in that are not listed as Render writes what a summary
// leaves out: runs of left-out records in branch order, each with the positional memory query
// that lists it (by date for off-path records), files and commits as the ends of their lists,
// the oldest runs folded when the lines do not fit in.Room. ok is false when even the most
// folded layout does not fit; need is then what it costs.
func OmittedSpans(in SpansInput) (spans Spans, need int, ok bool) {
	s := newState(Input{Reflections: in.Reflections, Observations: in.Observations, Folded: in.Folded, Branch: in.Branch, Format: ledger.FormatLines, ReflectionDates: ledger.ReflectionDateRanges(in.Folded)})
	p := &selected{on: map[*item]bool{}, opened: map[string]int{}, dropped: map[string]bool{}}
	for _, it := range s.items {
		if in.Listed[it.id()] {
			p.on[it] = true
		}
	}
	o, need, ok := s.omissions(p, in.Room)
	if !ok {
		return Spans{}, need, false
	}
	spans = Spans{Lines: []string{}, Snippets: o.snippets(), Records: o.records}
	for _, r := range o.rows {
		spans.Lines = append(spans.Lines, omittedLine(r))
	}
	for _, group := range []string{groupFiles, groupCommits} {
		if t, has := o.tails[group]; has {
			spans.Lines = append(spans.Lines, group+": "+tailText(t))
		}
	}
	return spans, 0, true
}
