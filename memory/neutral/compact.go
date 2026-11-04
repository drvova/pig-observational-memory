package neutral

import (
	"context"
	"fmt"

	"github.com/drvova/pig-observational-memory/memory/hooks"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/runtime"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/tokens"
)

// CutMarker is the custom entry a compaction cuts on. A harness that compacts summarizes
// its whole conversation, so the engine's compaction removes everything up to the tip: the
// marker, stored at the tip, is the first entry kept, and the next compaction's range starts
// at it (ledger.CompactionRangeStartIndex). Level 0 reads no custom entry, so it holds nothing.
const CutMarker = "om.cut"

// Compaction runs the engine's compaction of everything before now, as the harness is about
// to compact its own conversation (docs/universal.md): level 0 over what is not yet extracted,
// then the summary selection, with no request (the harness hands the engine no model). The
// result is stored as a compaction entry after the cut marker, and returned so the adapter can
// hand the summary to the harness.
func (h *Host) Compaction(ctx context.Context, rt *runtime.Runtime, reason string) (*hooks.CompactionResult, error) {
	marker, err := h.appendEntry(map[string]any{"type": "custom", "customType": CutMarker, "data": map[string]any{"reason": reason}})
	if err != nil {
		return nil, err
	}
	branch, err := h.read()
	if err != nil {
		return nil, err
	}
	event := hooks.BeforeCompactEvent{FirstKeptEntryID: marker, TokensBefore: tokensBefore(branch, marker), BranchEntries: branch, Reason: reason}
	outcome := hooks.HandleBeforeCompactRequest(ctx, rt, h, event)
	if outcome.Compaction == nil {
		return nil, fmt.Errorf("neutral: the engine declined the compaction")
	}
	c := outcome.Compaction
	entry := map[string]any{"type": "compaction", "summary": c.Summary, "firstKeptEntryId": c.FirstKeptEntryID, "tokensBefore": c.TokensBefore, "details": c.Details}
	if _, err := h.appendEntry(entry); err != nil {
		return nil, err
	}
	return c, nil
}

// tokensBefore is the estimated context the compaction replaces: the entries from the previous
// compaction's boundary up to the cut, as PiG's tokensBefore counts the messages it removes.
func tokensBefore(branch []session.Entry, cut string) int {
	total := 0
	end := ledger.EntryIndexForID(branch, cut)
	if end < 0 {
		end = len(branch)
	}
	for _, e := range branch[ledger.CompactionRangeStartIndex(branch):end] {
		total += tokens.EstimateEntryTokens(e)
	}
	return total
}

// LatestSummary is the summary of the latest compaction on the branch, "" when there is none:
// what an adapter hands the harness after it compacted.
func (h *Host) LatestSummary() (string, error) {
	branch, err := h.read()
	if err != nil {
		return "", err
	}
	i := ledger.FindLastCompactionIndex(branch)
	if i < 0 {
		return "", nil
	}
	summary, _ := branch[i].Summary.(string)
	return summary, nil
}
