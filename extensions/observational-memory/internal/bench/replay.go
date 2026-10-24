package bench

import (
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
)

// ReplayStats counts what ApplyDropFloor changed in a branch.
type ReplayStats struct {
	DropEntries   int `json:"dropEntries"`
	ProposedDrops int `json:"proposedDrops"`
	KeptDrops     int `json:"keptDrops"`
}

// ApplyDropFloor replays a recorded branch through agents.PreserveSpecifics:
// at every observations-dropped entry it folds the ledger up to that point and
// removes from the drop every observation the floor would have kept, under the
// same pool-fullness condition the dropper applies. Observations, reflections
// and coverage are unchanged, so the result is the branch the dropper would
// have written had the floor been in place, with the model's own drop choices
// and no new model calls. An entry left with no drops is removed.
func ApplyDropFloor(entries []session.Entry, cfg config.Config) ([]session.Entry, ReplayStats) {
	stats := ReplayStats{}
	out := make([]session.Entry, 0, len(entries))
	for _, entry := range entries {
		if entry.Type != "custom" || entry.CustomType != ledger.OMObservationsDropped {
			out = append(out, entry)
			continue
		}
		data, ok := ledger.ObservationsDroppedDataFrom(entry.Data)
		if !ok {
			out = append(out, entry)
			continue
		}
		stats.DropEntries++
		stats.ProposedDrops += len(data.ObservationIDs)
		folded := ledger.FoldLedger(out, "")
		metrics := agents.ObservationPoolMetrics(folded.ActiveObservations, cfg.ObservationsPoolTargetTokens)
		ids := data.ObservationIDs
		if metrics.Fullness <= agents.DropFloorMaxFullness {
			var kept []string
			ids, kept = agents.PreserveSpecifics(ids, folded.ActiveObservations, folded.Reflections)
			stats.KeptDrops += len(kept)
		}
		if len(ids) == 0 {
			continue
		}
		replaced := entry
		replaced.Data = map[string]any{"observationIds": toAnySlice(ids), "coversUpToId": data.CoversUpToID}
		out = append(out, replaced)
	}
	return out, stats
}

func toAnySlice(ids []string) []any {
	out := make([]any, len(ids))
	for i, id := range ids {
		out[i] = id
	}
	return out
}
