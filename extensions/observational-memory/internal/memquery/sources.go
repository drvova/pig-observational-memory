package memquery

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/recall"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
)

// The fields of each source's records.
var (
	NoteFields       = []string{"id", "when", "kind", "subject", "scope", "relevance", "content", "superseded_by", ledger.RawSubjectField, ledger.SubjectScopeField, FieldStatus, FieldEventDate, FieldSource, FieldSourceRole, FieldSourceTime}
	ReflectionFields = []string{"id", "dates", "content", "supports"}
	HistoryFields    = []string{"id", "when", "role", "content"}
	SavedFields      = []string{"name", "type", "scope", "updated", "description", "body"}
	// SavedFieldsV2 are saved's fields under memory v2: SavedFields and each memory's state
	// (FieldState).
	SavedFieldsV2 = append(append([]string{}, SavedFields...), FieldState)
	HarnessFields = []string{"model", "kind", "name", "state", "generation", "origin", "stale"}
)

// The notes source's fields memory v2's level 0 fills (docs/memory-v2.md, "Memory tool"), empty
// for a record without them: how the record was stated, the date it is about, and its first
// source entry with that entry's role and time.
const (
	// FieldStatus is the record's status (ledger.Observation.Status), or retracted when a
	// retraction withdrew it (ledger.RetractedIDs); "" when stated as fact.
	FieldStatus = "status"
	// FieldEventDate is the date the record is about (ledger.Observation.EventDate); since and
	// until compare it with event, and sort event_date falls back to the entry time.
	FieldEventDate = "event_date"
	// FieldSource is the record's first source entry id; FieldSourceRole and FieldSourceTime
	// are that entry's role (as history names it) and time, "" when it is not on the branch.
	FieldSource     = "source"
	FieldSourceRole = "source_role"
	FieldSourceTime = "source_time"
)

// FieldState is saved's state column under memory v2 (docs/memory-v2.md, "Curated memory"):
// staged (shown only in the sessions that staged it, absent from the index), flagged (a user
// correction matched it since its last edit, so it ranks last), pinned (the user pinned it),
// refused (a file the validator refuses on load), else live; empty for a defined query.
const FieldState = "state"

// The values of FieldState.
const (
	StateLive    = "live"
	StateStaged  = "staged"
	StateFlagged = "flagged"
	StatePinned  = "pinned"
	// StateRefused is a memory file the validator refuses on load: not loaded, listed with
	// its reason so it can be edited within the validator or forgotten.
	StateRefused = "refused"
)

// LevelZeroNoteFields are the fields above, which a schema may have added before the notes
// source listed them; the kernel owns them for schemas (kernel.Owns), so a schema written now
// cannot take them.
var LevelZeroNoteFields = []string{FieldStatus, FieldEventDate, FieldSource, FieldSourceRole, FieldSourceTime}

// laterMark follows a superseded_by id recorded after the bound of the point-in-time view:
// the record it supersedes was still current then.
const laterMark = " (later)"

// shadowedFieldPrefix names, in the notes source, a schema-added field whose name the kernel
// later took for a subject field or one of the fields above.
const shadowedFieldPrefix = "schema_"

// savedQueryType is the type a defined query is listed under in saved.
const savedQueryType = "query"

// loadSource reads a source's records, in their natural order: notes, reflections and history
// as they were recorded, saved as the memory index ranks them, then defined queries by name.
// view, when set, is the point-in-time view notes and reflections are read at.
func loadSource(env Env, source string, view *pointInTime) (*relation, error) {
	switch source {
	case SourceNotes:
		return notesRelation(env, view), nil
	case SourceReflections:
		return reflectionsRelation(env, view), nil
	case SourceHistory:
		return historyRelation(env), nil
	case SourceSaved:
		return savedRelation(env)
	case SourceHarness:
		return harnessRelation(env)
	}
	return nil, fmt.Errorf("unknown source %q", source)
}

// dayOf is the YYYY-MM-DD date a timestamp starts with, "" when it does not start with one.
func dayOf(timestamp string) string {
	if len(timestamp) < 10 || !validDate(timestamp[:10]) {
		return ""
	}
	return timestamp[:10]
}

// notesRelation lists the folded ledger's active observations. A relative date in the content
// is resolved against the record's date, as the memory view writes it. subject is the canonical
// subject over the active notes (and Records.SubjectContext) and the recorded subject aliases
// (ledger.CanonicalSubjectsWithAliases), scope is the scope as stored,
// subject_scope is what the note's own subject adds to the canonical one, and raw_subject is
// the subject the observer wrote, so a query by subject finds every related note, where
// subject= and subject~ also match the written subject (a definition written against it keeps
// its rows), and the stored record stays readable. status, event_date and the source fields
// follow (FieldStatus and the others).
//
// With view (until DATE asof, through ID asof), the notes are the memory as it stood at the
// bound: a drop recorded after it is not applied, so a record dropped later is listed, a
// record not yet in the memory then (one view does not hold) is not listed, and one that
// supersedes after the bound is listed in superseded_by with laterMark, so the record it
// replaced reads as the value current then. A retraction after the bound withdraws nothing yet.
func notesRelation(env Env, view *pointInTime) *relation {
	var all, active []ledger.Observation
	var aliases []ledger.SubjectAlias
	var contextSubjects []string
	if env.Records != nil {
		all, active, aliases, contextSubjects = env.Records.Observations, env.Records.Observations, env.Records.Aliases, env.Records.SubjectContext
	} else {
		folded := ledger.FoldLedger(env.Entries, "")
		all, active, aliases = folded.Observations, folded.ActiveObservations, folded.Aliases
		if view != nil {
			dropped := view.dropped(env.Entries)
			active = []ledger.Observation{}
			for _, o := range all {
				if !dropped[o.ID] && view.holds(o) {
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
			if view.holds(o) {
				stated = append(stated, o)
			} else {
				label += laterMark
			}
		}
		for _, id := range o.Supersedes {
			if id != o.ID && !contains(supersededBy[id], label) {
				supersededBy[id] = append(supersededBy[id], label)
			}
		}
	}
	retracted := ledger.RetractedIDs(stated)
	// Fields a note schema added (Observation.Fields) follow the kernel's, by name, when any
	// note carries one; a note without one has it empty. A field a schema added before the
	// kernel took its name (raw_subject, subject_scope, and under memory v2 status, event_date
	// and the source fields) is listed as schema_<name>, so its values stay readable beside the
	// kernel's field. Under v1, whose records leave the level-0 fields empty, a stored schema
	// field with one of those names keeps its name: its values fill that column (kept), so a
	// query or summary written against it reads what it read before.
	added := []string{}
	columns := map[string]string{}
	kept := map[int]string{}
	for _, o := range active {
		for name := range o.Fields {
			column := name
			if contains(LevelZeroNoteFields, name) && !env.V2 {
				kept[slices.Index(NoteFields, name)] = name
				continue
			}
			if contains(ledger.SubjectCanonicalizationNames, name) || contains(LevelZeroNoteFields, name) {
				column = shadowedFieldPrefix + name
			}
			if !contains(added, column) && !contains(NoteFields, column) {
				added = append(added, column)
				columns[column] = name
			}
		}
	}
	sort.Strings(added)
	fields := NoteFields
	if len(added) > 0 {
		fields = append(append([]string{}, NoteFields...), added...)
	}
	canonical := ledger.CanonicalSubjectsWithAliases(append(ledger.SubjectsOf(active), contextSubjects...), aliases)
	sources := newSourceProvenance(env.Entries)
	rel := &relation{name: SourceNotes, fields: fields, rows: [][]string{}, from: []string{}, to: []string{}, also: map[string]string{"subject": ledger.RawSubjectField}}
	for _, o := range active {
		id, keep := env.offPathRow(o.ID)
		if !keep {
			continue
		}
		day := dayOf(o.Timestamp)
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
		rel.rows = append(rel.rows, row)
		rel.from, rel.to = append(rel.from, day), append(rel.to, day)
	}
	return rel
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
	if dayOf(when) == "" {
		when = ""
	}
	return id, role, when
}

// reflectionsRelation lists the folded ledger's reflections, dated by the observations they
// cite. With view, only the reflections recorded by the bound are listed: the memory as it stood
// then.
func reflectionsRelation(env Env, view *pointInTime) *relation {
	var reflections []ledger.Reflection
	var dates map[string]string
	if env.Records != nil {
		reflections, dates = env.Records.Reflections, env.Records.ReflectionDates
	} else {
		folded := ledger.FoldLedger(env.Entries, "")
		reflections, dates = folded.Reflections, ledger.ReflectionDateRanges(folded)
		if view != nil {
			reflections = view.reflections(env.Entries)
		}
	}
	rel := &relation{name: SourceReflections, fields: ReflectionFields, rows: [][]string{}, from: []string{}, to: []string{}}
	for _, r := range reflections {
		from, to, ranged := strings.Cut(dates[r.ID], " to ")
		span := from
		if ranged {
			span = from + ".." + to
		} else {
			to = from
		}
		rel.rows = append(rel.rows, []string{r.ID, span, r.Content, strings.Join(r.SupportingObservationIDs, " ")})
		rel.from, rel.to = append(rel.from, from), append(rel.to, to)
	}
	return rel
}

// historyRelation lists the branch's user, assistant and tool text, compacted entries
// included (serialize.HistoryText); the calls and results of the memory tool and of the
// tools the agent defined with it are left out.
//
// Under memory v2 (Env.V2) history reads the text extraction reads (historyText: no thinking,
// no harness markup in a user message), leaves out the current turn, and lists newest first;
// the off-path fallback lists the session file's entries off the branch in file order, each id
// labelled OffPathLabel.
func historyRelation(env Env) *relation {
	rel := &relation{name: SourceHistory, fields: HistoryFields, rows: [][]string{}, from: []string{}, to: []string{}, clip: map[string]bool{"content": true}}
	skip := env.memoryTools()
	entries := env.Entries
	if env.V2 && !env.offPath {
		entries = entries[:currentTurnStart(entries)]
	}
	for _, entry := range entries {
		role, text, ok := historyText(entry, env.V2, skip)
		if !ok {
			continue
		}
		id, keep := env.offPathRow(entry.ID)
		if !keep {
			continue
		}
		when := serialize.FormatRecallTimestamp(entry.Message["timestamp"], entryTimestamp(entry))
		day := dayOf(when)
		if day == "" {
			when = ""
		}
		rel.rows = append(rel.rows, []string{id, when, role, text})
		rel.from, rel.to = append(rel.from, day), append(rel.to, day)
	}
	if env.V2 && !env.offPath {
		reverseRelation(rel)
	}
	return rel
}

// reverseRelation reverses the rows' order (and their dates').
func reverseRelation(rel *relation) {
	for i, j := 0, len(rel.rows)-1; i < j; i, j = i+1, j-1 {
		rel.rows[i], rel.rows[j] = rel.rows[j], rel.rows[i]
		rel.from[i], rel.from[j] = rel.from[j], rel.from[i]
		rel.to[i], rel.to[j] = rel.to[j], rel.to[i]
	}
}

func entryTimestamp(entry ledger.Entry) any {
	if entry.Timestamp == "" {
		return nil
	}
	return entry.Timestamp
}

// savedRelation lists the curated memories visible from this project, ranked as the index
// ranks them, then the defined queries. A staged memory is listed only in the sessions that
// staged it (curated.SeenBy); under memory v2 each row carries its state (FieldState).
func savedRelation(env Env) (*relation, error) {
	if env.Saved == nil {
		return nil, errCuratedOff
	}
	store := env.Saved.Store
	listed, err := store.List(env.Saved.Project)
	if err != nil {
		return nil, err
	}
	memories := []curated.Memory{}
	for _, m := range listed {
		if curated.SeenBy(m, env.Saved.Source) {
			memories = append(memories, m)
		}
	}
	usage := store.RankUsage()
	fields := SavedFields
	if env.V2 {
		fields = SavedFieldsV2
	}
	rel := &relation{name: SourceSaved, fields: fields, rows: [][]string{}, from: []string{}, to: []string{}}
	for _, m := range curated.Rank(memories, usage) {
		scope := "global"
		if m.Project != "" {
			scope = "project"
		}
		day := m.Updated.Format("2006-01-02")
		row := []string{m.Name, m.Type, scope, day, m.Description, m.Body}
		if env.V2 {
			row = append(row, memoryState(m, usage[m.Name]))
		}
		rel.rows = append(rel.rows, row)
		rel.from, rel.to = append(rel.from, day), append(rel.to, day)
	}
	if env.V2 {
		// A memory file the validator refuses is not loaded, but never silently: its row names
		// it and why, so the model can edit it within the validator or forget it.
		refused, _ := store.Refused(env.Saved.Project)
		for _, r := range refused {
			rel.rows = append(rel.rows, []string{r.Name, "", "", "", r.Reason + refusedHint(r.Name), "", StateRefused})
			rel.from, rel.to = append(rel.from, ""), append(rel.to, "")
		}
	}
	definitions, err := store.ListDefinitions()
	if err != nil {
		return nil, err
	}
	for _, d := range definitions {
		day := d.Created.Format("2006-01-02")
		row := []string{d.Name, savedQueryType, "global", day, d.Description, d.Query}
		if env.V2 {
			row = append(row, "")
		}
		rel.rows = append(rel.rows, row)
		rel.from, rel.to = append(rel.from, day), append(rel.to, day)
	}
	return rel, nil
}

// refusedHint follows a refused memory's reason in saved: how to bring it back or retire it.
func refusedHint(name string) string {
	return "; fix it with edit name=" + name + " or retire it with forget name=" + name
}

// memoryState is a memory's FieldState value.
func memoryState(m curated.Memory, e curated.Evidence) string {
	switch {
	case m.Staged:
		return StateStaged
	case m.Pinned:
		return StatePinned
	case curated.Flagged(m, e):
		return StateFlagged
	}
	return StateLive
}

// errLayerOff is the harness source's and writes' error without a layer.
var errLayerOff = &Error{Msg: "the harness layer is not available here", Hint: "the layer needs the per-model harness and curated memory (curatedMemory: true)"}

// harnessRelation lists the model's layer (Layerer.Listing), undated.
func harnessRelation(env Env) (*relation, error) {
	if env.Layer == nil {
		return nil, errLayerOff
	}
	listing, err := env.Layer.Listing()
	if err != nil {
		return nil, err
	}
	rel := &relation{name: SourceHarness, fields: HarnessFields, rows: [][]string{}, from: []string{}, to: []string{}}
	for _, r := range listing {
		rel.rows = append(rel.rows, []string{r.Model, r.Kind, r.Name, r.State, r.Generation, r.Origin, r.Stale})
		rel.from, rel.to = append(rel.from, ""), append(rel.to, "")
	}
	return rel, nil
}

// runRecall reads one record back: an observation or reflection id with its sources (the
// recall package's lookup) and, for an observation under Env.CitedBy, what rests on it
// (citedBy), else an entry
// on the branch, else a saved memory or a defined query by name.
func runRecall(env Env, id string) (string, *recall.Details, []string, error) {
	if ledger.MemoryIDPattern.MatchString(id) {
		recallEntries := env.Entries
		if env.V2 {
			// Thinking is never returned or counted under v2, by the record's sources either.
			recallEntries = serialize.WithoutThinking(env.Entries)
		}
		text, details := recall.Execute(recallEntries, id)
		if details.Status != recall.StatusNotFound {
			var cited, lines []string
			if env.CitedBy {
				cited, lines = citedBy(env.Entries, id)
			}
			if len(lines) > 0 {
				text += "\n\n" + citedByHeading + "\n" + strings.Join(lines, "\n")
			}
			return text, &details, cited, nil
		}
	}
	for _, entry := range env.Entries {
		if entry.ID != id {
			continue
		}
		render := serialize.RenderRecallSourceEntry
		if env.V2 {
			render = serialize.RenderRecallSourceEntryText
		}
		rendered, ok := render(entry)
		if !ok || strings.TrimSpace(rendered) == "" {
			kind := entry.Type
			if entry.CustomType != "" {
				kind += " " + entry.CustomType
			}
			return "", nil, nil, fmt.Errorf("entry %s is a %s entry with no conversation text", id, kind)
		}
		return rendered, nil, nil, nil
	}
	if env.Saved != nil && curated.ValidateName(id) == nil {
		store := env.Saved.Store
		if m, err := store.GetAs(id, env.Saved.Project, env.Saved.Source); err == nil {
			_ = store.Record(id, curated.EventView)
			return curated.Describe(m), nil, nil, nil
		} else if !errors.Is(err, curated.ErrNotFound) {
			return "", nil, nil, err
		}
		if d, err := store.GetDefinition(id); err == nil {
			return curated.DescribeDefinition(d), nil, nil, nil
		}
	}
	return "", nil, nil, &Error{Msg: fmt.Sprintf("nothing called %q: no note or reflection id, entry id or saved name on this branch", id), Hint: `find ids with notes | search "words" or history | search "words", and names with saved`}
}

// citedByHeading opens recall's list of what rests on an observation.
const citedByHeading = "Cited by (reflections that rest on it; retiring it takes their support):"

// citedBy are the reflections that cite observation id, from the fold's reverse links
// (ledger.FoldedLedger.CitedBy, computed and never stored), with one line each; none for a
// reflection id or an observation nothing cites. Only reflections that still stand are listed
// (ledger.ActiveReflections): one a later reflection replaced rests on nothing the memory still
// holds, so retiring the record takes no support from it. A level-1 merge keeps its sources'
// entry ids, not their record ids, so what it replaced shows through superseded_by, not here.
func citedBy(entries []ledger.Entry, id string) ([]string, []string) {
	folded := ledger.FoldLedger(entries, "")
	if _, ok := folded.ObservationsByID[id]; !ok {
		return nil, nil
	}
	standing := map[string]bool{}
	for _, r := range ledger.ActiveReflections(folded.Reflections) {
		standing[r.ID] = true
	}
	var cited []string
	lines := []string{}
	for _, rid := range folded.CitedBy[id] {
		if !standing[rid] {
			continue
		}
		cited = append(cited, rid)
		lines = append(lines, ledger.ReflectionToSummaryLine(folded.ReflectionsByID[rid]))
	}
	return cited, lines
}
