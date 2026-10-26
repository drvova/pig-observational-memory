package om

import (
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
)

// Note is one level-0 or level-1 record. Every value is the child's own copy of what the
// extension sent: changing a field, a slice element or a map entry changes only that copy.
type Note struct {
	// ID is the record id.
	ID string
	// When is the time the record was taken.
	When string
	// Kind is the record's kind (fact, decision, file, commit, attempt, correction, ...).
	Kind string
	// Subject is the canonical subject; RawSubject is the subject as the record wrote it, and
	// SubjectScope what RawSubject adds to the canonical one.
	Subject      string
	RawSubject   string
	SubjectScope string
	// Scope is the scope as stored.
	Scope string
	// Relevance is how much the record matters (high, medium, low).
	Relevance string
	// Content is the record's text, relative dates resolved against its date.
	Content string
	// Status is proposed, hypothetical or retracted; "" when stated as fact.
	Status string
	// EventDate is the date the record is about, "" when it names none.
	EventDate string
	// Origin is who appended it: code (level 0), model (level 1) or host.
	Origin string
	// Outcome is an attempt's outcome: failed, worked, or "" while open.
	Outcome string
	// Tool and Target are the tool and the path or object an attempt or file record names.
	Tool   string
	Target string
	// Source is the record's first source entry id; SourceRole and SourceTime are that entry's
	// role and time, "" when it is not on the branch.
	Source     string
	SourceRole string
	SourceTime string
	// SupersededBy are the records that supersede this one; Supersedes the ones it supersedes.
	SupersededBy []string
	Supersedes   []string
	// PossiblyUpdates are records this one may update, not yet settled.
	PossiblyUpdates []string
	// Conflicts are records this one conflicts with.
	Conflicts []string
	// Replaces are the records a level-1 record replaced.
	Replaces []string
	// SourceEntryIDs are the entries the record was taken from.
	SourceEntryIDs []string
	// Fields are the record's other fields by name (a schema's, read as schema_<name> when one
	// collides with a level-0 column).
	Fields map[string]string
	// Anchor is the entry id the record's summary line ends with.
	Anchor string
	// OffPath is true for a record read from the off-path fallback.
	OffPath bool
	// Dropped is true for a record dropped by rule (only from NoteByID and Dropped).
	Dropped bool
	// Standing is the record standing for this one when it was replaced.
	Standing string
}

// Line is the record as one summary line, ending with its anchor.
func (n Note) Line() string {
	var b strings.Builder
	if n.Kind != "" {
		b.WriteString("[" + n.Kind + "] ")
	}
	if n.Subject != "" {
		b.WriteString(n.Subject + ": ")
	}
	b.WriteString(n.Content)
	if n.Anchor != "" {
		b.WriteString(" " + n.Anchor)
	}
	return b.String()
}

// Recall reads the record's sources: om.Recall(n.ID).
func (n Note) Recall() (Text, error) { return Recall(n.ID) }

// CitedBy lists the standing reflections that cite the record.
func (n Note) CitedBy() ReflectionList {
	var l ReflectionList
	mustCall(omwire.MCitedBy, &l, n.ID)
	return l
}

// field is the value of a column by its listing name.
// offPathLabel follows the id of a row the off-path fallback read, as a table shows it.
const offPathLabel = " (off-path)"

// cell is a field as a table shows it: the id of an off-path row labelled.
func (n Note) cell(name string) string {
	if name == "id" && n.OffPath {
		return n.ID + offPathLabel
	}
	return n.field(name)
}

func (n Note) field(name string) string {
	switch name {
	case "id":
		return n.ID
	case "when":
		return n.When
	case "kind":
		return n.Kind
	case "subject":
		return n.Subject
	case FieldRawSubject:
		return n.RawSubject
	case FieldSubjectScope:
		return n.SubjectScope
	case "scope":
		return n.Scope
	case "relevance":
		return n.Relevance
	case "content":
		return n.Content
	case FieldStatus:
		return n.Status
	case FieldEventDate:
		return n.EventDate
	case "origin":
		return n.Origin
	case "outcome":
		return n.Outcome
	case "tool":
		return n.Tool
	case "target":
		return n.Target
	case FieldSource:
		return n.Source
	case FieldSourceRole:
		return n.SourceRole
	case FieldSourceTime:
		return n.SourceTime
	case "superseded_by":
		return strings.Join(n.SupersededBy, " ")
	case "supersedes":
		return strings.Join(n.Supersedes, " ")
	case "possibly_updates":
		return strings.Join(n.PossiblyUpdates, " ")
	case "conflicts":
		return strings.Join(n.Conflicts, " ")
	case "replaces":
		return strings.Join(n.Replaces, " ")
	case "anchor":
		return n.Anchor
	case "standing":
		return n.Standing
	}
	return n.Fields[name]
}

// Reflection is one standing reflection.
type Reflection struct {
	// ID is the reflection id.
	ID string
	// Dates are the dates of the records it cites (a day, or first..last).
	Dates string
	// Content is the reflection's text.
	Content string
	// Supports are the record ids it cites.
	Supports []string
	// Supersedes are the reflections it replaced.
	Supersedes []string
	// Anchor is the entry id its summary line ends with.
	Anchor string
	// OffPath is true for a reflection read from the off-path fallback.
	OffPath bool
}

// Line is the reflection as one summary line, ending with its anchor.
func (r Reflection) Line() string {
	s := r.Content
	if r.Dates != "" {
		s = r.Dates + " " + s
	}
	if r.Anchor != "" {
		s += " " + r.Anchor
	}
	return s
}

// Supporting lists the records the reflection cites.
func (r Reflection) Supporting() NoteList {
	var l NoteList
	mustCall(omwire.MSupporting, &l, r.ID)
	return l
}

// cell is a field as a table shows it: the id of an off-path reflection labelled.
func (r Reflection) cell(name string) string {
	if name == "id" && r.OffPath {
		return r.ID + offPathLabel
	}
	return r.field(name)
}

func (r Reflection) field(name string) string {
	switch name {
	case "id":
		return r.ID
	case "dates":
		return r.Dates
	case "content":
		return r.Content
	case "supports":
		return strings.Join(r.Supports, " ")
	case "supersedes":
		return strings.Join(r.Supersedes, " ")
	case "anchor":
		return r.Anchor
	}
	return ""
}

// Entry is one user, assistant or tool message of the conversation, as history shows it.
type Entry struct {
	// ID is the entry id.
	ID string
	// When is the entry's time.
	When string
	// Role is user, assistant or tool.
	Role string
	// Text is what history reads of the entry, without thinking.
	Text string
	// Clipped is true when Text is a window around the first searched word.
	Clipped bool
	// OffPath is true for an entry that is not on the current branch.
	OffPath bool
}

// Recall reads the whole entry, paged.
func (e Entry) Recall() (Text, error) { return Recall(e.ID) }

// cell is a field as a table shows it: the id of an off-path entry labelled.
func (e Entry) cell(name string) string {
	if name == "id" && e.OffPath {
		return e.ID + offPathLabel
	}
	return e.field(name)
}

func (e Entry) field(name string) string {
	switch name {
	case "id":
		return e.ID
	case "when":
		return e.When
	case "role":
		return e.Role
	case "text":
		return e.Text
	}
	return ""
}

// Memory is one curated memory.
type Memory struct {
	// Name is the memory's name, which Recall, Edit, ForgetMemory and Feedback take.
	Name string
	// Type is the memory's type.
	Type string
	// Scope is global or project.
	Scope string
	// Updated is the day it was last written.
	Updated string
	// Description is the memory's one line.
	Description string
	// Body is the memory's text.
	Body string
	// State is live, staged, flagged, pinned or refused.
	State string
	// Reason is why a refused memory file was not loaded.
	Reason string
}

func (m Memory) field(name string) string {
	switch name {
	case "name":
		return m.Name
	case "type":
		return m.Type
	case "scope":
		return m.Scope
	case "updated":
		return m.Updated
	case "description":
		return m.Description
	case "body":
		return m.Body
	case "state":
		return m.State
	case "reason":
		return m.Reason
	}
	return ""
}

func (r LayerRow) field(name string) string {
	switch name {
	case "model":
		return r.Model
	case "kind":
		return r.Kind
	case "name":
		return r.Name
	case "state":
		return r.State
	case "generation":
		return r.Generation
	case "origin":
		return r.Origin
	case "stale":
		return r.Stale
	case "engine":
		return r.Engine
	case "text":
		return r.Text
	}
	return ""
}

// RetiredQuery is a defined query of the removed query language, never run.
type RetiredQuery struct {
	// Name is the defined query's name.
	Name string
	// Origin is where it is stored: layer (a tool definition) or legacy (definitions/*.rows).
	Origin string
	// Query is its stored query text.
	Query string
	// Snippet is the equivalent code for a pure read, "" when there is none.
	Snippet string
	// Reason is why there is no snippet.
	Reason string
}

func (r RetiredQuery) field(name string) string {
	switch name {
	case "name":
		return r.Name
	case "origin":
		return r.Origin
	case "query":
		return r.Query
	case "snippet":
		return r.Snippet
	case "reason":
		return r.Reason
	}
	return ""
}

// Text is a page of recalled text.
type Text struct {
	// ID is what was recalled: a record id, an entry id or a saved name.
	ID string
	// Body is the page's text.
	Body string
	// Status is a record recall's status (found, or how it was found).
	Status string
	// From and To are the page's character offsets, Total the text's length in characters.
	From, To, Total int
	// Next is the snippet for the next page, "" on the last.
	Next string
	// CitedBy are the standing reflections citing a recalled record.
	CitedBy []string
}

// String is the page, then where it sits and the next page's snippet when there is one.
func (t Text) String() string {
	var b strings.Builder
	b.WriteString(t.Body)
	if t.Total > 0 && (t.From > 0 || t.To < t.Total) {
		fmt.Fprintf(&b, "\n[characters %d-%d of %d]", t.From, t.To, t.Total)
	}
	if t.Next != "" {
		b.WriteString("\nnext page: " + t.Next)
	}
	if len(t.CitedBy) > 0 {
		b.WriteString("\ncited by: " + strings.Join(t.CitedBy, " "))
	}
	return b.String()
}

// StatusInfo is the /om:status figures that belong to no model, then each model's.
type StatusInfo struct {
	// Lines are the figures that belong to no model.
	Lines []string
	// Models are one block per model with usage events, sorted by model name.
	Models []ModelStatus
}

// ModelStatus is one model's profile figures, computed from its own usage.
type ModelStatus struct {
	// Model is the model the figures belong to.
	Model string
	// Lines are its figures.
	Lines []string
}

// String is the model-independent figures, then one block per model.
func (s StatusInfo) String() string {
	var b strings.Builder
	for _, l := range s.Lines {
		b.WriteString(l + "\n")
	}
	for _, m := range s.Models {
		b.WriteString("model " + m.Model + ":\n")
		for _, l := range m.Lines {
			b.WriteString("  " + l + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}
