// Package om is the memory tool's domain API (docs/code-tool.md, "The domain API"): the value,
// option and list types the agent's Go code reads, their pure methods, and the constants every
// read is bounded by. It sits at the bottom of the import graph: besides the standard library it
// imports only internal/tables (for the lists' Table) and its own transport, internal/om/omwire,
// and neither imports anything beyond the standard library. So every package that prints a
// snippet or bounds a list can import it without a cycle, and neither of those two may ever
// import om or a package that does.
//
// Step 1 of the design's order of work moves the read bounds, the notes and reflections fields
// and the layer listing's row type here out of the query language (internal/memquery), which
// keeps aliases to them until it is deleted.
package om

// The bounds of a list read: a read without a limit shows DefaultLimit rows, and no read shows
// more than MaxLimit.
const (
	DefaultLimit = 20
	MaxLimit     = 200
)

// RecallPageChars is the most one result shows: one recall page (serialize.RecallPageChars,
// which om does not import, to stay at the bottom of the graph; omhost's tests pin it). The
// code tool derives its bounds from it (docs/code-tool.md, "Limits: derived, not tuned"): a call's input, its
// result, one kept unit, and the code policy's constant-array size in bytes.
const RecallPageChars = 10_000

// HistoryClipChars bounds a history row's content in a result: a window of this many characters
// around the first searched word. A recall reads the entry in full.
const HistoryClipChars = 400

// The notes' fields memory v2's level 0 fills (docs/memory-v2.md, "Memory tool"), empty for a
// record without them: how the record was stated, the date it is about, and its first source
// entry with that entry's role and time.
const (
	// FieldStatus is the record's status (ledger.Observation.Status), or retracted when a
	// retraction withdrew it (ledger.RetractedIDs); "" when stated as fact.
	FieldStatus = "status"
	// FieldEventDate is the date the record is about (ledger.Observation.EventDate).
	FieldEventDate = "event_date"
	// FieldSource is the record's first source entry id; FieldSourceRole and FieldSourceTime
	// are that entry's role (as history names it) and time, "" when it is not on the branch.
	FieldSource     = "source"
	FieldSourceRole = "source_role"
	FieldSourceTime = "source_time"
)

// The notes' subject fields beside the canonical subject: the subject as the record wrote it
// and what it adds to the canonical one. They equal ledger.RawSubjectField and
// ledger.SubjectScopeField, which om does not import, to stay at the bottom of the graph
// (omhost's tests pin it).
const (
	FieldRawSubject   = "raw_subject"
	FieldSubjectScope = "subject_scope"
)

// The fields of each record list, in order.
var (
	NoteFields       = []string{"id", "when", "kind", "subject", "scope", "relevance", "content", "superseded_by", FieldRawSubject, FieldSubjectScope, FieldStatus, FieldEventDate, FieldSource, FieldSourceRole, FieldSourceTime}
	ReflectionFields = []string{"id", "dates", "content", "supports"}
)

// LevelZeroNoteFields are the fields level 0 fills, which a schema may have added before the
// notes listed them; the kernel owns them for schemas (kernel.Owns), so a schema written now
// cannot take them.
var LevelZeroNoteFields = []string{FieldStatus, FieldEventDate, FieldSource, FieldSourceRole, FieldSourceTime}

// LayerRow is one row of the layer listing: a definition of a model's layer, or a seed standing
// where the model defined nothing.
type LayerRow struct {
	// Model is the model whose layer holds the row.
	Model string
	// Kind and Name are the definition's kind and name.
	Kind, Name string
	// State is seed, held, written (with its probation: written, probation or established),
	// or skipped for a file the layer refused.
	State string
	// Generation is the generation that wrote it, "" for a seed or a held definition.
	Generation string
	// Origin is seed (shipped and standing), override (the model's definition of a seed's
	// name), extends or replaces (the model's instructions appended after the seed, or in its
	// place: their mode) or own (a name no seed has).
	Origin string
	// Stale is why an override was skipped as stale, "" when it is not.
	Stale string
	// Engine is the engine that wrote the definition (config.EngineV2), "" for a seed or one
	// written before engines were recorded.
	Engine string
	// Text is the definition's text.
	Text string
}
