package omhost

import (
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
)

// DetectorPass is what one compaction's detector runs read (docs/code-tool.md, "The detector
// om"): the source entries the compaction removed and the level-0 records extracted from them,
// built by the hook as plain data beside the summary, so nothing in it refers to live state.
type DetectorPass struct {
	// Covered are the source entries the compaction removed (from the previous fold boundary to
	// the cut): History reads them, filtered as a detector's read is (no current-turn cut, no
	// tool-call shadowing).
	Covered []ledger.Entry
	// Notes are the level-0 records of the batches the cut folds: Notes reads them.
	Notes []ledger.Observation
	// Known are the active records of the memory as of the compaction: the notes' subjects are
	// canonical over them and Notes (Subjects).
	Known []ledger.Observation
	// Aliases are the subject aliases recorded as of the compaction.
	Aliases []ledger.SubjectAlias
	// Folded is the folded ledger as of the compaction: NoteByID reads a record of it, dropped
	// or replaced ones included.
	Folded ledger.FoldedLedger
}

// reader is the read engine over the pass, with the detector naming (Reader.ToolCall unset: a
// level-0 field a schema added is not shadowed, as a detector query read it).
func (p *DetectorPass) reader() Reader {
	return Reader{
		Entries: p.Covered,
		Records: &Records{Observations: p.Notes, Reflections: []ledger.Reflection{}, Aliases: p.Aliases, SubjectContext: ledger.SubjectsOf(p.Known)},
	}
}

// DetectorCall is the call context of one detector run over p: it answers only the detector's
// reads (omwire.DetectorMethods: Notes, History, Subjects and NoteByID), from p alone. It holds
// no curated call, no layer writer, no Notify and no record, so no method it answers can reach a
// write.
func DetectorCall(p *DetectorPass) *Call {
	return &Call{Entries: p.Covered, detector: p}
}
