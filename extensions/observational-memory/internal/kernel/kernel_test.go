package kernel_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/memquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// The kernel's lists are data; these are the packages that define the names they hold, so a
// new verb, record kind or event cannot be added without the kernel owning it.
func TestKernelOwnsWhatTheCodeDefines(t *testing.T) {
	if kernel.MemoryToolName != memquery.ToolName {
		t.Fatalf("the kernel names the memory tool %q, memquery %q", kernel.MemoryToolName, memquery.ToolName)
	}
	groups := map[string][]string{
		kernel.OwnerMemoryTool:  {memquery.ToolName},
		kernel.OwnerQuerySource: memquery.Sources,
		kernel.OwnerQueryStage:  memquery.Stages,
		kernel.OwnerQueryWrite:  memquery.Writes,
		kernel.OwnerStorage:     {ledger.OMObservationsRecorded, ledger.OMReflectionsRecorded, ledger.OMObservationsDropped, ledger.OMFolded, ledger.OMSubjectsAliased},
		kernel.OwnerEventLedger: {curated.ModelKindMemory, curated.ModelKindDecode, curated.ModelKindDefined, curated.ModelKindPass,
			curated.OutcomeOK, curated.OutcomeError, curated.OutcomeEmpty, curated.OutcomeShown, curated.OutcomeOmitted, curated.OutcomeColumns},
		kernel.OwnerInvariant:   {"compactAfterTokens", "compactionMaxRetainedTokens", "compactionSummaryMaxTokens", "compactionCatchUpMaxChunks", "kernelFloor"},
		kernel.OwnerPromptRules: kernel.TextIDs(),
		kernel.OwnerSubjects:    ledger.SubjectCanonicalizationNames,
	}
	for owner, names := range groups {
		for _, name := range names {
			// A detector reads the event ledger, so every group is owned for it.
			got, ok := kernel.Owns(layer.KindDetector, name)
			// "memory" is both the tool and an event kind; the tool owns it first.
			if name == memquery.ToolName && ok {
				continue
			}
			// "harness" is a source and a write: whichever group lists it first owns it.
			if ok && contains(memquery.Sources, name) && contains(memquery.Writes, name) && (got == kernel.OwnerQuerySource || got == kernel.OwnerQueryWrite) {
				continue
			}
			if !ok || got != owner {
				t.Errorf("Owns(detector, %q) = %q, %v; want %q", name, got, ok, owner)
			}
		}
	}
	// The notes source's level-0 fields are owned for schemas, so no schema field shadows one,
	// and a schema field cannot take them either.
	for _, name := range memquery.LevelZeroNoteFields {
		if got, ok := kernel.Owns(layer.KindSchema, name); !ok || got != kernel.OwnerNoteSchema {
			t.Errorf("Owns(schema, %q) = %q, %v; want %q", name, got, ok, kernel.OwnerNoteSchema)
		}
		if ledger.SchemaAddedFieldName(name) {
			t.Errorf("a schema can still add the field %q", name)
		}
	}
	for _, id := range kernel.TextIDs() {
		if strings.TrimSpace(kernel.Text(id)) == "" {
			t.Errorf("kernel text %s is empty", id)
		}
	}
}

// Every kernel-owned name is refused for every kind it is owned for, with an error naming its
// owner, and a name owned for one kind only is free for the others.
func TestCheckRefusesEveryKernelNameForEveryKind(t *testing.T) {
	for _, n := range kernel.Owned() {
		for _, kind := range layer.Kinds {
			err := kernel.Check(kernel.Def{Kind: kind, Name: n.Name, Text: "x"})
			ownedHere := len(n.Kinds) == 0 || contains(n.Kinds, kind)
			r, refused := kernel.AsRefusal(err)
			if ownedHere {
				if !refused || r.Owner != n.Owner || !strings.Contains(err.Error(), n.Owner) {
					t.Errorf("%s %q: %v; want a refusal naming %q", kind, n.Name, err, n.Owner)
				}
				continue
			}
			if refused {
				t.Errorf("%s %q is owned only for %v but was refused: %v", kind, n.Name, n.Kinds, err)
			}
		}
	}
	for _, kind := range layer.Kinds {
		if err := kernel.Check(kernel.Def{Kind: kind, Name: "bake-count", Text: "definition[1]{name}:\n  bake-count"}); err != nil {
			t.Errorf("%s bake-count: %v", kind, err)
		}
	}
}

// Spelling does not get a definition past the kernel.
func TestOwnsIgnoresSpelling(t *testing.T) {
	for _, name := range []string{"om-folded", "OM_FOLDED", "Memory", "compact-after-tokens", "room_ceiling", "kernel.observer"} {
		if _, ok := kernel.Owns(layer.KindRemedy, name); !ok {
			t.Errorf("%q is not owned", name)
		}
	}
	if _, ok := kernel.Owns(layer.KindTool, "relevance"); ok {
		t.Error("relevance is a kernel field of the note schema only")
	}
	if _, ok := kernel.Owns(layer.KindSchema, "relevance"); !ok {
		t.Error("a schema cannot redefine relevance")
	}
}

// A definition shadows the kernel through its text as well as its name: a tool whose row
// names the memory tool, a schema field the kernel fields hold, a remedy setting an invariant.
func TestCheckRefusesKernelNamesInText(t *testing.T) {
	enc := func(name string, fields []string, row ...string) string {
		return tables.Rows.Encode(tables.Table{Name: name, Fields: fields, Rows: [][]string{row}})
	}
	cases := []struct {
		def   kernel.Def
		owner string
	}{
		{kernel.Def{Kind: layer.KindTool, Name: "helper", Text: enc("definition", []string{"name", "query"}, "memory", "notes")}, kernel.OwnerMemoryTool},
		{kernel.Def{Kind: layer.KindSchema, Name: "my-notes", Text: enc("fields", []string{"name", "description"}, "relevance", "how much it matters")}, kernel.OwnerNoteSchema},
		{kernel.Def{Kind: layer.KindRemedy, Name: "bigger", Text: enc("remedy", []string{"setting", "value"}, "compactionSummaryMaxTokens", "999999")}, kernel.OwnerInvariant},
		{kernel.Def{Kind: layer.KindRemedy, Name: "roomier", Text: enc("remedy", []string{"setting", "value"}, "room", "2")}, kernel.OwnerRoom},
		{kernel.Def{Kind: layer.KindDetector, Name: "counts", Text: enc("detector", []string{"tool"}, "memory")}, kernel.OwnerMemoryTool},
	}
	for _, c := range cases {
		err := kernel.Check(c.def)
		r, ok := kernel.AsRefusal(err)
		if !ok || r.Owner != c.owner {
			t.Errorf("%s %s: %v; want a refusal naming %q", c.def.Kind, c.def.Name, err, c.owner)
		}
	}
	// A schema cannot add a field shadowing the notes source's raw subject, and a summary
	// cannot take the canonicalization's name.
	for _, def := range []kernel.Def{
		{Kind: layer.KindSchema, Name: "my-notes", Text: enc("fields", []string{"name"}, ledger.RawSubjectField)},
		{Kind: layer.KindSummary, Name: ledger.CanonicalSubjectsName, Text: "notes | group by raw_subject"},
		{Kind: layer.KindTool, Name: "Subject_Key", Text: enc("definition", []string{"name", "query"}, "Subject_Key", "notes")},
	} {
		if r, ok := kernel.AsRefusal(kernel.Check(def)); !ok || r.Owner != kernel.OwnerSubjects {
			t.Errorf("%s %s: %v; want a refusal naming %q", def.Kind, def.Name, r, kernel.OwnerSubjects)
		}
	}
	// A schema with fields of its own passes.
	if err := kernel.Check(kernel.Def{Kind: layer.KindSchema, Name: "my-notes", Text: enc("fields", []string{"name"}, "mood")}); err != nil {
		t.Fatal(err)
	}
}

// A table the kernel cannot read is refused for the kinds that are tables, since it cannot be
// checked; text kinds pass.
func TestCheckFailsClosedOnUnreadableTables(t *testing.T) {
	for _, kind := range []string{layer.KindTool, layer.KindSchema, layer.KindRemedy} {
		if err := kernel.Check(kernel.Def{Kind: kind, Name: "odd", Text: "not[2]{a}:\n  one"}); err == nil {
			t.Errorf("%s: an unreadable table was accepted", kind)
		}
	}
	if err := kernel.Check(kernel.Def{Kind: layer.KindInstructions, Name: "observer", Text: "Record every number the user gives, with its unit."}); err != nil {
		t.Fatal(err)
	}
}

// The floor lowers only with who asked and why, and only after recording both; a lowering that
// cannot be recorded does not happen. Lowered, Check admits kernel names; engaged again, it
// refuses them, and the engagement is recorded too.
func TestFloorRecordsBeforeItLowers(t *testing.T) {
	memoryTool := kernel.Def{Kind: layer.KindTool, Name: "memory", Text: "x"}
	var nilFloor *kernel.Floor
	if err := nilFloor.Check(memoryTool); err == nil {
		t.Fatal("a nil floor is engaged")
	}
	var records []kernel.Lowering
	f := &kernel.Floor{}
	f.Record = func(l kernel.Lowering) error {
		if _, lowered := f.Lowered(); lowered {
			t.Fatal("the floor was lowered before its lowering was recorded")
		}
		records = append(records, l)
		return nil
	}
	for _, args := range [][2]string{{"", "testing"}, {"me", ""}, {"  ", "  "}} {
		if err := f.Lower(args[0], args[1]); err == nil {
			t.Fatalf("lowered without who and why: %q", args)
		}
	}
	if _, lowered := f.Lowered(); lowered || len(records) != 0 {
		t.Fatalf("a refused lowering took effect or was recorded: %+v", records)
	}
	failing := &kernel.Floor{Record: func(kernel.Lowering) error { return errors.New("disk full") }}
	if err := failing.Lower("me", "testing"); err == nil {
		t.Fatal("an unrecorded lowering was accepted")
	}
	if _, lowered := failing.Lowered(); lowered || failing.Check(memoryTool) == nil {
		t.Fatal("an unrecorded lowering took effect")
	}
	if err := (&kernel.Floor{}).Lower("me", "testing"); err == nil {
		t.Fatal("a floor with no record was lowered")
	}

	if err := f.Lower("drvova", "rebuilding the memory tool"); err != nil {
		t.Fatal(err)
	}
	l, lowered := f.Lowered()
	if !lowered || l.RequestedBy != "drvova" || l.Reason != "rebuilding the memory tool" || len(records) != 1 || records[0].Op != kernel.OpLower {
		t.Fatalf("lowering %+v, records %+v", l, records)
	}
	if err := f.Check(memoryTool); err != nil {
		t.Fatalf("a lowered floor refused: %v", err)
	}
	if kernel.Check(memoryTool) == nil {
		t.Fatal("the package Check honours a floor")
	}
	if err := f.Engage(); err != nil {
		t.Fatal(err)
	}
	if f.Check(memoryTool) == nil || len(records) != 2 || records[1].Op != kernel.OpEngage {
		t.Fatalf("engaging: records %+v", records)
	}
}

func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}

// OwnsForEveryKind names only what no definition of any kind may take, whatever its spelling,
// the path separator included: subject canonicalization refuses subject paths and aliases
// that spell one.
func TestOwnsForEveryKind(t *testing.T) {
	for _, name := range []string{"om/subjects/aliased", "om.subjects.aliased", "raw/subject", "memory", "notes", "canonical/subjects"} {
		if _, ok := kernel.OwnsForEveryKind(name); !ok {
			t.Errorf("%q is not owned for every kind", name)
		}
	}
	// "room" is owned only for the ledger kinds, "relevance" only for schemas.
	for _, name := range []string{"room", "relevance", "running", ""} {
		if owner, ok := kernel.OwnsForEveryKind(name); ok {
			t.Errorf("%q is owned for every kind by %s", name, owner)
		}
	}
}
