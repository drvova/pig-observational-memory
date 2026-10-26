// Package omhost is the extension's side of the memory tool's domain API (docs/code-tool.md,
// "The domain API"): the read engine every read is answered from. It holds the semantics the
// query language's sources had (the deleted internal/memquery), moved here in step 1 of the
// design's order of work so they no longer depend on the grammar: the notes, reflections and history rows, the
// point-in-time view, the off-path fallback, branch-position anchors, recall paging, clipping,
// the reflections citing a record, and the history text quotes are checked against.
//
// The rows it builds are plain tables (Rows): a caller names them and filters, sorts, cuts and
// encodes them itself. It never imports harness, commands, hooks, agents or a layer writer.
package omhost

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
)

// HistoryFields are the fields of history's rows.
var HistoryFields = []string{"id", "when", "role", "content"}

// OffPathLabel follows the id of a row or entry that is not on the current branch: a summary's
// off-path span ends, and memory v2's fallback rows (docs/memory-v2.md, "Branches").
const OffPathLabel = " (off-path)"

// LaterMark follows a superseded_by id recorded after the bound of the point-in-time view: the
// record it supersedes was still current then.
const LaterMark = " (later)"

// ShadowedFieldPrefix names, in the notes, a schema-added field whose name the kernel later took
// for a subject field or one of the level-0 fields (om.LevelZeroNoteFields).
const ShadowedFieldPrefix = "schema_"

// Records are a memory given to a read directly instead of the ledger folded from the entries:
// a compaction's projection for a summary, or one pass's notes for a detector.
type Records struct {
	// Observations are the notes' records, all active.
	Observations []ledger.Observation
	Reflections  []ledger.Reflection
	// ReflectionDates are the reflections' date ranges (ledger.ReflectionDateRanges).
	ReflectionDates map[string]string
	// Aliases are the subject aliases the notes read subjects with
	// (ledger.CanonicalSubjectsWithAliases).
	Aliases []ledger.SubjectAlias
	// SubjectContext are subjects of notes outside Observations that the canonical subjects
	// are computed over beside the records' own, so a slice of the memory (one pass's notes)
	// groups as the whole memory does; nil for none.
	SubjectContext []string
}

// Reader is what the reads run against.
type Reader struct {
	// Entries are the entries read: the current branch, or the session file under the off-path
	// fallback (Fallback).
	Entries []session.Entry
	// SessionEntries are every entry of the session file, in file order: memory v2's off-path
	// fallback (docs/memory-v2.md, "Branches"). Nil when the host cannot read them.
	SessionEntries []session.Entry
	// Records, when set, are the memory the notes and reflections read instead of the ledger
	// folded from Entries.
	Records *Records
	// ToolCall reads as the memory tool does (docs/memory-v2.md, "Memory tool"): history leaves
	// out the current turn and harness markup and lists newest first, and a level-0 field a
	// schema added is shadowed (ShadowedFieldPrefix). A detector's or summary's read leaves it
	// unset.
	ToolCall bool
	// Skip are the tools whose calls are memory reads: history and the quote checks leave their
	// calls and results out.
	Skip []string
	// Off marks the off-path fallback's read over the session file: rows on the branch are left
	// out and the others labelled OffPathLabel. Nil on the branch.
	Off *OffPath
}

// OffPath is the off-path fallback's filter: the ids of the entries and records on the branch.
type OffPath struct {
	onBranch map[string]bool
}

// NewOffPath is the filter of a fallback from the branch entries.
func NewOffPath(branch []session.Entry) *OffPath {
	on := map[string]bool{}
	for _, entry := range branch {
		on[entry.ID] = true
	}
	for id := range ledger.FoldLedger(branch, "").ObservationsByID {
		on[id] = true
	}
	return &OffPath{onBranch: on}
}

// Fallback is r reading the session file instead of the branch (docs/memory-v2.md,
// "Branches"): the rows of entries and records on the branch are left out, since the branch read
// found nothing among them, and the rest are labelled OffPathLabel, in session-file order.
func (r Reader) Fallback() Reader {
	off := r
	off.Entries, off.SessionEntries = r.SessionEntries, nil
	off.Off = NewOffPath(r.Entries)
	return off
}

// Row reports whether a fallback read keeps the row of id, and the id it shows; on the branch
// (a nil OffPath) every row is kept as it is.
func (o *OffPath) Row(id string) (string, bool) {
	if o == nil {
		return id, true
	}
	if o.onBranch[id] {
		return "", false
	}
	return id + OffPathLabel, true
}

// Rows are a read's records as a table: fields, rows of values ("" is absent) and each row's
// date range.
type Rows struct {
	Fields []string
	Rows   [][]string
	// From and To are each row's date range (YYYY-MM-DD, "" when undated).
	From, To []string
	// Clip names the fields shortened for display (om.HistoryClipChars).
	Clip map[string]bool
	// Also maps a field to another whose value a filter also matches (the notes' subject to
	// raw_subject), so a filter written against the stored value keeps matching.
	Also map[string]string
}

func (rs *Rows) add(row []string, from, to string) {
	rs.Rows = append(rs.Rows, row)
	rs.From, rs.To = append(rs.From, from), append(rs.To, to)
}

// reverse reverses the rows' order (and their dates').
func (rs *Rows) reverse() {
	for i, j := 0, len(rs.Rows)-1; i < j; i, j = i+1, j-1 {
		rs.Rows[i], rs.Rows[j] = rs.Rows[j], rs.Rows[i]
		rs.From[i], rs.From[j] = rs.From[j], rs.From[i]
		rs.To[i], rs.To[j] = rs.To[j], rs.To[i]
	}
}

// DayOf is the YYYY-MM-DD date a timestamp starts with, "" when it does not start with one.
func DayOf(timestamp string) string {
	if len(timestamp) < 10 || !validDate(timestamp[:10]) {
		return ""
	}
	return timestamp[:10]
}

func validDate(s string) bool {
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

// Notes lists the folded ledger's active observations. A relative date in the content is
// resolved against the record's date, as the memory view writes it. subject is the canonical
// subject over the active notes (and Records.SubjectContext) and the recorded subject aliases
// (ledger.CanonicalSubjectsWithAliases), scope is the scope as stored, subject_scope is what the
// note's own subject adds to the canonical one, and raw_subject is the subject the observer
// wrote, so a read by subject finds every related note, where a filter on subject also matches
// the written subject (Rows.Also), and the stored record stays readable. status, event_date and
// the source fields follow (om.FieldStatus and the others).
//
// With view (the point-in-time view), the notes are the memory as it stood at the bound: a drop
// recorded after it is not applied, so a record dropped later is listed, a record not yet in the
// memory then (one view does not hold) is not listed, and one that supersedes after the bound is
// listed in superseded_by with LaterMark, so the record it replaced reads as the value current
// then. A retraction after the bound withdraws nothing yet.
func (r Reader) Notes(view *PointInTime) Rows {
	var all, active []ledger.Observation
	var aliases []ledger.SubjectAlias
	var contextSubjects []string
	if r.Records != nil {
		all, active, aliases, contextSubjects = r.Records.Observations, r.Records.Observations, r.Records.Aliases, r.Records.SubjectContext
	} else {
		folded := ledger.FoldLedger(r.Entries, "")
		if view != nil {
			// A link is read through level 1's replacements only as far as they held at the
			// bound: a correction made before a later relabel supersedes the record it named.
			folded = ledger.FoldLedgerUnresolved(r.Entries, "")
			ledger.ResolveFolded(&folded, func(id string) bool { return view.Holds(folded.ObservationsByID[id]) })
		}
		all, active, aliases = folded.Observations, folded.ActiveObservations, folded.Aliases
		if view != nil {
			dropped := view.dropped(r.Entries)
			active = []ledger.Observation{}
			for _, o := range all {
				if !dropped[o.ID] && view.Holds(o) {
					active = append(active, o)
				}
			}
		}
	}
	supersededBy := map[string][]string{}
	stated := all
	if view != nil {
		stated = []ledger.Observation{}
	}
	for _, o := range all {
		label := o.ID
		if view != nil {
			if view.Holds(o) {
				stated = append(stated, o)
			} else {
				label += LaterMark
			}
		}
		for _, id := range o.Supersedes {
			if id != o.ID && !slices.Contains(supersededBy[id], label) {
				supersededBy[id] = append(supersededBy[id], label)
			}
		}
	}
	retracted := ledger.RetractedIDs(stated)
	// Fields a note schema added (Observation.Fields) follow the kernel's, by name, when any
	// note carries one; a note without one has it empty. A field a schema added before the
	// kernel took its name (raw_subject, subject_scope, and under memory v2 status, event_date
	// and the source fields) is listed as schema_<name>, so its values stay readable beside the
	// kernel's field. Outside a tool call (a detector's or summary's read over records that may
	// be memory v1's, which leave the level-0 fields empty), a stored schema field with one of
	// those names keeps its name: its values fill that column (kept), so a query or summary
	// written against it reads what it read before.
	added := []string{}
	columns := map[string]string{}
	kept := map[int]string{}
	for _, o := range active {
		for name := range o.Fields {
			column := name
			if slices.Contains(om.LevelZeroNoteFields, name) && !r.ToolCall {
				kept[slices.Index(om.NoteFields, name)] = name
				continue
			}
			if slices.Contains(ledger.SubjectCanonicalizationNames, name) || slices.Contains(om.LevelZeroNoteFields, name) {
				column = ShadowedFieldPrefix + name
			}
			if !slices.Contains(added, column) && !slices.Contains(om.NoteFields, column) {
				added = append(added, column)
				columns[column] = name
			}
		}
	}
	sort.Strings(added)
	fields := om.NoteFields
	if len(added) > 0 {
		fields = append(append([]string{}, om.NoteFields...), added...)
	}
	canonical := ledger.CanonicalSubjectsWithAliases(append(ledger.SubjectsOf(active), contextSubjects...), aliases)
	sources := newSourceProvenance(r.Entries)
	rs := Rows{Fields: fields, Rows: [][]string{}, From: []string{}, To: []string{}, Also: map[string]string{"subject": ledger.RawSubjectField}}
	for _, o := range active {
		id, keep := r.Off.Row(o.ID)
		if !keep {
			continue
		}
		day := DayOf(o.Timestamp)
		content := o.Content
		if day != "" {
			anchor, _ := time.Parse("2006-01-02", day)
			content = ledger.ResolveRelativeDates(content, anchor)
		}
		subject := canonical[o.Subject]
		status := o.Status
		if retracted[o.ID] {
			status = ledger.StatusRetracted
		}
		source, role, when := sources.of(o)
		row := []string{id, o.Timestamp, string(o.Kind), subject.Subject, o.Scope, string(o.Relevance), content, strings.Join(supersededBy[o.ID], " "), o.Subject, subject.ExtraScope, status, o.EventDate, source, role, when}
		for c, name := range kept {
			row[c] = o.Fields[name]
		}
		for _, column := range added {
			row = append(row, o.Fields[columns[column]])
		}
		rs.add(row, day, day)
	}
	return rs
}

// sourceProvenance reads a record's first source entry off the branch: its id, its role (as
// history names roles) and its time.
type sourceProvenance struct {
	byID map[string]ledger.Entry
}

func newSourceProvenance(entries []ledger.Entry) sourceProvenance {
	byID := make(map[string]ledger.Entry, len(entries))
	for _, entry := range entries {
		if _, seen := byID[entry.ID]; !seen {
			byID[entry.ID] = entry
		}
	}
	return sourceProvenance{byID: byID}
}

// of is o's first source entry id, role and time; role and time are "" when the entry is not
// on the branch, and time is "" when it has none.
func (p sourceProvenance) of(o ledger.Observation) (id, role, when string) {
	if len(o.SourceEntryIDs) == 0 {
		return "", "", ""
	}
	id = o.SourceEntryIDs[0]
	entry, ok := p.byID[id]
	if !ok {
		return id, "", ""
	}
	role = entry.Type
	var stamp any
	if entry.Type == "message" && entry.Message != nil {
		switch entry.Message["role"] {
		case "user":
			role = serialize.HistoryUser
		case "assistant":
			role = serialize.HistoryAssistant
		default:
			role = serialize.HistoryTool
		}
		stamp = entry.Message["timestamp"]
	}
	when = serialize.FormatRecallTimestamp(stamp, entryTimestamp(entry))
	if DayOf(when) == "" {
		when = ""
	}
	return id, role, when
}

// Reflections lists the folded ledger's reflections, dated by the observations they cite. With
// view, only the reflections recorded by the bound are listed: the memory as it stood then.
func (r Reader) Reflections(view *PointInTime) Rows {
	var reflections []ledger.Reflection
	var dates map[string]string
	if r.Records != nil {
		reflections, dates = r.Records.Reflections, r.Records.ReflectionDates
	} else {
		folded := ledger.FoldLedger(r.Entries, "")
		reflections, dates = folded.Reflections, ledger.ReflectionDateRanges(folded)
		if view != nil {
			reflections = view.reflections(r.Entries)
		}
	}
	rs := Rows{Fields: om.ReflectionFields, Rows: [][]string{}, From: []string{}, To: []string{}}
	for _, ref := range reflections {
		from, to, ranged := strings.Cut(dates[ref.ID], " to ")
		span := from
		if ranged {
			span = from + ".." + to
		} else {
			to = from
		}
		rs.add([]string{ref.ID, span, ref.Content, strings.Join(ref.SupportingObservationIDs, " ")}, from, to)
	}
	return rs
}

// History lists the branch's user, assistant and tool text, compacted entries included
// (serialize.HistoryText); the calls and results of the tools in Skip are left out.
//
// On a tool call (ToolCall) history reads the text extraction reads (HistoryText: no thinking,
// no harness markup in a user message), leaves out the current turn, and lists newest first; the
// off-path fallback lists the session file's entries off the branch in file order, each id
// labelled OffPathLabel.
func (r Reader) History() Rows {
	rs := Rows{Fields: HistoryFields, Rows: [][]string{}, From: []string{}, To: []string{}, Clip: map[string]bool{"content": true}}
	entries := r.Entries
	onBranch := r.Off == nil
	if r.ToolCall && onBranch {
		entries = entries[:CurrentTurnStart(entries)]
	}
	for _, entry := range entries {
		role, text, ok := HistoryText(entry, r.ToolCall, r.Skip)
		if !ok {
			continue
		}
		id, keep := r.Off.Row(entry.ID)
		if !keep {
			continue
		}
		when := serialize.FormatRecallTimestamp(entry.Message["timestamp"], entryTimestamp(entry))
		day := DayOf(when)
		if day == "" {
			when = ""
		}
		rs.add([]string{id, when, role, text}, day, day)
	}
	if r.ToolCall && onBranch {
		rs.reverse()
	}
	return rs
}

func entryTimestamp(entry ledger.Entry) any {
	if entry.Timestamp == "" {
		return nil
	}
	return entry.Timestamp
}

// CurrentTurnStart is the index of the current turn's first entry, the last user message on the
// branch; len(entries) when there is none. Memory v2's history leaves the current turn out: it
// is in the context already, and a search would find its own request.
func CurrentTurnStart(entries []session.Entry) int {
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i].Type == "message" && entries[i].Message != nil && entries[i].Message["role"] == "user" {
			return i
		}
	}
	return len(entries)
}

// HistoryText is an entry's text as history reads it; v2 reads it as memory v2's history does:
// what level 0's extraction reads (serialize.HistoryText, thinking left out), with harness
// markup taken out of a user message (serialize.WithoutMarkup). skip are the tools whose calls
// are left out.
func HistoryText(entry session.Entry, v2 bool, skip []string) (string, string, bool) {
	role, text, ok := serialize.HistoryText(entry, skip...)
	if !ok || !v2 || role != serialize.HistoryUser {
		return role, text, ok
	}
	text = serialize.WithoutMarkup(text)
	return role, text, strings.TrimSpace(text) != ""
}

// QuotedInConversation reports whether quote, whitespace collapsed and case ignored, is in a
// user or assistant message on the branch. Tool output is not evidence (it may carry injected
// text), nor are the calls of the memory tools in skip, the current feedback among them.
func QuotedInConversation(entries []session.Entry, quote string, skip []string) bool {
	return quotedIn(entries, quote, skip, false, serialize.HistoryUser, serialize.HistoryAssistant)
}

// QuotedByUser reports whether quote, whitespace collapsed and case ignored, is in a user
// message on the branch: memory v2's confirmation of a save and its evidence for a staged memory
// (docs/memory-v2.md, "Curated memory"). The assistant's words confirm nothing, nor does harness
// markup in a user message (an attached file, a reminder): it reads the text memory v2's history
// reads (HistoryText).
func QuotedByUser(entries []session.Entry, quote string, skip []string) bool {
	return quotedIn(entries, quote, skip, true, serialize.HistoryUser)
}

// quotedIn reports whether quote is in a message on the branch whose role is one of roles; v2
// reads it as memory v2's history does.
func quotedIn(entries []session.Entry, quote string, skip []string, v2 bool, roles ...string) bool {
	needle := strings.ToLower(strings.Join(strings.Fields(quote), " "))
	for _, entry := range entries {
		role, text, ok := HistoryText(entry, v2, skip)
		if !ok || !slices.Contains(roles, role) {
			continue
		}
		if strings.Contains(strings.ToLower(strings.Join(strings.Fields(text), " ")), needle) {
			return true
		}
	}
	return false
}
