package hooks

import (
	"context"

	"github.com/drvova/pig-observational-memory/memory/core"
	"github.com/drvova/pig-observational-memory/memory/debuglog"
	"github.com/drvova/pig-observational-memory/memory/extract"
	"github.com/drvova/pig-observational-memory/memory/harness"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/runtime"
)

// The core log at a compaction (docs/brain-memory.md, phase 6; memory/core): code promotes the
// session's standing constraints and open intentions into the shared core through its gates, and
// carries its project and user forgets across sessions as tombstones. No request is made; the
// session reads the core again when its start context is next rendered (engine.Index).

// CoreSession is what a session's records are offered to the core with (core.Session): the
// project scope (its repoId, else its root), the writer, and the generation it read through.
func CoreSession(log core.Log, h host.Host, repoID string) core.Session {
	base, _ := log.Consumed(h.SessionID())
	return core.Session{
		Scope:   core.ProjectScope(repoID, harness.ProjectRoot(h.Cwd())),
		Writer:  core.Writer{Model: harness.ModelKey(h.SessionModel()), Session: h.SessionID()},
		Base:    base,
		OneTime: extract.IsOneTime,
	}
}

// promoteCoreV2 offers the ledger's candidates (core.Candidates) to the core and appends a forget
// line for each project or user forget record it does not hold yet (core.State.ForgetDue, which
// never re-offers what a restatement lifted).
func promoteCoreV2(ctx context.Context, rt *runtime.Runtime, h host.Host, entries []ledger.Entry) {
	store, on := harness.StoreOf(h, rt.Config())
	if !on {
		return
	}
	repo, _ := repoOfV2(rt, h, entries)
	log := store.Core()
	s := CoreSession(log, h, repo.ID)
	folded := ledger.FoldLedger(entries, "")
	closed := map[string]bool{}
	for id := range ledger.ClosedBy(folded.Observations) {
		closed[id] = true
	}
	cands := core.Candidates(folded.ActiveObservations, folded.Labels(), closed, s)
	raw := map[string]ledger.Observation{}
	for _, e := range entries {
		if data, ok := ledger.ObservationsRecordedEntry(e); ok {
			for _, o := range data.Observations {
				if _, seen := raw[o.ID]; !seen {
					raw[o.ID] = o
				}
			}
		}
	}
	var forgets []core.Line
	for _, o := range folded.Observations {
		if o.Kind != ledger.KindForget {
			continue
		}
		var forgotten []ledger.Observation
		for _, t := range o.Forgets {
			if r, ok := raw[t.ID]; ok {
				forgotten = append(forgotten, r)
			}
		}
		if line, ok := core.ForgetLine(o, forgotten, s); ok {
			forgets = append(forgets, line)
		}
	}
	if len(cands) == 0 && len(forgets) == 0 {
		return
	}
	verdicts := make([]string, len(cands))
	lines, err := log.Commit(func(st *core.State, emit func(core.Line)) {
		for _, f := range forgets {
			if st.ForgetDue(f) {
				emit(f)
			}
		}
		for i, c := range cands {
			line, v := core.Decide(st, c)
			verdicts[i] = v
			if line.Op != "" {
				emit(line)
			}
		}
	})
	data := map[string]any{"candidates": len(cands), "lines": len(lines), "verdicts": verdicts}
	if err != nil {
		data["error"] = err.Error()
	}
	debuglog.Log(ctx, "core.promote", data)
}
