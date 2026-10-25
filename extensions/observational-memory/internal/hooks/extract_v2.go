package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/extract"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
)

// extractMu serializes memory v2's level-0 extractions (a turn end's and a compaction's), so
// two never read the same unextracted range and append it twice.
var extractMu sync.Mutex

// extractTurnV2 is memory v2's turn-end extraction (docs/memory-v2.md, "Level 0: code only"):
// level 0 reads the source entries since the last extraction marker and appends their records
// and a marker through the last entry read, with no model request. The detectors run over what
// it read first (detectV2), a keep-head rule's notes ride in its batches, and the run is
// recorded once the extraction is appended. Passive mode turns it off;
// the compaction then extracts the whole cut range. A panic is recovered: extraction never
// fails a turn.
func extractTurnV2(rt *runtime.Runtime, h host.Host) {
	cfg := rt.Config()
	if cfg.Passive {
		return
	}
	ctx := debuglog.With(context.Background(), debugScope(rt, h))
	defer func() {
		if recovered := recover(); recovered != nil {
			debuglog.Log(ctx, "extract.v2.recovered", map[string]any{"panic": fmt.Sprint(recovered), "at": "turn_end"})
		}
	}()
	extractMu.Lock()
	defer extractMu.Unlock()
	entries, err := h.GetBranch()
	if err != nil {
		return
	}
	start := max(ledger.ExtractionProgressIndex(entries)+1, ledger.CompactionRangeStartIndex(entries))
	folded := ledger.FoldLedger(entries, "")
	result := extract.Extract(extract.Input{
		Entries:   entries,
		Start:     start,
		End:       len(entries),
		SkipTools: harness.MemoryToolNames(rt),
		Ledger:    folded,
		Cwd:       h.Cwd(),
	})
	result, record := detectV2(ctx, rt, h, "turn_end", entries, start, len(entries), folded, result)
	appended, err := appendExtraction(h, result, true)
	logExtraction(ctx, "turn_end", result, appended, err)
	if err == nil && len(appended) > 0 {
		record()
	}
}

// extractCutV2 extracts whatever of a compaction's cut range no earlier extraction read, plus
// the paths PiG reports touched, appends the records, and returns the branch with the
// extraction's entries after it, so the compaction folds them through PiG's cut. Every hook
// appends, a concurrent or cancelled one included: whichever compaction PiG persists, the
// records of its range are on the session, since turn ends never read a range again once it is
// compacted. Under extractMu, the range left to read is measured on a fresh branch (the event's
// is a snapshot another hook may have extended since), so two hooks never append the same
// records. A cut not on the branch reads nothing: its entries have no ids for records to cite.
func extractCutV2(ctx context.Context, rt *runtime.Runtime, h host.Host, event BeforeCompactEvent) []ledger.Entry {
	entries := event.BranchEntries
	if ledger.EntryIndexForID(entries, event.FirstKeptEntryID) < ledger.CompactionRangeStartIndex(entries) {
		return entries
	}
	extractMu.Lock()
	defer extractMu.Unlock()
	fresh := false
	if branch, err := h.GetBranch(); err == nil {
		if cut := ledger.EntryIndexForID(branch, event.FirstKeptEntryID); cut >= 0 && cut >= ledger.CompactionRangeStartIndex(branch) {
			entries, fresh = branch, true
		}
	}
	rangeStart := ledger.CompactionRangeStartIndex(entries)
	cut := ledger.EntryIndexForID(entries, event.FirstKeptEntryID)
	ops := []extract.FileOp{}
	for _, p := range event.Preparation.FileOps.Read {
		ops = append(ops, extract.FileOp{Path: p, State: "read"})
	}
	for _, p := range event.Preparation.FileOps.Written {
		ops = append(ops, extract.FileOp{Path: p, State: "written"})
	}
	for _, p := range event.Preparation.FileOps.Edited {
		ops = append(ops, extract.FileOp{Path: p, State: "edited"})
	}
	start := max(ledger.ExtractionProgressIndex(entries)+1, rangeStart)
	folded := ledger.FoldLedger(entries, "")
	result := extract.Extract(extract.Input{
		Entries:   entries,
		Start:     start,
		End:       cut,
		SkipTools: harness.MemoryToolNames(rt),
		Ledger:    folded,
		Cwd:       h.Cwd(),
		FileOps:   extract.FileOps{Ops: ops, From: rangeStart},
	})
	result, record := detectV2(ctx, rt, h, "compaction", entries, start, cut, folded, result)
	appended, err := appendExtraction(h, result, true)
	logExtraction(ctx, "compaction", result, appended, err)
	if err == nil && len(appended) > 0 && fresh {
		// Counted only for an extraction that landed on a range measured on the fresh branch:
		// one read from the event's snapshot may repeat a turn end's run already recorded.
		record()
	}
	if len(appended) == 0 {
		return entries
	}
	return append(append(make([]ledger.Entry, 0, len(entries)+len(appended)), entries...), appended...)
}

// appendExtraction appends an extraction's batches, then an om.extracted marker through the
// last entry read when the last batch does not reach it, and returns them as branch entries
// (with ids of their own when they were not appended, or when an append failed part way: a
// compaction still folds them). With write false nothing is appended.
func appendExtraction(h host.Host, result extract.Result, write bool) ([]ledger.Entry, error) {
	type pending struct {
		customType string
		data       any
	}
	queue := []pending{}
	for _, batch := range result.Batches {
		if data := ledger.BuildObservationsRecordedData(batch.Observations, batch.CoversUpToID); data != nil {
			queue = append(queue, pending{ledger.OMObservationsRecorded, data})
		}
	}
	if result.CoversUpToID != "" && (len(result.Batches) == 0 || result.Batches[len(result.Batches)-1].CoversUpToID != result.CoversUpToID) {
		queue = append(queue, pending{ledger.OMExtracted, ledger.ExtractedData{CoversUpToID: result.CoversUpToID}})
	}
	out := make([]ledger.Entry, 0, len(queue))
	var appendErr error
	for n, p := range queue {
		if write && appendErr == nil {
			appendErr = h.AppendEntry(p.customType, p.data)
		}
		out = append(out, session.Entry{Type: "custom", ID: fmt.Sprintf("om-v2-extracted-%d", n), CustomType: p.customType, Data: jsonValue(p.data)})
	}
	return out, appendErr
}

// jsonValue is v as decoded JSON, the shape entries read back from the session carry.
func jsonValue(v any) any {
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil
	}
	return decoded
}

// logExtraction logs one extraction: where it ran, what it read and recorded, and any append
// failure.
func logExtraction(ctx context.Context, at string, result extract.Result, appended []ledger.Entry, err error) {
	byKind := map[string]int{}
	for _, o := range result.Observations() {
		byKind[string(o.Kind)]++
	}
	data := map[string]any{"at": at, "coversUpToId": result.CoversUpToID, "records": len(result.Observations()), "byKind": byKind, "entries": len(appended)}
	if err != nil {
		data["errorMessage"] = err.Error()
	}
	debuglog.Log(ctx, "extract.v2", data)
}

// dropByRule appends the compaction's drops by rule (extract.Drops) as one
// om.observations.dropped entry covering through coversUpToID, the last source entry PiG
// removes. Every hook appends its drops: dropping an id twice is harmless, and a compaction PiG
// persists never leaves the records its summary dropped active.
func dropByRule(ctx context.Context, h host.Host, ids []string, coversUpToID string) {
	data := ledger.BuildObservationsDroppedData(ids, coversUpToID)
	if data == nil {
		return
	}
	err := h.AppendEntry(ledger.OMObservationsDropped, data)
	logData := map[string]any{"count": len(ids), "coversUpToId": coversUpToID}
	if err != nil {
		logData["errorMessage"] = err.Error()
	}
	debuglog.Log(ctx, "compaction.v2.dropped", logData)
}
