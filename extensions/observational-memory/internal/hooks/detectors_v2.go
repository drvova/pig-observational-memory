package hooks

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omhost"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/sandbox"
)

// Go detectors (docs/code-tool.md, "Detectors"). A detector a model defined is Go: a func
// Count() int over a read-only om. It never blocks anything (decision 5): it runs neither at a
// turn end nor inside session_before_compact. The hook only builds the detector pass as data
// (what the compaction removed and the level-0 records extracted from it) and, once it has
// produced its compaction, starts one background goroutine for the pass that it does not wait
// on. The pass runs the detectors one after the other, each in a short-lived detector child of
// its own, and writes one curated.ModelKindDetectorPass event when its last run ends, or when
// the next compaction, session_start or session_shutdown stops it, which kills the run in
// flight (an error for that detector: unfinished) and starts no other. The bound is therefore an
// event PiG raises, never a tuned number, and nothing waits on model code but a kill.

// runDetector runs one detector over a pass; tests replace it.
var runDetector = sandbox.RunDetector

// detectorGroup is which part of a pass's order a run belongs to.
type detectorGroup int

const (
	// groupPending is a detector level 1 proposed at the compaction: its smoke run holds it when
	// it returns a count, and refuses it otherwise.
	groupPending detectorGroup = iota
	// groupHeld is a held detector: its started run is evidence for it (RanHeld).
	groupHeld
	// groupWritten is a written detector: an error flags it, never removes it.
	groupWritten
)

// detectorRun is one detector of a pass.
type detectorRun struct {
	group detectorGroup
	def   rules.DetectorDef
	// layerDef is its layer definition: the proposal for a pending run, the held definition
	// (whose HeldID RanHeld names) for a held one.
	layerDef layer.Definition
}

// detectorPassV2 is one compaction's detector pass in flight (runtime.DetectorPass).
type detectorPassV2 struct {
	cancel context.CancelCauseFunc
	done   chan struct{}
}

// Stop kills the run in flight with reason as its error and waits for the pass's event.
func (p *detectorPassV2) Stop(reason string) {
	p.cancel(errors.New(reason))
	<-p.done
}

// Done is closed once the pass's event is written.
func (p *detectorPassV2) Done() <-chan struct{} { return p.done }

// detectorPassData is the pass the hook builds beside the summary: the source entries the
// compaction removed (cut, from the previous fold boundary to PiG's cut) and the level-0 records
// extracted from them (the records of level0, the branch with level 0's extraction, citing an
// entry of cut), with the folded ledger as of the compaction (final, level 1's entries
// included). It is plain data: nothing in it refers to live state.
func detectorPassData(level0, final, cut []ledger.Entry) *omhost.DetectorPass {
	cutIDs := make(map[string]bool, len(cut))
	for _, e := range cut {
		cutIDs[e.ID] = true
	}
	notes := []ledger.Observation{}
	for _, o := range ledger.FoldLedger(level0, "").Observations {
		if citesAny(o.SourceEntryIDs, cutIDs) {
			notes = append(notes, o)
		}
	}
	folded := ledger.FoldLedger(final, "")
	return &omhost.DetectorPass{
		Covered: append([]ledger.Entry(nil), cut...),
		Notes:   notes,
		Known:   folded.ActiveObservations,
		Aliases: folded.Aliases,
		Folded:  folded,
	}
}

// detectorOrder is the pass's runs in order (docs/code-tool.md, "Running, and the time bound"):
// the pending smoke runs first, then the held detectors (so held evidence is not starved), then
// the written ones, each group starting after the detector the previous pass killed unfinished
// (previous.Unfinished), so one looping detector cannot keep the same others from ever running,
// and a written detector whose previous run errored moved to the end of its group. Only Go
// detectors run: a kernel-check override counts through the kernel and a query detector is
// retired. active is the model's effective layer, read once the compaction was recorded (so a
// held detector its settling reverted or wrote is in its new state); pending are the detectors
// level 1 proposed.
func detectorOrder(active layer.Harness, model string, pending []layer.Definition, previous curated.ModelEvent) []detectorRun {
	var runs [3][]detectorRun
	for _, p := range pending {
		if def, err := rules.ParseDetector(p.Name, p.Text); err == nil && def.IsCode() {
			runs[groupPending] = append(runs[groupPending], detectorRun{group: groupPending, def: def, layerDef: p})
		}
	}
	proposed := map[string]bool{}
	for _, p := range pending {
		proposed[p.Name] = true
	}
	for _, d := range active.List(layer.KindDetector) {
		def, err := rules.ParseDetector(d.Name, d.Text)
		if err != nil || !def.IsCode() || proposed[d.Name] {
			continue
		}
		if d.State == layer.StateHeld {
			if d.Model == model && rules.SettlesUnderV2(d) {
				runs[groupHeld] = append(runs[groupHeld], detectorRun{group: groupHeld, def: def, layerDef: d})
			}
			continue
		}
		runs[groupWritten] = append(runs[groupWritten], detectorRun{group: groupWritten, def: def, layerDef: d})
	}
	var out []detectorRun
	for g := range runs {
		group := rotateAfter(runs[g], previous.Unfinished)
		if detectorGroup(g) == groupWritten {
			sort.SliceStable(group, func(i, j int) bool {
				_, a := previous.Errors[group[i].def.Name]
				_, b := previous.Errors[group[j].def.Name]
				return !a && b
			})
		}
		out = append(out, group...)
	}
	return out
}

// rotateAfter is runs starting after the one named name, wrapping round; runs as they are when
// none is.
func rotateAfter(runs []detectorRun, name string) []detectorRun {
	i := slices.IndexFunc(runs, func(r detectorRun) bool { return r.def.Name == name })
	if i < 0 {
		return runs
	}
	return append(append([]detectorRun(nil), runs[i+1:]...), runs[:i+1]...)
}

// startDetectorPassV2 starts the compaction's detector pass in the background for the session
// model's ledger l, cut at cutID: the pending smoke runs of what level 1 proposed, then the held
// and written Go detectors of the model's layer as it stands now. It starts nothing, and builds
// no pass, when no detector would run; otherwise build makes the pass (detectorPassData) here,
// before the hook returns, so the goroutine reads plain data only. It never waits on a run: the
// pass is the runtime's (runtime.Runtime.SwapDetectorPass), stopped by the next compaction,
// session_start or session_shutdown. ctx carries the debug scope only; the pass does not end
// with it. The goroutine is outside the hook's recover, so it recovers a host-side panic itself
// (runDetectorPass charges one to the run it happened in); the pass's done channel is closed
// whatever happens, so a Stop never hangs.
func startDetectorPassV2(ctx context.Context, rt *runtime.Runtime, l harness.Ledger, cutID string, build func() *omhost.DetectorPass, pending []layer.Definition) {
	runs := detectorOrder(l.Layer().Harness(), l.Model, pending, rules.LatestDetectorPass(l.Store, l.Model))
	if len(runs) == 0 {
		return
	}
	pass := build()
	passCtx, cancel := context.WithCancelCause(context.Background())
	p := &detectorPassV2{cancel: cancel, done: make(chan struct{})}
	logCtx := context.WithoutCancel(ctx)
	rt.SwapDetectorPass(p)
	go func() {
		defer close(p.done)
		defer rt.ClearDetectorPass(p)
		defer cancel(nil)
		defer func() {
			if r := recover(); r != nil {
				debuglog.Log(logCtx, "harness.v2.detector_pass_panic", map[string]any{"model": l.Model, "cut": cutID, "errorMessage": fmt.Sprint(r)})
			}
		}()
		e := runDetectorPass(logCtx, passCtx, l, cutID, pass, runs)
		if err := l.Store.RecordModel(e); err != nil {
			debuglog.Log(logCtx, "harness.v2.detector_pass_record_failed", map[string]any{"model": l.Model, "errorMessage": err.Error()})
		}
		debuglog.Log(logCtx, "harness.v2.detector_pass", map[string]any{"model": l.Model, "cut": cutID, "counts": e.Counts, "errors": e.Errors, "ranHeld": len(e.RanHeld), "unfinished": e.Unfinished})
	}()
}

// runOne is one run of runDetectorPass: runDetector, with a host-side panic (the read engine
// answering the child's om calls) recovered into the run's error, "crashed: host panic: ...",
// so it is charged to that detector alone and the pass goes on. runDetector's own deferred kill
// still ends the child.
func runOne(ctx context.Context, run detectorRun, pass *omhost.DetectorPass) (res sandbox.DetectorResult) {
	defer func() {
		if r := recover(); r != nil {
			res = sandbox.DetectorResult{Outcome: sandbox.OutcomeCrashed, Err: "host panic: " + fmt.Sprint(r)}
		}
	}()
	return runDetector(ctx, run.def.Code, omhost.DetectorCall(pass))
}

// runDetectorPass runs the pass's detectors one after the other until the last ends or ctx is
// stopped, and returns the pass's event. A run that started when the stop came is an error for
// its detector, with the stop's reason (unfinished at the next compaction, or at session end);
// one still queued gets nothing, no count, no error and no RanHeld entry, and so does one the
// stop reached before its child was spawned (sandbox.DetectorResult.Queued). A pending detector
// whose smoke run returns a count is held now (layer.Store.Hold, under the layer's lock), and
// one whose run gives no count is refused (rules.CountLevel1Refused) and never held. A run's
// crash, kill or host-side panic is charged to its detector alone: the next run is a fresh
// child.
func runDetectorPass(logCtx, ctx context.Context, l harness.Ledger, cutID string, pass *omhost.DetectorPass, runs []detectorRun) curated.ModelEvent {
	e := curated.ModelEvent{Model: l.Model, Kind: curated.ModelKindDetectorPass, Engine: curated.EngineV2, Cut: cutID, Tokens: rules.SourceTokens(pass.Covered), Counts: map[string]int{}, Errors: map[string]string{}, RanHeld: []string{}}
	for _, run := range runs {
		if ctx.Err() != nil {
			break
		}
		name := run.def.Name
		res := runOne(ctx, run, pass)
		if res.Queued {
			break
		}
		if run.group == groupHeld {
			e.RanHeld = append(e.RanHeld, run.layerDef.HeldID())
		}
		if run.group == groupPending {
			e.Pending = append(e.Pending, name)
		}
		failure := ""
		switch {
		case res.Outcome == sandbox.OutcomeCancelled && ctx.Err() != nil:
			failure = context.Cause(ctx).Error()
			e.Unfinished = name
		case res.Failed():
			failure = res.String()
		}
		if failure != "" {
			e.Errors[name] = failure
			if run.group == groupPending {
				e.Counts[rules.CountLevel1Refused]++
				debuglog.Log(logCtx, "harness.v2.definition_refused", map[string]any{"model": l.Model, "definition": layer.KindDetector + "/" + name, "errorMessage": "smoke run: " + failure})
			}
			continue
		}
		e.Counts[rules.DefinedCountKey(name)] = res.Count
		if run.group == groupPending {
			if held, err := holdPending(l, run.layerDef); err != nil {
				e.Errors[name] = "smoke run counted, but holding it failed: " + err.Error()
			} else {
				debuglog.Log(logCtx, "harness.v2.detector_held", map[string]any{"model": l.Model, "definition": held.Kind + "/" + held.Name, "count": res.Count})
			}
		}
	}
	sort.Strings(e.RanHeld)
	if len(e.Errors) == 0 {
		e.Errors = nil
	}
	return e
}

// holdPending holds a pending detector whose smoke run counted, with a panic in the layer's
// code recovered into the error.
func holdPending(l harness.Ledger, d layer.Definition) (held layer.Definition, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("host panic: %v", r)
		}
	}()
	return l.Store.Layer().Hold(d)
}
