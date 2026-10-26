package memquery

import (
	"errors"
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omhost"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/recall"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
)

// The fields of each source's records. NoteFields, ReflectionFields and the level-0 fields
// moved to internal/om (docs/code-tool.md, "Order of work", step 1); these alias them.
var (
	NoteFields       = om.NoteFields
	ReflectionFields = om.ReflectionFields
	HistoryFields    = omhost.HistoryFields
	SavedFields      = []string{"name", "type", "scope", "updated", "description", "body"}
	// SavedFieldsV2 are saved's fields under memory v2: SavedFields and each memory's state
	// (FieldState).
	SavedFieldsV2 = append(append([]string{}, SavedFields...), FieldState)
	HarnessFields = []string{"model", "kind", "name", "state", "generation", "origin", "stale"}
)

// The notes source's fields memory v2's level 0 fills (om.FieldStatus and the others).
const (
	FieldStatus     = om.FieldStatus
	FieldEventDate  = om.FieldEventDate
	FieldSource     = om.FieldSource
	FieldSourceRole = om.FieldSourceRole
	FieldSourceTime = om.FieldSourceTime
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

// LevelZeroNoteFields are the fields above (om.LevelZeroNoteFields).
var LevelZeroNoteFields = om.LevelZeroNoteFields

// laterMark follows a superseded_by id recorded after the bound of the point-in-time view
// (omhost.LaterMark).
const laterMark = omhost.LaterMark

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

// notesRelation lists the notes (omhost.Reader.Notes): the folded ledger's active
// observations, or the point-in-time view's when view is set.
func notesRelation(env Env, view *pointInTime) *relation {
	return newRelation(SourceNotes, env.reader().Notes(view.bound()))
}

// reflectionsRelation lists the reflections (omhost.Reader.Reflections).
func reflectionsRelation(env Env, view *pointInTime) *relation {
	return newRelation(SourceReflections, env.reader().Reflections(view.bound()))
}

// historyRelation lists the branch's user, assistant and tool text (omhost.Reader.History); the
// calls and results of the memory tool and of the tools the agent defined with it are left out.
func historyRelation(env Env) *relation {
	return newRelation(SourceHistory, env.reader().History())
}

// newRelation is a read's rows as the relation the stages carry.
func newRelation(name string, rows omhost.Rows) *relation {
	return &relation{name: name, fields: rows.Fields, rows: rows.Rows, from: rows.From, to: rows.To, clip: rows.Clip, also: rows.Also}
}

// savedRelation lists the curated memories visible from this project, ranked as the index
// ranks them, then the defined queries. A staged memory is listed only in the sessions that
// staged it (curated.SeenBy); on a tool call each row carries its state (FieldState).
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
	if env.ToolCall {
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
		if env.ToolCall {
			row = append(row, memoryState(m, usage[m.Name]))
		}
		rel.rows = append(rel.rows, row)
		rel.from, rel.to = append(rel.from, day), append(rel.to, day)
	}
	if env.ToolCall {
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
		if env.ToolCall {
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
		if env.ToolCall {
			// Thinking is never returned or counted under v2, by the record's sources either.
			recallEntries = serialize.WithoutThinking(env.Entries)
		}
		text, details := recall.Execute(recallEntries, id)
		if details.Status != recall.StatusNotFound {
			var cited, lines []string
			if env.CitedBy {
				cited, lines = omhost.CitedBy(env.Entries, id)
			}
			text = omhost.WithCitedBy(text, lines)
			return text, &details, cited, nil
		}
	}
	for _, entry := range env.Entries {
		if entry.ID != id {
			continue
		}
		render := serialize.RenderRecallSourceEntry
		if env.ToolCall {
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

// citedByHeading opens recall's list of what rests on an observation (omhost.CitedByHeading).
const citedByHeading = omhost.CitedByHeading

// shadowedFieldPrefix names, in the notes source, a schema-added field whose name the kernel
// later took (omhost.ShadowedFieldPrefix).
const shadowedFieldPrefix = omhost.ShadowedFieldPrefix
