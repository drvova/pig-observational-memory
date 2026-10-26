package omhost

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/recall"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
)

// The reads (docs/code-tool.md, "Reads"), with the query language's semantics: a list is built
// as rows (Reader), filtered by the options in a fixed order (searches, field filters, dates,
// branch positions, then the sort), cut by one bound (bound), and copied into om values.

// table is a read's rows while the options filter them, each row with the index of the value
// it stands for.
type table struct {
	name   string
	fields []string
	rows   [][]string
	// from and to are each row's date range; nil for rows with no date.
	from, to []string
	also     map[string]string
	clip     map[string]bool
	idx      []int
	// anchors are the rows' branch positions; nil for rows with none.
	anchors *Anchors
}

func newTable(name string, rs Rows, anchors *Anchors) *table {
	t := &table{name: name, fields: rs.Fields, rows: rs.Rows, from: rs.From, to: rs.To, also: rs.Also, clip: rs.Clip, anchors: anchors}
	t.idx = make([]int, len(t.rows))
	for i := range t.idx {
		t.idx[i] = i
	}
	return t
}

func (t *table) column(field string) (int, error) {
	if i := slices.Index(t.fields, field); i >= 0 {
		return i, nil
	}
	return 0, fmt.Errorf("unknown field %q in %s: the fields are %s", field, t.name, strings.Join(t.fields, ", "))
}

// keep filters the rows by keepRow, stopping on ctx every few rows.
func (t *table) keep(ctx context.Context, keepRow func(i int) bool) error {
	var rows [][]string
	var from, to []string
	var idx []int
	for i, row := range t.rows {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if !keepRow(i) {
			continue
		}
		rows, idx = append(rows, row), append(idx, t.idx[i])
		if t.from != nil {
			from, to = append(from, t.from[i]), append(to, t.to[i])
		}
	}
	t.rows, t.idx = rows, idx
	if t.from != nil {
		t.from, t.to = nonNil(from), nonNil(to)
	}
	return nil
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

// match keeps the rows whose field (or the field it also matches) passes test.
func (t *table) match(ctx context.Context, field string, test func(string) bool) error {
	c, err := t.column(field)
	if err != nil {
		return err
	}
	columns := []int{c}
	if other, ok := t.also[field]; ok {
		if d, err := t.column(other); err == nil {
			columns = append(columns, d)
		}
	}
	return t.keep(ctx, func(i int) bool {
		for _, col := range columns {
			if test(t.rows[i][col]) {
				return true
			}
		}
		return false
	})
}

// words are the searched words, lowercased, that a clipped field is shortened around.
func searchWords(o omwire.Options) []string {
	var words []string
	for _, s := range o.Search {
		words = append(words, strings.Fields(strings.ToLower(s))...)
	}
	for _, p := range o.Contains {
		words = append(words, strings.Fields(strings.ToLower(p.Value))...)
	}
	return words
}

// apply filters and sorts the rows by o, in the query language's order of meaning: searches
// over every column, equality and substring filters, the subject and status, the dates
// (ByEvent compares a note's event date), the branch positions, then the sort.
func (t *table) apply(ctx context.Context, o omwire.Options) error {
	for _, s := range o.Search {
		needles := strings.Fields(strings.ToLower(s))
		if err := t.keep(ctx, func(i int) bool {
			hay := strings.ToLower(strings.Join(t.rows[i], "\n"))
			for _, w := range needles {
				if !strings.Contains(hay, w) {
					return false
				}
			}
			return true
		}); err != nil {
			return err
		}
	}
	for _, p := range o.Where {
		value := p.Value
		if err := t.match(ctx, p.Field, func(v string) bool { return strings.EqualFold(v, value) }); err != nil {
			return err
		}
	}
	for _, p := range o.Contains {
		needle := strings.ToLower(p.Value)
		if err := t.match(ctx, p.Field, func(v string) bool { return strings.Contains(strings.ToLower(v), needle) }); err != nil {
			return err
		}
	}
	if o.Subject != "" {
		value := o.Subject
		if err := t.match(ctx, "subject", func(v string) bool { return strings.EqualFold(v, value) }); err != nil {
			return err
		}
	}
	if o.Status != "" {
		value := o.Status
		if err := t.match(ctx, om.FieldStatus, func(v string) bool { return strings.EqualFold(v, value) }); err != nil {
			return err
		}
	}
	if o.Since != "" || o.Until != "" {
		if t.from == nil {
			return fmt.Errorf("Since and Until filter dated rows; %s rows have no date", t.name)
		}
		from, to := func(i int) string { return t.from[i] }, func(i int) string { return t.to[i] }
		if o.ByEvent {
			c := slices.Index(t.fields, om.FieldEventDate)
			from = func(i int) string { return eventOr(t.rows[i][c], t.from[i]) }
			to = func(i int) string { return eventOr(t.rows[i][c], t.to[i]) }
		}
		if o.Since != "" {
			if err := t.keep(ctx, func(i int) bool { return to(i) != "" && to(i) >= o.Since }); err != nil {
				return err
			}
		}
		if o.Until != "" && !o.AsOf {
			if err := t.keep(ctx, func(i int) bool { return from(i) != "" && from(i) <= o.Until }); err != nil {
				return err
			}
		}
	}
	for _, bound := range []struct {
		id   string
		from bool
	}{{o.From, true}, {o.Through, false}} {
		if bound.id == "" || !bound.from && o.AsOf {
			continue
		}
		if t.anchors == nil {
			return fmt.Errorf("From and Through filter notes, reflections and history by branch position, not %s", t.name)
		}
		at, ok := t.anchors.Positions[bound.id]
		if !ok {
			// A record id bounds at its own anchor.
			at, ok = t.anchors.Of(bound.id)
		}
		if !ok {
			return fmt.Errorf("entry %s is not on this branch: take entry ids from history or from a summary line on this branch", bound.id)
		}
		isFrom := bound.from
		if err := t.keep(ctx, func(i int) bool {
			pos, ok := t.anchors.Of(strings.TrimSuffix(t.rows[i][0], OffPathLabel))
			return ok && (isFrom && pos >= at || !isFrom && pos <= at)
		}); err != nil {
			return err
		}
	}
	if o.SortField != "" {
		c, err := t.column(o.SortField)
		if err != nil {
			return err
		}
		key := func(row []string) string { return row[c] }
		if o.SortField == om.FieldEventDate {
			if when := slices.Index(t.fields, "when"); when >= 0 {
				// A note without an event date sorts by its entry time.
				key = func(row []string) string { return eventOr(row[c], row[when]) }
			}
		}
		t.sortBy(key, o.SortDesc)
	}
	return nil
}

// eventOr is a note's event date, else its entry time.
func eventOr(event, entry string) string {
	if event != "" {
		return event
	}
	return entry
}

// sortBy orders the rows by key, numerically when both values are whole numbers, stably.
func (t *table) sortBy(key func(row []string) string, desc bool) {
	order := make([]int, len(t.rows))
	for i := range order {
		order[i] = i
	}
	less := func(a, b string) bool {
		x, errX := strconv.Atoi(a)
		y, errY := strconv.Atoi(b)
		if errX == nil && errY == nil {
			return x < y
		}
		return a < b
	}
	sort.SliceStable(order, func(i, j int) bool {
		a, b := key(t.rows[order[i]]), key(t.rows[order[j]])
		if desc {
			return less(b, a)
		}
		return less(a, b)
	})
	rows := make([][]string, len(order))
	idx := make([]int, len(order))
	var from, to []string
	for i, k := range order {
		rows[i], idx[i] = t.rows[k], t.idx[k]
		if t.from != nil {
			from, to = append(from, t.from[k]), append(to, t.to[k])
		}
	}
	t.rows, t.idx = rows, idx
	if t.from != nil {
		t.from, t.to = nonNil(from), nonNil(to)
	}
}

// limitOf is o's row bound: DefaultLimit when not given, at most MaxLimit.
func limitOf(o omwire.Options) (int, error) {
	if o.Limit == nil {
		return om.DefaultLimit, nil
	}
	if *o.Limit < 1 {
		return 0, fmt.Errorf("Limit(%d): a limit is 1 to %d", *o.Limit, om.MaxLimit)
	}
	return min(*o.Limit, om.MaxLimit), nil
}

// bound is the one helper that cuts every list: the rows shown, how many matched, and the
// snippet of the next page (next, which names the next page's options, "" for none).
func bound(n, matched int, next func() (omwire.Options, bool), fn string, o omwire.Options) (shown int, nextSnippet string) {
	shown = min(n, matched)
	if shown >= matched {
		return shown, ""
	}
	if next != nil {
		if no, ok := next(); ok {
			return shown, om.FormatRead(fn, "", no)
		}
	}
	// No position to page from: the next page is a bigger one, while the bound allows it.
	if n < om.MaxLimit {
		grown := o
		size := min(matched, om.MaxLimit)
		grown.Limit = &size
		return shown, om.FormatRead(fn, "", grown)
	}
	return shown, ""
}

// checkOptions refuses the options a read does not take.
func checkOptions(fn string, o omwire.Options) error {
	notes := fn == omwire.MNotes || fn == omwire.MFiles || fn == omwire.MCommits || fn == omwire.MAttempts || fn == omwire.MDropped
	switch {
	case o.Offset != nil:
		return errors.New("Offset pages a Recall only")
	case (o.Failed || o.Worked || o.Open) && fn != omwire.MAttempts:
		return errors.New("Failed, Worked and Open select attempts: use them with om.Attempts")
	case (o.Subject != "" || o.Status != "" || o.ByEvent) && !notes:
		return fmt.Errorf("Subject, WithStatus and ByEvent filter notes, not %s", fn)
	case o.AsOf && fn != omwire.MNotes && fn != omwire.MReflections:
		return errors.New("AsOf reads notes and reflections as they stood at the bound")
	case o.AsOf && o.ByEvent:
		return errors.New("ByEvent compares event dates, which the point-in-time view (AsOf) never does")
	case o.AsOf && o.Until != "" && o.Through != "":
		return errors.New("AsOf reads the memory at one bound: give Until or Through, not both")
	case o.AsOf && o.Until == "" && o.Through == "":
		return errors.New("AsOf reads the memory at the bound of Until or Through: give one")
	}
	return nil
}

// reader is the read engine over the call's snapshot, as the memory tool reads.
func (c *Call) reader() Reader {
	return Reader{Entries: c.Entries, SessionEntries: c.SessionEntries, ToolCall: true, Skip: c.Skip}
}

// list answers a list read.
func (c *Call) list(ctx context.Context, fn string, o omwire.Options) (any, error) {
	if err := checkOptions(fn, o); err != nil {
		return nil, err
	}
	n, err := limitOf(o)
	if err != nil {
		return nil, err
	}
	switch fn {
	case omwire.MNotes, omwire.MFiles, omwire.MCommits, omwire.MAttempts, omwire.MDropped:
		return c.notes(ctx, fn, o, n)
	case omwire.MReflections:
		return c.reflections(ctx, o, n)
	case omwire.MHistory:
		return c.history(ctx, o, n)
	case omwire.MSaved:
		return c.saved(ctx, o, n)
	case omwire.MLayer:
		return c.layer(ctx, o, n)
	}
	return c.retired(ctx, o, n)
}

// view is o's point-in-time view over r's entries; nil without AsOf.
func view(r Reader, o omwire.Options) (*PointInTime, error) {
	if !o.AsOf {
		return nil, nil
	}
	if o.Through != "" {
		v, ok := PointInTimeThrough(r.Entries, o.Through)
		if !ok {
			return nil, fmt.Errorf("entry %s is not on this branch: take entry ids from history or from a summary line on this branch", o.Through)
		}
		return v, nil
	}
	return PointInTimeUntil(r.Entries, o.Until), nil
}

// noteSet is one note read's rows and the records they stand for, in row order.
type noteSet struct {
	t     *table
	notes []om.Note
}

// noteRows reads the notes of r (records, when set, instead of r's ledger) as om values and
// their rows. Only the kinds fn reads are listed, files as each path's newest state, attempts
// by outcome.
func noteRows(r Reader, fn string, o omwire.Options, v *PointInTime) noteSet {
	folded := ledger.FoldLedger(r.Entries, "")
	if fn == omwire.MDropped {
		var dropped []ledger.Observation
		for _, obs := range folded.Observations {
			if folded.DroppedObservationIDs[obs.ID] {
				dropped = append(dropped, obs)
			}
		}
		r.Records = &Records{Observations: dropped, Reflections: folded.Reflections, ReflectionDates: ledger.ReflectionDateRanges(folded), Aliases: folded.Aliases, SubjectContext: ledger.SubjectsOf(folded.ActiveObservations)}
	}
	rows := r.Notes(v)
	positions := ledger.EntryIndexByID(r.Entries)
	byID := folded.ObservationsByID
	keep := func(obs ledger.Observation) bool {
		switch fn {
		case omwire.MFiles:
			return obs.Kind == "file"
		case omwire.MCommits:
			return obs.Kind == "commit"
		case omwire.MAttempts:
			if obs.Kind != "attempt" {
				return false
			}
			if !o.Failed && !o.Worked && !o.Open {
				return true
			}
			return o.Failed && obs.Outcome == ledger.OutcomeFailed || o.Worked && obs.Outcome == ledger.OutcomeWorked || o.Open && obs.Outcome != ledger.OutcomeFailed && obs.Outcome != ledger.OutcomeWorked
		}
		return true
	}
	// A file's newest state per path: the last file record of each target.
	newest := map[string]int{}
	if fn == omwire.MFiles {
		for i, row := range rows.Rows {
			obs := byID[strings.TrimSuffix(row[0], OffPathLabel)]
			if obs.Kind == "file" && obs.Target != "" {
				newest[obs.Target] = i
			}
		}
	}
	var kept Rows
	kept.Fields, kept.Also, kept.Clip = rows.Fields, rows.Also, rows.Clip
	kept.Rows, kept.From, kept.To = [][]string{}, []string{}, []string{}
	var notes []om.Note
	for i, row := range rows.Rows {
		id := strings.TrimSuffix(row[0], OffPathLabel)
		obs, ok := byID[id]
		if !ok {
			for _, rec := range recordsOf(r) {
				if rec.ID == id {
					obs, ok = rec, true
				}
			}
		}
		if !keep(obs) {
			continue
		}
		if fn == omwire.MFiles && obs.Target != "" && newest[obs.Target] != i {
			continue
		}
		kept.add(row, rows.From[i], rows.To[i])
		n := noteOf(rows.Fields, row, obs, positions, r.Off != nil)
		n.Dropped = fn == omwire.MDropped
		notes = append(notes, n)
	}
	return noteSet{t: newTable("notes", kept, nil), notes: notes}
}

func recordsOf(r Reader) []ledger.Observation {
	if r.Records == nil {
		return nil
	}
	return r.Records.Observations
}

// noteOf is one notes row as an om value, with the record's links, origin and anchor.
func noteOf(fields, row []string, obs ledger.Observation, positions map[string]int, offPath bool) om.Note {
	get := func(name string) string {
		if i := slices.Index(fields, name); i >= 0 {
			return row[i]
		}
		return ""
	}
	n := om.Note{
		ID: strings.TrimSuffix(get("id"), OffPathLabel), When: get("when"), Kind: get("kind"), Subject: get("subject"),
		RawSubject: get(om.FieldRawSubject), SubjectScope: get(om.FieldSubjectScope), Scope: get("scope"),
		Relevance: get("relevance"), Content: get("content"), Status: get(om.FieldStatus), EventDate: get(om.FieldEventDate),
		Source: get(om.FieldSource), SourceRole: get(om.FieldSourceRole), SourceTime: get(om.FieldSourceTime),
		Origin: obs.Origin, Outcome: obs.Outcome, Tool: obs.Tool, Target: obs.Target,
		SupersededBy: splitIDs(get("superseded_by")), Supersedes: slices.Clone(obs.Supersedes),
		PossiblyUpdates: slices.Clone(obs.PossiblyUpdates), Conflicts: slices.Clone(obs.Conflicts),
		Replaces: slices.Clone(obs.Replaces), SourceEntryIDs: slices.Clone(obs.SourceEntryIDs), OffPath: offPath,
	}
	for i, f := range fields {
		if !slices.Contains(om.NoteFields, f) {
			if n.Fields == nil {
				n.Fields = map[string]string{}
			}
			n.Fields[f] = row[i]
		}
	}
	if anchor, _, ok := ledger.ObservationAnchor(obs, positions); ok {
		n.Anchor = anchor
	}
	return n
}

func splitIDs(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, " ")
}

// notes answers Notes, Files, Commits, Attempts and Dropped: on the branch, else (for every
// read but Dropped) the off-path fallback when the branch has none.
func (c *Call) notes(ctx context.Context, fn string, o omwire.Options, n int) (any, error) {
	read := func(r Reader) (noteSet, error) {
		v, err := view(r, o)
		if err != nil {
			return noteSet{}, err
		}
		set := noteRows(r, fn, o, v)
		if r.Off == nil {
			set.t.anchors = NewAnchors(r.Entries, nil, AnchorNotes)
		}
		return set, filterSet(ctx, set.t, o)
	}
	r := c.reader()
	set, err := read(r)
	if err != nil {
		return nil, err
	}
	if len(set.t.rows) == 0 && fn != omwire.MDropped && len(c.SessionEntries) > 0 {
		if off, err := read(r.Fallback()); err == nil && len(off.t.rows) > 0 {
			set = off
		}
	}
	matched := len(set.t.rows)
	var next func() (omwire.Options, bool)
	if set.t.anchors != nil && o.SortField == "" {
		next = func() (omwire.Options, bool) {
			return nextFrom(set.t, func(i int) string { return set.notes[i].Anchor }, n, o)
		}
	}
	shown, nextSnippet := bound(n, matched, next, fn, o)
	out := om.NoteList{Rows: []om.Note{}, Matched: matched, Showing: shown, Next: nextSnippet}
	var hits []string
	for _, i := range set.t.idx[:shown] {
		out.Rows = append(out.Rows, set.notes[i])
		hits = append(hits, set.notes[i].ID)
	}
	c.Record.read(hits, shown > 0, o.Search)
	return out, nil
}

// filterSet applies o, the point-in-time bound aside (the view applied it).
func filterSet(ctx context.Context, t *table, o omwire.Options) error {
	return t.apply(ctx, o)
}

// nextFrom is the next page of a list in branch order: From the anchor of the first row left
// out, when that anchor is past the first row shown (a page whose rows all share one anchor
// cannot move forward that way). Rows sharing the new From anchor with the last shown rows
// appear again on the next page; none is skipped.
func nextFrom(t *table, anchorOf func(i int) string, n int, o omwire.Options) (omwire.Options, bool) {
	if n >= len(t.rows) {
		return o, false
	}
	first, left := anchorOf(t.idx[0]), anchorOf(t.idx[n])
	at, ok := t.anchors.Positions[left]
	start, ok2 := t.anchors.Positions[first]
	if !ok || !ok2 || at <= start {
		return o, false
	}
	o.From = left
	return o, true
}

// reflections answers Reflections.
func (c *Call) reflections(ctx context.Context, o omwire.Options, n int) (any, error) {
	r := c.reader()
	v, err := view(r, o)
	if err != nil {
		return nil, err
	}
	rows := r.Reflections(v)
	folded := ledger.FoldLedger(r.Entries, "")
	positions := ledger.EntryIndexByID(r.Entries)
	values := make([]om.Reflection, len(rows.Rows))
	for i, row := range rows.Rows {
		ref := folded.ReflectionsByID[row[0]]
		values[i] = om.Reflection{ID: row[0], Dates: row[1], Content: row[2], Supports: splitIDs(row[3]), Supersedes: slices.Clone(ref.Supersedes)}
		if anchor, _, ok := ledger.ReflectionAnchor(ref, folded.ObservationsByID, positions); ok {
			values[i].Anchor = anchor
		}
	}
	t := newTable("reflections", rows, NewAnchors(r.Entries, nil, AnchorReflections))
	if err := t.apply(ctx, o); err != nil {
		return nil, err
	}
	matched := len(t.rows)
	var next func() (omwire.Options, bool)
	if o.SortField == "" {
		next = func() (omwire.Options, bool) {
			return nextFrom(t, func(i int) string { return values[i].Anchor }, n, o)
		}
	}
	shown, nextSnippet := bound(n, matched, next, omwire.MReflections, o)
	out := om.ReflectionList{Rows: []om.Reflection{}, Matched: matched, Showing: shown, Next: nextSnippet}
	var hits []string
	for _, i := range t.idx[:shown] {
		out.Rows = append(out.Rows, values[i])
		hits = append(hits, values[i].ID)
	}
	c.Record.read(hits, shown > 0, o.Search)
	return out, nil
}

// history answers History: newest first on the branch, the current turn and memory plumbing
// left out; else the off-path fallback; else, for a search, each word's matches on the branch.
func (c *Call) history(ctx context.Context, o omwire.Options, n int) (any, error) {
	read := func(r Reader) (*table, error) {
		rows := r.History()
		// om.Entry names the text column text, as its Count, Group and Table do.
		rows.Fields = []string{"id", "when", "role", "text"}
		rows.Clip = map[string]bool{"text": true}
		var anchors *Anchors
		if r.Off == nil {
			anchors = NewAnchors(r.Entries, nil, AnchorHistory)
		}
		t := newTable("history", rows, anchors)
		return t, t.apply(ctx, o)
	}
	r := c.reader()
	t, err := read(r)
	if err != nil {
		return nil, err
	}
	offPath := false
	if len(t.rows) == 0 && len(c.SessionEntries) > 0 {
		if off, err := read(r.Fallback()); err == nil && len(off.rows) > 0 {
			t, offPath = off, true
		}
	}
	matched := len(t.rows)
	var next func() (omwire.Options, bool)
	if !offPath && o.SortField == "" {
		next = func() (omwire.Options, bool) {
			no := o
			no.Through = t.rows[n][0]
			return no, true
		}
	}
	shown, nextSnippet := bound(n, matched, next, omwire.MHistory, o)
	out := om.EntryList{Rows: []om.Entry{}, Matched: matched, Showing: shown, Next: nextSnippet}
	words := searchWords(o)
	for _, row := range t.rows[:shown] {
		text := ClipAround(row[3], words, om.HistoryClipChars)
		out.Rows = append(out.Rows, om.Entry{ID: strings.TrimSuffix(row[0], OffPathLabel), When: row[1], Role: row[2], Text: text, Clipped: text != row[3], OffPath: offPath})
	}
	if shown == 0 && len(o.Search) > 0 {
		out.Counts = map[string]int{}
		branch := r.History()
		for _, w := range strings.Fields(strings.ToLower(strings.Join(o.Search, " "))) {
			count := 0
			for _, row := range branch.Rows {
				if strings.Contains(strings.ToLower(strings.Join(row, "\n")), w) {
					count++
				}
			}
			out.Counts[w] = count
		}
	}
	c.Record.read(nil, shown > 0, o.Search)
	return out, nil
}

// saved answers Saved: the curated memories visible from this project ranked as the index ranks
// them, a staged one only in the sessions that staged it, then the files the validator refused
// with their reason.
func (c *Call) saved(ctx context.Context, o omwire.Options, n int) (any, error) {
	if c.Curated == nil {
		return nil, errCuratedOff
	}
	store := c.Curated.Store
	listed, err := store.List(c.Curated.Project)
	if err != nil {
		return nil, err
	}
	usage := store.RankUsage()
	var memories []curated.Memory
	for _, m := range listed {
		if curated.SeenBy(m, c.Curated.Source) {
			memories = append(memories, m)
		}
	}
	rows := Rows{Fields: []string{"name", "type", "scope", "updated", "description", "body", "state", "reason"}, Rows: [][]string{}, From: []string{}, To: []string{}}
	var values []om.Memory
	for _, m := range curated.Rank(memories, usage) {
		scope := "global"
		if m.Project != "" {
			scope = "project"
		}
		day := m.Updated.Format("2006-01-02")
		v := om.Memory{Name: m.Name, Type: m.Type, Scope: scope, Updated: day, Description: m.Description, Body: m.Body, State: memoryState(m, usage[m.Name])}
		rows.add([]string{v.Name, v.Type, v.Scope, v.Updated, v.Description, v.Body, v.State, ""}, day, day)
		values = append(values, v)
	}
	refused, _ := store.Refused(c.Curated.Project)
	for _, r := range refused {
		v := om.Memory{Name: r.Name, State: "refused", Reason: r.Reason + "; fix it with om.Edit or retire it with om.ForgetMemory"}
		rows.add([]string{v.Name, "", "", "", "", "", v.State, v.Reason}, "", "")
		values = append(values, v)
	}
	t := newTable("saved", rows, nil)
	if err := t.apply(ctx, o); err != nil {
		return nil, err
	}
	matched := len(t.rows)
	shown, nextSnippet := bound(n, matched, nil, omwire.MSaved, o)
	out := om.MemoryList{Rows: []om.Memory{}, Matched: matched, Showing: shown, Next: nextSnippet}
	var hits []string
	for _, i := range t.idx[:shown] {
		out.Rows = append(out.Rows, values[i])
		hits = append(hits, values[i].Name)
	}
	c.Record.read(hits, shown > 0, o.Search)
	return out, nil
}

// memoryState is a memory's state as Saved lists it.
func memoryState(m curated.Memory, e curated.Evidence) string {
	switch {
	case m.Staged:
		return "staged"
	case m.Pinned:
		return "pinned"
	case curated.Flagged(m, e):
		return "flagged"
	}
	return "live"
}

// layer answers Layer: every model's layer rows, each labelled with its model, undated.
func (c *Call) layer(ctx context.Context, o omwire.Options, n int) (any, error) {
	if c.Layer == nil {
		return nil, errLayerOff
	}
	listing, err := c.Layer.Listing()
	if err != nil {
		return nil, err
	}
	rows := Rows{Fields: []string{"model", "kind", "name", "state", "generation", "origin", "stale", "engine", "text"}, Rows: [][]string{}}
	for _, r := range listing {
		rows.Rows = append(rows.Rows, []string{r.Model, r.Kind, r.Name, r.State, r.Generation, r.Origin, r.Stale, r.Engine, r.Text})
	}
	t := newTable("layer", rows, nil)
	if err := t.apply(ctx, o); err != nil {
		return nil, err
	}
	matched := len(t.rows)
	shown, nextSnippet := bound(n, matched, nil, omwire.MLayer, o)
	out := om.LayerList{Rows: []om.LayerRow{}, Matched: matched, Showing: shown, Next: nextSnippet}
	for _, i := range t.idx[:shown] {
		out.Rows = append(out.Rows, listing[i])
	}
	c.Record.read(nil, shown > 0, o.Search)
	return out, nil
}

// retired answers Retired: the defined queries of the removed query language, never run.
func (c *Call) retired(ctx context.Context, o omwire.Options, n int) (any, error) {
	var listing []om.RetiredQuery
	if c.Retired != nil {
		var err error
		if listing, err = c.Retired(); err != nil {
			return nil, err
		}
	}
	rows := Rows{Fields: []string{"name", "origin", "query", "snippet", "reason"}, Rows: [][]string{}}
	for _, r := range listing {
		rows.Rows = append(rows.Rows, []string{r.Name, r.Origin, r.Query, r.Snippet, r.Reason})
	}
	t := newTable("retired", rows, nil)
	if err := t.apply(ctx, o); err != nil {
		return nil, err
	}
	matched := len(t.rows)
	shown, nextSnippet := bound(n, matched, nil, omwire.MRetired, o)
	out := om.RetiredList{Rows: []om.RetiredQuery{}, Matched: matched, Showing: shown, Next: nextSnippet}
	for _, i := range t.idx[:shown] {
		out.Rows = append(out.Rows, listing[i])
	}
	c.Record.read(nil, shown > 0, o.Search)
	return out, nil
}

// noteByID answers NoteByID: one record of the folded ledger, dropped or replaced ones included.
func (c *Call) noteByID(id string) (any, error) {
	folded := ledger.FoldLedger(c.Entries, "")
	obs, ok := folded.ObservationsByID[id]
	if !ok {
		return nil, fmt.Errorf("no record %s on this branch", id)
	}
	n := c.oneNote(folded, obs)
	n.Dropped = folded.DroppedObservationIDs[id]
	if standing := ledger.Standing(id, folded.ReplacedBy, nil); standing != id {
		n.Standing = standing
	}
	c.Record.read([]string{id}, true, nil)
	return n, nil
}

// oneNote is a record of folded as a notes row reads it, its subject canonical over the active
// records.
func (c *Call) oneNote(folded ledger.FoldedLedger, obs ledger.Observation) om.Note {
	r := c.reader()
	r.Records = &Records{Observations: []ledger.Observation{obs}, Aliases: folded.Aliases, SubjectContext: ledger.SubjectsOf(folded.ActiveObservations)}
	rows := r.Notes(nil)
	if len(rows.Rows) == 0 {
		return om.Note{ID: obs.ID, Content: obs.Content}
	}
	return noteOf(rows.Fields, rows.Rows[0], obs, ledger.EntryIndexByID(c.Entries), false)
}

// reflectionByID answers ReflectionByID.
func (c *Call) reflectionByID(id string) (any, error) {
	folded := ledger.FoldLedger(c.Entries, "")
	ref, ok := folded.ReflectionsByID[id]
	if !ok {
		return nil, fmt.Errorf("no reflection %s on this branch", id)
	}
	dates := ledger.ReflectionDateRanges(folded)
	from, to, ranged := strings.Cut(dates[id], " to ")
	span := from
	if ranged {
		span = from + ".." + to
	}
	out := om.Reflection{ID: ref.ID, Dates: span, Content: ref.Content, Supports: slices.Clone(ref.SupportingObservationIDs), Supersedes: slices.Clone(ref.Supersedes)}
	if anchor, _, ok := ledger.ReflectionAnchor(ref, folded.ObservationsByID, ledger.EntryIndexByID(c.Entries)); ok {
		out.Anchor = anchor
	}
	c.Record.read([]string{id}, true, nil)
	return out, nil
}

// citedBy answers Note.CitedBy: the standing reflections citing the record.
func (c *Call) citedBy(ctx context.Context, id string) (any, error) {
	cited, _ := CitedBy(c.Entries, id)
	out := om.ReflectionList{Rows: []om.Reflection{}}
	for _, rid := range cited {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		r, err := c.reflectionByID(rid)
		if err != nil {
			continue
		}
		out.Rows = append(out.Rows, r.(om.Reflection))
	}
	out.Matched, out.Showing = len(out.Rows), len(out.Rows)
	return out, nil
}

// supporting answers Reflection.Supporting: the records the reflection cites.
func (c *Call) supporting(ctx context.Context, id string) (any, error) {
	folded := ledger.FoldLedger(c.Entries, "")
	ref, ok := folded.ReflectionsByID[id]
	if !ok {
		return nil, fmt.Errorf("no reflection %s on this branch", id)
	}
	out := om.NoteList{Rows: []om.Note{}}
	var hits []string
	for _, oid := range ref.SupportingObservationIDs {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		obs, ok := folded.ObservationsByID[oid]
		if !ok {
			continue
		}
		out.Rows = append(out.Rows, c.oneNote(folded, obs))
		hits = append(hits, oid)
	}
	out.Matched, out.Showing = len(out.Rows), len(out.Rows)
	c.Record.read(hits, len(hits) > 0, nil)
	return out, nil
}

// subjects answers Subjects: each canonical subject's active records on the branch.
func (c *Call) subjects(ctx context.Context) (any, error) {
	rows := c.reader().Notes(nil)
	col := slices.Index(rows.Fields, "subject")
	out := map[string]int{}
	for i, row := range rows.Rows {
		if i%256 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		if row[col] != "" {
			out[row[col]]++
		}
	}
	return out, nil
}

// recallText answers Recall: a record id (its sources without thinking, and the reflections
// citing it), else an entry id (paged), else a saved name (recorded as a view); an id not on the
// branch is read from the session file, labelled off-path.
func (c *Call) recallText(ctx context.Context, id string, o omwire.Options) (any, error) {
	offset := 0
	if o.Offset != nil {
		offset = *o.Offset
	}
	c.Record.recall(id)
	text, status, cited, err := c.recall(c.Entries, id, true)
	if err != nil && len(c.SessionEntries) > 0 {
		if offText, offStatus, _, offErr := c.recall(c.SessionEntries, id, false); offErr == nil {
			text, status, cited, err = strings.TrimSpace(OffPathLabel)+" "+offText, offStatus, nil, nil
		}
	}
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, ok := PageOf(text, offset)
	if !ok {
		return nil, fmt.Errorf("offset %d is past the end: %s is %d characters", offset, id, p.Total)
	}
	out := om.Text{ID: id, Body: p.Text, Status: status, From: p.From, To: p.To, Total: p.Total, CitedBy: cited}
	if p.To < p.Total {
		next := p.To
		out.Next = om.FormatRead(omwire.MRecall, id, omwire.Options{Offset: &next})
	}
	c.Record.read([]string{id}, true, nil)
	return out, nil
}

// recall reads id over entries; onBranch also lists the reflections citing a record and reads
// saved names.
func (c *Call) recall(entries []ledger.Entry, id string, onBranch bool) (string, string, []string, error) {
	if ledger.MemoryIDPattern.MatchString(id) {
		text, details := recall.Execute(serialize.WithoutThinking(entries), id)
		if details.Status != recall.StatusNotFound {
			var cited, lines []string
			if onBranch {
				cited, lines = CitedBy(entries, id)
			}
			return WithCitedBy(text, lines), string(details.Status), cited, nil
		}
	}
	for _, entry := range entries {
		if entry.ID != id {
			continue
		}
		rendered, ok := serialize.RenderRecallSourceEntryText(entry)
		if !ok || strings.TrimSpace(rendered) == "" {
			kind := entry.Type
			if entry.CustomType != "" {
				kind += " " + entry.CustomType
			}
			return "", "", nil, fmt.Errorf("entry %s is a %s entry with no conversation text", id, kind)
		}
		return rendered, "entry", nil, nil
	}
	if onBranch && c.Curated != nil && curated.ValidateName(id) == nil {
		store := c.Curated.Store
		if m, err := store.GetAs(id, c.Curated.Project, c.Curated.Source); err == nil {
			_ = store.Record(id, curated.EventView)
			return curated.Describe(m), "saved", nil, nil
		} else if !errors.Is(err, curated.ErrNotFound) {
			return "", "", nil, err
		}
	}
	return "", "", nil, fmt.Errorf("nothing called %q: no record or reflection id, entry id or saved name on this branch; find ids with %s and names with %s", id, om.SnippetHistorySearch, om.SnippetSavedSearch)
}
