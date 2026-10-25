package extract

import (
	"slices"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ids"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// HostSummary is the record memory v2 stores for the latest compaction on the branch when the
// ledger does not back it (docs/memory-v2.md, "Migration > Records and boundaries"): a native
// PiG summary, or one written before install. A compaction whose details are om.folded (a v1
// or v2 one) is ledger-backed and gets none. The record is the summary on one line (origin
// host, kind unset, so it never claims the user's class), citing only that compaction's entry,
// so it is stored once: none is returned while any record cites that entry (a level-1 label or
// merge of it keeps the citation).
//
// coversUpToID is the batch's coverage claim: the entry just before that compaction's retention
// boundary, the last one its summary stands for, so the record folds into the next compaction
// and no entry kept after the boundary reads as extracted. ok is false when there is nothing to
// store, or no entry before the boundary to claim.
func HostSummary(entries []ledger.Entry, folded ledger.FoldedLedger) (record ledger.Observation, coversUpToID string, ok bool) {
	index := ledger.FindLastCompactionIndex(entries)
	if index < 0 {
		return ledger.Observation{}, "", false
	}
	compaction := entries[index]
	if _, backed := ledger.MemoryDetailsFrom(compaction.Details); backed || compaction.ID == "" {
		return ledger.Observation{}, "", false
	}
	summary, _ := compaction.Summary.(string)
	// On one line, as every record is: a native summary is multi-line markdown whose headings
	// would read as sections of the v2 summary and whose lines would carry no anchor.
	summary = oneLine(summary)
	if summary == "" {
		return ledger.Observation{}, "", false
	}
	for _, o := range folded.Observations {
		if slices.Contains(o.SourceEntryIDs, compaction.ID) {
			return ledger.Observation{}, "", false
		}
	}
	// The range the compaction kept starts at its firstKeptEntryId, or after it when that id
	// is not on the branch (ledger.CompactionRangeStartIndex).
	boundary := ledger.EntryIndexForID(entries, compaction.FirstKeptEntryID)
	if boundary < 0 {
		boundary = index + 1
	}
	if boundary < 1 {
		return ledger.Observation{}, "", false
	}
	record = ledger.Observation{
		Content:        summary,
		Timestamp:      serialize.FormatTimestamp(entryTime(compaction)),
		Relevance:      codeRelevance,
		SourceEntryIDs: []string{compaction.ID},
		TokenCount:     tokens.EstimateStringTokens(summary),
		Origin:         ledger.OriginHost,
	}
	record.ID = ids.HashID(ledger.OriginHost + "\x00" + compaction.ID)
	return record, entries[boundary-1].ID, true
}
