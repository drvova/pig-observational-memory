// Package hooks implements the extension's event handlers: the consolidation
// trigger, the proactive compaction trigger, and the compaction hook.
package hooks

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"
	"time"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/jsfmt"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/serialize"
)

// Now is the clock; tests replace it.
var Now = time.Now

// The worker entry points; tests replace them.
var (
	runObserver  = agents.RunObserver
	runReflector = agents.RunReflector
	runDropper   = agents.RunDropper
)

// ConsolidationHooks are callbacks around the consolidation trigger.
type ConsolidationHooks struct {
	// AfterIdleConsolidation runs after an idle-mode consolidation run finishes
	// (or is skipped) with the agent_settled host, so work that shares the
	// settled event (proactive compaction) gets its turn afterwards.
	AfterIdleConsolidation func(h host.Host)
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

func anyStageDue(entries []ledger.Entry, cfg config.Config, current int, hasCurrent bool) bool {
	return stageDue(entries, current, hasCurrent, ledger.OMObservationsRecorded, ledger.RawTokensSinceObservationCoverage, cfg.ObserveAfterTokens) ||
		stageDue(entries, current, hasCurrent, ledger.OMReflectionsRecorded, ledger.ObservedTokensSinceReflectionCoverage, cfg.ReflectAfterTokens)
}

func shouldNotifyWorker(cfg config.Config, h host.Host) bool {
	return cfg.ShowWorkerNotifications && h.HasUI()
}

func notifyWorker(cfg config.Config, h host.Host, message string) {
	if shouldNotifyWorker(cfg, h) {
		h.Notify(message, "info")
	}
}

func notifyUI(h host.Host, message, level string) {
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
	if fallbackModel := h.Models().Find(cfg.FallbackModel.Provider, cfg.FallbackModel.ID); fallbackModel != nil {
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
	// Coverage markers are only appended on success, so an aborted run simply
	// retries after the next settled event.
	if rt.AbortConsolidation() {
		debuglog.Log(context.Background(), "consolidation.aborted", map[string]any{"reason": "agent_start"})
		notifyWorker(rt.Config(), h, "Observational memory: memory workers paused while the agent runs (consolidateWhenIdle)")
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
	launched := MaybeLaunchConsolidation(rt, h)
	if hooks.AfterIdleConsolidation == nil {
		return
	}
	if launched == nil {
		hooks.AfterIdleConsolidation(h)
		return
	}
	go func() {
		<-launched
		hooks.AfterIdleConsolidation(h)
	}()
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
	if !anyStageDue(entries, cfg, current, hasCurrent) {
		return nil
	}
	scope := debuglog.Context{Enabled: cfg.DebugLog, Cwd: h.Cwd(), SessionID: h.SessionID(), SessionFile: h.SessionFile(), RunID: newRunID("consolidation")}
	return rt.LaunchConsolidationTask(h, func(ctx context.Context) error {
		return RunConsolidationPipeline(debuglog.With(ctx, scope), rt, h)
	})
}

// RunConsolidationPipeline runs the observer, reflector and dropper stages.
func RunConsolidationPipeline(ctx context.Context, rt *runtime.Runtime, h host.Host) error {
	resolver := &modelResolver{rt: rt, h: h}

	rt.SetConsolidationPhase(runtime.PhaseObserver)
	outcome, err := runObserverStage(ctx, rt, h, resolver)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		debuglog.Log(ctx, "observer.error", map[string]any{"errorMessage": rt.RecordConsolidationStageError(h, runtime.PhaseObserver, err)})
		return nil
	}
	if outcome == outcomeAbort || ctx.Err() != nil {
		return nil
	}

	rt.SetConsolidationPhase(runtime.PhaseReflector)
	reflector, err := runReflectorStage(ctx, rt, h, resolver)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		debuglog.Log(ctx, "reflector.error", map[string]any{"errorMessage": rt.RecordConsolidationStageError(h, runtime.PhaseReflector, err)})
		return nil
	}
	if reflector.outcome == outcomeAbort || ctx.Err() != nil {
		return nil
	}

	rt.SetConsolidationPhase(runtime.PhaseDropper)
	if _, err := runDropperStage(ctx, rt, h, resolver, reflector.sameRunReflections, reflector.effectiveReflectionCoverageID); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		debuglog.Log(ctx, "dropper.error", map[string]any{"errorMessage": rt.RecordConsolidationStageError(h, runtime.PhaseDropper, err)})
	}
	return nil
}

func workerArgs(cfg config.Config, h host.Host, worker runtime.ResolveResult) agents.WorkerArgs {
	return agents.WorkerArgs{
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

func runObserverStage(ctx context.Context, rt *runtime.Runtime, h host.Host, resolver *modelResolver) (stageOutcome, error) {
	cfg := rt.Config()
	entries, err := h.GetBranch()
	if err != nil {
		return outcomeAbort, err
	}
	current, hasCurrent := realContextTokens(h)
	tokens := stageTokens(entries, ledger.OMObservationsRecorded, current, hasCurrent, ledger.RawTokensSinceObservationCoverage)
	if tokens < cfg.ObserveAfterTokens {
		return outcomeContinue, nil
	}
	sessionIdentity := h.SessionID()
	if sessionIdentity == "" {
		sessionIdentity = h.SessionFile()
	}
	coverageID := ledger.LatestCoverageMarkerID(entries, ledger.OMObservationsRecorded)

	// Deliberate-empty backoff: an intentional "nothing to record" verdict
	// must not re-fire the observer every turn over the same span.
	if backoff := rt.ObserverEmptyBackoff(); backoff != nil {
		if sessionIdentity != backoff.SessionIdentity || coverageID != backoff.CoverageID || tokens >= backoff.TokensAtEmpty+cfg.ObserveAfterTokens {
			rt.SetObserverEmptyBackoff(nil)
		} else {
			debuglog.Log(ctx, "observer.empty_backoff", map[string]any{"tokens": tokens, "resumeAtTokens": backoff.TokensAtEmpty + cfg.ObserveAfterTokens})
			return outcomeContinue, nil
		}
	}

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
	maxChunkTokens := config.ResolveObserverChunkMaxTokens(cfg, contextWindow)
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
	memory := ledger.BoundWorkerMemory(fullMemory.Reflections, fullMemory.Observations, config.ResolveWorkerMemoryMaxTokens(cfg, contextWindow), false)
	priorReflections := make([]string, 0, len(memory.Reflections))
	for _, reflection := range memory.Reflections {
		priorReflections = append(priorReflections, ledger.ReflectionToSummaryLine(reflection))
	}
	priorObservations := make([]string, 0, len(memory.Observations))
	for _, observation := range memory.Observations {
		priorObservations = append(priorObservations, ledger.ObservationToSummaryLine(observation))
	}

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

	observations, err := runStageWithFallback(ctx, h, runtime.PhaseObserver, *resolved, resolver, func(worker runtime.ResolveResult) ([]ledger.Observation, error) {
		return runObserver(ctx, agents.ObserverArgs{
			WorkerArgs:            workerArgs(cfg, h, worker),
			PriorReflections:      priorReflections,
			PriorObservations:     priorObservations,
			Chunk:                 serialized.Text,
			AllowedSourceEntryIDs: serialized.SourceEntryIDs,
		})
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
			return outcomeAbort, nil
		}
		return outcomeAbort, err
	}
	if ctx.Err() != nil {
		debuglog.Log(ctx, "observer.aborted", map[string]any{"coversUpToId": coversUpToID})
		return outcomeAbort, nil
	}
	if len(observations) == 0 {
		debuglog.Log(ctx, "observer.empty", map[string]any{"coversUpToId": coversUpToID})
		rt.SetObserverEmptyBackoff(&runtime.ObserverEmptyBackoff{SessionIdentity: sessionIdentity, CoverageID: coverageID, TokensAtEmpty: tokens})
		notifyWorker(cfg, h, "Observational memory: observer found nothing new in this chunk (coverage unchanged; will retry later)")
		return outcomeContinue, nil
	}
	rt.SetObserverEmptyBackoff(nil)

	data := ledger.BuildObservationsRecordedData(observations, coversUpToID)
	if data == nil {
		return outcomeContinue, nil
	}
	observationTokens := 0
	for _, observation := range observations {
		observationTokens += observation.TokenCount
	}
	debuglog.Log(ctx, "observer.records", map[string]any{"count": len(observations), "observationTokens": observationTokens, "coversUpToId": coversUpToID})
	if err := h.AppendEntry(ledger.OMObservationsRecorded, data); err != nil {
		return outcomeAbort, err
	}
	debuglog.Log(ctx, "observer.appended", map[string]any{"count": len(observations), "coversUpToId": coversUpToID})
	notifyWorker(cfg, h, fmt.Sprintf("Observational memory: %d %s recorded", len(observations), jsfmt.Plural(len(observations), "observation")))
	return outcomeContinue, nil
}

func runReflectorStage(ctx context.Context, rt *runtime.Runtime, h host.Host, resolver *modelResolver) (reflectorStageResult, error) {
	cfg := rt.Config()
	none := reflectorStageResult{outcome: outcomeContinue, sameRunReflections: []ledger.Reflection{}}
	entries, err := h.GetBranch()
	if err != nil {
		return reflectorStageResult{outcome: outcomeAbort}, err
	}
	current, hasCurrent := realContextTokens(h)
	reflectionTokens := stageTokens(entries, ledger.OMReflectionsRecorded, current, hasCurrent, ledger.ObservedTokensSinceReflectionCoverage)
	if reflectionTokens < cfg.ReflectAfterTokens {
		return none, nil
	}
	observationCoverageID := ledger.LatestCoverageMarkerID(entries, ledger.OMObservationsRecorded)
	if observationCoverageID == "" {
		return none, nil
	}
	notifyWorker(cfg, h, fmt.Sprintf("Observational memory: reflector running (~%s tokens)", jsfmt.Locale(reflectionTokens)))
	resolved := resolver.resolve(ctx, runtime.PhaseReflector)
	if resolved == nil {
		return reflectorStageResult{outcome: outcomeAbort, sameRunReflections: []ledger.Reflection{}}, nil
	}
	if ctx.Err() != nil {
		debuglog.Log(ctx, "reflector.aborted", map[string]any{})
		return reflectorStageResult{outcome: outcomeAbort, sameRunReflections: []ledger.Reflection{}}, nil
	}
	folded := ledger.FoldLedger(entries, "")
	reflections, err := runStageWithFallback(ctx, h, runtime.PhaseReflector, *resolved, resolver, func(worker runtime.ResolveResult) ([]ledger.Reflection, error) {
		memory := ledger.BoundWorkerMemory(folded.Reflections, folded.ActiveObservations, config.ResolveWorkerMemoryMaxTokens(cfg, host.ModelContextWindow(worker.Model)), false)
		return runReflector(ctx, agents.ReflectorArgs{WorkerArgs: workerArgs(cfg, h, worker), Reflections: memory.Reflections, Observations: memory.Observations})
	})
	if err != nil {
		if ctx.Err() != nil || errors.Is(err, agents.ErrAborted) {
			debuglog.Log(ctx, "reflector.aborted", map[string]any{})
			return reflectorStageResult{outcome: outcomeAbort, sameRunReflections: []ledger.Reflection{}}, nil
		}
		return reflectorStageResult{outcome: outcomeAbort, sameRunReflections: []ledger.Reflection{}}, err
	}
	if ctx.Err() != nil {
		debuglog.Log(ctx, "reflector.aborted", map[string]any{})
		return reflectorStageResult{outcome: outcomeAbort, sameRunReflections: []ledger.Reflection{}}, nil
	}
	if len(reflections) == 0 {
		return none, nil
	}
	data := ledger.BuildReflectionsRecordedData(reflections, observationCoverageID)
	if data == nil {
		return none, nil
	}
	if err := h.AppendEntry(ledger.OMReflectionsRecorded, data); err != nil {
		return reflectorStageResult{outcome: outcomeAbort, sameRunReflections: []ledger.Reflection{}}, err
	}
	return reflectorStageResult{outcome: outcomeContinue, sameRunReflections: reflections, effectiveReflectionCoverageID: data.CoversUpToID}, nil
}

func runDropperStage(ctx context.Context, rt *runtime.Runtime, h host.Host, resolver *modelResolver, sameRunReflections []ledger.Reflection, sameRunReflectionCoverageID string) (stageOutcome, error) {
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
	droppedIDs, err := runStageWithFallback(ctx, h, runtime.PhaseDropper, *resolved, resolver, func(worker runtime.ResolveResult) ([]string, error) {
		// The dropper prunes old observations, so it sees the oldest ones that fit.
		memory := ledger.BoundWorkerMemory(reflectionsForDropper, folded.ActiveObservations, config.ResolveWorkerMemoryMaxTokens(cfg, host.ModelContextWindow(worker.Model)), true)
		return runDropper(ctx, agents.DropperArgs{WorkerArgs: workerArgs(cfg, h, worker), Reflections: memory.Reflections, Observations: memory.Observations, TargetTokens: cfg.ObservationsPoolTargetTokens})
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
	coversUpToID := ledger.EarlierCoverageMarkerID(entries, observationCoverageID, sameRunReflectionCoverageID)
	var data *ledger.ObservationsDroppedData
	if coversUpToID != "" && len(droppedIDs) > 0 {
		data = ledger.BuildObservationsDroppedData(droppedIDs, coversUpToID)
	}
	debuglog.Log(ctx, "dropper.append", map[string]any{"droppedIdsCount": len(droppedIDs), "coversUpToId": coversUpToID, "dataBuilt": data != nil, "appended": data != nil})
	if data != nil {
		if err := h.AppendEntry(ledger.OMObservationsDropped, data); err != nil {
			return outcomeAbort, err
		}
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
