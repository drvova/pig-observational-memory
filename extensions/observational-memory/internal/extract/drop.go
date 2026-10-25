package extract

import "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"

// Drops are the ids of the active records a compaction drops by rule (docs/memory-v2.md,
// "Dropping (code)"): a record superseded by a record the summary renders, a record a rendered
// reflection cites, and, transitively, every record a dropped record supersedes, so a supersede
// chain leaves with its head and nothing behind it comes back as unsuperseded later. A rendered
// record is never dropped. byID resolves the chain's links through records no longer active
// (ledger.FoldedLedger.ObservationsByID; nil reads only active). They stay recallable; nothing
// else is dropped. The ids come in the order of active. Selection drops by this same rule
// (selection.Render).
func Drops(active, rendered []ledger.Observation, renderedReflections []ledger.Reflection, byID map[string]ledger.Observation) []string {
	direct := []string{}
	for _, o := range rendered {
		for _, id := range o.Supersedes {
			if id != o.ID {
				direct = append(direct, id)
			}
		}
	}
	for _, r := range renderedReflections {
		direct = append(direct, r.SupportingObservationIDs...)
	}
	shown := make(map[string]bool, len(rendered))
	for _, o := range rendered {
		shown[o.ID] = true
	}
	replaced := DropClosure(direct, active, byID)
	out := []string{}
	for _, o := range active {
		if replaced[o.ID] && !shown[o.ID] {
			out = append(out, o.ID)
		}
	}
	return out
}

// DropClosure is ids with every record they supersede, transitively: the records that leave
// when the records ids names leave by rule (Drops). The links are read from active, then byID.
func DropClosure(ids []string, active []ledger.Observation, byID map[string]ledger.Observation) map[string]bool {
	lookup := make(map[string]ledger.Observation, len(active))
	for _, o := range active {
		lookup[o.ID] = o
	}
	out := map[string]bool{}
	queue := append([]string{}, ids...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if out[id] {
			continue
		}
		out[id] = true
		o, ok := lookup[id]
		if !ok {
			o, ok = byID[id]
		}
		if !ok {
			continue
		}
		for _, next := range o.Supersedes {
			if next != id && !out[next] {
				queue = append(queue, next)
			}
		}
	}
	return out
}
