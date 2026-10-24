package memquery

import (
	"errors"
	"fmt"
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
	NoteFields       = []string{"id", "when", "kind", "subject", "scope", "relevance", "content", "superseded_by", ledger.RawSubjectField, ledger.SubjectScopeField}
	ReflectionFields = []string{"id", "dates", "content", "supports"}
	HistoryFields    = []string{"id", "when", "role", "content"}
	SavedFields      = []string{"name", "type", "scope", "updated", "description", "body"}
	HarnessFields    = []string{"model", "kind", "name", "state", "generation", "origin", "stale"}
)

// shadowedFieldPrefix names, in the notes source, a schema-added field whose name the kernel
// later took for a subject field.
const shadowedFieldPrefix = "schema_"

// savedQueryType is the type a defined query is listed under in saved.
const savedQueryType = "query"

// loadSource reads a source's records, in their natural order: notes, reflections and history
// as they were recorded, saved as the memory index ranks them, then defined queries by name.
func loadSource(env Env, source string) (*relation, error) {
	switch source {
	case SourceNotes:
		return notesRelation(env), nil
	case SourceReflections:
		return reflectionsRelation(env), nil
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
// its rows), and the stored record stays readable.
func notesRelation(env Env) *relation {
	var all, active []ledger.Observation
	var aliases []ledger.SubjectAlias
	var contextSubjects []string
	if env.Records != nil {
		all, active, aliases, contextSubjects = env.Records.Observations, env.Records.Observations, env.Records.Aliases, env.Records.SubjectContext
	} else {
		folded := ledger.FoldLedger(env.Entries, "")
		all, active, aliases = folded.Observations, folded.ActiveObservations, folded.Aliases
	}
	supersededBy := map[string][]string{}
	for _, o := range all {
		for _, id := range o.Supersedes {
			if id != o.ID && !contains(supersededBy[id], o.ID) {
				supersededBy[id] = append(supersededBy[id], o.ID)
			}
		}
	}
	// Fields a note schema added (Observation.Fields) follow the kernel's, by name, when any
	// note carries one; a note without one has it empty. A field a schema added before the
	// kernel took its name (raw_subject, subject_scope) is listed as schema_<name>, so its
	// values stay readable beside the kernel's field.
	added := []string{}
	columns := map[string]string{}
	for _, o := range active {
		for name := range o.Fields {
			column := name
			if contains(ledger.SubjectCanonicalizationNames, name) {
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
	rel := &relation{name: SourceNotes, fields: fields, rows: [][]string{}, from: []string{}, to: []string{}, also: map[string]string{"subject": ledger.RawSubjectField}}
	for _, o := range active {
		day := dayOf(o.Timestamp)
		content := o.Content
		if day != "" {
			anchor, _ := time.Parse("2006-01-02", day)
			content = ledger.ResolveRelativeDates(content, anchor)
		}
		subject := canonical[o.Subject]
		row := []string{o.ID, o.Timestamp, string(o.Kind), subject.Subject, o.Scope, string(o.Relevance), content, strings.Join(supersededBy[o.ID], " "), o.Subject, subject.ExtraScope}
		for _, column := range added {
			row = append(row, o.Fields[columns[column]])
		}
		rel.rows = append(rel.rows, row)
		rel.from, rel.to = append(rel.from, day), append(rel.to, day)
	}
	return rel
}

// reflectionsRelation lists the folded ledger's reflections, dated by the observations they
// cite.
func reflectionsRelation(env Env) *relation {
	var reflections []ledger.Reflection
	var dates map[string]string
	if env.Records != nil {
		reflections, dates = env.Records.Reflections, env.Records.ReflectionDates
	} else {
		folded := ledger.FoldLedger(env.Entries, "")
		reflections, dates = folded.Reflections, ledger.ReflectionDateRanges(folded)
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
func historyRelation(env Env) *relation {
	rel := &relation{name: SourceHistory, fields: HistoryFields, rows: [][]string{}, from: []string{}, to: []string{}, clip: map[string]bool{"content": true}}
	skip := env.memoryTools()
	for _, entry := range env.Entries {
		role, text, ok := serialize.HistoryText(entry, skip...)
		if !ok {
			continue
		}
		when := serialize.FormatRecallTimestamp(entry.Message["timestamp"], entryTimestamp(entry))
		day := dayOf(when)
		if day == "" {
			when = ""
		}
		rel.rows = append(rel.rows, []string{entry.ID, when, role, text})
		rel.from, rel.to = append(rel.from, day), append(rel.to, day)
	}
	return rel
}

func entryTimestamp(entry ledger.Entry) any {
	if entry.Timestamp == "" {
		return nil
	}
	return entry.Timestamp
}

// savedRelation lists the curated memories visible from this project, ranked as the index
// ranks them, then the defined queries.
func savedRelation(env Env) (*relation, error) {
	if env.Saved == nil {
		return nil, errCuratedOff
	}
	store := env.Saved.Store
	memories, err := store.List(env.Saved.Project)
	if err != nil {
		return nil, err
	}
	usage, _ := store.Usage()
	rel := &relation{name: SourceSaved, fields: SavedFields, rows: [][]string{}, from: []string{}, to: []string{}}
	for _, m := range curated.Rank(memories, usage) {
		scope := "global"
		if m.Project != "" {
			scope = "project"
		}
		day := m.Updated.Format("2006-01-02")
		rel.rows = append(rel.rows, []string{m.Name, m.Type, scope, day, m.Description, m.Body})
		rel.from, rel.to = append(rel.from, day), append(rel.to, day)
	}
	definitions, err := store.ListDefinitions()
	if err != nil {
		return nil, err
	}
	for _, d := range definitions {
		day := d.Created.Format("2006-01-02")
		rel.rows = append(rel.rows, []string{d.Name, savedQueryType, "global", day, d.Description, d.Query})
		rel.from, rel.to = append(rel.from, day), append(rel.to, day)
	}
	return rel, nil
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
// recall package's lookup), else an entry on the branch, else a saved memory or a defined
// query by name.
func runRecall(env Env, id string) (string, *recall.Details, error) {
	if ledger.MemoryIDPattern.MatchString(id) {
		text, details := recall.Execute(env.Entries, id)
		if details.Status != recall.StatusNotFound {
			return text, &details, nil
		}
	}
	for _, entry := range env.Entries {
		if entry.ID != id {
			continue
		}
		rendered, ok := serialize.RenderRecallSourceEntry(entry)
		if !ok || strings.TrimSpace(rendered) == "" {
			kind := entry.Type
			if entry.CustomType != "" {
				kind += " " + entry.CustomType
			}
			return "", nil, fmt.Errorf("entry %s is a %s entry with no conversation text", id, kind)
		}
		return rendered, nil, nil
	}
	if env.Saved != nil && curated.ValidateName(id) == nil {
		store := env.Saved.Store
		if m, err := store.GetIn(id, env.Saved.Project); err == nil {
			_ = store.Record(id, curated.EventView)
			return curated.Describe(m), nil, nil
		} else if !errors.Is(err, curated.ErrNotFound) {
			return "", nil, err
		}
		if d, err := store.GetDefinition(id); err == nil {
			return curated.DescribeDefinition(d), nil, nil
		}
	}
	return "", nil, &Error{Msg: fmt.Sprintf("nothing called %q: no note or reflection id, entry id or saved name on this branch", id), Hint: `find ids with notes | search "words" or history | search "words", and names with saved`}
}
