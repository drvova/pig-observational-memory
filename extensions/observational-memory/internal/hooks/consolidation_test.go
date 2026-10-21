package hooks

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/testfix"
)

const opencodeBaseURL = "https://opencode.ai/zen/go/v1"

type consArgs struct {
	entries            []ledger.Entry
	observeAfterTokens int
	reflectAfterTokens int
	chunkMaxTokens     int
	poolMaxTokens      int
	poolTargetTokens   int
	workerMemoryTokens int
	quiet              bool
	passive            bool
	sessionID          string
	idle               bool
	// primaryModel and primaryAuth describe the configured memory model; the
	// defaults are anthropic/memory with reasoning, key "key" and header h: v.
	primaryModel host.Model
	primaryAuth  *host.Auth
	// fallback configures config.fallbackModel; fallbackModel/fallbackAuth
	// register it in the fake registry.
	fallback      *config.ConfiguredModel
	fallbackModel host.Model
	fallbackAuth  host.Auth
}

type consFixture struct {
	t    *testing.T
	rt   *runtime.Runtime
	h    *testfix.FakeHost
	obs  *observerMock
	ref  *reflectorMock
	drop *dropperMock
}

func strp(s string) *string { return &s }

func defaultPrimaryAuth() host.Auth {
	return host.Auth{OK: true, APIKey: strp("key"), Headers: map[string]any{"h": "v"}}
}

func consSetup(t *testing.T, a consArgs) *consFixture {
	t.Helper()
	obs, ref, drop := mockWorkers(t)
	cfg := config.Defaults
	cfg.ShowWorkerNotifications = !a.quiet
	cfg.Passive = a.passive
	cfg.ConsolidateWhenIdle = a.idle
	cfg.DebugLog = false
	cfg.ObserveAfterTokens = 1
	if a.observeAfterTokens > 0 {
		cfg.ObserveAfterTokens = a.observeAfterTokens
	}
	cfg.ReflectAfterTokens = 1
	if a.reflectAfterTokens > 0 {
		cfg.ReflectAfterTokens = a.reflectAfterTokens
	}
	cfg.ObserverChunkMaxTokens = a.chunkMaxTokens
	cfg.WorkerMemoryMaxTokens = a.workerMemoryTokens
	cfg.ObservationsPoolMaxTokens = 100
	if a.poolMaxTokens > 0 {
		cfg.ObservationsPoolMaxTokens = a.poolMaxTokens
	}
	cfg.ObservationsPoolTargetTokens = cfg.ObservationsPoolMaxTokens / 2
	if a.poolTargetTokens > 0 {
		cfg.ObservationsPoolTargetTokens = a.poolTargetTokens
	}
	cfg.AgentMaxTurns = 9
	cfg.AgentMaxTokens = 32000
	primary := a.primaryModel
	if primary == nil {
		primary = testfix.Model("anthropic", "memory", map[string]any{"reasoning": true})
	}
	cfg.Model = &config.ConfiguredModel{Provider: host.ModelProvider(primary), ID: host.ModelID(primary), Thinking: "minimal"}
	cfg.FallbackModel = a.fallback
	rt := runtime.New()
	rt.SetConfig(cfg)

	h := testfix.NewFakeHost(a.entries...)
	h.ID = "session-1"
	if a.sessionID != "" {
		h.ID = a.sessionID
	}
	h.Model = host.Model{"provider": "session"}
	auth := defaultPrimaryAuth()
	if a.primaryAuth != nil {
		auth = *a.primaryAuth
	}
	h.ModelsFake.Add(primary, auth)
	if a.fallbackModel != nil {
		h.ModelsFake.Add(a.fallbackModel, a.fallbackAuth)
	}
	return &consFixture{t: t, rt: rt, h: h, obs: obs, ref: ref, drop: drop}
}

// launch fires the trigger and returns the run's completion channel (nil when nothing launched).
func (fx *consFixture) launch() <-chan struct{} { return MaybeLaunchConsolidation(fx.rt, fx.h) }

// run fires the trigger, requires a launch, and waits for the run to finish.
func (fx *consFixture) run() {
	fx.t.Helper()
	done := fx.launch()
	if done == nil {
		fx.t.Fatal("consolidation did not launch")
	}
	waitDone(fx.t, done)
}

// runIfDue fires the trigger and waits for the run when one launched.
func (fx *consFixture) runIfDue() { waitDone(fx.t, fx.launch()) }

func (fx *consFixture) addEntries(entries ...ledger.Entry) {
	current, _ := fx.h.GetBranch()
	fx.h.SetBranch(append(current, entries...))
}

func (fx *consFixture) expectAppended(index int, customType string, data any) {
	fx.t.Helper()
	expectAppended(fx.t, fx.h, index, customType, data)
}

func (fx *consFixture) expectNoAppends() {
	fx.t.Helper()
	if len(fx.h.Appended) != 0 {
		fx.t.Fatalf("unexpected appends %+v", fx.h.Appended)
	}
}

// blockObserver makes the next observer call wait until the returned release runs.
func (fx *consFixture) blockObserver() (release func()) {
	gate := make(chan struct{})
	fx.obs.impl(func(ctx context.Context, args agents.ObserverArgs) (agents.ObserverResult, error) {
		select {
		case <-gate:
		case <-ctx.Done():
			return agents.ObserverResult{}, ctx.Err()
		}
		return agents.ObserverResult{}, nil
	})
	return func() { close(gate) }
}

func observationWith(id string, sourceEntryIDs []string, tokenCount int) ledger.Observation {
	return testfix.Observation(id, func(o *ledger.Observation) {
		o.SourceEntryIDs = sourceEntryIDs
		o.TokenCount = tokenCount
	})
}

var (
	obsA = observationWith("aaaaaaaaaaaa", []string{"raw-1"}, 10)
	obsB = observationWith("bbbbbbbbbbbb", []string{"raw-2"}, 10)
	refA = testfix.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})
)

func newObservation() ledger.Observation {
	return observationWith("cccccccccccc", []string{"raw-1"}, 4)
}

func opencodeHeaders(sessionID string) map[string]any {
	return map[string]any{"x-opencode-session": sessionID, "x-opencode-client": "pi"}
}

func TestConsolidationDoesNotLaunchBelowThresholds(t *testing.T) {
	entries := []ledger.Entry{
		text("raw-1", "aaaa"),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA}, "raw-1"),
		testfix.ReflectionsRecordedEntry("om-ref", []ledger.Reflection{refA}, "raw-1"),
		testfix.ObservationsDroppedEntry("om-drop", []string{"aaaaaaaaaaaa"}, "raw-1"),
	}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 10, reflectAfterTokens: 10})

	OnAgentStart(fx.rt, fx.h)
	OnTurnEnd(fx.rt, fx.h)

	if fx.rt.ConsolidationInFlight() || fx.launch() != nil || fx.obs.count() != 0 {
		t.Fatal("consolidation launched below thresholds")
	}
}

func TestConsolidationDoesNotLaunchInPassiveMode(t *testing.T) {
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, passive: true})

	OnAgentStart(fx.rt, fx.h)
	OnTurnEnd(fx.rt, fx.h)

	if fx.rt.ConsolidationInFlight() || fx.launch() != nil {
		t.Fatal("consolidation launched in passive mode")
	}
}

func TestConsolidationSharesOneLockAcrossEntrypoints(t *testing.T) {
	for _, order := range [][]string{{"agent_start", "turn_end"}, {"turn_end", "agent_start"}} {
		fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}})
		release := fx.blockObserver()
		for _, event := range order {
			switch event {
			case "agent_start":
				OnAgentStart(fx.rt, fx.h)
			case "turn_end":
				OnTurnEnd(fx.rt, fx.h)
			}
		}
		if !fx.rt.ConsolidationInFlight() {
			t.Fatalf("%v: nothing launched", order)
		}
		if fx.launch() != nil {
			t.Fatalf("%v: a second run launched while one was in flight", order)
		}
		done := fx.rt.ConsolidationDone()
		release()
		waitDone(t, done)
		if fx.obs.count() != 1 {
			t.Fatalf("%v: observer calls %d", order, fx.obs.count())
		}
	}
}

func TestConsolidationLaunchesOnDueRawBacklogDespiteLowProviderGrowth(t *testing.T) {
	// Coverage stops before the latest compaction, so the provider delta is
	// measured from the post-compaction baseline (1000 -> 1000 = 0 growth)
	// while the uncovered raw backlog is far above observeAfterTokens.
	entries := []ledger.Entry{
		text("raw-1", "aaaaaaaaaaaa"),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA}, "raw-1"),
		text("raw-2", strings.Repeat("b", 40)),
		testfix.CompactionEntry("cmp-1", testfix.CompactionArgs{FirstKeptEntryID: "assistant-1"}),
		assistantDone(1000),
		text("raw-3", "cccc"),
	}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 5, reflectAfterTokens: 1000})
	fx.h.Usage = usage(1000, 65536)

	fx.run()
}

func TestConsolidationDoesNotLaunchWhenBacklogAndGrowthBelowThreshold(t *testing.T) {
	entries := []ledger.Entry{
		text("raw-1", "aaaa"),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA}, "raw-1"),
		testfix.CompactionEntry("cmp-1", testfix.CompactionArgs{FirstKeptEntryID: "assistant-1"}),
		assistantDone(1000),
		text("raw-2", "bbbb"),
	}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 5, reflectAfterTokens: 1000})
	fx.h.Usage = usage(1002, 65536)

	if fx.launch() != nil {
		t.Fatal("consolidation launched")
	}
}

func TestConsolidationRunsObserverFirstAndAppendsObservations(t *testing.T) {
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999})
	obs := newObservation()
	fx.obs.resolve(agents.Observed([]ledger.Observation{obs}))

	fx.run()

	call := fx.obs.call(0)
	if !reflect.DeepEqual(call.AllowedSourceEntryIDs, []string{"raw-1"}) || call.MaxTurns != 9 || call.ThinkingLevel != "minimal" {
		t.Fatalf("observer args %+v", call)
	}
	fx.expectAppended(0, ledger.OMObservationsRecorded, recordedObservations([]ledger.Observation{obs}, "raw-1"))
}

func TestConsolidationForwardsOAuthShapedAuth(t *testing.T) {
	fx := consSetup(t, consArgs{
		entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999,
		primaryModel: testfix.Model("kimi-coding", "memory"),
		primaryAuth:  &host.Auth{OK: true, Headers: map[string]any{"Authorization": "Bearer oauth-token"}},
	})
	obs := newObservation()
	fx.obs.resolve(agents.Observed([]ledger.Observation{obs}))

	fx.run()

	call := fx.obs.call(0)
	if call.APIKey != nil || !reflect.DeepEqual(call.Headers, map[string]any{"Authorization": "Bearer oauth-token"}) {
		t.Fatalf("observer args %+v", call)
	}
	fx.expectAppended(0, ledger.OMObservationsRecorded, recordedObservations([]ledger.Observation{obs}, "raw-1"))
}

func TestConsolidationAddsOpencodeSessionHeaders(t *testing.T) {
	fx := consSetup(t, consArgs{
		entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999, sessionID: "session-abc",
		primaryModel: testfix.Model("opencode-go", "memory", map[string]any{"baseUrl": opencodeBaseURL, "reasoning": true}),
		primaryAuth:  &host.Auth{OK: true, APIKey: strp("go-key")},
	})
	obs := newObservation()
	fx.obs.resolve(agents.Observed([]ledger.Observation{obs}))

	fx.run()

	call := fx.obs.call(0)
	if call.APIKey == nil || *call.APIKey != "go-key" || !reflect.DeepEqual(call.Headers, opencodeHeaders("session-abc")) {
		t.Fatalf("observer args %+v", call)
	}
	fx.expectAppended(0, ledger.OMObservationsRecorded, recordedObservations([]ledger.Observation{obs}, "raw-1"))
}

func TestConsolidationMergesOpencodeHeadersWithAuthHeaders(t *testing.T) {
	fx := consSetup(t, consArgs{
		entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999,
		primaryModel: testfix.Model("opencode-go", "memory", map[string]any{"baseUrl": opencodeBaseURL}),
		primaryAuth:  &host.Auth{OK: true, APIKey: strp("go-key"), Headers: map[string]any{"Authorization": "Bearer go-key"}},
	})
	fx.obs.resolve(agents.Observed([]ledger.Observation{newObservation()}))

	fx.run()

	want := map[string]any{"Authorization": "Bearer go-key", "x-opencode-session": "session-1", "x-opencode-client": "pi"}
	if !reflect.DeepEqual(fx.obs.call(0).Headers, want) {
		t.Fatalf("headers %v", fx.obs.call(0).Headers)
	}
}

func TestConsolidationDetectsOpencodeByBaseURL(t *testing.T) {
	fx := consSetup(t, consArgs{
		entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999,
		primaryModel: testfix.Model("custom", "memory", map[string]any{"baseUrl": opencodeBaseURL}),
		primaryAuth:  &host.Auth{OK: true, APIKey: strp("go-key")},
	})
	fx.obs.resolve(agents.Observed([]ledger.Observation{newObservation()}))

	fx.run()

	if !reflect.DeepEqual(fx.obs.call(0).Headers, opencodeHeaders("session-1")) {
		t.Fatalf("headers %v", fx.obs.call(0).Headers)
	}
}

func TestConsolidationLeavesHeadersForOtherProviders(t *testing.T) {
	fx := consSetup(t, consArgs{
		entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999,
		primaryModel: testfix.Model("anthropic", "memory", map[string]any{"baseUrl": "https://api.anthropic.com"}),
		primaryAuth:  &host.Auth{OK: true, APIKey: strp("k")},
	})
	fx.obs.resolve(agents.Observed([]ledger.Observation{newObservation()}))

	fx.run()

	call := fx.obs.call(0)
	if call.APIKey == nil || *call.APIKey != "k" || len(call.Headers) != 0 {
		t.Fatalf("observer args %+v", call)
	}
}

func TestConsolidationUsesExistingCoverageAndRetriesLargerRanges(t *testing.T) {
	prior := observationWith("cccccccccccc", []string{"raw-1"}, 10)
	newObs := observationWith("dddddddddddd", []string{"raw-2"}, 10)
	entries := []ledger.Entry{
		text("raw-1", "aaaa"),
		testfix.ObservationsRecordedEntry("om-prior", []ledger.Observation{prior}, "raw-1"),
		text("raw-2", "bbbbbbbb"),
		text("raw-3", "cccccccc"),
	}
	fx := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999})
	fx.obs.resolve(agents.Observed([]ledger.Observation{newObs}))

	fx.run()

	if !reflect.DeepEqual(fx.obs.call(0).AllowedSourceEntryIDs, []string{"raw-2", "raw-3"}) {
		t.Fatalf("allowed %v", fx.obs.call(0).AllowedSourceEntryIDs)
	}
	fx.expectAppended(0, ledger.OMObservationsRecorded, recordedObservations([]ledger.Observation{newObs}, "raw-3"))
}

func TestConsolidationObserverNoOutputAppendsNothing(t *testing.T) {
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}})

	fx.run()

	fx.expectNoAppends()
	if fx.ref.count() != 0 || fx.drop.count() != 0 {
		t.Fatal("later stages ran without coverage")
	}
}

var observerRunning = regexp.MustCompile(`^Observational memory: observer running on ~\d+-token chunk$`)

func TestConsolidationShowsRoutineNotificationsByDefault(t *testing.T) {
	newRef := testfix.Reflection("ffffffffffff", []string{"aaaaaaaaaaaa"})
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, poolTargetTokens: 5})
	fx.obs.resolve(agents.Observed([]ledger.Observation{obsA}))
	fx.ref.resolve([]ledger.Reflection{newRef})
	fx.drop.resolve([]string{"aaaaaaaaaaaa"})

	fx.run()

	n := fx.h.Notifications
	if len(n) != 4 || !observerRunning.MatchString(n[0].Message) || n[0].Level != "info" {
		t.Fatalf("notifications %+v", n)
	}
	want := []testfix.Notification{
		{Message: "Observational memory: 1 observation recorded", Level: "info"},
		{Message: "Observational memory: reflector running (~2 tokens)", Level: "info"},
		{Message: "Observational memory: dropper running after reflection — active observation pool ~19 / 5 target tokens (380%)", Level: "info"},
	}
	if !reflect.DeepEqual(n[1:], want) {
		t.Fatalf("notifications %+v", n)
	}
}

func TestConsolidationSuppressesRoutineNotificationsButNotWarnings(t *testing.T) {
	entries := []ledger.Entry{text("raw-1", "aaaaaaaa")}
	newRef := testfix.Reflection("ffffffffffff", []string{"aaaaaaaaaaaa"})
	quiet := consSetup(t, consArgs{entries: entries, poolTargetTokens: 5, quiet: true})
	quiet.obs.resolve(agents.Observed([]ledger.Observation{obsA}))
	quiet.ref.resolve([]ledger.Reflection{newRef})
	quiet.drop.resolve([]string{"aaaaaaaaaaaa"})
	quiet.run()
	if quiet.obs.count() != 1 || quiet.ref.count() != 1 || quiet.drop.count() != 1 || len(quiet.h.Notifications) != 0 {
		t.Fatalf("quiet run: obs %d ref %d drop %d notifications %+v", quiet.obs.count(), quiet.ref.count(), quiet.drop.count(), quiet.h.Notifications)
	}

	// Deliberate empty is routine info: also hidden when quiet.
	noOutput := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999, quiet: true})
	noOutput.run()
	if len(noOutput.h.Notifications) != 0 {
		t.Fatalf("no-output notifications %+v", noOutput.h.Notifications)
	}

	// Real failures still surface as warnings when quiet.
	failed := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999, quiet: true})
	failed.obs.reject(&agents.StreamError{Stage: "observer", StopReason: "error", ErrorMessage: "prompt is too long"})
	failed.run()
	if len(failed.h.Notifications) != 1 || failed.h.Notifications[0].Level != "warning" || !strings.Contains(failed.h.Notifications[0].Message, "observer failed") {
		t.Fatalf("failed notifications %+v", failed.h.Notifications)
	}
}

func TestConsolidationReportsDeliberateEmptyAsInfo(t *testing.T) {
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999})

	fx.run()

	n := fx.h.Notifications
	if len(n) != 2 || !observerRunning.MatchString(n[0].Message) || n[0].Level != "info" ||
		n[1] != (testfix.Notification{Message: "Observational memory: observer found nothing new in this chunk (coverage unchanged; will retry later)", Level: "info"}) {
		t.Fatalf("notifications %+v", n)
	}
}

func TestConsolidationBacksOffAfterDeliberateEmpty(t *testing.T) {
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", strings.Repeat("a", 40))}, observeAfterTokens: 10, reflectAfterTokens: 999}) // 10 tokens

	fx.run()
	if fx.obs.count() != 1 {
		t.Fatalf("observer calls %d", fx.obs.count())
	}
	backoff := fx.rt.ObserverEmptyBackoff()
	if backoff == nil || *backoff != (runtime.ObserverEmptyBackoff{SessionIdentity: "session-1", CoverageID: "", TokensAtEmpty: 10, ChunkEndID: "raw-1"}) {
		t.Fatalf("backoff %+v", backoff)
	}

	// Same span, only 5 new tokens (< observeAfterTokens more): launched, but no re-fire.
	fx.addEntries(text("raw-2", strings.Repeat("b", 20)))
	fx.run()
	if fx.obs.count() != 1 {
		t.Fatalf("observer calls %d", fx.obs.count())
	}

	// 10 more new tokens: backoff satisfied, observer re-fires over the grown span.
	fx.addEntries(text("raw-3", strings.Repeat("c", 40)))
	fx.obs.resolve(agents.Observed([]ledger.Observation{obsA}))
	fx.run()
	if fx.obs.count() != 2 || fx.rt.ObserverEmptyBackoff() != nil {
		t.Fatalf("observer calls %d backoff %+v", fx.obs.count(), fx.rt.ObserverEmptyBackoff())
	}
}

func TestConsolidationDoesNotApplyBackoffToAnotherSession(t *testing.T) {
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", strings.Repeat("a", 40))}, observeAfterTokens: 10, reflectAfterTokens: 999})

	fx.run()
	if fx.obs.count() != 1 {
		t.Fatalf("observer calls %d", fx.obs.count())
	}

	fx.h.ID = "session-2"
	fx.run()
	if fx.obs.count() != 2 {
		t.Fatalf("observer calls %d", fx.obs.count())
	}
}

func TestConsolidationSurfacesStreamErrorsAsObserverFailure(t *testing.T) {
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999})
	fx.obs.reject(&agents.StreamError{Stage: "observer", StopReason: "error", ErrorMessage: "prompt is too long: 5198507 tokens > 1000000 maximum"})

	fx.run()

	if !strings.Contains(fx.rt.Snapshot().LastObserverError, "prompt is too long") {
		t.Fatalf("last observer error %q", fx.rt.Snapshot().LastObserverError)
	}
	expectNotification(t, fx.h, `Observational memory: observer failed: observer stream ended with stopReason "error": prompt is too long: 5198507 tokens > 1000000 maximum`, "warning")
	if hasNotificationContaining(fx.h, "no observations", "") || len(fx.h.Appended) != 0 || fx.rt.ObserverEmptyBackoff() != nil || fx.ref.count() != 0 {
		t.Fatalf("notifications %+v appended %d", fx.h.Notifications, len(fx.h.Appended))
	}
}

func TestConsolidationModelResolutionFailureSkipsAndNotifiesOnce(t *testing.T) {
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, primaryAuth: &host.Auth{OK: false}})

	fx.run()

	fx.expectNoAppends()
	skipped := 0
	for _, n := range fx.h.Notifications {
		if strings.HasPrefix(n.Message, "Observational memory: observer skipped — ") && n.Level == "warning" {
			skipped++
		}
	}
	if skipped != 1 || fx.obs.count() != 0 {
		t.Fatalf("notifications %+v observer calls %d", fx.h.Notifications, fx.obs.count())
	}
}

func TestConsolidationRereadsBranchSoObserverUnblocksReflector(t *testing.T) {
	newRef := testfix.Reflection("ffffffffffff", []string{"aaaaaaaaaaaa"})
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}})
	fx.obs.resolve(agents.Observed([]ledger.Observation{obsA}))
	fx.ref.resolve([]ledger.Reflection{newRef})

	fx.run()

	if fx.obs.count() != 1 || fx.ref.count() != 1 || !reflect.DeepEqual(fx.ref.call(0).Observations, []ledger.Observation{obsA}) {
		t.Fatalf("obs %d ref %d reflector observations %+v", fx.obs.count(), fx.ref.count(), fx.ref.calls)
	}
	fx.expectAppended(0, ledger.OMObservationsRecorded, recordedObservations([]ledger.Observation{obsA}, "raw-1"))
	fx.expectAppended(1, ledger.OMReflectionsRecorded, recordedReflections([]ledger.Reflection{newRef}, "raw-1"))
}

func TestConsolidationRunsReflectorOnly(t *testing.T) {
	newRef := testfix.Reflection("ffffffffffff", []string{"aaaaaaaaaaaa"})
	entries := []ledger.Entry{
		text("raw-1", "aaaaaaaa"),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA}, "raw-1"),
		text("raw-2", "bbbbbbbb"),
		testfix.ObservationsDroppedEntry("om-drop", []string{"bbbbbbbbbbbb"}, "raw-2"),
	}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 999})
	fx.ref.resolve([]ledger.Reflection{newRef})

	fx.run()

	call := fx.ref.call(0)
	if !reflect.DeepEqual(call.Observations, []ledger.Observation{obsA}) || call.MaxTurns != 9 || call.ThinkingLevel != "minimal" || fx.drop.count() != 0 {
		t.Fatalf("reflector args %+v dropper calls %d", call, fx.drop.count())
	}
	fx.expectAppended(0, ledger.OMReflectionsRecorded, recordedReflections([]ledger.Reflection{newRef}, "raw-1"))
}

func TestConsolidationRunsDropperAfterSameRunReflection(t *testing.T) {
	newRef := testfix.Reflection("ffffffffffff", []string{"aaaaaaaaaaaa"})
	entries := []ledger.Entry{
		text("raw-1", "aaaaaaaa"),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA}, "raw-1"),
		text("raw-2", "bbbbbbbb"),
	}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 999, poolTargetTokens: 5})
	fx.ref.resolve([]ledger.Reflection{newRef})
	fx.drop.resolve([]string{"aaaaaaaaaaaa"})

	fx.run()

	call := fx.drop.call(0)
	if !reflect.DeepEqual(call.Reflections, []ledger.Reflection{newRef}) || !reflect.DeepEqual(call.Observations, []ledger.Observation{obsA}) {
		t.Fatalf("dropper args %+v", call)
	}
	fx.expectAppended(0, ledger.OMReflectionsRecorded, recordedReflections([]ledger.Reflection{newRef}, "raw-1"))
	fx.expectAppended(1, ledger.OMObservationsDropped, droppedObservations([]string{"aaaaaaaaaaaa"}, "raw-1"))
}

func TestConsolidationDoesNotLaunchDropperOnlyWork(t *testing.T) {
	entries := []ledger.Entry{
		text("raw-1", "aaaaaaaa"),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA}, "raw-1"),
		testfix.ReflectionsRecordedEntry("om-ref", []ledger.Reflection{refA}, "raw-1"),
	}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 999, reflectAfterTokens: 999, poolTargetTokens: 5})

	if fx.launch() != nil {
		t.Fatal("dropper-only work launched")
	}
}

func TestConsolidationWaitsForReflectionWhenPoolOverTarget(t *testing.T) {
	entries := []ledger.Entry{
		text("raw-1", "aaaaaaaa"),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA}, "raw-1"),
		text("raw-2", "bbbbbbbb"),
	}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 999, reflectAfterTokens: 1, poolTargetTokens: 5})

	fx.run()

	if fx.ref.count() != 1 || fx.drop.count() != 0 {
		t.Fatalf("ref %d drop %d", fx.ref.count(), fx.drop.count())
	}
}

func TestConsolidationDoesNotLaunchWhenTombstonesReducePool(t *testing.T) {
	heavy := observationWith("cccccccccccc", []string{"raw-1"}, 100)
	entries := []ledger.Entry{
		text("raw-1", "aaaaaaaa"),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{heavy}, "raw-1"),
		testfix.ObservationsDroppedEntry("om-drop", []string{"cccccccccccc"}, "raw-1"),
		text("raw-2", "bbbbbbbb"),
		testfix.ReflectionsRecordedEntry("om-ref", []ledger.Reflection{refA}, "raw-2"),
	}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 999, reflectAfterTokens: 1, poolMaxTokens: 100})

	if fx.launch() != nil {
		t.Fatal("consolidation launched")
	}
}

func twoCoverageBranch() []ledger.Entry {
	return []ledger.Entry{
		text("raw-1", "aaaaaaaa"),
		testfix.ObservationsRecordedEntry("om-obs-a", []ledger.Observation{obsA}, "raw-1"),
		text("raw-2", "bbbbbbbb"),
		testfix.ObservationsRecordedEntry("om-obs-b", []ledger.Observation{obsB}, "raw-2"),
	}
}

func TestConsolidationUsesSameRunReflectionCoverageForDrops(t *testing.T) {
	newRef := testfix.Reflection("ffffffffffff", []string{"bbbbbbbbbbbb"})
	fx := consSetup(t, consArgs{entries: twoCoverageBranch(), observeAfterTokens: 999, poolMaxTokens: 10})
	fx.ref.resolve([]ledger.Reflection{newRef})
	fx.drop.resolve([]string{"bbbbbbbbbbbb"})

	fx.run()

	if !reflect.DeepEqual(fx.drop.call(0).Reflections, []ledger.Reflection{newRef}) {
		t.Fatalf("dropper reflections %+v", fx.drop.call(0).Reflections)
	}
	fx.expectAppended(0, ledger.OMReflectionsRecorded, recordedReflections([]ledger.Reflection{newRef}, "raw-2"))
	fx.expectAppended(1, ledger.OMObservationsDropped, droppedObservations([]string{"bbbbbbbbbbbb"}, "raw-2"))
}

func TestConsolidationDoesNotBootstrapDropperWithoutSameRunReflection(t *testing.T) {
	entries := []ledger.Entry{
		text("raw-1", "aaaaaaaa"),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA}, "raw-1"),
		text("raw-2", "bbbbbbbb"),
	}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 999, poolMaxTokens: 10})
	fx.drop.resolve([]string{"aaaaaaaaaaaa"})

	fx.run()

	if fx.ref.count() != 1 || fx.drop.count() != 0 {
		t.Fatalf("ref %d drop %d", fx.ref.count(), fx.drop.count())
	}
	fx.expectNoAppends()
}

func TestConsolidationDoesNotAppendReflectDropWithoutCoverage(t *testing.T) {
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, observeAfterTokens: 999})
	fx.ref.resolve([]ledger.Reflection{testfix.Reflection("ffffffffffff", []string{"aaaaaaaaaaaa"})})
	fx.drop.resolve([]string{"aaaaaaaaaaaa"})

	fx.runIfDue()

	if fx.ref.count() != 0 || fx.drop.count() != 0 {
		t.Fatalf("ref %d drop %d", fx.ref.count(), fx.drop.count())
	}
	fx.expectNoAppends()
}

func TestConsolidationAppendsNoEmptyReflectionOrDropEntries(t *testing.T) {
	entries := []ledger.Entry{text("raw-1", "aaaaaaaa"), testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA}, "raw-1")}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 999})

	fx.run()

	fx.expectNoAppends()
	if fx.drop.count() != 0 || hasNotificationContaining(fx.h, "dropper running", "info") {
		t.Fatalf("drop %d notifications %+v", fx.drop.count(), fx.h.Notifications)
	}
}

func TestConsolidationPreservesStageFailureBoundaries(t *testing.T) {
	observerFailure := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}})
	observerFailure.obs.reject(errors.New("observe failed"))
	observerFailure.run()
	if observerFailure.rt.Snapshot().LastObserverError != "observe failed" || observerFailure.ref.count() != 0 || observerFailure.drop.count() != 0 {
		t.Fatalf("observer failure state %+v", observerFailure.rt.Snapshot())
	}

	covered := []ledger.Entry{text("raw-1", "aaaaaaaa"), testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA}, "raw-1")}
	reflectorFailure := consSetup(t, consArgs{entries: covered, observeAfterTokens: 999})
	reflectorFailure.ref.reject(errors.New("reflect failed"))
	reflectorFailure.run()
	if reflectorFailure.rt.Snapshot().LastReflectorError != "reflect failed" || reflectorFailure.drop.count() != 0 || len(reflectorFailure.h.Appended) != 0 {
		t.Fatalf("reflector failure state %+v", reflectorFailure.rt.Snapshot())
	}

	newRef := testfix.Reflection("ffffffffffff", []string{"aaaaaaaaaaaa"})
	dropperFailure := consSetup(t, consArgs{entries: covered, observeAfterTokens: 999, poolMaxTokens: 10})
	dropperFailure.ref.resolve([]ledger.Reflection{newRef})
	dropperFailure.drop.reject(errors.New("drop failed"))
	dropperFailure.run()
	if dropperFailure.rt.Snapshot().LastDropperError != "drop failed" || len(dropperFailure.h.Appended) != 1 {
		t.Fatalf("dropper failure state %+v appended %d", dropperFailure.rt.Snapshot(), len(dropperFailure.h.Appended))
	}
	dropperFailure.expectAppended(0, ledger.OMReflectionsRecorded, recordedReflections([]ledger.Reflection{newRef}, "raw-1"))
}

// Fallback model retry.

var fallbackTarget = config.ConfiguredModel{Provider: "opencode-go", ID: "deepseek-v4.1-flash"}

func fallbackConfig(thinking config.ThinkingLevel) *config.ConfiguredModel {
	target := fallbackTarget
	target.Thinking = thinking
	return &target
}

func fallbackRegistryModel(extra ...map[string]any) host.Model {
	return testfix.Model(fallbackTarget.Provider, fallbackTarget.ID, extra...)
}

func isFallbackModel(model host.Model) bool {
	return host.ModelProvider(model) == fallbackTarget.Provider && host.ModelID(model) == fallbackTarget.ID
}

// authCount is how often the registry resolved credentials for a provider:
// a fallback resolution asks for them, the chunk-window lookup does not.
func authCount(models *testfix.FakeModels, provider string) int {
	count := 0
	for _, call := range models.AuthCalls {
		if call == provider {
			count++
		}
	}
	return count
}

func TestFallbackUsesResolutionLevelFallbackForObserver(t *testing.T) {
	obs := newObservation()
	fx := consSetup(t, consArgs{
		entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999,
		primaryModel: testfix.Model("anthropic", "haiku"), primaryAuth: &host.Auth{OK: false},
		fallback: fallbackConfig("high"), fallbackModel: fallbackRegistryModel(map[string]any{"baseUrl": opencodeBaseURL}), fallbackAuth: testfix.KeyAuth("go-key"),
	})
	fx.obs.resolve(agents.Observed([]ledger.Observation{obs}))

	fx.run()

	call := fx.obs.call(0)
	if fx.obs.count() != 1 || !isFallbackModel(call.Model) || *call.APIKey != "go-key" || !reflect.DeepEqual(call.Headers, opencodeHeaders("session-1")) || call.ThinkingLevel != "high" {
		t.Fatalf("observer calls %d args %+v", fx.obs.count(), call)
	}
	fx.expectAppended(0, ledger.OMObservationsRecorded, recordedObservations([]ledger.Observation{obs}, "raw-1"))
}

func TestFallbackRetriesObserverOnceAfterPrimaryStreamError(t *testing.T) {
	obs := newObservation()
	fx := consSetup(t, consArgs{
		entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999,
		fallback: fallbackConfig(""), fallbackModel: fallbackRegistryModel(map[string]any{"baseUrl": opencodeBaseURL}), fallbackAuth: testfix.KeyAuth("go-key"),
	})
	fx.obs.reject(&agents.StreamError{Stage: "observer", StopReason: "error", ErrorMessage: "primary down"})
	fx.obs.resolve(agents.Observed([]ledger.Observation{obs}))

	fx.run()

	if fx.obs.count() != 2 {
		t.Fatalf("observer calls %d", fx.obs.count())
	}
	second := fx.obs.call(1)
	if *second.APIKey != "go-key" || !reflect.DeepEqual(second.Headers, opencodeHeaders("session-1")) {
		t.Fatalf("retry args %+v", second)
	}
	fx.expectAppended(0, ledger.OMObservationsRecorded, recordedObservations([]ledger.Observation{obs}, "raw-1"))
	if fx.rt.Snapshot().LastObserverError != "" || !hasNotificationContaining(fx.h, "retrying with fallback model", "warning") {
		t.Fatalf("state %+v notifications %+v", fx.rt.Snapshot(), fx.h.Notifications)
	}
}

func TestFallbackReusedForLaterStagesInSamePass(t *testing.T) {
	obs := newObservation()
	fx := consSetup(t, consArgs{
		entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 1,
		fallback: fallbackConfig(""), fallbackModel: fallbackRegistryModel(), fallbackAuth: testfix.KeyAuth("go-key"),
	})
	fx.obs.reject(&agents.StreamError{Stage: "observer", StopReason: "error", ErrorMessage: "primary down"})
	fx.obs.resolve(agents.Observed([]ledger.Observation{obs}))
	fx.ref.resolve([]ledger.Reflection{testfix.Reflection("ffffffffffff", []string{"cccccccccccc"})})

	fx.run()

	if fx.ref.count() != 1 || *fx.ref.call(0).APIKey != "go-key" {
		t.Fatalf("reflector calls %d args %+v", fx.ref.count(), fx.ref.calls)
	}
	if authCount(fx.h.ModelsFake, fallbackTarget.Provider) != 1 {
		t.Fatalf("fallback resolved more than once: %v", fx.h.ModelsFake.AuthCalls)
	}
}

func TestFallbackAbortsObserverWhenRetryAlsoFails(t *testing.T) {
	fx := consSetup(t, consArgs{
		entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999,
		fallback: fallbackConfig(""), fallbackModel: fallbackRegistryModel(), fallbackAuth: testfix.KeyAuth("go-key"),
	})
	fx.obs.reject(&agents.StreamError{Stage: "observer", StopReason: "error", ErrorMessage: "primary down"})
	fx.obs.reject(&agents.StreamError{Stage: "observer", StopReason: "error", ErrorMessage: "fallback down"})

	fx.run()

	if fx.obs.count() != 2 || !strings.Contains(fx.rt.Snapshot().LastObserverError, "fallback down") {
		t.Fatalf("observer calls %d state %+v", fx.obs.count(), fx.rt.Snapshot())
	}
	fx.expectNoAppends()
}

func TestFallbackRetriesReflectorOnce(t *testing.T) {
	newRef := testfix.Reflection("ffffffffffff", []string{"aaaaaaaaaaaa"})
	entries := []ledger.Entry{text("raw-1", "aaaaaaaa"), testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA}, "raw-1")}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 999, fallback: fallbackConfig(""), fallbackModel: fallbackRegistryModel(), fallbackAuth: testfix.KeyAuth("go-key")})
	fx.ref.reject(errors.New("reflect failed"))
	fx.ref.resolve([]ledger.Reflection{newRef})

	fx.run()

	if fx.ref.count() != 2 {
		t.Fatalf("reflector calls %d", fx.ref.count())
	}
	fx.expectAppended(0, ledger.OMReflectionsRecorded, recordedReflections([]ledger.Reflection{newRef}, "raw-1"))
}

func TestFallbackRetriesDropperOnce(t *testing.T) {
	newRef := testfix.Reflection("ffffffffffff", []string{"aaaaaaaaaaaa"})
	entries := []ledger.Entry{text("raw-1", "aaaaaaaa"), testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA}, "raw-1")}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 999, poolMaxTokens: 10, fallback: fallbackConfig(""), fallbackModel: fallbackRegistryModel(), fallbackAuth: testfix.KeyAuth("go-key")})
	fx.ref.resolve([]ledger.Reflection{newRef})
	fx.drop.reject(errors.New("primary down"))
	fx.drop.resolve([]string{"aaaaaaaaaaaa"})

	fx.run()

	if fx.drop.count() != 2 {
		t.Fatalf("dropper calls %d", fx.drop.count())
	}
	first, second := fx.drop.call(0), fx.drop.call(1)
	if *first.APIKey != "key" || !reflect.DeepEqual(first.Headers, map[string]any{"h": "v"}) || !isFallbackModel(second.Model) || *second.APIKey != "go-key" {
		t.Fatalf("dropper args %+v / %+v", first, second)
	}
	if fx.rt.Snapshot().LastDropperError != "" || !hasNotificationContaining(fx.h, "dropper failed (primary down); retrying with fallback model", "warning") {
		t.Fatalf("state %+v notifications %+v", fx.rt.Snapshot(), fx.h.Notifications)
	}
	fx.expectAppended(0, ledger.OMReflectionsRecorded, recordedReflections([]ledger.Reflection{newRef}, "raw-1"))
	fx.expectAppended(1, ledger.OMObservationsDropped, droppedObservations([]string{"aaaaaaaaaaaa"}, "raw-1"))
	if len(fx.h.Appended) != 2 {
		t.Fatalf("appended %d", len(fx.h.Appended))
	}
}

func TestFallbackNotUsedWhenNotConfigured(t *testing.T) {
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999})
	fx.obs.reject(errors.New("observe failed"))

	fx.run()

	if fx.obs.count() != 1 || fx.rt.Snapshot().LastObserverError != "observe failed" {
		t.Fatalf("observer calls %d state %+v", fx.obs.count(), fx.rt.Snapshot())
	}
}

func TestFallbackDoesNotRetryStageAlreadyOnFallback(t *testing.T) {
	fx := consSetup(t, consArgs{
		entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999,
		primaryAuth: &host.Auth{OK: false},
		fallback:    fallbackConfig(""), fallbackModel: fallbackRegistryModel(), fallbackAuth: testfix.KeyAuth("go-key"),
	})
	fx.obs.reject(&agents.StreamError{Stage: "observer", StopReason: "error", ErrorMessage: "fallback down"})

	fx.run()

	if fx.obs.count() != 1 || !isFallbackModel(fx.obs.call(0).Model) {
		t.Fatalf("observer calls %d args %+v", fx.obs.count(), fx.obs.calls)
	}
}

func TestFallbackUsesFallbackThinkingLevelOnRetry(t *testing.T) {
	fx := consSetup(t, consArgs{
		entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999,
		fallback: fallbackConfig("high"), fallbackModel: fallbackRegistryModel(), fallbackAuth: testfix.KeyAuth("go-key"),
	})
	fx.obs.reject(&agents.StreamError{Stage: "observer", StopReason: "error", ErrorMessage: "primary down"})
	fx.obs.resolve(agents.Observed([]ledger.Observation{newObservation()}))

	fx.run()

	if fx.obs.count() != 2 || fx.obs.call(0).ThinkingLevel != "minimal" || fx.obs.call(1).ThinkingLevel != "high" {
		t.Fatalf("observer calls %+v", fx.obs.calls)
	}
}

func TestFallbackCapsObserverChunkToSmallerWindow(t *testing.T) {
	// Only the fallback model advertises a window; its 100-token window caps the
	// chunk at the 256-token minimum so a single source entry fits per run.
	entries := []ledger.Entry{text("raw-1", strings.Repeat("a", 800)), text("raw-2", strings.Repeat("b", 800))}
	fx := consSetup(t, consArgs{
		entries: entries, reflectAfterTokens: 999,
		primaryModel: testfix.Model("anthropic", "memory", map[string]any{"contextWindow": 200000}),
		fallback:     fallbackConfig(""), fallbackModel: fallbackRegistryModel(map[string]any{"contextWindow": 100}), fallbackAuth: testfix.KeyAuth("go-key"),
	})
	fx.obs.resolve(agents.Observed([]ledger.Observation{observationWith("111111111111", []string{"raw-1"}, 4)}))

	fx.run()

	if !reflect.DeepEqual(fx.obs.call(0).AllowedSourceEntryIDs, []string{"raw-1"}) {
		t.Fatalf("allowed %v", fx.obs.call(0).AllowedSourceEntryIDs)
	}
}

// Observer chunk cap.

func TestChunkCapDrainsBacklogIncrementally(t *testing.T) {
	first := observationWith("111111111111", []string{"raw-1"}, 4)
	second := observationWith("222222222222", []string{"raw-2"}, 4)
	entries := []ledger.Entry{text("raw-1", strings.Repeat("a", 800)), text("raw-2", strings.Repeat("b", 800)), text("raw-3", strings.Repeat("c", 800))}
	fx := consSetup(t, consArgs{entries: entries, chunkMaxTokens: 256, reflectAfterTokens: 999})
	fx.obs.resolve(agents.Observed([]ledger.Observation{first}))
	fx.obs.resolve(agents.Observed([]ledger.Observation{second}))

	fx.run()

	// Only the oldest entry fits under the cap; coverage advances to it, not to the backlog tail.
	if !reflect.DeepEqual(fx.obs.call(0).AllowedSourceEntryIDs, []string{"raw-1"}) {
		t.Fatalf("allowed %v", fx.obs.call(0).AllowedSourceEntryIDs)
	}
	fx.expectAppended(0, ledger.OMObservationsRecorded, recordedObservations([]ledger.Observation{first}, "raw-1"))

	// The next run continues from the advanced coverage.
	fx.run()

	if !reflect.DeepEqual(fx.obs.call(1).AllowedSourceEntryIDs, []string{"raw-2"}) {
		t.Fatalf("allowed %v", fx.obs.call(1).AllowedSourceEntryIDs)
	}
	fx.expectAppended(1, ledger.OMObservationsRecorded, recordedObservations([]ledger.Observation{second}, "raw-2"))
}

func TestChunkCapBoundsOversizedToolResult(t *testing.T) {
	first := observationWith("333333333333", []string{"raw-huge"}, 4)
	second := observationWith("555555555555", []string{"raw-next"}, 4)
	hugeText := "HEAD:" + strings.Repeat("m", 2_000) + ":TAIL"
	entries := []ledger.Entry{
		testfix.Entry(map[string]any{
			"type": "message", "id": "raw-huge", "parentId": nil, "timestamp": testfix.DefaultTimestamp,
			"message": map[string]any{"role": "toolResult", "toolCallId": "tool-1", "toolName": "bash", "content": []any{map[string]any{"type": "text", "text": hugeText}}, "isError": false, "timestamp": 1777716000000},
		}),
		text("raw-next", "later"),
	}
	fx := consSetup(t, consArgs{entries: entries, chunkMaxTokens: 100, reflectAfterTokens: 999})
	fx.obs.resolve(agents.Observed([]ledger.Observation{first}))
	fx.obs.resolve(agents.Observed([]ledger.Observation{second}))

	fx.run()

	call := fx.obs.call(0)
	if !reflect.DeepEqual(call.AllowedSourceEntryIDs, []string{"raw-huge"}) {
		t.Fatalf("allowed %v", call.AllowedSourceEntryIDs)
	}
	for _, fragment := range []string{"HEAD:", ":TAIL", "middle omitted: source exceeds observer input budget"} {
		if !strings.Contains(call.Chunk, fragment) {
			t.Fatalf("chunk lacks %q:\n%s", fragment, call.Chunk)
		}
	}
	if strings.Contains(call.Chunk, "raw-next") {
		t.Fatalf("chunk leaked the next entry:\n%s", call.Chunk)
	}
	fx.expectAppended(0, ledger.OMObservationsRecorded, recordedObservations([]ledger.Observation{first}, "raw-huge"))

	// The source id still points at the full ledger entry; the next run starts
	// after it instead of retrying the oversized input forever.
	fx.run()

	if !reflect.DeepEqual(fx.obs.call(1).AllowedSourceEntryIDs, []string{"raw-next"}) {
		t.Fatalf("allowed %v", fx.obs.call(1).AllowedSourceEntryIDs)
	}
	fx.expectAppended(1, ledger.OMObservationsRecorded, recordedObservations([]ledger.Observation{second}, "raw-next"))
}

func TestChunkCapDerivedFromModelContextWindow(t *testing.T) {
	// contextWindow 1,280 -> cap = floor(1,280 * 0.2) = 256, so only raw-1 fits.
	entries := []ledger.Entry{text("raw-1", strings.Repeat("a", 800)), text("raw-2", strings.Repeat("b", 800))}
	fx := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999, primaryModel: testfix.Model("anthropic", "memory", map[string]any{"reasoning": true, "contextWindow": 1_280})})
	obs := observationWith("444444444444", []string{"raw-1"}, 4)
	fx.obs.resolve(agents.Observed([]ledger.Observation{obs}))

	fx.run()

	if !reflect.DeepEqual(fx.obs.call(0).AllowedSourceEntryIDs, []string{"raw-1"}) {
		t.Fatalf("allowed %v", fx.obs.call(0).AllowedSourceEntryIDs)
	}
	fx.expectAppended(0, ledger.OMObservationsRecorded, recordedObservations([]ledger.Observation{obs}, "raw-1"))
}

// consolidateWhenIdle.

func dueEntries() []ledger.Entry { return []ledger.Entry{text("raw-1", "aaaaaaaa")} }

func TestIdleLaunchesOnlyFromAgentSettled(t *testing.T) {
	fx := consSetup(t, consArgs{entries: dueEntries(), idle: true})
	release := fx.blockObserver()

	OnAgentStart(fx.rt, fx.h)
	OnTurnEnd(fx.rt, fx.h)
	if fx.rt.ConsolidationInFlight() {
		t.Fatal("launched from a non-settled event")
	}

	OnAgentSettled(fx.rt, fx.h, ConsolidationHooks{})
	if !fx.rt.ConsolidationInFlight() {
		t.Fatal("agent_settled did not launch")
	}
	done := fx.rt.ConsolidationDone()
	release()
	waitDone(t, done)
}

func TestIdleKeepsDefaultEntrypointsWhenDisabled(t *testing.T) {
	fx := consSetup(t, consArgs{entries: dueEntries(), idle: false})
	release := fx.blockObserver()

	OnAgentSettled(fx.rt, fx.h, ConsolidationHooks{})
	if fx.rt.ConsolidationInFlight() {
		t.Fatal("agent_settled launched while disabled")
	}
	OnTurnEnd(fx.rt, fx.h)
	if !fx.rt.ConsolidationInFlight() {
		t.Fatal("turn_end did not launch")
	}
	done := fx.rt.ConsolidationDone()
	release()
	waitDone(t, done)
}

func settledWithAfterHook(fx *consFixture) <-chan struct{} {
	after := make(chan struct{})
	OnAgentSettled(fx.rt, fx.h, ConsolidationHooks{AfterIdleConsolidation: func(h host.Host) bool { close(after); return false }})
	return after
}

func TestIdlePassesRunContextToWorkers(t *testing.T) {
	fx := consSetup(t, consArgs{entries: dueEntries(), idle: true, reflectAfterTokens: 999})
	fx.obs.resolve(agents.Observed([]ledger.Observation{newObservation()}))

	waitDone(t, settledWithAfterHook(fx))

	if fx.obs.count() != 1 || fx.obs.ctxs[0] == nil || fx.obs.ctxs[0] == context.Background() {
		t.Fatalf("observer calls %d ctx %v", fx.obs.count(), fx.obs.ctxs)
	}
}

// abortingObserver makes the next observer call block until its context is
// cancelled; the returned channel closes once the observer has been entered.
func (fx *consFixture) abortingObserver() <-chan struct{} {
	entered := make(chan struct{})
	fx.obs.impl(func(ctx context.Context, args agents.ObserverArgs) (agents.ObserverResult, error) {
		close(entered)
		<-ctx.Done()
		return agents.ObserverResult{}, ctx.Err()
	})
	return entered
}

func TestIdleAbortsInFlightRunOnAgentStart(t *testing.T) {
	fx := consSetup(t, consArgs{entries: dueEntries(), idle: true, reflectAfterTokens: 999})
	entered := fx.abortingObserver()

	after := settledWithAfterHook(fx)
	done := fx.rt.ConsolidationDone()
	if done == nil {
		t.Fatal("agent_settled did not launch")
	}
	waitDone(t, entered)
	OnAgentStart(fx.rt, fx.h)
	waitDone(t, done)
	// An aborted run is not idle time: the compaction after-hook is skipped.
	expectNeverClosed(t, after)

	fx.expectNoAppends()
	if fx.rt.Snapshot().LastObserverError != "" || hasNotificationContaining(fx.h, "failed", "warning") {
		t.Fatalf("state %+v notifications %+v", fx.rt.Snapshot(), fx.h.Notifications)
	}
	expectNotification(t, fx.h, "Observational memory: memory workers paused while the agent runs (consolidateWhenIdle)", "info")
}

func TestIdleDoesNotRetryWithFallbackWhenAborted(t *testing.T) {
	fx := consSetup(t, consArgs{
		entries: dueEntries(), idle: true, reflectAfterTokens: 999,
		fallback: fallbackConfig(""), fallbackModel: fallbackRegistryModel(), fallbackAuth: testfix.KeyAuth("go-key"),
	})
	entered := fx.abortingObserver()

	after := settledWithAfterHook(fx)
	done := fx.rt.ConsolidationDone()
	waitDone(t, entered)
	OnAgentStart(fx.rt, fx.h)
	waitDone(t, done)
	expectNeverClosed(t, after)

	if fx.obs.count() != 1 || authCount(fx.h.ModelsFake, fallbackTarget.Provider) != 0 {
		t.Fatalf("observer calls %d auth lookups %v", fx.obs.count(), fx.h.ModelsFake.AuthCalls)
	}
	fx.expectNoAppends()
}

func TestIdleDoesNotAbortWhenNothingInFlight(t *testing.T) {
	fx := consSetup(t, consArgs{entries: dueEntries(), idle: true})

	OnAgentStart(fx.rt, fx.h)

	if fx.rt.ConsolidationInFlight() || len(fx.h.Notifications) != 0 {
		t.Fatalf("in flight %v notifications %+v", fx.rt.ConsolidationInFlight(), fx.h.Notifications)
	}
}

func TestIdleCallsAfterHookWhenRunFinishesOrImmediately(t *testing.T) {
	fx := consSetup(t, consArgs{entries: dueEntries(), idle: true, reflectAfterTokens: 999})
	release := fx.blockObserver()
	after := settledWithAfterHook(fx)
	select {
	case <-after:
		t.Fatal("after hook ran before the consolidation finished")
	default:
	}
	release()
	waitDone(t, after)

	idle := consSetup(t, consArgs{
		entries:            []ledger.Entry{text("raw-1", "aaaa"), testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{newObservation()}, "raw-1")},
		idle:               true,
		observeAfterTokens: 100,
		reflectAfterTokens: 100,
	})
	calls := 0
	OnAgentSettled(idle.rt, idle.h, ConsolidationHooks{AfterIdleConsolidation: func(h host.Host) bool { calls++; return false }})
	if calls != 1 {
		t.Fatalf("after hook calls %d", calls)
	}
}

// Bounded worker memory.

func TestBoundedWorkerMemorySendsNewestPriorMemoryThatFits(t *testing.T) {
	old := []ledger.Observation{}
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("a%d", i)
		id += strings.Repeat("a", 12-len(id))
		old = append(old, testfix.Observation(id, func(o *ledger.Observation) {
			o.SourceEntryIDs = []string{fmt.Sprintf("raw-%d", i)}
			o.Content = "Old " + strings.Repeat("o", 120)
		}))
	}
	entries := []ledger.Entry{
		text("raw-1", "a"), text("raw-2", "b"), text("raw-3", "c"),
		testfix.ObservationsRecordedEntry("om-obs", old, "raw-3"),
		text("raw-4", "dddddddd"),
	}
	fx := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999, workerMemoryTokens: 50})
	fx.obs.resolve(agents.Observed([]ledger.Observation{observationWith("cccccccccccc", []string{"raw-4"}, 10)}))

	fx.run()

	prior := fx.obs.call(0).PriorObservations
	if len(prior) != 1 || !strings.Contains(prior[0], "[a3aaaaaaaaaa]") {
		t.Fatalf("prior observations %v", prior)
	}
}

// A session that ends mid-run abandons the run quietly (upstream issue #22).
func TestSessionEndAbortsInFlightRunQuietly(t *testing.T) {
	fx := consSetup(t, consArgs{entries: dueEntries(), reflectAfterTokens: 999})
	entered := fx.abortingObserver()

	done := fx.launch()
	if done == nil {
		t.Fatal("consolidation did not launch")
	}
	waitDone(t, entered)
	OnSessionEnd(fx.rt, "session_shutdown")
	waitDone(t, done)

	fx.expectNoAppends()
	// The routine "observer running" notice preceded the abort; no failure or
	// pause notice follows it.
	if fx.rt.Snapshot().LastObserverError != "" || fx.rt.ConsolidationInFlight() || hasNotificationContaining(fx.h, "failed", "") || hasNotificationContaining(fx.h, "paused", "") {
		t.Fatalf("state %+v notifications %+v", fx.rt.Snapshot(), fx.h.Notifications)
	}
	before := len(fx.h.Notifications)

	// Nothing in flight: a no-op.
	OnSessionEnd(fx.rt, "session_before_switch")
	if len(fx.h.Notifications) != before {
		t.Fatalf("notifications %+v", fx.h.Notifications)
	}
}

// A session end (a tree jump) waits for the run it aborted, and a stage that
// passed its branch check as the abort came appends nothing: the host moves
// the leaf once the handler returns, and the entry would land on the new branch.
func TestSessionEndWaitsForAbortedRunAndDropsItsAppend(t *testing.T) {
	observed := []ledger.Entry{text("raw-1", "aaaaaaaa"), testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA}, "raw-1")}
	newRef := testfix.Reflection("ffffffffffff", []string{"aaaaaaaaaaaa"})
	for _, tc := range []struct {
		stage string
		args  consArgs
		// script makes the stage call park before its branch check.
		script func(fx *consFixture, park func())
		// appends is what the run records before that stage (the dropper follows a reflection).
		appends int
	}{
		{"observer", consArgs{entries: dueEntries(), reflectAfterTokens: 999}, func(fx *consFixture, park func()) {
			fx.obs.impl(func(context.Context, agents.ObserverArgs) (agents.ObserverResult, error) {
				park()
				return agents.Observed([]ledger.Observation{newObservation()}), nil
			})
		}, 0},
		{"reflector", consArgs{entries: observed, observeAfterTokens: 999}, func(fx *consFixture, park func()) {
			fx.ref.impl(func(context.Context, agents.ReflectorArgs) ([]ledger.Reflection, error) {
				park()
				return []ledger.Reflection{newRef}, nil
			})
		}, 0},
		{"dropper", consArgs{entries: observed, observeAfterTokens: 999, poolMaxTokens: 10}, func(fx *consFixture, park func()) {
			fx.ref.resolve([]ledger.Reflection{newRef})
			fx.drop.impl(func(context.Context, agents.DropperArgs) ([]string, error) {
				park()
				return []string{"aaaaaaaaaaaa"}, nil
			})
		}, 1},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			fx := consSetup(t, tc.args)
			h := &hookedBranchHost{FakeHost: fx.h}
			parked, release := make(chan struct{}), make(chan struct{})
			// The stage's branch check is its next host read after the model call.
			tc.script(fx, func() { h.onNextGetBranch(func() { close(parked); <-release }) })
			done := MaybeLaunchConsolidation(fx.rt, h)
			if done == nil {
				t.Fatal("consolidation did not launch")
			}
			waitDone(t, parked)

			returned := make(chan struct{})
			go func() {
				OnSessionEnd(fx.rt, "session_before_tree")
				close(returned)
			}()
			expectNeverClosed(t, returned)
			close(release)
			waitDone(t, returned)
			select {
			case <-done:
			default:
				t.Fatal("OnSessionEnd returned before the aborted run was over")
			}
			if len(fx.h.Appended) != tc.appends {
				t.Fatalf("appended %+v", fx.h.Appended)
			}
		})
	}
}

// The wait is bounded: a run stuck in an uncancellable call does not hold up
// the host's session change.
func TestSessionEndWaitIsBounded(t *testing.T) {
	old := SessionEndWaitTimeout
	SessionEndWaitTimeout = 20 * time.Millisecond
	t.Cleanup(func() { SessionEndWaitTimeout = old })
	fx := consSetup(t, consArgs{entries: dueEntries(), reflectAfterTokens: 999})
	entered, release := make(chan struct{}), make(chan struct{})
	fx.obs.impl(func(context.Context, agents.ObserverArgs) (agents.ObserverResult, error) {
		close(entered)
		<-release
		return agents.Observed([]ledger.Observation{newObservation()}), nil
	})
	done := fx.launch()
	waitDone(t, entered)

	returned := make(chan struct{})
	go func() {
		OnSessionEnd(fx.rt, "session_shutdown")
		close(returned)
	}()
	waitDone(t, returned)
	close(release)
	waitDone(t, done)
	fx.expectNoAppends()
}

func eventually(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met in time")
}

// An unresolvable model is reported once; the reflector announces "running"
// only after the model resolved.
func TestReflectorAnnouncesRunningOnlyAfterResolving(t *testing.T) {
	entries := []ledger.Entry{text("raw-1", "aaaaaaaa"), testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA}, "raw-1")}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 999, primaryAuth: &host.Auth{OK: false}})

	fx.run()

	skipped := 0
	for _, n := range fx.h.Notifications {
		if strings.HasPrefix(n.Message, "Observational memory: reflector skipped — ") {
			skipped++
		}
	}
	if skipped != 1 || hasNotificationContaining(fx.h, "reflector running", "") {
		t.Fatalf("notifications %+v", fx.h.Notifications)
	}
}

// Provider growth alone does not make the reflector due.
func TestReflectorNotDueWithoutNewObservations(t *testing.T) {
	entries := []ledger.Entry{
		text("raw-1", "aaaaaaaa"),
		assistantDone(1000),
		testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA}, "assistant-1"),
		testfix.ReflectionsRecordedEntry("om-ref", []ledger.Reflection{refA}, "assistant-1"),
	}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 1_000_000_000, reflectAfterTokens: 1})
	fx.h.Usage = usage(90_000, 200_000)

	if fx.launch() != nil || fx.ref.count() != 0 {
		t.Fatal("reflector launched on provider growth alone")
	}
}

// A run aborted while the fallback resolves is not retried and not reported.
func TestFallbackRetrySkippedWhenAbortedDuringResolution(t *testing.T) {
	fx := consSetup(t, consArgs{entries: dueEntries(), reflectAfterTokens: 999, fallback: fallbackConfig(""), fallbackModel: fallbackRegistryModel(), fallbackAuth: testfix.KeyAuth("go-key")})
	fx.obs.reject(&agents.StreamError{Stage: "observer", StopReason: "error", ErrorMessage: "primary down"})
	fx.obs.resolve(agents.Observed([]ledger.Observation{newObservation()}))
	finds := 0
	fx.h.ModelsFake.OnFind = func(provider, id string) {
		// The first lookup sizes the observer chunk; the second resolves the retry.
		if provider == fallbackTarget.Provider {
			finds++
			if finds == 2 {
				fx.rt.AbortConsolidation()
			}
		}
	}

	fx.run()

	if fx.obs.count() != 1 || hasNotificationContaining(fx.h, "retrying with fallback", "") || len(fx.h.Appended) != 0 || fx.rt.Snapshot().LastObserverError != "" {
		t.Fatalf("observer calls %d state %+v notifications %+v", fx.obs.count(), fx.rt.Snapshot(), fx.h.Notifications)
	}
}

// In idle mode a settle while a run is in flight waits for it before the
// compaction check; the direct check runs only outside idle mode.
func TestIdleSettleWaitsForInFlightRunBeforeCompactionCheck(t *testing.T) {
	fx := consSetup(t, consArgs{entries: dueEntries(), idle: true, reflectAfterTokens: 999})
	release := fx.blockObserver()
	var mu sync.Mutex
	after := 0
	count := func() int { mu.Lock(); defer mu.Unlock(); return after }
	hook := ConsolidationHooks{AfterIdleConsolidation: func(host.Host) bool { mu.Lock(); after++; mu.Unlock(); return false }}

	OnAgentSettled(fx.rt, fx.h, hook) // launches
	OnAgentSettled(fx.rt, fx.h, hook) // in flight: waits
	if count() != 0 {
		t.Fatalf("after hook ran %d times while the run was in flight", count())
	}
	release()
	eventually(t, func() bool { return count() == 2 })

	// The event entry point: idle mode defers the compaction check until the
	// run it launched is over, never beside the worker.
	trigger := consSetup(t, consArgs{entries: append(covered(dueEntries(), ""), text("raw-2", "bbbbbbbb")), idle: true, reflectAfterTokens: 999})
	cfg := trigger.rt.Config()
	cfg.CompactAfterTokens = 1
	trigger.rt.SetConfig(cfg)
	releaseTrigger := trigger.blockObserver()
	OnAgentSettledEvent(trigger.rt, trigger.h)
	if !trigger.rt.ConsolidationInFlight() {
		t.Fatal("agent_settled did not launch the due run")
	}
	never(t, "compaction ran beside the idle worker", func() bool { return trigger.h.CompactCount() != 0 })
	releaseTrigger()
	eventually(t, func() bool { return trigger.h.CompactCount() == 1 })

	// Nothing due or in flight: the check runs at once.
	nothingDue := consSetup(t, consArgs{entries: covered(dueEntries(), ""), idle: true, reflectAfterTokens: 999, observeAfterTokens: 1_000_000_000})
	cfg = nothingDue.rt.Config()
	cfg.CompactAfterTokens = 1
	nothingDue.rt.SetConfig(cfg)
	OnAgentSettledEvent(nothingDue.rt, nothingDue.h)
	eventually(t, func() bool { return nothingDue.h.CompactCount() == 1 })
}

// compactionDue makes proactive compaction due on fx's branch: a one-token
// threshold over raw source, as counted when Pi's own compaction is off.
func (fx *consFixture) compactionDue() {
	cfg := fx.rt.Config()
	cfg.CompactAfterTokens = 1
	cfg.PiAutoCompactionEnabled = false
	fx.rt.SetConfig(cfg)
}

// An idle run the next prompt aborts owes its compaction check to the next
// settle, which runs it before launching again. Otherwise a stage that stays
// due relaunches, and is aborted, at every settle, and compaction never runs.
func TestIdleAbortedRunOwesCompactionCheckToNextSettle(t *testing.T) {
	fx := consSetup(t, consArgs{entries: dueEntries(), idle: true, reflectAfterTokens: 999})
	fx.compactionDue()

	entered := fx.abortingObserver()
	OnAgentSettledEvent(fx.rt, fx.h)
	done := fx.rt.ConsolidationDone()
	if done == nil {
		t.Fatal("agent_settled did not launch")
	}
	waitDone(t, entered)
	OnAgentStart(fx.rt, fx.h)
	waitDone(t, done)
	// The agent is busy again: nothing compacts after the aborted run.
	never(t, "compacted after a run the next prompt aborted", func() bool { return fx.h.CompactCount() != 0 })

	entered = fx.abortingObserver()
	OnAgentSettledEvent(fx.rt, fx.h)
	launched := fx.rt.ConsolidationDone()
	if launched != nil {
		waitDone(t, entered)
	}
	OnAgentStart(fx.rt, fx.h)
	waitDone(t, launched)
	eventually(t, func() bool { return fx.h.CompactCount() == 1 })
	if launched != nil || fx.obs.count() != 1 {
		t.Fatalf("a run launched beside the owed compaction: observer calls %d", fx.obs.count())
	}
}

// A settle that finds a run the previous prompt already aborted, still
// unwinding, waits for it and then settles again, since the agent is idle: it
// relaunches the due run or runs the compaction check. A prompt that starts,
// or a session end, meanwhile keeps both skipped.
func TestIdleSettleOnAbortedRunSettlesAgainOnceItEnds(t *testing.T) {
	setup := func(compact bool) (fx *consFixture, release chan struct{}, done <-chan struct{}) {
		fx = consSetup(t, consArgs{entries: dueEntries(), idle: true, reflectAfterTokens: 999})
		if compact {
			fx.compactionDue()
		}
		// The observer outlives the abort, as an uncancellable host call does.
		entered, release := make(chan struct{}), make(chan struct{})
		fx.obs.impl(func(ctx context.Context, args agents.ObserverArgs) (agents.ObserverResult, error) {
			close(entered)
			<-release
			return agents.ObserverResult{}, ctx.Err()
		})
		OnAgentSettledEvent(fx.rt, fx.h)
		done = fx.rt.ConsolidationDone()
		waitDone(t, entered)
		OnAgentStart(fx.rt, fx.h)
		OnAgentSettledEvent(fx.rt, fx.h) // that prompt ended at once
		return fx, release, done
	}

	relaunch, release, done := setup(false)
	close(release)
	waitDone(t, done)
	eventually(t, func() bool { return relaunch.obs.count() == 2 })
	waitDone(t, relaunch.rt.ConsolidationDone())

	compact, release, done := setup(true)
	close(release)
	waitDone(t, done)
	eventually(t, func() bool { return compact.h.CompactCount() == 1 })

	busy, release, done := setup(true)
	OnAgentStart(busy.rt, busy.h)
	close(release)
	waitDone(t, done)
	never(t, "worked while the agent was busy", func() bool { return busy.h.CompactCount() != 0 || busy.obs.count() != 1 })

	// A session end meanwhile does the same: the session is being replaced.
	old := SessionEndWaitTimeout
	SessionEndWaitTimeout = 20 * time.Millisecond
	t.Cleanup(func() { SessionEndWaitTimeout = old })
	ended, release, done := setup(true)
	OnSessionEnd(ended.rt, "session_before_switch") // gives up on the stuck run
	close(release)
	waitDone(t, done)
	never(t, "worked after the session ended", func() bool { return ended.h.CompactCount() != 0 || ended.obs.count() != 1 })
}

// slowBranchHost adds host latency to every branch read once slow is set, so
// concurrent settles all pass their launch check before any of them launches.
type slowBranchHost struct {
	*testfix.FakeHost
	slow atomic.Bool
}

func (h *slowBranchHost) GetBranch() ([]ledger.Entry, error) {
	if h.slow.Load() {
		time.Sleep(5 * time.Millisecond)
	}
	return h.FakeHost.GetBranch()
}

// Settles that adopted the same aborted run all wake when it ends. One of them
// settles again, so a single run is relaunched and the next agent_start can
// abort it; a second, orphaned run would outlive the abort.
func TestIdleSettlesOnOneAbortedRunRelaunchOnce(t *testing.T) {
	fx := consSetup(t, consArgs{entries: dueEntries(), idle: true, reflectAfterTokens: 999})
	h := &slowBranchHost{FakeHost: fx.h}
	entered, release := make(chan struct{}), make(chan struct{})
	var live, peak atomic.Int32
	// The first observer outlives the abort, as an uncancellable host call does.
	fx.obs.impl(func(ctx context.Context, args agents.ObserverArgs) (agents.ObserverResult, error) {
		close(entered)
		<-release
		return agents.ObserverResult{}, ctx.Err()
	})
	relaunched := func(ctx context.Context, args agents.ObserverArgs) (agents.ObserverResult, error) {
		n := live.Add(1)
		defer live.Add(-1)
		for p := peak.Load(); n > p && !peak.CompareAndSwap(p, n); p = peak.Load() {
		}
		<-ctx.Done()
		return agents.ObserverResult{}, ctx.Err()
	}
	fx.obs.impl(relaunched)
	fx.obs.impl(relaunched)

	OnAgentSettledEvent(fx.rt, h)
	stuck := fx.rt.ConsolidationDone()
	waitDone(t, entered)
	OnAgentStart(fx.rt, h)
	// Two settles with no agent_start between them, as PiG sends for a prompt
	// it rejects while another run holds the session.
	OnAgentSettledEvent(fx.rt, h)
	OnAgentSettledEvent(fx.rt, h)
	h.slow.Store(true)
	close(release)
	waitDone(t, stuck)
	eventually(t, func() bool { return live.Load() == 1 })
	never(t, "relaunched two runs", func() bool { return fx.obs.count() > 2 })

	OnAgentStart(fx.rt, h)
	eventually(t, func() bool { return live.Load() == 0 && !fx.rt.ConsolidationInFlight() })
	if peak.Load() != 1 {
		t.Fatalf("peak concurrent relaunched runs %d", peak.Load())
	}
}

// When the settles that adopted an aborted run owe a compaction check, the one
// that settles again starts the compaction and the others stand down, so no
// worker is launched beside it.
func TestIdleSettlesOnOneAbortedRunLaunchNothingBesideOwedCompaction(t *testing.T) {
	fx := consSetup(t, consArgs{entries: dueEntries(), idle: true, reflectAfterTokens: 999})
	fx.compactionDue()
	entered, release := make(chan struct{}), make(chan struct{})
	fx.obs.impl(func(ctx context.Context, args agents.ObserverArgs) (agents.ObserverResult, error) {
		close(entered)
		<-release
		return agents.ObserverResult{}, ctx.Err()
	})
	OnAgentSettledEvent(fx.rt, fx.h)
	stuck := fx.rt.ConsolidationDone()
	waitDone(t, entered)
	OnAgentStart(fx.rt, fx.h)
	OnAgentSettledEvent(fx.rt, fx.h)
	OnAgentSettledEvent(fx.rt, fx.h)
	fx.rt.SetAfterHookOwed(true)
	close(release)
	waitDone(t, stuck)
	// The fake compaction never reports back, so it stays in flight.
	eventually(t, func() bool { return fx.h.CompactCount() == 1 })
	never(t, "launched a worker beside the compaction", func() bool { return fx.obs.count() != 1 || fx.h.CompactCount() != 1 })
}

// A settle that adopted a run another settle then aborted, for an agent_start
// that came before this settle, is still in idle time: once the run is over it
// settles again instead of skipping its work.
func TestIdleSettleOnRunAbortedForEarlierIdleEndSettlesAgain(t *testing.T) {
	fx := consSetup(t, consArgs{entries: dueEntries(), idle: true, reflectAfterTokens: 999})
	entered, release := make(chan struct{}), make(chan struct{})
	fx.obs.impl(func(ctx context.Context, args agents.ObserverArgs) (agents.ObserverResult, error) {
		close(entered)
		<-release
		return agents.ObserverResult{}, ctx.Err()
	})
	// Settle A launches; an agent_start and that run's settle B follow before A
	// notices the idle end and aborts its run.
	done := MaybeLaunchConsolidation(fx.rt, fx.h)
	waitDone(t, entered)
	fx.rt.NoteIdleEnd()
	OnAgentSettledEvent(fx.rt, fx.h)
	if !fx.rt.AbortConsolidation() {
		t.Fatal("nothing to abort")
	}
	close(release)
	waitDone(t, done)
	eventually(t, func() bool { return fx.obs.count() == 2 })
}

// A second launch while a run is in flight launches nothing.
func TestLaunchRefusesWhileRunInFlight(t *testing.T) {
	rt := runtime.New()
	release := make(chan struct{})
	first := rt.LaunchConsolidationTask(nil, func(context.Context) error { <-release; return nil })
	if second := rt.LaunchConsolidationTask(nil, func(context.Context) error { t.Error("second run started"); return nil }); second != nil {
		t.Fatal("second launch returned a run")
	}
	if rt.ConsolidationDone() != first {
		t.Fatal("second launch replaced the running run")
	}
	close(release)
	waitDone(t, first)
}

// A !!command run renders nothing for the observer, so it neither lifts the
// observer's empty backoff nor sends the judged chunk again.
func TestExcludedBashRunDoesNotLiftObserverBackoff(t *testing.T) {
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", strings.Repeat("a", 40))}, observeAfterTokens: 10, reflectAfterTokens: 999})
	fx.run()
	if fx.obs.count() != 1 || fx.rt.ObserverEmptyBackoff() == nil {
		t.Fatalf("observer calls %d, backoff %+v", fx.obs.count(), fx.rt.ObserverEmptyBackoff())
	}
	fx.addEntries(testfix.MessageEntry("bash-1", map[string]any{"role": "bashExecution", "command": "cat log", "output": strings.Repeat("x", 400), "excludeFromContext": true}))
	fx.runIfDue()
	if fx.obs.count() != 1 {
		t.Fatalf("observer ran again on the same chunk: %d calls", fx.obs.count())
	}
	entries, _ := fx.h.GetBranch()
	if got := ObserverBacklog(entries); got != fx.rt.ObserverEmptyBackoff().TokensAtEmpty {
		t.Fatalf("observer backlog %d counts the excluded run", got)
	}
}

// An agent_start that lands while a settle is launching finds no run to abort
// yet; the settle sees it once the run is launched and aborts it, so no worker
// runs beside the agent.
func TestIdleSettleAbortsRunLaunchedAsAgentStarts(t *testing.T) {
	fx := consSetup(t, consArgs{entries: dueEntries(), idle: true, reflectAfterTokens: 999})
	h := &hookedBranchHost{FakeHost: fx.h}
	// The launch's branch read is the settle's first host read.
	h.onNextGetBranch(func() { OnAgentStart(fx.rt, h) })
	fx.abortingObserver()
	OnAgentSettledEvent(fx.rt, h)
	done := fx.rt.ConsolidationDone()
	if done == nil {
		t.Fatal("agent_settled did not launch")
	}
	waitDone(t, done)
	if !fx.rt.RunAborted(done) {
		t.Fatal("the run launched as the agent started was not aborted")
	}
	fx.expectNoAppends()
}

// Aborting during the reflector or the dropper records nothing and retries nothing.
func TestAbortDuringReflectorAndDropperIsQuiet(t *testing.T) {
	covered := []ledger.Entry{text("raw-1", "aaaaaaaa"), testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA}, "raw-1")}
	fallback := consArgs{fallback: fallbackConfig(""), fallbackModel: fallbackRegistryModel(), fallbackAuth: testfix.KeyAuth("go-key")}

	reflector := consSetup(t, consArgs{entries: covered, observeAfterTokens: 999, fallback: fallback.fallback, fallbackModel: fallback.fallbackModel, fallbackAuth: fallback.fallbackAuth})
	entered := make(chan struct{})
	reflector.ref.impl(func(ctx context.Context, args agents.ReflectorArgs) ([]ledger.Reflection, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	done := reflector.launch()
	waitDone(t, entered)
	reflector.rt.AbortConsolidation()
	waitDone(t, done)
	if len(reflector.h.Appended) != 0 || reflector.rt.Snapshot().LastReflectorError != "" || hasNotificationContaining(reflector.h, "failed", "warning") || authCount(reflector.h.ModelsFake, fallbackTarget.Provider) != 0 {
		t.Fatalf("reflector abort: state %+v notifications %+v", reflector.rt.Snapshot(), reflector.h.Notifications)
	}

	newRef := testfix.Reflection("ffffffffffff", []string{"aaaaaaaaaaaa"})
	dropper := consSetup(t, consArgs{entries: covered, observeAfterTokens: 999, poolMaxTokens: 10, fallback: fallback.fallback, fallbackModel: fallback.fallbackModel, fallbackAuth: fallback.fallbackAuth})
	dropper.ref.resolve([]ledger.Reflection{newRef})
	enteredDrop := make(chan struct{})
	dropper.drop.impl(func(ctx context.Context, args agents.DropperArgs) ([]string, error) {
		close(enteredDrop)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	done = dropper.launch()
	waitDone(t, enteredDrop)
	dropper.rt.AbortConsolidation()
	waitDone(t, done)
	if len(dropper.h.Appended) != 1 || dropper.rt.Snapshot().LastDropperError != "" || hasNotificationContaining(dropper.h, "failed", "warning") || authCount(dropper.h.ModelsFake, fallbackTarget.Provider) != 0 {
		t.Fatalf("dropper abort: appended %d state %+v notifications %+v", len(dropper.h.Appended), dropper.rt.Snapshot(), dropper.h.Notifications)
	}
}

// A branch or session that changed under the model call gets no append.
func TestObserverSkipsAppendWhenBranchChanged(t *testing.T) {
	fx := consSetup(t, consArgs{entries: dueEntries(), reflectAfterTokens: 999})
	fx.obs.impl(func(ctx context.Context, args agents.ObserverArgs) (agents.ObserverResult, error) {
		// The user jumped to another branch while the model ran.
		fx.h.SetBranch([]ledger.Entry{text("other-1", "bbbbbbbb")})
		return agents.Observed([]ledger.Observation{newObservation()}), nil
	})
	fx.run()
	fx.expectNoAppends()

	session := consSetup(t, consArgs{entries: dueEntries(), reflectAfterTokens: 999})
	session.obs.impl(func(ctx context.Context, args agents.ObserverArgs) (agents.ObserverResult, error) {
		session.h.ID = "session-2"
		return agents.Observed([]ledger.Observation{newObservation()}), nil
	})
	session.run()
	session.expectNoAppends()
	if fx.rt.Snapshot().LastObserverError != "" || session.rt.Snapshot().LastObserverError != "" || hasNotificationContaining(fx.h, "failed", "") || hasNotificationContaining(session.h, "failed", "") {
		t.Fatalf("notifications %+v / %+v", fx.h.Notifications, session.h.Notifications)
	}
}

// A partial observer run claims coverage only through the last cited entry.
func TestPartialObserverRunClaimsOnlyCitedCoverage(t *testing.T) {
	entries := []ledger.Entry{text("raw-1", "aaaaaaaa"), text("raw-2", "bbbbbbbb"), text("raw-3", "cccccccc")}
	obs := observationWith("cccccccccccc", []string{"raw-2"}, 4)
	fx := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999})
	fx.obs.resolve(agents.ObserverResult{Observations: []ledger.Observation{obs}, Partial: true, StreamError: &agents.StreamError{Stage: "observer", StopReason: "error", ErrorMessage: "connection reset"}})

	fx.run()

	fx.expectAppended(0, ledger.OMObservationsRecorded, recordedObservations([]ledger.Observation{obs}, "raw-2"))
	if !hasNotificationContaining(fx.h, "observer stopped early", "info") || fx.rt.Snapshot().LastObserverError != "" {
		t.Fatalf("state %+v notifications %+v", fx.rt.Snapshot(), fx.h.Notifications)
	}

	// Nothing cited: nothing is appended and the failure is recorded.
	uncited := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999})
	uncited.obs.resolve(agents.ObserverResult{Observations: []ledger.Observation{observationWith("dddddddddddd", []string{"raw-9"}, 4)}, Partial: true, StreamError: &agents.StreamError{Stage: "observer", StopReason: "error", ErrorMessage: "connection reset"}})
	uncited.run()
	uncited.expectNoAppends()
	if !strings.Contains(uncited.rt.Snapshot().LastObserverError, "connection reset") {
		t.Fatalf("state %+v", uncited.rt.Snapshot())
	}
}

// A deliberate-empty backoff does not follow the observer to another branch.
func TestBackoffClearsWhenChunkLeavesBranch(t *testing.T) {
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", strings.Repeat("a", 40))}, observeAfterTokens: 10, reflectAfterTokens: 999})

	fx.run()
	if fx.obs.count() != 1 || fx.rt.ObserverEmptyBackoff() == nil {
		t.Fatalf("observer calls %d backoff %+v", fx.obs.count(), fx.rt.ObserverEmptyBackoff())
	}

	// A sibling branch with content the observer never read.
	fx.h.SetBranch([]ledger.Entry{text("other-1", strings.Repeat("b", 40))})
	fx.run()
	if fx.obs.count() != 2 {
		t.Fatalf("observer calls %d", fx.obs.count())
	}
}

// A reflector run that records nothing is not repeated over unchanged memory:
// no launch, no model call, until the memory changes.
func TestReflectorBacksOffAfterEmptyVerdict(t *testing.T) {
	entries := []ledger.Entry{text("raw-1", "aaaaaaaa"), testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA}, "raw-1")}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 999})

	fx.run() // the reflector records nothing
	if fx.ref.count() != 1 || fx.rt.ReflectorEmptyBackoff() == nil {
		t.Fatalf("reflector calls %d backoff %+v", fx.ref.count(), fx.rt.ReflectorEmptyBackoff())
	}
	if fx.launch() != nil {
		t.Fatal("a consolidation launched over unchanged memory")
	}

	// New observations change the memory: the reflector runs again and records.
	fx.addEntries(text("raw-2", "bbbbbbbb"), testfix.ObservationsRecordedEntry("om-obs-b", []ledger.Observation{obsB}, "raw-2"))
	newRef := testfix.Reflection("ffffffffffff", []string{"bbbbbbbbbbbb"})
	fx.ref.resolve([]ledger.Reflection{newRef})
	fx.run()
	if fx.ref.count() != 2 || fx.rt.ReflectorEmptyBackoff() != nil {
		t.Fatalf("reflector calls %d backoff %+v", fx.ref.count(), fx.rt.ReflectorEmptyBackoff())
	}
	fx.expectAppended(0, ledger.OMReflectionsRecorded, recordedReflections([]ledger.Reflection{newRef}, "raw-2"))
}

// An empty verdict from a fallback retry owes the primary one try: it never
// judged that memory. A primary that fails that try, or a pass that is on the
// fallback again, settles the backoff, so a primary that stays down does not
// send the fallback the same prompt every turn. A fallback chosen at
// resolution time backs off at once, as the next pass resolves it again.
func TestReflectorFallbackRetryEmptyVerdictOwesPrimaryOneTry(t *testing.T) {
	entries := []ledger.Entry{text("raw-1", "aaaaaaaa"), testfix.ObservationsRecordedEntry("om-obs", []ledger.Observation{obsA}, "raw-1")}
	withFallback := consArgs{entries: entries, observeAfterTokens: 999, fallback: fallbackConfig(""), fallbackModel: fallbackRegistryModel(), fallbackAuth: testfix.KeyAuth("go-key")}
	owed := func(fx *consFixture) bool {
		backoff := fx.rt.ReflectorEmptyBackoff()
		return backoff != nil && backoff.PrimaryOwed
	}
	settled := func(fx *consFixture) bool {
		backoff := fx.rt.ReflectorEmptyBackoff()
		return backoff != nil && !backoff.PrimaryOwed && fx.launch() == nil
	}
	fx := consSetup(t, withFallback)
	fx.ref.reject(errors.New("primary down")) // the fallback retry then records nothing

	fx.run()
	if fx.ref.count() != 2 || !isFallbackModel(fx.ref.call(1).Model) || !owed(fx) {
		t.Fatalf("reflector calls %d backoff %+v", fx.ref.count(), fx.rt.ReflectorEmptyBackoff())
	}

	// The primary is healthy again and reads the same memory.
	newRef := testfix.Reflection("ffffffffffff", []string{"aaaaaaaaaaaa"})
	fx.ref.resolve([]ledger.Reflection{newRef})
	fx.run()
	if fx.ref.count() != 3 || isFallbackModel(fx.ref.call(2).Model) || fx.rt.ReflectorEmptyBackoff() != nil {
		t.Fatalf("reflector calls %d backoff %+v", fx.ref.count(), fx.rt.ReflectorEmptyBackoff())
	}
	fx.expectAppended(0, ledger.OMReflectionsRecorded, recordedReflections([]ledger.Reflection{newRef}, "raw-1"))

	// The primary is still down: its one try fails without a fallback retry.
	down := consSetup(t, withFallback)
	down.ref.reject(errors.New("primary down"))
	down.run()
	down.ref.reject(errors.New("primary still down"))
	down.run()
	if down.ref.count() != 3 || isFallbackModel(down.ref.call(2).Model) || down.rt.Snapshot().LastReflectorError != "primary still down" || !settled(down) {
		t.Fatalf("reflector calls %d backoff %+v state %+v", down.ref.count(), down.rt.ReflectorEmptyBackoff(), down.rt.Snapshot())
	}

	// The same holds when an earlier stage's retry left the pass on the fallback.
	earlier := withFallback
	earlier.entries, earlier.observeAfterTokens = []ledger.Entry{text("raw-1", "aaaaaaaa")}, 0
	later := consSetup(t, earlier)
	later.obs.reject(&agents.StreamError{Stage: "observer", StopReason: "error", ErrorMessage: "primary down"})
	later.obs.resolve(agents.Observed([]ledger.Observation{newObservation()}))
	later.run()
	if later.ref.count() != 1 || !isFallbackModel(later.ref.call(0).Model) || !owed(later) {
		t.Fatalf("reflector calls %d backoff %+v", later.ref.count(), later.rt.ReflectorEmptyBackoff())
	}
	// The next pass resolves the fallback again, which already judged this memory.
	later.h.ModelsFake.Auth["anthropic"] = host.Auth{OK: false}
	later.run()
	if later.ref.count() != 1 || !settled(later) {
		t.Fatalf("reflector calls %d backoff %+v", later.ref.count(), later.rt.ReflectorEmptyBackoff())
	}

	resolved := withFallback
	resolved.primaryAuth = &host.Auth{OK: false}
	onFallback := consSetup(t, resolved)
	onFallback.run()
	if onFallback.ref.count() != 1 || !isFallbackModel(onFallback.ref.call(0).Model) || !settled(onFallback) {
		t.Fatalf("reflector calls %d backoff %+v", onFallback.ref.count(), onFallback.rt.ReflectorEmptyBackoff())
	}
}
