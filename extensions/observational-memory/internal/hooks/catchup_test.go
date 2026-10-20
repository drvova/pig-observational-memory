package hooks

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/testfix"
)

type catchUpArgs struct {
	entries               []ledger.Entry
	maxChunks             int
	maxChunksSet          bool
	chunkMaxTokens        int
	consolidationInFlight bool
	resolveFails          bool
}

type catchUpFixture struct {
	rt      *runtime.Runtime
	h       *testfix.FakeHost
	entries []ledger.Entry
	release func()
}

func catchUpSetup(t *testing.T, a catchUpArgs) *catchUpFixture {
	t.Helper()
	cfg := config.Defaults
	cfg.ObservationsPoolMaxTokens = 20_000
	cfg.CompactionCatchUpMaxChunks = 2
	if a.maxChunksSet {
		cfg.CompactionCatchUpMaxChunks = a.maxChunks
	}
	cfg.ObserverChunkMaxTokens = a.chunkMaxTokens
	cfg.AgentMaxTurns = 4
	cfg.AgentMaxTokens = 8192
	cfg.ShowWorkerNotifications = true
	cfg.Model = &config.ConfiguredModel{Provider: "gtr7", ID: "memory", Thinking: "off"}
	rt := runtime.New()
	rt.SetConfig(cfg)
	h := testfix.NewFakeHost(a.entries...)
	h.Model = host.Model{"contextWindow": 65536}
	if !a.resolveFails {
		h.ModelsFake.Add(testfix.Model("gtr7", "memory", map[string]any{"contextWindow": 65536}), testfix.KeyAuth("key"))
	}
	fx := &catchUpFixture{rt: rt, h: h, entries: a.entries, release: func() {}}
	if a.consolidationInFlight {
		release := make(chan struct{})
		done := rt.LaunchConsolidationTask(nil, func(ctx context.Context) error { <-release; return nil })
		fx.release = func() {
			close(release)
			waitDone(t, done)
		}
	}
	t.Cleanup(func() { fx.release() })
	return fx
}

func (fx *catchUpFixture) run(firstKeptEntryID string) BeforeCompactOutcome {
	return HandleBeforeCompact(context.Background(), fx.rt, fx.h, BeforeCompactEvent{FirstKeptEntryID: firstKeptEntryID, TokensBefore: 123, BranchEntries: fx.entries, Reason: "threshold"})
}

func gapObservation(sourceEntryIDs ...string) ledger.Observation {
	return testfix.Observation("cccccccccccc", func(o *ledger.Observation) {
		o.SourceEntryIDs = sourceEntryIDs
		o.TokenCount = 12
	})
}

func TestCatchUpObservesGapThenCutsAtProposedBoundary(t *testing.T) {
	obs, _, _ := mockWorkers(t)
	gapObs := gapObservation("t1", "a2")
	obs.resolve([]ledger.Observation{gapObs})
	fx := catchUpSetup(t, catchUpArgs{entries: laggingBranch()})

	result := compactionOf(t, fx.run("u2"))

	if !reflect.DeepEqual(fx.h.ModelsFake.FindCalls, []string{"gtr7/memory"}) || obs.count() != 1 {
		t.Fatalf("finds %v observer calls %d", fx.h.ModelsFake.FindCalls, obs.count())
	}
	call := obs.call(0)
	if !reflect.DeepEqual(call.AllowedSourceEntryIDs, []string{"t1", "a2"}) || call.MaxOutputTokens != 8192 || call.ThinkingLevel != "off" {
		t.Fatalf("observer args %+v", call)
	}
	expectAppended(t, fx.h, 0, ledger.OMObservationsRecorded, recordedObservations([]ledger.Observation{gapObs}, "a2"))
	if result.FirstKeptEntryID != "u2" || !reflect.DeepEqual(observationIDs(result.Details.Observations), []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc"}) {
		t.Fatalf("result %+v", result)
	}
	expectNotification(t, fx.h, "Observational memory: observing 2 unobserved source entries (~37 tokens) before compacting", "info")
	if hasNotificationContaining(fx.h, "keeping them in context", "info") {
		t.Fatalf("notifications %+v", fx.h.Notifications)
	}
}

func TestCatchUpRetainsTailWhenObserverRecordsNothing(t *testing.T) {
	obs, _, _ := mockWorkers(t)
	obs.resolve(nil)
	fx := catchUpSetup(t, catchUpArgs{entries: laggingBranch()})

	result := compactionOf(t, fx.run("u2"))

	if len(fx.h.Appended) != 0 || result.FirstKeptEntryID != "a1" {
		t.Fatalf("appended %d result %+v", len(fx.h.Appended), result)
	}
}

func TestCatchUpRetainsTailAndWarnsWhenObserverFails(t *testing.T) {
	obs, _, _ := mockWorkers(t)
	obs.reject(errors.New("Connection error."))
	fx := catchUpSetup(t, catchUpArgs{entries: laggingBranch()})

	result := compactionOf(t, fx.run("u2"))

	if len(fx.h.Appended) != 0 || result.FirstKeptEntryID != "a1" {
		t.Fatalf("appended %d result %+v", len(fx.h.Appended), result)
	}
	expectNotification(t, fx.h, "Observational memory: catch-up observer failed: Connection error.", "warning")
}

func TestCatchUpCoversOneChunkWhenCapStopsIt(t *testing.T) {
	obs, _, _ := mockWorkers(t)
	obs.resolve([]ledger.Observation{gapObservation("t1")})
	// t1 alone exceeds the minimum chunk budget, so each chunk carries one entry.
	fx := catchUpSetup(t, catchUpArgs{entries: laggingBranch(string(make([]byte, 0)) + repeatX(4000)), maxChunks: 1, maxChunksSet: true, chunkMaxTokens: 256})

	result := compactionOf(t, fx.run("u2"))

	if obs.count() != 1 || len(fx.h.Appended) != 1 {
		t.Fatalf("observer calls %d appended %d", obs.count(), len(fx.h.Appended))
	}
	if data, _ := fx.h.Appended[0].Data.(map[string]any); fx.h.Appended[0].CustomType != ledger.OMObservationsRecorded || data["coversUpToId"] != "t1" {
		t.Fatalf("appended %+v", fx.h.Appended[0])
	}
	// a2 is still unobserved: the cut lands on it and it stays in context.
	if result.FirstKeptEntryID != "a2" {
		t.Fatalf("result %+v", result)
	}
}

func repeatX(n int) string {
	out := make([]byte, n)
	for i := range out {
		out[i] = 'x'
	}
	return string(out)
}

func TestCatchUpTreatsSkippedEntriesAsCovered(t *testing.T) {
	obs, _, _ := mockWorkers(t)
	obs.resolve([]ledger.Observation{gapObservation("a2")})
	// An assistant message with no content renders nothing and is never part
	// of a chunk; it must not count as a leftover after coverage passes it.
	entries := laggingBranch()
	empty := testfix.RawMessage("empty", "", map[string]any{"message": map[string]any{"role": "assistant", "content": []any{}, "stopReason": "aborted"}})
	entries = append(entries[:5], append([]ledger.Entry{empty}, entries[5:]...)...)
	fx := catchUpSetup(t, catchUpArgs{entries: entries, maxChunks: 1, maxChunksSet: true})

	if compactionOf(t, fx.run("u2")).FirstKeptEntryID != "u2" {
		t.Fatal("cut should stay at u2")
	}
}

func TestCatchUpDoesNothingWhenDisabled(t *testing.T) {
	obs, _, _ := mockWorkers(t)
	fx := catchUpSetup(t, catchUpArgs{entries: laggingBranch(), maxChunks: 0, maxChunksSet: true})

	result := compactionOf(t, fx.run("u2"))

	if len(fx.h.ModelsFake.FindCalls) != 0 || obs.count() != 0 || result.FirstKeptEntryID != "a1" {
		t.Fatalf("finds %v observer calls %d result %+v", fx.h.ModelsFake.FindCalls, obs.count(), result)
	}
}

func TestCatchUpDoesNotRunDuringBackgroundConsolidation(t *testing.T) {
	obs, _, _ := mockWorkers(t)
	fx := catchUpSetup(t, catchUpArgs{entries: laggingBranch(), consolidationInFlight: true})

	result := compactionOf(t, fx.run("u2"))

	if len(fx.h.ModelsFake.FindCalls) != 0 || obs.count() != 0 || result.FirstKeptEntryID != "a1" {
		t.Fatalf("finds %v observer calls %d result %+v", fx.h.ModelsFake.FindCalls, obs.count(), result)
	}
}

func TestCatchUpDoesNotBootstrapMemory(t *testing.T) {
	obs, _, _ := mockWorkers(t)
	fx := catchUpSetup(t, catchUpArgs{entries: []ledger.Entry{userMessage("u0"), assistantMessage("a0"), userMessage("u1")}})

	expectDelegated(t, fx.run("u1"))

	if len(fx.h.ModelsFake.FindCalls) != 0 || obs.count() != 0 {
		t.Fatalf("finds %v observer calls %d", fx.h.ModelsFake.FindCalls, obs.count())
	}
}

func TestCatchUpFallsBackWhenModelUnavailable(t *testing.T) {
	obs, _, _ := mockWorkers(t)
	fx := catchUpSetup(t, catchUpArgs{entries: laggingBranch(), resolveFails: true})

	result := compactionOf(t, fx.run("u2"))

	if obs.count() != 0 || len(fx.h.Appended) != 0 || result.FirstKeptEntryID != "a1" {
		t.Fatalf("observer calls %d appended %d result %+v", obs.count(), len(fx.h.Appended), result)
	}
}

func TestCatchUpStartsAtGlobalObservationFrontier(t *testing.T) {
	// Coverage stops at a0. A previous compaction retained raw text from u2.
	// u1/a1 are unread and outside the retained range. A marker through a3
	// must not claim them unread, so the catch-up starts at u1.
	obs, _, _ := mockWorkers(t)
	obs.resolve([]ledger.Observation{testfix.Observation("cccccccccccc", func(o *ledger.Observation) { o.SourceEntryIDs = []string{"u1", "a3"} })})
	entries := []ledger.Entry{
		userMessage("u0"), assistantMessage("a0"),
		coverage("om-a0", "a0", "aaaaaaaaaaaa"),
		userMessage("u1"), assistantMessage("a1"), userMessage("u2"), assistantMessage("a2"),
		testfix.CompactionEntry("cmp-0", testfix.CompactionArgs{FirstKeptEntryID: "u2"}),
		userMessage("u3"), assistantMessage("a3"), userMessage("u4"),
	}
	fx := catchUpSetup(t, catchUpArgs{entries: entries})

	fx.run("u4")

	if obs.count() != 1 || !reflect.DeepEqual(obs.call(0).AllowedSourceEntryIDs, []string{"u1", "a1", "u2", "a2", "u3", "a3"}) {
		t.Fatalf("observer calls %d args %+v", obs.count(), obs.calls)
	}
}

var _ = agents.ErrAborted
