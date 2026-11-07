package hooks

import (
	"context"

	"github.com/drvova/pig-observational-memory/memory/config"
	"github.com/drvova/pig-observational-memory/memory/debuglog"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/project"
	"github.com/drvova/pig-observational-memory/memory/runtime"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/sleep"
)

// The sleep pass's compaction fallback (docs/brain-memory.md, decision 13; docs/memory-v2.md,
// "Sleep"): a compaction that follows sleepAfterCompactions compactions with no sleep record on
// the branch runs the pass (memory/sleep) before level 1 and the render, so its duplicates leave
// this summary. It makes no request, reads the HEAD structure view once, and is skipped when the
// ledger holds more active records than sleepMaxRecords or the request was cancelled.

// SleepInput is the sleep pass's input over entries for a host: the decay setting, and the
// repository and its structure view's answer when the host reads one.
func SleepInput(rt *runtime.Runtime, h host.Host, cfg config.Config, entries []ledger.Entry, trigger string) sleep.Input {
	in := sleep.Input{Entries: entries, Trigger: trigger, Decay: cfg.Decay, DecayHorizon: cfg.DecayHorizon}
	reader, ok := h.(host.StructureReader)
	if !ok {
		return in
	}
	var repo host.Repo
	if rt != nil {
		repo, ok = repoOfV2(rt, h, entries)
	} else if r, isRepo := h.(host.RepoReader); isRepo {
		var err error
		repo, ok, err = r.Repo()
		ok = ok && err == nil
	}
	if !ok || repo.ID == "" {
		return in
	}
	q := project.Query(ledger.FoldLedger(entries, "").ActiveObservations, repo)
	if project.Empty(q) {
		return in
	}
	answer, ok, err := reader.Structure(q)
	if err != nil || !ok {
		return in
	}
	in.Repo, in.Structure = repo, &answer
	return in
}

// RunSleep runs one pass over entries and appends its record through appendEntry (redacted);
// it returns the record's entry as the branch would read it back, false when nothing was
// appended (an empty branch, or one whose tip is a sleep record).
func RunSleep(ctx context.Context, rt *runtime.Runtime, h host.Host, cfg config.Config, entries []ledger.Entry, trigger string) (ledger.Entry, bool) {
	if len(entries) == 0 {
		return ledger.Entry{}, false
	}
	if _, at := ledger.Sleeps(entries); len(at) > 0 && at[len(at)-1] == len(entries)-1 {
		return ledger.Entry{}, false
	}
	data, ok := sleep.Pass(SleepInput(rt, h, cfg, entries, trigger))
	if !ok {
		return ledger.Entry{}, false
	}
	if err := appendEntry(h, ledger.OMSleep, data); err != nil {
		debuglog.Log(ctx, "sleep.append_failed", map[string]any{"trigger": trigger, "errorMessage": err.Error()})
		return ledger.Entry{}, false
	}
	debuglog.Log(ctx, "sleep.v2", map[string]any{"trigger": trigger, "records": data.Records, "duplicates": len(data.Duplicates),
		"links": len(data.Links), "decayed": len(data.Decayed), "verified": len(data.Verified), "retired": len(data.Retired), "checks": len(data.Checks)})
	return session.Entry{Type: "custom", ID: "om-v2-sleep", CustomType: ledger.OMSleep, Data: jsonValue(data)}, true
}

// sleepFallbackV2 runs the pass inside a compaction when it is due (ledger.CompactionsSinceSleep
// reached sleepAfterCompactions) and fits (sleepMaxRecords), returning entries with its record.
func sleepFallbackV2(ctx, request context.Context, rt *runtime.Runtime, h host.Host, entries []ledger.Entry) []ledger.Entry {
	cfg := rt.Config()
	k := cfg.SleepAfterCompactions
	if k <= 0 || request.Err() != nil || ledger.CompactionsSinceSleep(entries) < k {
		return entries
	}
	if limit := cfg.SleepMaxRecords; limit > 0 && len(ledger.FoldLedger(entries, "").ActiveObservations) > limit {
		debuglog.Log(ctx, "sleep.skipped", map[string]any{"trigger": ledger.SleepAtCompaction, "reason": "budget", "maxRecords": limit})
		return entries
	}
	if e, ok := RunSleep(ctx, rt, h, cfg, entries, ledger.SleepAtCompaction); ok {
		return append(append(make([]ledger.Entry, 0, len(entries)+1), entries...), e)
	}
	return entries
}

// SleepHidden are the records the sleep records on entries hid as duplicates (selection's
// Hidden), nil when none.
func SleepHidden(entries []ledger.Entry) map[string]bool {
	st := ledger.SleepStateOf(entries)
	if len(st.Duplicates) == 0 {
		return nil
	}
	out := make(map[string]bool, len(st.Duplicates))
	for id := range st.Duplicates {
		out[id] = true
	}
	return out
}
