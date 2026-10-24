// Package hooks implements the extension's event handlers: the consolidation
// trigger, the proactive compaction trigger, and the compaction hook.
package hooks

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ids"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/jsfmt"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
)

// Now is the clock; tests replace it.
var Now = time.Now

// The worker entry points; tests replace them.
var (
	runObserver  = agents.RunObserverDetailed
	runReflector = agents.RunReflector
	runDropper   = agents.RunDropper
	// runBuilder is the harness build and revision passes' worker (docs/memory-interface.md,
	// section 8).
	runBuilder = agents.RunBuilder
)

// ConsolidationHooks are callbacks around the consolidation trigger.
type ConsolidationHooks struct {
	// AfterIdleConsolidation runs after an idle-mode consolidation run finishes
	// (or is skipped) with the agent_settled host, so work that shares the
	// settled event (proactive compaction) gets its turn afterwards. It reports
	// whether it started such work; a settle that runs an owed check first
	// launches no worker beside that work.
	AfterIdleConsolidation func(h host.Host) bool
}

type stageOutcome string

const (
	outcomeContinue stageOutcome = "continue"
	outcomeAbort    stageOutcome = "abort"
)

type reflectorStageResult struct {
	outcome                       stageOutcome
	sameRunReflections            []ledger.Reflection
	effectiveReflectionCoverageID string
}

func sourceEntriesAfter(entries []ledger.Entry, index int) []ledger.Entry {
	out := []ledger.Entry{}
	for i := index + 1; i < len(entries); i++ {
		if i >= 0 && ledger.IsSourceEntry(entries[i]) {
			out = append(out, entries[i])
		}
	}
	return out
}

func mergeReflections(existing, additional []ledger.Reflection) []ledger.Reflection {
	seen := map[string]bool{}
	merged := append([]ledger.Reflection{}, existing...)
	for _, reflection := range existing {
		seen[reflection.ID] = true
	}
	for _, reflection := range additional {
		if seen[reflection.ID] {
			continue
		}
		seen[reflection.ID] = true
		merged = append(merged, reflection)
	}
	return merged
}

// realContextTokens is the provider-reported context size, when the host knows it.
func realContextTokens(h host.Host) (int, bool) {
	usage, err := h.GetContextUsage()
	if err != nil || usage == nil || usage.Tokens == nil {
		return 0, false
	}
	return *usage.Tokens, true
}

func stageDue(entries []ledger.Entry, current int, hasCurrent bool, customType string, rawEstimate func([]ledger.Entry) int, threshold int) bool {
	// The raw estimate counts every source entry this stage has not covered yet,
	// including entries a compaction removed from context, so a due backlog
	// always counts; the provider delta alone would starve coverage that fell
	// behind a compaction.
	if rawEstimate(entries) >= threshold {
		return true
	}
	if hasCurrent {
		if real, ok := ledger.RealTokensSinceAnchor(entries, customType, current); ok {
			return real >= threshold
		}
	}
	return false
}

// stageTokens is the larger of the raw uncovered backlog and the provider-reported growth.
func stageTokens(entries []ledger.Entry, customType string, current int, hasCurrent bool, rawEstimate func([]ledger.Entry) int) int {
	raw := rawEstimate(entries)
	if hasCurrent {
		if real, ok := ledger.RealTokensSinceAnchor(entries, customType, current); ok && real > raw {
			return real
		}
	}
	return raw
}

// reflectorDue is stageDue for the reflector with one more condition: the
// provider delta alone never makes it due. The reflector works on
// observations, so while no new observation has landed since the last
// reflection there is nothing to reflect on, however much the context grew.
func reflectorDue(entries []ledger.Entry, cfg config.Config, current int, hasCurrent bool) bool {
	if ledger.ObservedTokensSinceReflectionCoverage(entries) == 0 {
		return false
	}
	return stageDue(entries, current, hasCurrent, ledger.OMReflectionsRecorded, ledger.ObservedTokensSinceReflectionCoverage, cfg.ReflectAfterTokens)
}

// reflectorInputKey identifies what the reflector reads: the active
// observations and the reflections the ledger folds to. Over an unchanged key
// the same model gets an identical prompt, so a run that recorded nothing
// would record nothing again.
func reflectorInputKey(entries []ledger.Entry) string {
	folded := ledger.FoldLedger(entries, "")
	parts := make([]string, 0, len(folded.ActiveObservations)+len(folded.Reflections))
	for _, observation := range folded.ActiveObservations {
		parts = append(parts, "o:"+observation.ID)
	}
	for _, reflection := range folded.Reflections {
		parts = append(parts, "r:"+reflection.ID)
	}
	return ids.HashID(strings.Join(parts, ","))
}

// ReflectorBackedOff reports whether the reflector already found nothing
// stable in exactly this memory; it stays quiet until the memory changes. A
// verdict that still owes the primary a try does not hold it back.
func ReflectorBackedOff(rt *runtime.Runtime, h host.Host, entries []ledger.Entry) bool {
	backoff := reflectorBackoffFor(rt, h, entries)
	return backoff != nil && !backoff.PrimaryOwed
}

// reflectorBackoffFor is the reflector backoff recorded over exactly this
// memory in this session, or nil.
func reflectorBackoffFor(rt *runtime.Runtime, h host.Host, entries []ledger.Entry) *runtime.ReflectorEmptyBackoff {
	backoff := rt.ReflectorEmptyBackoff()
	if backoff == nil || backoff.SessionIdentity != sessionIdentityOf(h) || backoff.InputKey != reflectorInputKey(entries) {
		return nil
	}
	return backoff
}

// ActiveObserverBackoff is the deliberate-empty observer backoff that applies
// to this branch, or nil. It holds while the session, the observation coverage
// and the judged chunk are unchanged and the raw uncovered backlog stays below
// TokensAtEmpty+ObserveAfterTokens.
func ActiveObserverBackoff(rt *runtime.Runtime, h host.Host, entries []ledger.Entry, cfg config.Config) *runtime.ObserverEmptyBackoff {
	backoff := rt.ObserverEmptyBackoff()
	if backoff == nil || backoff.SessionIdentity != sessionIdentityOf(h) || backoff.CoverageID != ledger.LatestCoverageMarkerID(entries, ledger.OMObservationsRecorded) {
		return nil
	}
	if backoff.ChunkEndID != "" && ledger.EntryIndexForID(entries, backoff.ChunkEndID) == -1 {
		return nil
	}
	if ObserverBacklog(entries) >= backoff.TokensAtEmpty+cfg.ObserveAfterTokens {
		return nil
	}
	return backoff
}

// ObserverBacklog is the uncovered source the observer can read: the raw
// backlog without entries that render nothing for it (a !!command run PiG keeps
// out of context, an aborted turn with no content). Counting those would make
// the observer due, or lift its empty backoff, on input it never sees.
func ObserverBacklog(entries []ledger.Entry) int {
	return ledger.RawTokensAfterIndexWhere(entries, ledger.LatestCoverageIndex(entries, ledger.OMObservationsRecorded), serialize.ObserverRenders)
}

func anyStageDue(rt *runtime.Runtime, h host.Host, entries []ledger.Entry, cfg config.Config, current int, hasCurrent bool) bool {
	return stageDue(entries, current, hasCurrent, ledger.OMObservationsRecorded, ObserverBacklog, cfg.ObserveAfterTokens) ||
		(reflectorDue(entries, cfg, current, hasCurrent) && !ReflectorBackedOff(rt, h, entries))
}

// ObserverProgressTokens is what gates the observer: the larger of the raw
// uncovered backlog and the provider-reported growth since its anchor.
func ObserverProgressTokens(entries []ledger.Entry, h host.Host) int {
	current, hasCurrent := realContextTokens(h)
	return stageTokens(entries, ledger.OMObservationsRecorded, current, hasCurrent, ObserverBacklog)
}

// ReflectorProgressTokens is what gates the reflector, on the same basis. Like
// reflectorDue it reads 0 until an observation lands after the last reflection
// coverage, however much the context grew.
func ReflectorProgressTokens(entries []ledger.Entry, h host.Host) int {
	if ledger.ObservedTokensSinceReflectionCoverage(entries) == 0 {
		return 0
	}
	current, hasCurrent := realContextTokens(h)
	return stageTokens(entries, ledger.OMReflectionsRecorded, current, hasCurrent, ledger.ObservedTokensSinceReflectionCoverage)
}

func sessionIdentityOf(h host.Host) string {
	if id := h.SessionID(); id != "" {
		return id
	}
	return h.SessionFile()
}

func branchLeafID(entries []ledger.Entry) string {
	if len(entries) == 0 {
		return ""
	}
	return entries[len(entries)-1].ID
}

// branchStillCurrent reports whether the branch a stage read is still the one
// the host would append to: the same session, with the entry that was its
// leaf still on it. A session replacement or a tree jump during the model
// call fails the check, and the stage abandons its result rather than record
// coverage for entries that are not on the current branch.
func branchStillCurrent(h host.Host, sessionIdentity, leafID string) bool {
	if sessionIdentityOf(h) != sessionIdentity {
		return false
	}
	if leafID == "" {
		return true
	}
	fresh, err := h.GetBranch()
	if err != nil {
		return false
	}
	return ledger.EntryIndexForID(fresh, leafID) != -1
}

func notifyWorker(cfg config.Config, h host.Host, message string) {
	if cfg.ShowWorkerNotifications {
		notifyUI(h, message, "info")
	}
}

// notifyUI shows a notification. It never panics: background work may hold a
// host whose session has been replaced, and a notification is never worth
// taking the extension process down.
func notifyUI(h host.Host, message, level string) {
	defer func() { _ = recover() }()
	if h.HasUI() {
		h.Notify(message, level)
	}
}

// workerHeadersFor mirrors Pi's own session headers on worker calls to Console
// Go (opencode.ai), which rejects requests without x-opencode-session.
func workerHeadersFor(h host.Host, resolved runtime.ResolveResult) runtime.ResolveResult {
	provider := host.ModelProvider(resolved.Model)
	baseURL := host.ModelString(resolved.Model, "baseUrl")
	if provider != "opencode" && provider != "opencode-go" && !containsOpencode(baseURL) {
		return resolved
	}
	sessionID := h.SessionID()
	if sessionID == "" {
		return resolved
	}
	headers := make(map[string]any, len(resolved.Headers)+2)
	for k, v := range resolved.Headers {
		headers[k] = v
	}
	headers["x-opencode-session"] = sessionID
	headers["x-opencode-client"] = "pi"
	resolved.Headers = headers
	return resolved
}

func containsOpencode(url string) bool {
	return len(url) > 0 && stringsContains(url, "opencode.ai")
}

func stringsContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// workerThinkingLevel is the worker call's thinking level: the fallback's own
// setting wins when the fallback is active.
func workerThinkingLevel(cfg config.Config, resolved runtime.ResolveResult) string {
	if resolved.FallbackUsed {
		if cfg.FallbackModel != nil && cfg.FallbackModel.Thinking != "" {
			return string(cfg.FallbackModel.Thinking)
		}
	}
	if cfg.Model != nil && cfg.Model.Thinking != "" {
		return string(cfg.Model.Thinking)
	}
	return "low"
}

// observerChunkContextWindow is the window the observer chunk is sized
// against: the smaller of the primary and fallback windows, since the chunk is
// reused verbatim if the run falls back mid-call.
func observerChunkContextWindow(cfg config.Config, h host.Host, resolved runtime.ResolveResult) int {
	primary := host.ModelContextWindow(resolved.Model)
	if cfg.FallbackModel == nil {
		return primary
	}
	fallbackWindow := 0
	if fallbackModel := h.Models().Find(runtime.EffectiveProvider(cfg.FallbackModel, h.SessionModel()), cfg.FallbackModel.ID); fallbackModel != nil {
		fallbackWindow = host.ModelContextWindow(fallbackModel)
	}
	switch {
	case primary <= 0:
		return fallbackWindow
	case fallbackWindow <= 0:
		return primary
	}
	return min(primary, fallbackWindow)
}

type modelResolver struct {
	rt             *runtime.Runtime
	h              host.Host
	cached         *runtime.ResolveResult
	fallbackActive *runtime.ResolveResult
}

func (r *modelResolver) resolveCtx() runtime.ResolveCtx {
	return runtime.ResolveCtx{Model: r.h.SessionModel(), Models: r.h.Models(), HasUI: r.h.HasUI(), Notify: r.h.Notify}
}

func (r *modelResolver) resolve(ctx context.Context, stage runtime.Phase) *runtime.ResolveResult {
	if r.fallbackActive != nil {
		r.rt.SetResolveFailureNotified(false)
		return r.fallbackActive
	}
	if r.cached == nil {
		result := r.rt.ResolveModel(ctx, r.resolveCtx())
		r.cached = &result
	}
	if r.cached.OK {
		r.rt.SetResolveFailureNotified(false)
		resolved := workerHeadersFor(r.h, *r.cached)
		return &resolved
	}
	debuglog.Log(ctx, string(stage)+".model_unavailable", map[string]any{"reason": r.cached.Reason})
	if !r.rt.ResolveFailureNotified() && r.h.HasUI() {
		r.h.Notify(fmt.Sprintf("Observational memory: %s skipped — %s", stage, r.cached.Reason), "warning")
		r.rt.SetResolveFailureNotified(true)
	}
	return nil
}

// resolveFallback resolves the configured fallback, caching it for the rest of
// the pass so later stages do not re-pay a known-broken primary.
func (r *modelResolver) resolveFallback(ctx context.Context, stage runtime.Phase) *runtime.ResolveResult {
	if r.fallbackActive != nil {
		return r.fallbackActive
	}
	result := r.rt.ResolveFallbackModel(ctx, r.resolveCtx())
	if !result.OK {
		debuglog.Log(ctx, string(stage)+".fallback_unavailable", map[string]any{"reason": result.Reason})
		return nil
	}
	result.FallbackUsed = true
	resolved := workerHeadersFor(r.h, result)
	r.fallbackActive = &resolved
	debuglog.Log(ctx, string(stage)+".fallback_active", map[string]any{"provider": host.ModelProvider(resolved.Model), "id": host.ModelID(resolved.Model)})
	return r.fallbackActive
}

// retriedOnFallback reports whether worker is the fallback this pass switched
// to after a primary call failed, rather than the model the pass resolved. The
// next pass tries that primary again; a fallback chosen at resolution time
// (no usable primary) is what the next pass resolves too.
func (r *modelResolver) retriedOnFallback(worker runtime.ResolveResult) bool {
	return worker.FallbackUsed && (r.cached == nil || !r.cached.FallbackUsed)
}

// runStageWithFallback runs one worker stage against the resolved primary
// model, retrying once with the configured fallback when the call fails. A
// stage that already resolved through the fallback is not retried again. An
// aborted run is not a model failure.
func runStageWithFallback[T any](ctx context.Context, h host.Host, stage runtime.Phase, resolved runtime.ResolveResult, resolver *modelResolver, work func(runtime.ResolveResult) (T, error)) (T, error) {
	value, err := work(resolved)
	if err == nil {
		return value, nil
	}
	if ctx.Err() != nil || errors.Is(err, agents.ErrAborted) {
		return value, err
	}
	if resolved.FallbackUsed {
		return value, err
	}
	fallback := resolver.resolveFallback(ctx, stage)
	if fallback == nil {
		return value, err
	}
	// The primary's pass failed even if the fallback's succeeds: its held definitions are
	// not proven by it.
	failPass(ctx, resolved.Model, "a kernel error: the worker call failed: "+err.Error())
	// Resolving the fallback can take a while (an OAuth refresh, the
	// availability re-check); the run may have been aborted meanwhile.
	if ctx.Err() != nil {
		return value, err
	}
	debuglog.Log(ctx, string(stage)+".fallback_retry", map[string]any{"primaryError": err.Error(), "provider": host.ModelProvider(fallback.Model), "id": host.ModelID(fallback.Model)})
	notifyUI(h, fmt.Sprintf("Observational memory: %s failed (%s); retrying with fallback model", stage, err.Error()), "warning")
	return work(*fallback)
}

func idleMode(rt *runtime.Runtime, h host.Host) bool {
	rt.EnsureConfig(h.Cwd())
	return rt.Config().ConsolidateWhenIdle
}

// OnAgentStart handles agent_start: launch a due consolidation, or in idle mode
// abort the in-flight one so memory workers get off the shared server.
func OnAgentStart(rt *runtime.Runtime, h host.Host) {
	if !idleMode(rt, h) {
		MaybeLaunchConsolidation(rt, h)
		return
	}
	// A settle still waiting on a run must not treat the time after it as idle.
	rt.NoteIdleEnd()
	// Coverage markers are only appended on success, so an aborted run simply
	// retries after the next settled event.
	if rt.AbortConsolidation() {
		debuglog.Log(context.Background(), "consolidation.aborted", map[string]any{"reason": "agent_start"})
		notifyWorker(rt.Config(), h, "Observational memory: memory workers paused while the agent runs (consolidateWhenIdle)")
	}
}

// SessionEndWaitTimeout bounds how long OnSessionEnd waits for the run it
// aborted; tests shorten it.
var SessionEndWaitTimeout = time.Second

// OnSessionEnd handles session_shutdown, session_before_switch,
// session_before_fork and session_before_tree. The host replaces the session
// (under PiG the extension process too) or moves the leaf, so an in-flight run
// is abandoned quietly instead of failing later against a stale context
// (upstream issue #22). Coverage is appended only on success, so only the
// abandoned run's results are lost; the next due turn starts it over.
//
// The host acts once this handler returns, so it waits, bounded, for the
// aborted run; with each stage re-checking the run context just before its
// append, a worker past its branch check cannot land an entry on the new leaf.
func OnSessionEnd(rt *runtime.Runtime, reason string) {
	// A settle waiting on a run must not settle again into a session that is
	// being replaced.
	rt.NoteIdleEnd()
	done := rt.ConsolidationDone()
	if !rt.AbortConsolidation() {
		return
	}
	debuglog.Log(context.Background(), "consolidation.aborted", map[string]any{"reason": reason})
	if rt.RunAborted(done) {
		select {
		case <-done:
		case <-time.After(SessionEndWaitTimeout):
		}
	}
}

// OnTurnEnd handles turn_end: launch a due consolidation unless in idle mode.
func OnTurnEnd(rt *runtime.Runtime, h host.Host) {
	if idleMode(rt, h) {
		return
	}
	MaybeLaunchConsolidation(rt, h)
}

// OnAgentSettled handles agent_settled in idle mode: launch a due
// consolidation and run the after-hook once it finishes.
func OnAgentSettled(rt *runtime.Runtime, h host.Host, hooks ConsolidationHooks) {
	if !idleMode(rt, h) {
		return
	}
	settleIdle(rt, h, hooks, rt.IdleEnds())
}

// settleIdle is OnAgentSettled for a settle that came after idleEnds
// idle-ending events; a later one means the agent is busy again or the
// session is ending.
func settleIdle(rt *runtime.Runtime, h host.Host, hooks ConsolidationHooks, idleEnds uint64) {
	if hooks.AfterIdleConsolidation != nil && !rt.ConsolidationInFlight() && rt.TakeAfterHookOwed() {
		// An earlier settle's run was aborted, so its check never ran. Run it
		// before launching a run the next prompt could abort the same way;
		// work it starts keeps this idle window, and the next settle launches.
		if hooks.AfterIdleConsolidation(h) {
			return
		}
	}
	launched := MaybeLaunchConsolidation(rt, h)
	if launched != nil && rt.IdleEnds() != idleEnds {
		// An agent_start (or a session end) came while this settle was
		// launching, before there was a run for it to abort.
		if rt.AbortConsolidation() {
			debuglog.Log(context.Background(), "consolidation.aborted", map[string]any{"reason": "idle_ended_at_launch"})
		}
	}
	if hooks.AfterIdleConsolidation == nil {
		return
	}
	if launched == nil {
		// Nothing launched now, but an earlier run may still be in flight; the
		// after-hook waits for it rather than racing it.
		launched = rt.ConsolidationDone()
	}
	if launched == nil {
		hooks.AfterIdleConsolidation(h)
		return
	}
	go func() {
		defer func() { _ = recover() }()
		<-launched
		switch {
		case rt.IdleEnds() != idleEnds:
			// The agent started again (or the session ended) before the run was
			// over, so this is no longer idle time. The next idle settle owes the
			// check and runs it before launching: a stage that stays due relaunches
			// at every settle, and the next prompt may abort every one of them.
			rt.SetAfterHookOwed(true)
			debuglog.Log(context.Background(), "consolidation.after_hook_skipped", map[string]any{"reason": "aborted"})
		case rt.RunAborted(launched):
			// Every abort follows an idle end, and none came after this settle:
			// the run was aborted for one before it, so this window is still idle
			// and the run is over. Settle again. Every settle that waited on the
			// run wakes here together; only the first settles again.
			if rt.ClaimResettle(launched) {
				settleIdle(rt, h, hooks, idleEnds)
			}
		default:
			hooks.AfterIdleConsolidation(h)
		}
	}()
}

// OnAgentSettledEvent is the agent_settled handler. In idle mode the proactive
// compaction check runs after the idle consolidation (or at once when nothing
// is due or in flight), never beside a running worker; otherwise it runs now.
func OnAgentSettledEvent(rt *runtime.Runtime, h host.Host) {
	if idleMode(rt, h) {
		OnAgentSettled(rt, h, ConsolidationHooks{AfterIdleConsolidation: func(settled host.Host) bool { return MaybeTriggerCompaction(rt, settled) != nil }})
		return
	}
	MaybeTriggerCompaction(rt, h)
}

func newRunID(prefix string) string {
	return prefix + "-" + strconv.FormatInt(Now().UnixMilli(), 36) + "-" + fmt.Sprintf("%06x", rand.Intn(1<<24))
}

// MaybeLaunchConsolidation launches a consolidation run when any stage is due.
// It returns the run's completion channel, or nil when nothing launched.
func MaybeLaunchConsolidation(rt *runtime.Runtime, h host.Host) <-chan struct{} {
	rt.EnsureConfig(h.Cwd())
	cfg := rt.Config()
	if cfg.Passive || rt.ConsolidationInFlight() {
		return nil
	}
	entries, err := h.GetBranch()
	if err != nil {
		return nil
	}
	current, hasCurrent := realContextTokens(h)
	if _, _, build := buildDue(h, cfg); !build && !anyStageDue(rt, h, entries, cfg, current, hasCurrent) {
		return nil
	}
	scope := debuglog.Context{Enabled: cfg.DebugLog, Cwd: h.Cwd(), SessionID: h.SessionID(), SessionFile: h.SessionFile(), RunID: newRunID("consolidation")}
	return rt.LaunchConsolidationTask(h, func(ctx context.Context) error {
		return RunConsolidationPipeline(debuglog.With(ctx, scope), rt, h)
	})
}

// RunConsolidationPipeline runs the harness build pass when the memory model has no layer yet,
// the observer, reflector and dropper stages, and then a revision pass when one is due
// (docs/memory-interface.md, section 8). An aborted run (Runtime.AbortConsolidation) stops at
// its next check.
func RunConsolidationPipeline(ctx context.Context, rt *runtime.Runtime, h host.Host) error {
	resolver := &modelResolver{rt: rt, h: h}
	if store, model, due := buildDue(h, rt.Config()); due {
		rt.SetConsolidationPhase(runtime.PhaseHarness)
		runHarnessPass(ctx, rt, h, resolver, store, model, nil)
		if ctx.Err() != nil {
			return nil
		}
	}
	runConsolidationStages(ctx, rt, h, resolver)
	if ctx.Err() == nil {
		maybeRevise(ctx, rt, h, resolver)
	}
	return nil
}

// runConsolidationStages runs the observer, reflector and dropper stages.
func runConsolidationStages(ctx context.Context, rt *runtime.Runtime, h host.Host, resolver *modelResolver) {
	rt.SetConsolidationPhase(runtime.PhaseObserver)
	outcome, err := runObserverStage(ctx, rt, h, resolver)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		debuglog.Log(ctx, "observer.error", map[string]any{"errorMessage": rt.RecordConsolidationStageError(h, runtime.PhaseObserver, err)})
		return
	}
	if outcome == outcomeAbort || ctx.Err() != nil {
		return
	}

	rt.SetConsolidationPhase(runtime.PhaseReflector)
	reflector, err := runReflectorStage(ctx, rt, h, resolver)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		debuglog.Log(ctx, "reflector.error", map[string]any{"errorMessage": rt.RecordConsolidationStageError(h, runtime.PhaseReflector, err)})
		return
	}
	if reflector.outcome == outcomeAbort || ctx.Err() != nil {
		return
	}

	rt.SetConsolidationPhase(runtime.PhaseDropper)
	if _, err := runDropperStage(ctx, rt, h, resolver, reflector.sameRunReflections, reflector.effectiveReflectionCoverageID); err != nil {
		if ctx.Err() != nil {
			return
		}
		debuglog.Log(ctx, "dropper.error", map[string]any{"errorMessage": rt.RecordConsolidationStageError(h, runtime.PhaseDropper, err)})
	}
}

// workerArgs are a stage's worker call arguments for worker, with the prompt-line rules that
// apply to the worker model for stage and the model's own instructions for it from its layer.
func workerArgs(ctx context.Context, cfg config.Config, h host.Host, worker runtime.ResolveResult, stage string) agents.WorkerArgs {
	return agents.WorkerArgs{
		SystemLines:     rulesFor(h, cfg, worker.Model).PromptLines(stage),
		Instructions:    instructionsFor(ctx, h, cfg, worker.Model, stage),
		Model:           worker.Model,
		APIKey:          worker.APIKey,
		Headers:         worker.Headers,
		Env:             worker.Env,
		MaxTurns:        cfg.AgentMaxTurns,
		MaxOutputTokens: cfg.AgentMaxTokens,
		ThinkingLevel:   workerThinkingLevel(cfg, worker),
		Stream:          h.Models().Stream,
	}
}

// observerArgs are an observer run's arguments for worker: the chunk and prior memory (with the
// memory's canonical subjects), the
// worker model's harness profile (docs/memory-interface.md, section 6): the codec its rows
// parameter uses, the long spec when the profile carries it, and every rows decode counted in
// the model's usage ledger; and the note schema of its layer (section 8), the seed's when it
// defined none.
func observerArgs(ctx context.Context, cfg config.Config, h host.Host, worker runtime.ResolveResult, priorReflections, priorObservations []string, subjects []ledger.SubjectCount, chunk string, allowed []string) agents.ObserverArgs {
	active := layerFor(ctx, h, cfg, worker.Model)
	schema := active.Schema()
	args := agents.ObserverArgs{
		WorkerArgs:            workerArgs(ctx, cfg, h, worker, rules.StageObserver),
		PriorReflections:      priorReflections,
		PriorObservations:     priorObservations,
		Subjects:              subjects,
		Chunk:                 chunk,
		AllowedSourceEntryIDs: allowed,
		Schema:                &schema,
	}
	if l, ok := harness.For(h, cfg, worker.Model); ok {
		profile := l.ProfileWith(active)
		codec := profile.CodecValue()
		args.Codec, args.LongSpec = codec, profile.LongSpec
		args.RecordDecode = func(outcome string) {
			recorded := decodeOutcome(outcome)
			l.Record(curated.ModelEvent{Kind: curated.ModelKindDecode, Outcome: recorded, Codec: codec.Name()})
			if recorded != curated.OutcomeOK {
				failPass(ctx, worker.Model, "a decode failure ("+recorded+") in the "+codec.Name()+" codec")
			}
		}
	}
	return args
}

// decodeOutcome is an observer decode outcome as the usage ledger records it.
func decodeOutcome(outcome string) string {
	switch outcome {
	case agents.DecodeError:
		return curated.OutcomeError
	case agents.DecodeColumns:
		return curated.OutcomeColumns
	}
	return curated.OutcomeOK
}

// runObserverStage runs one observer pass with one layer snapshot (layerFor), then settles the
// held definitions it ran with (settlePass).
func runObserverStage(ctx context.Context, rt *runtime.Runtime, h host.Host, resolver *modelResolver) (stageOutcome, error) {
	ctx = withPass(ctx, passObserver)
	outcome, err := runObserverPass(ctx, rt, h, resolver)
	settlePass(ctx, rt.Config(), h, err)
	return outcome, err
}

func runObserverPass(ctx context.Context, rt *runtime.Runtime, h host.Host, resolver *modelResolver) (stageOutcome, error) {
	cfg := rt.Config()
	entries, err := h.GetBranch()
	if err != nil {
		return outcomeAbort, err
	}
	current, hasCurrent := realContextTokens(h)
	tokens := stageTokens(entries, ledger.OMObservationsRecorded, current, hasCurrent, ObserverBacklog)
	if tokens < cfg.ObserveAfterTokens {
		return outcomeContinue, nil
	}
	leafID := branchLeafID(entries)
	rawTokens := ObserverBacklog(entries)
	sessionIdentity := sessionIdentityOf(h)
	coverageID := ledger.LatestCoverageMarkerID(entries, ledger.OMObservationsRecorded)

	// Deliberate-empty backoff: an intentional "nothing to record" verdict
	// must not re-fire the observer every turn over the same span.
	if backoff := ActiveObserverBackoff(rt, h, entries, cfg); backoff != nil {
		debuglog.Log(ctx, "observer.empty_backoff", map[string]any{"tokens": rawTokens, "resumeAtTokens": backoff.TokensAtEmpty + cfg.ObserveAfterTokens})
		return outcomeContinue, nil
	}
	rt.SetObserverEmptyBackoff(nil)

	// Resolve the model before building the chunk: the default chunk cap
	// derives from the resolved model's context window.
	resolved := resolver.resolve(ctx, runtime.PhaseObserver)
	if resolved == nil {
		return outcomeAbort, nil
	}
	if ctx.Err() != nil {
		debuglog.Log(ctx, "observer.aborted", map[string]any{})
		return outcomeAbort, nil
	}

	lastCoverageIdx := ledger.LatestCoverageIndex(entries, ledger.OMObservationsRecorded)
	backlog := sourceEntriesAfter(entries, lastCoverageIdx)
	contextWindow := observerChunkContextWindow(cfg, h, *resolved)
	// A split-chunk rule for the resolved worker halves the chunk. A chunk retried on the
	// fallback keeps that size (re-sizing would change what its marker covers) and is recorded
	// as the fallback's pass, which ran it; later chunks in the pass resolve to the fallback and
	// are sized by its rules.
	maxChunkTokens := rulesFor(h, cfg, resolved.Model).SplitChunk(config.ResolveObserverChunkMaxTokens(cfg, contextWindow))
	serialized := serialize.SerializeSourceAddressedBranchEntries(backlog, serialize.SourceAddressedOptions{MaxTokens: maxChunkTokens})
	if trimmed(serialized.Text) == "" || len(serialized.SourceEntryIDs) == 0 {
		return outcomeContinue, nil
	}
	coversUpToID := serialized.SourceEntryIDs[len(serialized.SourceEntryIDs)-1]
	if len(serialized.SourceEntryIDs) < len(backlog) || len(serialized.TruncatedSourceEntryIDs) > 0 {
		debuglog.Log(ctx, "observer.chunk_capped", map[string]any{
			"maxChunkTokens":          maxChunkTokens,
			"backlogEntries":          len(backlog),
			"backlogTokens":           tokens,
			"chunkEntries":            len(serialized.SourceEntryIDs),
			"chunkTokens":             serialized.EstimatedTokens,
			"truncatedSourceEntryIds": serialized.TruncatedSourceEntryIDs,
		})
	}

	fullMemory := ledger.FullProjection(entries, "")
	memory := ledger.BoundObserverMemory(fullMemory.Reflections, fullMemory.Observations, config.ResolveWorkerMemoryMaxTokens(cfg, contextWindow))
	priorReflections := make([]string, 0, len(memory.Reflections))
	for _, reflection := range memory.Reflections {
		priorReflections = append(priorReflections, ledger.ReflectionToSummaryLine(reflection))
	}
	priorObservations := memory.ObserverLines()

	notifyWorker(cfg, h, fmt.Sprintf("Observational memory: observer running on ~%s-token chunk", jsfmt.Locale(serialized.EstimatedTokens)))
	debuglog.Log(ctx, "observer.start", map[string]any{
		"tokens":              tokens,
		"chunkTokens":         serialized.EstimatedTokens,
		"coversUpToId":        coversUpToID,
		"sourceEntryIds":      serialized.SourceEntryIDs,
		"sourceEntryCount":    len(serialized.SourceEntryIDs),
		"priorReflections":    len(priorReflections),
		"priorObservations":   len(priorObservations),
		"omittedReflections":  memory.OmittedReflections,
		"omittedObservations": memory.OmittedObservations,
	})

	used := *resolved
	outcome, err := runStageWithFallback(ctx, h, runtime.PhaseObserver, *resolved, resolver, func(worker runtime.ResolveResult) (agents.ObserverResult, error) {
		used = worker
		return runObserver(ctx, observerArgs(ctx, cfg, h, worker, priorReflections, priorObservations, memory.Subjects, serialized.Text, serialized.SourceEntryIDs))
	})
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, agents.ErrAborted) {
			debuglog.Log(ctx, "observer.aborted", map[string]any{"coversUpToId": coversUpToID})
			return outcomeAbort, nil
		}
		var streamErr *agents.StreamError
		if errors.As(err, &streamErr) {
			// An API/stream failure is not a clean empty: surface it as a real
			// failure instead of the "no observations" path. Coverage stays put.
			rt.RecordConsolidationStageError(h, runtime.PhaseObserver, err)
			failPass(ctx, nil, "a kernel error: "+err.Error())
			return outcomeAbort, nil
		}
		return outcomeAbort, err
	}
	if ctx.Err() != nil {
		debuglog.Log(ctx, "observer.aborted", map[string]any{"coversUpToId": coversUpToID})
		return outcomeAbort, nil
	}
	observations := outcome.Observations
	if len(observations) == 0 {
		debuglog.Log(ctx, "observer.empty", map[string]any{"coversUpToId": coversUpToID})
		rt.SetObserverEmptyBackoff(&runtime.ObserverEmptyBackoff{SessionIdentity: sessionIdentity, CoverageID: coverageID, TokensAtEmpty: rawTokens, ChunkEndID: coversUpToID})
		notifyWorker(cfg, h, "Observational memory: observer found nothing new in this chunk (coverage unchanged; will retry later)")
		// Counted, never proof: a held definition that makes the observer record nothing is
		// not written by it (settlePass), and a written one is judged by it.
		chunk := observerChunk{branch: entries, serialized: serialized, known: fullMemory.Observations}
		recordEmptyPass(ctx, cfg, h, used.Model, emptyObserverPass(ctx, cfg, h, used.Model, chunk, coversUpToID))
		return outcomeContinue, nil
	}
	rt.SetObserverEmptyBackoff(nil)
	if outcome.Partial {
		// The model stopped before the end of the chunk (a stream failure after
		// some batches, or the turn cap). Coverage is a positional claim, so it
		// advances only through the last source entry the observations cite;
		// the rest of the chunk is read again next run.
		cited := agents.LastCitedSourceEntryID(observations, serialized.SourceEntryIDs)
		reason := "turn cap reached"
		if outcome.StreamError != nil {
			reason = outcome.StreamError.Error()
		}
		debuglog.Log(ctx, "observer.partial", map[string]any{"reason": reason, "count": len(observations), "chunkEndId": coversUpToID, "citedUpToId": cited})
		if cited == "" {
			if outcome.StreamError != nil {
				rt.RecordConsolidationStageError(h, runtime.PhaseObserver, outcome.StreamError)
				failPass(ctx, nil, "a kernel error: "+outcome.StreamError.Error())
				return outcomeAbort, nil
			}
			return outcomeContinue, nil
		}
		coversUpToID = cited
		notifyWorker(cfg, h, fmt.Sprintf("Observational memory: observer stopped early (%s); coverage advances to the last cited entry", reason))
	}
	chunk := observerChunk{branch: entries, serialized: serialized, priorReflections: priorReflections, priorObservations: priorObservations, subjects: memory.Subjects, known: fullMemory.Observations}
	observations, pass := applyObserverRules(ctx, cfg, h, used, chunk, observations, coversUpToID, outcome.Partial)

	data := ledger.BuildObservationsRecordedData(observations, coversUpToID)
	if data == nil {
		return outcomeContinue, nil
	}
	observationTokens := 0
	for _, observation := range observations {
		observationTokens += observation.TokenCount
	}
	debuglog.Log(ctx, "observer.records", map[string]any{"count": len(observations), "observationTokens": observationTokens, "coversUpToId": coversUpToID})
	if !branchStillCurrent(h, sessionIdentity, leafID) {
		debuglog.Log(ctx, "observer.branch_changed", map[string]any{"coversUpToId": coversUpToID})
		incompletePass(ctx)
		return outcomeAbort, nil
	}
	// A session end may have aborted the run during the branch check; the host
	// moves the leaf once its handler returns, so an aborted run appends nothing.
	if ctx.Err() != nil {
		debuglog.Log(ctx, "observer.aborted", map[string]any{"coversUpToId": coversUpToID})
		return outcomeAbort, nil
	}
	if err := h.AppendEntry(ledger.OMObservationsRecorded, data); err != nil {
		return outcomeAbort, err
	}
	debuglog.Log(ctx, "observer.appended", map[string]any{"count": len(observations), "coversUpToId": coversUpToID})
	notifyWorker(cfg, h, fmt.Sprintf("Observational memory: %d %s recorded", len(observations), jsfmt.Plural(len(observations), "observation")))
	recordPass(ctx, cfg, h, used.Model, pass)
	return outcomeContinue, nil
}

// runReflectorStage runs one reflector pass with one layer snapshot (layerFor), then settles
// the held definitions it ran with (settlePass).
func runReflectorStage(ctx context.Context, rt *runtime.Runtime, h host.Host, resolver *modelResolver) (reflectorStageResult, error) {
	ctx = withPass(ctx, passReflector)
	result, err := runReflectorPass(ctx, rt, h, resolver)
	settlePass(ctx, rt.Config(), h, err)
	return result, err
}

func runReflectorPass(ctx context.Context, rt *runtime.Runtime, h host.Host, resolver *modelResolver) (reflectorStageResult, error) {
	cfg := rt.Config()
	none := reflectorStageResult{outcome: outcomeContinue, sameRunReflections: []ledger.Reflection{}}
	entries, err := h.GetBranch()
	if err != nil {
		return reflectorStageResult{outcome: outcomeAbort}, err
	}
	current, hasCurrent := realContextTokens(h)
	if !reflectorDue(entries, cfg, current, hasCurrent) {
		return none, nil
	}
	backoff := reflectorBackoffFor(rt, h, entries)
	if backoff != nil && !backoff.PrimaryOwed {
		debuglog.Log(ctx, "reflector.empty_backoff", map[string]any{})
		return none, nil
	}
	// The fallback found nothing stable in this memory after the primary
	// failed. The primary gets one try at it, and the fallback is not asked
	// the same question again.
	primaryOwed := backoff != nil
	inputKey := reflectorInputKey(entries)
	reflectionTokens := stageTokens(entries, ledger.OMReflectionsRecorded, current, hasCurrent, ledger.ObservedTokensSinceReflectionCoverage)
	observationCoverageID := ledger.LatestCoverageMarkerID(entries, ledger.OMObservationsRecorded)
	if observationCoverageID == "" {
		return none, nil
	}
	sessionIdentity := sessionIdentityOf(h)
	leafID := branchLeafID(entries)
	resolved := resolver.resolve(ctx, runtime.PhaseReflector)
	if resolved == nil {
		return reflectorStageResult{outcome: outcomeAbort, sameRunReflections: []ledger.Reflection{}}, nil
	}
	if ctx.Err() != nil {
		debuglog.Log(ctx, "reflector.aborted", map[string]any{})
		return reflectorStageResult{outcome: outcomeAbort, sameRunReflections: []ledger.Reflection{}}, nil
	}
	settled := &runtime.ReflectorEmptyBackoff{SessionIdentity: sessionIdentity, InputKey: inputKey}
	if primaryOwed && resolved.FallbackUsed {
		// This pass runs on the fallback, which already judged this memory.
		rt.SetReflectorEmptyBackoff(settled)
		debuglog.Log(ctx, "reflector.empty_backoff", map[string]any{"primaryOwed": true})
		return none, nil
	}
	// Announced only once the model resolved, like the observer: an
	// unresolvable model is reported once by the resolver, not every turn.
	notifyWorker(cfg, h, fmt.Sprintf("Observational memory: reflector running (~%s tokens)", jsfmt.Locale(reflectionTokens)))
	folded := ledger.FoldLedger(entries, "")
	var judge runtime.ResolveResult
	reflectWith := func(worker runtime.ResolveResult) ([]ledger.Reflection, error) {
		judge = worker
		memory := ledger.BoundWorkerMemory(folded.Reflections, folded.ActiveObservations, config.ResolveWorkerMemoryMaxTokens(cfg, host.ModelContextWindow(worker.Model)), false)
		return runReflector(ctx, agents.ReflectorArgs{WorkerArgs: workerArgs(ctx, cfg, h, worker, rules.StageReflector), Reflections: memory.Reflections, Observations: memory.Observations})
	}
	var reflections []ledger.Reflection
	if primaryOwed {
		reflections, err = reflectWith(*resolved)
	} else {
		reflections, err = runStageWithFallback(ctx, h, runtime.PhaseReflector, *resolved, resolver, reflectWith)
	}
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, agents.ErrAborted) {
			debuglog.Log(ctx, "reflector.aborted", map[string]any{})
			return reflectorStageResult{outcome: outcomeAbort, sameRunReflections: []ledger.Reflection{}}, nil
		}
		if primaryOwed {
			// The primary failed again: wait for the memory to change.
			rt.SetReflectorEmptyBackoff(settled)
		}
		return reflectorStageResult{outcome: outcomeAbort, sameRunReflections: []ledger.Reflection{}}, err
	}
	if ctx.Err() != nil {
		debuglog.Log(ctx, "reflector.aborted", map[string]any{})
		return reflectorStageResult{outcome: outcomeAbort, sameRunReflections: []ledger.Reflection{}}, nil
	}
	if len(reflections) == 0 {
		// Nothing stable enough in this memory: do not send the same prompt
		// again until the memory changes. A fallback retry's verdict still owes
		// the primary one try: it never judged this memory, and its prompt may
		// hold more of it.
		settled.PrimaryOwed = resolver.retriedOnFallback(judge)
		rt.SetReflectorEmptyBackoff(settled)
		debuglog.Log(ctx, "reflector.empty", map[string]any{"primaryOwed": settled.PrimaryOwed})
		recordEmptyPass(ctx, cfg, h, judge.Model, rules.Pass{Stage: rules.StageReflector})
		return none, nil
	}
	rt.SetReflectorEmptyBackoff(nil)
	data := ledger.BuildReflectionsRecordedData(reflections, observationCoverageID)
	if data == nil {
		return none, nil
	}
	if !branchStillCurrent(h, sessionIdentity, leafID) {
		debuglog.Log(ctx, "reflector.branch_changed", map[string]any{})
		incompletePass(ctx)
		return reflectorStageResult{outcome: outcomeAbort, sameRunReflections: []ledger.Reflection{}}, nil
	}
	if ctx.Err() != nil {
		debuglog.Log(ctx, "reflector.aborted", map[string]any{})
		return reflectorStageResult{outcome: outcomeAbort, sameRunReflections: []ledger.Reflection{}}, nil
	}
	if err := h.AppendEntry(ledger.OMReflectionsRecorded, data); err != nil {
		return reflectorStageResult{outcome: outcomeAbort, sameRunReflections: []ledger.Reflection{}}, err
	}
	recordPass(ctx, cfg, h, judge.Model, rules.Pass{Stage: rules.StageReflector})
	return reflectorStageResult{outcome: outcomeContinue, sameRunReflections: reflections, effectiveReflectionCoverageID: data.CoversUpToID}, nil
}

// runDropperStage runs one dropper pass with one layer snapshot (layerFor), then settles the
// held definitions it ran with (settlePass).
func runDropperStage(ctx context.Context, rt *runtime.Runtime, h host.Host, resolver *modelResolver, sameRunReflections []ledger.Reflection, sameRunReflectionCoverageID string) (stageOutcome, error) {
	ctx = withPass(ctx, passDropper)
	outcome, err := runDropperPass(ctx, rt, h, resolver, sameRunReflections, sameRunReflectionCoverageID)
	settlePass(ctx, rt.Config(), h, err)
	return outcome, err
}

func runDropperPass(ctx context.Context, rt *runtime.Runtime, h host.Host, resolver *modelResolver, sameRunReflections []ledger.Reflection, sameRunReflectionCoverageID string) (stageOutcome, error) {
	cfg := rt.Config()
	if sameRunReflectionCoverageID == "" || len(sameRunReflections) == 0 {
		debuglog.Log(ctx, "dropper.waiting_for_reflection", map[string]any{"sameRunReflections": len(sameRunReflections)})
		return outcomeContinue, nil
	}
	entries, err := h.GetBranch()
	if err != nil {
		return outcomeAbort, err
	}
	observationCoverageID := ledger.LatestCoverageMarkerID(entries, ledger.OMObservationsRecorded)
	if observationCoverageID == "" {
		return outcomeContinue, nil
	}
	sessionIdentity := sessionIdentityOf(h)
	leafID := branchLeafID(entries)
	folded := ledger.FoldLedger(entries, "")
	metrics := agents.ObservationPoolMetrics(folded.ActiveObservations, cfg.ObservationsPoolTargetTokens)
	if !metrics.Ready {
		debuglog.Log(ctx, "dropper.not_ready", map[string]any{
			"observationTokens":      metrics.ObservationTokens,
			"targetTokens":           metrics.TargetTokens,
			"tokensOverTarget":       metrics.TokensOverTarget,
			"fullness":               metrics.Fullness,
			"activeObservationCount": metrics.ActiveObservationCount,
			"droppableCount":         metrics.DroppableCount,
			"maxDropsAllowed":        metrics.MaxDropsAllowed,
		})
		return outcomeContinue, nil
	}
	debuglog.Log(ctx, "dropper.stage_start", map[string]any{
		"observationCoverageId":       observationCoverageID,
		"sameRunReflectionCoverageId": sameRunReflectionCoverageID,
		"sameRunReflectionCount":      len(sameRunReflections),
		"activeObservationCount":      metrics.ActiveObservationCount,
		"observationTokens":           metrics.ObservationTokens,
		"targetTokens":                metrics.TargetTokens,
		"tokensOverTarget":            metrics.TokensOverTarget,
		"fullness":                    metrics.Fullness,
		"maxDropsAllowed":             metrics.MaxDropsAllowed,
	})
	notifyWorker(cfg, h, fmt.Sprintf("Observational memory: dropper running after reflection — active observation pool ~%s / %s target tokens (%s%%)",
		jsfmt.Locale(metrics.ObservationTokens), jsfmt.Locale(metrics.TargetTokens), jsfmt.Locale(int(roundHalfAway(metrics.Fullness*100)))))
	resolved := resolver.resolve(ctx, runtime.PhaseDropper)
	if resolved == nil {
		return outcomeAbort, nil
	}
	if ctx.Err() != nil {
		debuglog.Log(ctx, "dropper.aborted", map[string]any{})
		return outcomeAbort, nil
	}
	reflectionsForDropper := mergeReflections(folded.Reflections, sameRunReflections)
	used := *resolved
	droppedIDs, err := runStageWithFallback(ctx, h, runtime.PhaseDropper, *resolved, resolver, func(worker runtime.ResolveResult) ([]string, error) {
		used = worker
		// The dropper prunes old observations, so it sees the oldest ones that fit.
		memory := ledger.BoundWorkerMemory(reflectionsForDropper, folded.ActiveObservations, config.ResolveWorkerMemoryMaxTokens(cfg, host.ModelContextWindow(worker.Model)), true)
		return runDropper(ctx, agents.DropperArgs{WorkerArgs: workerArgs(ctx, cfg, h, worker, rules.StageDropper), Reflections: memory.Reflections, Observations: memory.Observations, ActiveObservations: folded.ActiveObservations, TargetTokens: cfg.ObservationsPoolTargetTokens})
	})
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, agents.ErrAborted) {
			debuglog.Log(ctx, "dropper.aborted", map[string]any{})
			return outcomeAbort, nil
		}
		return outcomeAbort, err
	}
	if ctx.Err() != nil {
		debuglog.Log(ctx, "dropper.aborted", map[string]any{})
		return outcomeAbort, nil
	}
	droppedIDs = protectDrops(ctx, cfg, h, used, droppedIDs, folded.ActiveObservations, metrics.Fullness)
	if len(droppedIDs) == 0 {
		// The dropper ran and dropped nothing: counted, never proof (settlePass).
		recordEmptyPass(ctx, cfg, h, used.Model, rules.Pass{Stage: rules.StageDropper})
	}
	coversUpToID := ledger.EarlierCoverageMarkerID(entries, observationCoverageID, sameRunReflectionCoverageID)
	var data *ledger.ObservationsDroppedData
	if coversUpToID != "" && len(droppedIDs) > 0 {
		data = ledger.BuildObservationsDroppedData(droppedIDs, coversUpToID)
	}
	debuglog.Log(ctx, "dropper.append", map[string]any{"droppedIdsCount": len(droppedIDs), "coversUpToId": coversUpToID, "dataBuilt": data != nil, "appended": data != nil})
	if data != nil {
		if !branchStillCurrent(h, sessionIdentity, leafID) {
			debuglog.Log(ctx, "dropper.branch_changed", map[string]any{})
			incompletePass(ctx)
			return outcomeAbort, nil
		}
		if ctx.Err() != nil {
			debuglog.Log(ctx, "dropper.aborted", map[string]any{})
			return outcomeAbort, nil
		}
		if err := h.AppendEntry(ledger.OMObservationsDropped, data); err != nil {
			return outcomeAbort, err
		}
		recordPass(ctx, cfg, h, used.Model, rules.Pass{Stage: rules.StageDropper})
	}
	return outcomeContinue, nil
}

func trimmed(s string) string {
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\n' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\n' || s[end-1] == '\t' || s[end-1] == '\r') {
		end--
	}
	return s[start:end]
}

// roundHalfAway is JavaScript's Math.round for non-negative values.
func roundHalfAway(v float64) float64 {
	if v < 0 {
		return -roundHalfAway(-v)
	}
	return float64(int(v + 0.5))
}
