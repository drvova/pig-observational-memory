// Package kernel is the part of the memory the model cannot change (docs/memory-interface.md,
// section 8), after nodecode's kernel floor: storage and the om.* record kinds, the compaction
// invariants, the query engine and its bounds, the memory tool, the event ledger and the
// kernel events it counts, the room ceiling, and the text that states these rules in every
// worker prompt.
//
// One ownership predicate, Owns, names it, and Check applies it to a definition. Every path that
// accepts a definition into the model-built layer (internal/layer) calls Check when the
// definition is made and again when it is loaded, so a definition that would replace, shadow
// or disable a kernel-owned name is refused at both ends with the same predicate, and the
// refusal names the owner.
//
// The package is a leaf: it lists the kernel's names as data, and its tests check the lists
// against the packages that define them, so it can be imported by every package that accepts
// a definition without an import cycle.
package kernel

import (
	"errors"
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// The definition kinds whose names or texts the kernel checks specially. They are the layer's
// kinds (layer.Kinds); the kernel names them as strings so it stays a leaf.
const (
	kindSchema   = "schema"
	kindRemedy   = "remedy"
	kindTool     = "tool"
	kindDetector = "detector"
	kindSummary  = "summary"
)

// ledgerKinds are the kinds that read or act on the event ledger and the room budget, so the
// only ones a kernel event or a room name can shadow: a detector counts events, a remedy sets
// what a pass does, a summary runs under the room budget. A tool named "error" shadows
// nothing, so those names stay free for tools, schemas, formats and instructions.
var ledgerKinds = []string{kindDetector, kindRemedy, kindSummary}

// The owners a refusal names.
const (
	OwnerMemoryTool  = "the memory tool"
	OwnerQuerySource = "the query engine (a source)"
	OwnerQueryStage  = "the query engine (a stage)"
	OwnerQueryWrite  = "the query engine (a write)"
	OwnerStorage     = "storage (an om.* record kind)"
	OwnerEventLedger = "the event ledger (a kernel event)"
	OwnerRoom        = "the room ceiling"
	OwnerInvariant   = "the compaction invariants (a setting or boundary)"
	OwnerPromptRules = "the kernel's fixed worker-prompt rules"
	OwnerNoteSchema  = "the note schema's kernel fields"
)

// entry is one kernel-owned name. Kinds limits it to definitions of those kinds; none means
// every kind.
type entry struct {
	name  string
	owner string
	kinds []string
}

// MemoryToolName is the base memory tool's name (memquery.ToolName).
const MemoryToolName = "memory"

// The query engine's vocabulary (memquery.Sources, memquery.Stages, memquery.Writes), with the
// writes section 8 adds for the layer.
var (
	// "harness" is also a source (it lists the layer); it is owned as the write it was first.
	querySources = []string{"notes", "reflections", "history", "saved", "recall"}
	queryStages  = []string{"search", "where", "since", "until", "sort", "limit", "count", "group"}
	queryWrites  = []string{"save", "edit", "forget", "feedback", "define", "propose", "harness", "rollback"}
)

// recordKinds are the om.* custom entry types (ledger.OM*).
var recordKinds = []string{"om.observations.recorded", "om.reflections.recorded", "om.observations.dropped", "om.folded"}

// kernelEvents are the event kinds and outcomes the usage ledger counts (curated.ModelKind*,
// curated.Outcome*): the evidence grading reads, which no definition may redefine.
var kernelEvents = []string{"memory", "decode", "defined", "pass", "ok", "error", "empty", "shown", "omitted", "columns"}

// roomNames are the room ceiling's names: the summary budget is derived from the room the
// next context has and nothing raises it.
var roomNames = []string{"room", "room-ceiling", "room-budget", "summary-budget"}

// invariantSettings are the settings and boundaries the compaction invariants rest on: never
// discard unobserved entries, valid cut points, positional coverage markers, the fold boundary.
var invariantSettings = []string{
	"compactAfterTokens", "compactAfterTokensMode", "compactAfterTokensRatio",
	"compactionMaxRetainedTokens", "compactionSummaryMaxTokens", "compactionCatchUpMaxChunks",
	"observationsPoolMaxTokens", "observationsPoolTargetTokens",
	"coverage", "coverage-marker", "cut-point", "fold-boundary", "firstKeptEntryId", "foldThroughEntryId",
	"kernelFloor",
}

// noteKernelFields are the fields every note carries that a schema definition only adds to.
var noteKernelFields = []string{"id", "timestamp", "content", "sources", "sourceEntryIds", "relevance"}

// owned is the kernel's list, built once from the groups above.
var owned = func() []entry {
	out := []entry{{name: MemoryToolName, owner: OwnerMemoryTool}}
	add := func(names []string, owner string, kinds ...string) {
		for _, n := range names {
			// A name already owned for every kind keeps its first owner ("memory" is the
			// tool before it is an event kind), so the list holds each name once.
			if taken(out, n) {
				continue
			}
			out = append(out, entry{name: n, owner: owner, kinds: kinds})
		}
	}
	add(querySources, OwnerQuerySource)
	add(queryStages, OwnerQueryStage)
	add(queryWrites, OwnerQueryWrite)
	add(recordKinds, OwnerStorage)
	add(kernelEvents, OwnerEventLedger, ledgerKinds...)
	add(roomNames, OwnerRoom, ledgerKinds...)
	add(invariantSettings, OwnerInvariant)
	add(TextIDs(), OwnerPromptRules)
	add(noteKernelFields, OwnerNoteSchema, kindSchema)
	return out
}()

// taken reports whether name is already owned for every kind in entries.
func taken(entries []entry, name string) bool {
	for _, e := range entries {
		if len(e.kinds) == 0 && normalize(e.name) == normalize(name) {
			return true
		}
	}
	return false
}

// normalize folds a name for comparison: case, '-', '_', '.', ':', '/' and spaces aside, so
// "om-folded", "OM_FOLDED" and "om.folded" are one name, and so are "compact-after-tokens" and
// "compactAfterTokens".
func normalize(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch r {
		case '-', '_', '.', ':', '/', ' ':
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Owns reports whether a definition of kind named name would replace, shadow or disable
// something the kernel owns, and names the owner. It is the one ownership predicate: the layer
// refuses such a definition when it is made and when it is loaded.
func Owns(kind, name string) (owner string, ok bool) {
	key := normalize(name)
	if key == "" {
		return "", false
	}
	for _, e := range owned {
		if normalize(e.name) != key {
			continue
		}
		if len(e.kinds) == 0 || contains(e.kinds, kind) {
			return e.owner, true
		}
	}
	return "", false
}

// Owned lists the kernel-owned names with their owners, for /om:status and tests.
func Owned() []Name {
	out := make([]Name, 0, len(owned))
	for _, e := range owned {
		out = append(out, Name{Name: e.name, Owner: e.owner, Kinds: append([]string(nil), e.kinds...)})
	}
	return out
}

// Name is one kernel-owned name as Owned lists it.
type Name struct {
	Name, Owner string
	// Kinds are the definition kinds it is owned for; none means every kind.
	Kinds []string
}

// Def is what Check needs of a definition: its kind, name and declarative text.
type Def struct {
	Kind, Name, Text string
}

// Refusal is Check's error: a definition that would replace, shadow or disable a kernel-owned
// name. It names the owner.
type Refusal struct {
	Kind, Name string
	// Field is where in the definition the name was found: "name", or a column of its text.
	Field string
	Owner string
	// found is the kernel-owned name when it was found in the text, not the definition's name.
	found string
}

// Error says what was refused, why, and who owns the name.
func (r *Refusal) Error() string {
	where := ""
	if r.Field != "" && r.Field != "name" {
		where = " (in its " + r.Field + ")"
	}
	return fmt.Sprintf("refused: the %s definition %q names %q%s, which %s owns; the kernel cannot be replaced, shadowed or disabled from the layer", r.Kind, r.Name, r.nameFound(), where, r.Owner)
}

// nameFound is the kernel-owned name itself, which may differ from the definition's name when
// it was found in the definition's text.
func (r *Refusal) nameFound() string {
	if r.found != "" {
		return r.found
	}
	return r.Name
}

// AsRefusal reports whether err is (or wraps) a kernel refusal.
func AsRefusal(err error) (*Refusal, bool) {
	var r *Refusal
	ok := errors.As(err, &r)
	return r, ok
}

// failClosed are the kinds whose text the kernel must read to check: a tool, a schema and a
// remedy are tables in the rows codec, and one the kernel cannot decode is refused rather than
// let through unchecked.
var failClosed = []string{kindTool, kindSchema, kindRemedy}

// checkedColumns are the columns of a definition's tables whose values name things: a tool's
// name, a schema's field names, a remedy's setting or tool. A kernel-owned value there shadows
// the kernel as surely as the definition's own name would.
var checkedColumns = []string{"name", "field", "setting", "tool"}

// Check refuses a definition that would replace, shadow or disable a kernel-owned name: by its
// own name, or by a name in one of its tables' naming columns. It runs with the floor engaged;
// Floor.Check is the same check honouring a recorded lowering.
func Check(d Def) error {
	if owner, ok := Owns(d.Kind, d.Name); ok {
		return &Refusal{Kind: d.Kind, Name: d.Name, Field: "name", Owner: owner}
	}
	decoded, err := tables.Rows.Decode(d.Text)
	if err != nil {
		if contains(failClosed, d.Kind) {
			return fmt.Errorf("refused: the %s definition %q is not a table the kernel can read, so it cannot be checked: %w", d.Kind, d.Name, err)
		}
		return nil
	}
	for _, t := range decoded {
		for _, column := range checkedColumns {
			c := t.Index(column)
			if c < 0 {
				continue
			}
			for r := range t.Rows {
				if t.IsAbsent(r, c) {
					continue
				}
				value := t.Rows[r][c]
				if owner, ok := Owns(d.Kind, value); ok {
					return &Refusal{Kind: d.Kind, Name: d.Name, Field: column, Owner: owner, found: value}
				}
			}
		}
	}
	return nil
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
