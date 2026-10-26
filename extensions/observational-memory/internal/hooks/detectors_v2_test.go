package hooks

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omhost"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/sandbox"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// The Go detectors' fixtures: each is a declaration's code column.
const (
	detectCountNotes = "func Count() int { return om.Notes().Matched }"
	detectLoop       = "func Count() int { for {} }"
	detectBlock      = "func Count() int { <-make(chan int); return 0 }"
	detectPanic      = "func Count() int { var m map[string]int; m[`x`] = 1; return 1 }"
	// detectForbidden calls a record's Recall method, which the host refuses from a detector
	// child: the child is killed.
	detectForbidden = "func Count() int { n := om.Notes().Rows[0]; n.Recall(); return 1 }"
)

// detectorSetup is a level-0 compaction setup (no level-1 request) with a store.
func detectorSetup(t *testing.T) (*runtime.Runtime, *testfix.FakeHost, curated.Store, string) {
	t.Helper()
	rt, h := v2Setup(t, level1Branch())
	h.Home = t.TempDir()
	store, ok := harness.StoreOf(h, rt.Config())
	if !ok {
		t.Fatal("no store")
	}
	t.Cleanup(func() { rt.StopDetectorPass(runtime.DetectorStopSessionEnd) })
	return rt, h, store, harness.ModelKey(h.Model)
}

// holdDetector holds a Go detector for model.
func holdDetector(t *testing.T, store curated.Store, model, name, code string) layer.Definition {
	t.Helper()
	d, err := store.Layer().Hold(layer.Definition{Kind: layer.KindDetector, Name: name, Model: model, Text: rules.CodeDetectorText(code, name)})
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// compactLevel0 runs one level-0 compaction of the fixture branch and checks it returned within
// bound (it never waits on a detector).
func compactLevel0(t *testing.T, rt *runtime.Runtime, h *testfix.FakeHost, bound time.Duration) {
	t.Helper()
	h.Entries = level1Branch()
	start := time.Now()
	level1CompactionOf(t, runV2Hook(rt, h, v2Event(h.Entries, "u2", 20_000, "threshold")))
	if took := time.Since(start); took > bound {
		t.Fatalf("the compaction took %v", took)
	}
}

// waitPass waits for the detector pass in flight, if any, to end on its own.
func waitPass(t *testing.T, rt *runtime.Runtime) {
	t.Helper()
	if p := rt.DetectorPassInFlight(); p != nil {
		waitDone(t, p.Done())
	}
}

// detectorPasses are model's detector pass events.
func detectorPasses(t *testing.T, store curated.Store, model string) []curated.ModelEvent {
	return modelEventsOf(t, store, model, curated.ModelKindDetectorPass)
}

// detectorState is a detector's state in model's layer: held, written or none.
func detectorState(t *testing.T, store curated.Store, model, name string) string {
	t.Helper()
	h, _, err := store.Layer().Load(model)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := h.Get(layer.KindDetector, name)
	if !ok {
		return "none"
	}
	return d.State
}

// A held Go detector runs in the background after a compaction, over what that compaction
// removed, and is written by the next compaction on that count. No model request is made, and
// nothing runs at turn end.
func TestV2GoDetectorRunsAfterTheCompaction(t *testing.T) {
	counter := testfix.CountRequests(t)
	rt, h, store, model := detectorSetup(t)
	held := holdDetector(t, store, model, "deploy-notes", detectCountNotes)

	// A turn end runs the kernel's detectors only.
	h.Entries = level1Branch()
	OnTurnEnd(rt, h)
	if rt.DetectorPassInFlight() != nil || len(detectorPasses(t, store, model)) != 0 {
		t.Fatal("a detector ran at turn end")
	}

	compactLevel0(t, rt, h, 5*time.Second)
	waitPass(t, rt)
	passes := detectorPasses(t, store, model)
	if len(passes) != 1 {
		t.Fatalf("detector passes %+v", passes)
	}
	p := passes[0]
	if p.Counts[rules.DefinedCountKey("deploy-notes")] != 2 || p.Cut != "u2" || len(p.RanHeld) != 1 || p.RanHeld[0] != held.HeldID() || p.Engine != curated.EngineV2 || p.Tokens == 0 {
		t.Fatalf("pass %+v", p)
	}
	if detectorState(t, store, model, "deploy-notes") != layer.StateHeld {
		t.Fatal("settled by the compaction that ran it")
	}
	// The next compaction settles it on that pass, and its own pass runs it as written.
	compactLevel0(t, rt, h, 5*time.Second)
	if s := detectorState(t, store, model, "deploy-notes"); s != layer.StateWritten {
		t.Fatalf("state %s after a clean pass", s)
	}
	waitPass(t, rt)
	if counter.Total() != 0 {
		t.Fatalf("detector runs made requests: %v", counter.ByStage())
	}
}

// A held detector that loops, or blocks on a channel, never delays a compaction: the next one
// kills its run (unfinished) and reverts it on that error. A detector queued behind a killed
// one is not started, so it gets no evidence from that pass; the next pass starts after the
// killed detector.
func TestV2LoopingHeldDetectorsAreRevertedAtTheNextCompaction(t *testing.T) {
	counter := testfix.CountRequests(t)
	rt, h, store, model := detectorSetup(t)
	holdDetector(t, store, model, "a-loops", detectLoop)
	blocks := holdDetector(t, store, model, "b-blocks", detectBlock)

	compactLevel0(t, rt, h, 5*time.Second)
	time.Sleep(200 * time.Millisecond) // a-loops is running
	compactLevel0(t, rt, h, 5*time.Second)
	passes := detectorPasses(t, store, model)
	if len(passes) != 1 {
		t.Fatalf("passes %+v", passes)
	}
	first := passes[0]
	if first.Errors["a-loops"] != runtime.DetectorStopCompaction || first.Unfinished != "a-loops" || len(first.RanHeld) != 1 {
		t.Fatalf("first pass %+v", first)
	}
	if _, ok := first.Errors["b-blocks"]; ok {
		t.Fatal("a detector queued at the kill got an error")
	}
	if s := detectorState(t, store, model, "a-loops"); s != "none" {
		t.Fatalf("the looping detector is %s, want reverted", s)
	}
	if s := detectorState(t, store, model, "b-blocks"); s != layer.StateHeld {
		t.Fatalf("the queued detector is %s, want still held", s)
	}
	// The second pass runs b-blocks; the third compaction kills and reverts it.
	time.Sleep(200 * time.Millisecond)
	compactLevel0(t, rt, h, 5*time.Second)
	passes = detectorPasses(t, store, model)
	if len(passes) != 2 || passes[1].Errors["b-blocks"] != runtime.DetectorStopCompaction || passes[1].RanHeld[0] != blocks.HeldID() {
		t.Fatalf("passes %+v", passes)
	}
	if s := detectorState(t, store, model, "b-blocks"); s != "none" {
		t.Fatalf("the blocking detector is %s, want reverted", s)
	}
	if counter.Total() != 0 {
		t.Fatalf("requests %v", counter.ByStage())
	}
}

// A detector that crashes its child, or panics, is charged alone: the other's count is recorded
// in the same pass, and only the failing ones are reverted.
func TestV2DetectorCrashIsChargedToItAlone(t *testing.T) {
	rt, h, store, model := detectorSetup(t)
	holdDetector(t, store, model, "a-forbidden", detectForbidden)
	holdDetector(t, store, model, "b-panics", detectPanic)
	holdDetector(t, store, model, "c-counts", detectCountNotes)
	compactLevel0(t, rt, h, 5*time.Second)
	waitPass(t, rt)
	p := detectorPasses(t, store, model)[0]
	if !strings.Contains(p.Errors["a-forbidden"], "crashed") || !strings.Contains(p.Errors["b-panics"], "runtime") || p.Counts[rules.DefinedCountKey("c-counts")] != 2 || len(p.RanHeld) != 3 {
		t.Fatalf("pass %+v", p)
	}
	compactLevel0(t, rt, h, 5*time.Second)
	for name, want := range map[string]string{"a-forbidden": "none", "b-panics": "none", "c-counts": layer.StateWritten} {
		if s := detectorState(t, store, model, name); s != want {
			t.Fatalf("%s is %s, want %s", name, s, want)
		}
	}
}

// Session end kills the run in flight (an error: unfinished at session end) and starts no
// other: a detector still queued gets no evidence and is not named in RanHeld.
func TestV2DetectorQueuedAtSessionEndGetsNoEvidence(t *testing.T) {
	rt, h, store, model := detectorSetup(t)
	holdDetector(t, store, model, "a-loops", detectLoop)
	queued := holdDetector(t, store, model, "b-counts", detectCountNotes)
	compactLevel0(t, rt, h, 5*time.Second)
	time.Sleep(200 * time.Millisecond)
	rt.StopDetectorPass(runtime.DetectorStopSessionEnd)
	p := detectorPasses(t, store, model)[0]
	if p.Errors["a-loops"] != runtime.DetectorStopSessionEnd {
		t.Fatalf("pass %+v", p)
	}
	for _, id := range p.RanHeld {
		if id == queued.HeldID() {
			t.Fatal("a queued detector is named in RanHeld")
		}
	}
	if _, counted := p.Counts[rules.DefinedCountKey("b-counts")]; counted {
		t.Fatal("a queued detector counted")
	}
}

// A Go detector level 1 proposes is pending, not held, until its smoke run over the proposing
// compaction's own pass returns a count; one whose smoke run panics, or is killed unfinished at
// the next compaction, is refused and never held. The level-1 request is the only one made.
func TestV2ProposedDetectorIsHeldOnlyByItsSmokeRun(t *testing.T) {
	for _, c := range []struct {
		name, code string
		held       bool
	}{
		{"counts", detectCountNotes, true},
		{"panics", detectPanic, false},
		{"loops", detectLoop, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			counter := testfix.CountRequests(t)
			rt, h, store, model := level1StoreSetup(t)
			t.Cleanup(func() { rt.StopDetectorPass(runtime.DetectorStopSessionEnd) })
			seedV2Evidence(t, store, model)
			def := map[string]any{"kind": layer.KindDetector, "name": "deploy-" + c.name, "text": rules.CodeDetectorText(c.code, "deploy notes")}
			h.Entries = level1Branch()
			h.ModelsFake.Streams.Replies = []map[string]any{level1Reply(def)}
			compaction := level1CompactionOf(t, runV2Hook(rt, h, v2Event(h.Entries, "u2", 20_000, "threshold")))
			if report := compaction.Details.Consolidation; len(report.Pending) != 1 || report.Pending[0] != "detector/deploy-"+c.name {
				t.Fatalf("report %+v", report)
			}
			if s := detectorState(t, store, model, "deploy-"+c.name); s != "none" {
				t.Fatalf("held before its smoke run: %s", s)
			}
			if c.name == "loops" {
				time.Sleep(200 * time.Millisecond)
				rt.StopDetectorPass(runtime.DetectorStopCompaction)
			} else {
				waitPass(t, rt)
			}
			p := detectorPasses(t, store, model)[0]
			if len(p.Pending) != 1 {
				t.Fatalf("pass %+v", p)
			}
			state := detectorState(t, store, model, "deploy-"+c.name)
			if c.held {
				if state != layer.StateHeld || p.Counts[rules.DefinedCountKey("deploy-counts")] != 2 {
					t.Fatalf("state %s pass %+v", state, p)
				}
			} else if state != "none" || p.Counts[rules.CountLevel1Refused] != 1 || p.Errors["deploy-"+c.name] == "" {
				t.Fatalf("state %s pass %+v", state, p)
			}
			if counter.Total() != 1 {
				t.Fatalf("requests %v, want the level-1 request alone", counter.ByStage())
			}
		})
	}
}

// A written Go detector's count reaches the next level-1 offer: the offer after its pass lists
// defined:<name> with the count, and that first non-zero count makes an offer due again.
func TestV2WrittenDetectorCountReachesTheNextOffer(t *testing.T) {
	rt, h, store, model := level1StoreSetup(t)
	t.Cleanup(func() { rt.StopDetectorPass(runtime.DetectorStopSessionEnd) })
	seedV2Evidence(t, store, model)
	held := holdDetector(t, store, model, "deploy-notes", detectCountNotes)
	if _, err := store.Layer().ClaimHeld([]layer.Definition{held}, true, "written for the test"); err != nil {
		t.Fatal(err)
	}
	if s := detectorState(t, store, model, "deploy-notes"); s != layer.StateWritten {
		t.Fatalf("state %s", s)
	}
	// Level 0 extracts the cut range inside the hook, so each compaction is an evidence unit
	// (source tokens read), and the detector counts its records.
	compact := func() string {
		t.Helper()
		h.Entries = slices.DeleteFunc(level1Branch(), func(e ledger.Entry) bool { return e.Type == "custom" })
		before := h.ModelsFake.Streams.Calls()
		h.ModelsFake.Streams.Replies = []map[string]any{testfix.AssistantText("nothing to change")}
		runV2Hook(rt, h, v2Event(h.Entries, "u2", 20_000, "threshold"))
		if h.ModelsFake.Streams.Calls() != before+1 {
			t.Fatalf("a compaction made %d requests, want exactly 1", h.ModelsFake.Streams.Calls()-before)
		}
		return fmt.Sprint(h.ModelsFake.Streams.Requests[before]["messages"])
	}
	first := compact()
	if !strings.Contains(first, "Counted over") || strings.Contains(first, rules.DefinedCountKey("deploy-notes")) {
		t.Fatalf("the first offer is missing, or carries a count no pass made yet:\n%s", first)
	}
	waitPass(t, rt)
	p := detectorPasses(t, store, model)
	if len(p) != 1 || p[0].Counts[rules.DefinedCountKey("deploy-notes")] == 0 {
		t.Fatalf("detector passes %+v", p)
	}
	key := fmt.Sprintf("%s: %d", rules.DefinedCountKey("deploy-notes"), p[0].Counts[rules.DefinedCountKey("deploy-notes")])
	if second := compact(); !strings.Contains(second, key) {
		t.Fatalf("the next offer does not carry %q:\n%s", key, second)
	}
}

// A held query detector is retired: no pass runs it, RanHeld never names it, and it is never
// settled; a written one counts nothing.
func TestV2QueryDetectorsAreRetired(t *testing.T) {
	rt, h, store, model := detectorSetup(t)
	query := "detector[1]{query,description}:\n  \"notes | where kind=fact\",facts"
	held, err := store.Layer().Hold(layer.Definition{Kind: layer.KindDetector, Name: "held-facts", Model: model, Text: query})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Layer().Put(layer.Definition{Kind: layer.KindDetector, Name: "written-facts", Model: model, Text: query}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		compactLevel0(t, rt, h, 5*time.Second)
		if rt.DetectorPassInFlight() != nil {
			t.Fatal("a pass started for query detectors")
		}
	}
	if len(detectorPasses(t, store, model)) != 0 {
		t.Fatal("a query detector ran")
	}
	if s := detectorState(t, store, model, "held-facts"); s != layer.StateHeld {
		t.Fatalf("the held query detector is %s", s)
	}
	for _, e := range modelEventsOf(t, store, model, curated.ModelKindPass) {
		for k := range e.Counts {
			if strings.HasPrefix(k, rules.DefinedCountKey("")) {
				t.Fatalf("a query detector counted: %+v", e.Counts)
			}
		}
	}
	_ = held
}

// The detector format level 1 is shown carries an example that is itself a valid Go detector.
func TestDetectorFormatExampleIsValid(t *testing.T) {
	example := rules.CodeDetectorText("func Count() int { return om.Notes(om.Kind(`plan`)).Matched }", "plans recorded")
	if !strings.Contains(detectorFormat, strings.ReplaceAll(strings.ReplaceAll(example, `\`, `\\`), "\n", `\n`)) {
		t.Fatalf("the format does not show the example: %s", detectorFormat)
	}
	if err := rules.CheckV2Proposal(layer.KindDetector, "plans", example); err != nil {
		t.Fatal(err)
	}
}

// stubDetectors replaces runDetector for one test with run, keyed by the detector's code.
func stubDetectors(t *testing.T, run func(ctx context.Context, code string) sandbox.DetectorResult) {
	t.Helper()
	saved := runDetector
	runDetector = func(ctx context.Context, code string, _ sandbox.Handler) sandbox.DetectorResult {
		return run(ctx, code)
	}
	t.Cleanup(func() { runDetector = saved })
}

// A host-side panic while a detector runs (the read engine answering its calls) is recovered in
// the pass's goroutine, outside the hook's recover: it is that detector's error, the next
// detector still runs, and the pass's event is written.
func TestV2DetectorHostPanicIsChargedToItsRun(t *testing.T) {
	stubDetectors(t, func(ctx context.Context, code string) sandbox.DetectorResult {
		if code == detectPanic {
			panic("read engine fault")
		}
		return sandbox.DetectorResult{Outcome: sandbox.OutcomeOK, Count: 3}
	})
	rt, h, store, model := detectorSetup(t)
	holdDetector(t, store, model, "a-host-panics", detectPanic)
	holdDetector(t, store, model, "b-counts", detectCountNotes)
	compactLevel0(t, rt, h, 5*time.Second)
	waitPass(t, rt)
	passes := detectorPasses(t, store, model)
	if len(passes) != 1 {
		t.Fatalf("passes %+v", passes)
	}
	p := passes[0]
	if !strings.Contains(p.Errors["a-host-panics"], "host panic: read engine fault") || p.Counts[rules.DefinedCountKey("b-counts")] != 3 || len(p.RanHeld) != 2 {
		t.Fatalf("pass %+v", p)
	}
	if rt.DetectorPassInFlight() != nil {
		t.Fatal("the pass is still in flight")
	}
}

// A run the stop reached before its child was spawned never started: it is not named in
// RanHeld, gets no error and is not the unfinished one, and the detectors after it are queued.
func TestV2DetectorCancelledBeforeSpawnIsQueued(t *testing.T) {
	stubDetectors(t, func(ctx context.Context, code string) sandbox.DetectorResult {
		if code == detectLoop {
			return sandbox.DetectorResult{Outcome: sandbox.OutcomeCancelled, Err: "context canceled", Queued: true}
		}
		return sandbox.DetectorResult{Outcome: sandbox.OutcomeOK, Count: 1}
	})
	rt, h, store, model := detectorSetup(t)
	first := holdDetector(t, store, model, "a-counts", detectCountNotes)
	holdDetector(t, store, model, "b-queued", detectLoop)
	holdDetector(t, store, model, "c-after", detectBlock)
	compactLevel0(t, rt, h, 5*time.Second)
	waitPass(t, rt)
	p := detectorPasses(t, store, model)[0]
	if len(p.RanHeld) != 1 || p.RanHeld[0] != first.HeldID() || len(p.Errors) != 0 || p.Unfinished != "" {
		t.Fatalf("pass %+v", p)
	}
	if _, counted := p.Counts[rules.DefinedCountKey("c-after")]; counted {
		t.Fatalf("a detector after a queued one ran: %+v", p)
	}
}

// A session_before_compact whose request is already cancelled compacts nothing, so it leaves the
// previous pass's run in flight: that run is not charged unfinished at a compaction.
func TestV2CancelledCompactionLeavesTheDetectorPassRunning(t *testing.T) {
	rt, h, store, model := detectorSetup(t)
	holdDetector(t, store, model, "a-loops", detectLoop)
	compactLevel0(t, rt, h, 5*time.Second)
	time.Sleep(200 * time.Millisecond)
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if outcome := HandleBeforeCompact(cancelled, rt, h, v2Event(h.Entries, "u2", 20_000, "threshold")); outcome.Compaction != nil {
		t.Fatalf("a cancelled request compacted: %+v", outcome)
	}
	if rt.DetectorPassInFlight() == nil || len(detectorPasses(t, store, model)) != 0 {
		t.Fatal("a cancelled compaction attempt ended the detector pass")
	}
	rt.StopDetectorPass(runtime.DetectorStopSessionEnd)
	if p := detectorPasses(t, store, model); len(p) != 1 || p[0].Errors["a-loops"] != runtime.DetectorStopSessionEnd {
		t.Fatalf("passes %+v", p)
	}
}

// No pass is built when no detector would run: the hook pays nothing for detectors a model
// does not have.
func TestV2DetectorPassIsBuiltOnlyWhenADetectorRuns(t *testing.T) {
	rt, h, store, model := detectorSetup(t)
	l, ok := harness.For(h, rt.Config(), h.Model)
	if !ok {
		t.Fatal("no ledger")
	}
	built := 0
	build := func() *omhost.DetectorPass { built++; return detectorPassData(nil, nil, nil) }
	startDetectorPassV2(context.Background(), rt, l, "u2", build, nil)
	if built != 0 || rt.DetectorPassInFlight() != nil {
		t.Fatalf("built %d with no detector", built)
	}
	holdDetector(t, store, model, "a-counts", detectCountNotes)
	startDetectorPassV2(context.Background(), rt, l, "u2", build, nil)
	waitPass(t, rt)
	if built != 1 {
		t.Fatalf("built %d with one detector", built)
	}
}
