package memquery

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/recall"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
)

// The fields of each source's records.
var (
	NoteFields       = []string{"id", "when", "kind", "subject", "scope", "relevance", "content", "superseded_by"}
	ReflectionFields = []string{"id", "dates", "content", "supports"}
	HistoryFields    = []string{"id", "when", "role", "content"}
	SavedFields      = []string{"name", "type", "scope", "updated", "description", "body"}
)

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
// is resolved against the record's date, as the memory view writes it.
func notesRelation(env Env) *relation {
	folded := ledger.FoldLedger(env.Entries, "")
	supersededBy := map[string][]string{}
	for _, o := range folded.Observations {
		for _, id := range o.Supersedes {
			if id != o.ID && !contains(supersededBy[id], o.ID) {
				supersededBy[id] = append(supersededBy[id], o.ID)
			}
		}
	}
	rel := &relation{name: SourceNotes, fields: NoteFields, rows: [][]string{}, from: []string{}, to: []string{}}
	for _, o := range folded.ActiveObservations {
		day := dayOf(o.Timestamp)
		content := o.Content
		if day != "" {
			anchor, _ := time.Parse("2006-01-02", day)
			content = ledger.ResolveRelativeDates(content, anchor)
		}
		rel.rows = append(rel.rows, []string{o.ID, o.Timestamp, string(o.Kind), o.Subject, o.Scope, string(o.Relevance), content, strings.Join(supersededBy[o.ID], " ")})
		rel.from, rel.to = append(rel.from, day), append(rel.to, day)
	}
	return rel
}

// reflectionsRelation lists the folded ledger's reflections, dated by the observations they
// cite.
func reflectionsRelation(env Env) *relation {
	folded := ledger.FoldLedger(env.Entries, "")
	dates := ledger.ReflectionDateRanges(folded)
	rel := &relation{name: SourceReflections, fields: ReflectionFields, rows: [][]string{}, from: []string{}, to: []string{}}
	for _, r := range folded.Reflections {
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
