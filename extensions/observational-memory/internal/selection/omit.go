package selection

import (
	"sort"
	"strconv"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/memquery"
)

// OffPathLabel follows an entry id that is not on the current branch (docs/memory-v2.md,
// "Branches"): the id names where the record came from, but no range query can start there.
const OffPathLabel = " (off-path)"

// spanRow is one row of the omitted table: a span of records left out (or several, folded),
// with its date range, record count, first and last entry id and the query that lists it.
type spanRow struct {
	from, to        string
	count, spans    int
	firstID         string
	lastID          string
	query           string
	source          string
	firstAt, lastAt int
	// positional says the query bounds the span by branch position (from/through), so spans
	// of it can fold into one positional row.
	positional bool
	inRange    func(lo, hi int) int
}

// listTail ends a capped list: how many of its records the list leaves out, how many it holds
// in all, and the queries that list the whole list (one when it fits memquery.MaxLimit rows,
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

// queries are every query the omission prints: the rows', then the tails' in list order.
func (o omission) queries() []string {
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
		o.tails[groupFiles] = listTail{n: files, all: len(fileItems), queries: s.listQueries(memquery.SourceNotes+" | where kind=file", fileItems, p)}
		o.records += files
	}
	if commits > 0 {
		o.tails[groupCommits] = listTail{n: commits, all: len(commitItems), queries: s.listQueries(memquery.SourceNotes+" | where kind=commit", commitItems, p)}
		o.records += commits
	}
	allNotes := sortedAnchors(noteAnchors)
	allReflections := sortedAnchors(reflectionAnchors)
	// A shown record breaks a span: the runs are over every live record of the source in
	// anchor order, files and commits passing through (their lists show them).
	breaks := func(it *item) bool { return p.on[it] && (it.role == roleNote || it.role == roleReflection) }
	noteRows := s.spans(memquery.SourceNotes, noteAnchors, notes, allNotes, breaks)
	reflectionRows := s.spans(memquery.SourceReflections, reflectionAnchors, reflections, allReflections, breaks)
	var offRows []spanRow
	if len(offReflections) > 0 {
		offRows = append(offRows, s.offPathRow(memquery.SourceReflections, offReflections, reflectionAnchors))
	}
	if len(offNotes) > 0 {
		offRows = append(offRows, s.offPathRow(memquery.SourceNotes, offNotes, noteAnchors))
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
// the whole list when it fits memquery.MaxLimit rows; else one positional query per chunk of at
// most that many anchored records holding a left-out one, and one for the off-path records left
// out (by date, as offPathRow).
func (s *state) listQueries(base string, all []*item, p *selected) []string {
	if len(all) <= memquery.MaxLimit {
		return []string{limited(base, len(all))}
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
// run is cut before its query would return more than memquery.MaxLimit rows, so every query
// the summary prints lists all of its span.
func (s *state) spans(source string, all, left []*item, anchors []int, breaks func(*item) bool) []spanRow {
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
		if len(run) > 0 && inRange(run[0].anchor, it.anchor) > memquery.MaxLimit {
			flush()
		}
		run = append(run, it)
	}
	flush()
	return rows
}

// positionalRow is the row of one run of anchored records.
func (s *state) positionalRow(source string, run []*item, inRange func(lo, hi int) int) spanRow {
	first, last := run[0], run[len(run)-1]
	r := spanRow{count: len(run), spans: 1, firstID: first.anchorID, lastID: last.anchorID, source: source, firstAt: first.anchor, lastAt: last.anchor, positional: true, inRange: inRange}
	r.from, r.to = dateRange(run)
	r.query = positionalQuery(source, r.firstID, r.lastID, inRange(r.firstAt, r.lastAt))
	return r
}

// offPathRow is the one row of a source's records whose first source is not on the branch:
// their ids are labelled off-path, and the query bounds them by date when every one is dated,
// else lists the whole source.
func (s *state) offPathRow(source string, left, all []*item) spanRow {
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
		r.query = limited(source+" | since "+r.from+" | until "+r.to, n)
	} else {
		r.query = limited(source, len(all))
	}
	return r
}

// fold joins the oldest k rows (k < 2 folds nothing) into as few rows as keep every query
// listing all of its rows: consecutive rows join while the joined range returns at most
// memquery.MaxLimit records (shown ones between the spans included), so a folded query is never
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
		if joined != nil && joined.inRange(joined.firstAt, r.lastAt) <= memquery.MaxLimit {
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

// positionalQuery lists the records of source anchored from first through last; n is how many
// it returns, so a limit is written only when the default would cut them.
func positionalQuery(source, first, last string, n int) string {
	return limited(source+" | "+memquery.StageFrom+" "+queryWord(first)+" | "+memquery.StageThrough+" "+queryWord(last), n)
}

// limited is query with a limit stage when n rows exceed memquery.DefaultLimit, at most
// memquery.MaxLimit (the result then says "matched N, showing M").
func limited(query string, n int) string {
	if n <= memquery.DefaultLimit {
		return query
	}
	return query + " | " + memquery.StageLimit + " " + strconv.Itoa(min(n, memquery.MaxLimit))
}

// queryWord is an id as a query word: bare when the lexer reads it as one word, else quoted.
func queryWord(id string) string {
	if id != "" && !strings.ContainsAny(id, " \t\r\n\"|=~\\") {
		return id
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(id) + `"`
}
