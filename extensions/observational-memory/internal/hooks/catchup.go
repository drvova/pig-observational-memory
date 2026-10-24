package hooks

import (
	"context"
	"fmt"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/jsfmt"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
)

// CatchUpResult reports a synchronous observer catch-up.
type CatchUpResult struct {
	// ChunksRecorded counts the observer chunks that recorded observations.
	ChunksRecorded int
	// StoppedBecause says why catch-up stopped before covering the whole gap:
	// "max_chunks", "empty", "error", "model_unavailable" or "aborted".
	StoppedBecause string
}

// CatchUpArgs configure CatchUpObserver.
type CatchUpArgs struct {
	Runtime   *runtime.Runtime
	Host      host.Host
	Entries   []ledger.Entry
	Gap       ledger.UnobservedSourceSpan
	MaxChunks int
}

// CatchUpObserver observes the unobserved source entries before Pi's proposed
// cut, one observer chunk at a time, appending coverage as it goes. It runs
// inside session_before_compact, where Pi waits for the hook and no session
// request is in flight. A chunk that yields no observations, fails, or hits the
// chunk cap stops the catch-up; nothing is appended for a failed chunk.
func CatchUpObserver(ctx context.Context, args CatchUpArgs) CatchUpResult {
	result := CatchUpResult{}
	if args.MaxChunks <= 0 {
		return result
	}
	rt, h := args.Runtime, args.Host
	cfg := rt.Config()
	resolved := rt.ResolveModel(ctx, runtime.ResolveCtx{Model: h.SessionModel(), Models: h.Models(), HasUI: h.HasUI(), Notify: h.Notify})
	if !resolved.OK {
		debuglog.Log(ctx, "compaction.catch_up.model_unavailable", map[string]any{"reason": resolved.Reason})
		result.StoppedBecause = "model_unavailable"
		return result
	}
	// The same request shaping and fallback handling as the background
	// pipeline: Console Go session headers, the fallback's thinking level, and
	// one retry on the configured fallback when a chunk's model call fails.
	resolver := &modelResolver{rt: rt, h: h, cached: &resolved}
	current := workerHeadersFor(h, resolved)
	contextWindow := observerChunkContextWindow(cfg, h, current)
	// A split-chunk rule for the resolved worker halves the chunk, as in the background stage.
	// A chunk retried on the fallback keeps the size the primary's rules gave it (re-sizing
	// would change what its marker covers); its pass is still the fallback's, which ran it.
	maxChunkTokens := rulesFor(h, cfg, current.Model).SplitChunk(config.ResolveObserverChunkMaxTokens(cfg, contextWindow))
	entries := args.Entries
	// Start at the global observation frontier, not at the gap: a coverage
	// marker claims everything before it, so starting later would mark unread
	// source between the frontier and the range start as observed.
	frontierIndex := ledger.LatestCoverageIndex(entries, ledger.OMObservationsRecorded)
	remaining := []ledger.Entry{}
	for i := frontierIndex + 1; i <= args.Gap.LastIndex && i < len(entries); i++ {
		if i >= 0 && ledger.IsSourceEntry(entries[i]) {
			remaining = append(remaining, entries[i])
		}
	}
	branch := entries
	for chunkIndex := 0; chunkIndex < args.MaxChunks && len(remaining) > 0; chunkIndex++ {
		// Each chunk is one pass with one layer snapshot (layerFor).
		ctx := withPassLayers(ctx)
		if ctx.Err() != nil {
			result.StoppedBecause = "aborted"
			return result
		}
		serialized := serialize.SerializeSourceAddressedBranchEntries(remaining, serialize.SourceAddressedOptions{MaxTokens: maxChunkTokens})
		if trimmed(serialized.Text) == "" || len(serialized.SourceEntryIDs) == 0 {
			break
		}
		coversUpToID := serialized.SourceEntryIDs[len(serialized.SourceEntryIDs)-1]
		fullMemory := ledger.FullProjection(branch, "")
		memory := ledger.BoundObserverMemory(fullMemory.Reflections, fullMemory.Observations, config.ResolveWorkerMemoryMaxTokens(cfg, contextWindow))
		if cfg.ShowWorkerNotifications && h.HasUI() {
			noun := "entries"
			if len(serialized.SourceEntryIDs) == 1 {
				noun = "entry"
			}
			h.Notify(fmt.Sprintf("Observational memory: observing %d unobserved source %s (~%s tokens) before compacting", len(serialized.SourceEntryIDs), noun, jsfmt.Locale(serialized.EstimatedTokens)), "info")
		}
		debuglog.Log(ctx, "compaction.catch_up.chunk", map[string]any{"chunkIndex": chunkIndex, "sourceEntryCount": len(serialized.SourceEntryIDs), "estimatedTokens": serialized.EstimatedTokens, "coversUpToId": coversUpToID})
		priorReflections := make([]string, 0, len(memory.Reflections))
		for _, reflection := range memory.Reflections {
			priorReflections = append(priorReflections, ledger.ReflectionToSummaryLine(reflection))
		}
		priorObservations := memory.ObserverLines()
		used := current
		outcome, err := runStageWithFallback(ctx, h, runtime.PhaseObserver, current, resolver, func(worker runtime.ResolveResult) (agents.ObserverResult, error) {
			used = worker
			return runObserver(ctx, observerArgs(ctx, cfg, h, worker, priorReflections, priorObservations, memory.Subjects, serialized.Text, serialized.SourceEntryIDs))
		})
		if resolver.fallbackActive != nil && !current.FallbackUsed {
			// Later chunks go straight to the fallback that worked, sized by its own
			// context window and rules, as the background stage sizes them.
			current = *resolver.fallbackActive
			contextWindow = observerChunkContextWindow(cfg, h, current)
			maxChunkTokens = rulesFor(h, cfg, current.Model).SplitChunk(config.ResolveObserverChunkMaxTokens(cfg, contextWindow))
		}
		if err != nil {
			if ctx.Err() != nil {
				result.StoppedBecause = "aborted"
				return result
			}
			debuglog.Log(ctx, "compaction.catch_up.error", map[string]any{"chunkIndex": chunkIndex, "errorMessage": err.Error()})
			notifyUI(h, "Observational memory: catch-up observer failed: "+err.Error(), "warning")
			settlePass(ctx, cfg, h, err)
			result.StoppedBecause = "error"
			return result
		}
		observations := outcome.Observations
		if outcome.Partial && len(observations) > 0 {
			// The model stopped mid-chunk: claim coverage only through the
			// last source entry the observations cite.
			cited := agents.LastCitedSourceEntryID(observations, serialized.SourceEntryIDs)
			debuglog.Log(ctx, "compaction.catch_up.partial", map[string]any{"chunkIndex": chunkIndex, "coversUpToId": coversUpToID, "citedUpToId": cited})
			if cited == "" {
				result.StoppedBecause = "error"
				return result
			}
			coversUpToID = cited
		}
		var data *ledger.ObservationsRecordedData
		var pass rules.Pass
		if len(observations) > 0 {
			// The same remedies as the background stage, with the same constraint: they act
			// only on a chunk that recorded something, and keep-head only inside the coverage
			// boundary, so the marker appended below stays a positional claim.
			chunk := observerChunk{branch: branch, serialized: serialized, priorReflections: priorReflections, priorObservations: priorObservations, subjects: memory.Subjects, known: fullMemory.Observations}
			observations, pass = applyObserverRules(ctx, cfg, h, used, chunk, observations, coversUpToID, outcome.Partial)
			data = ledger.BuildObservationsRecordedData(observations, coversUpToID)
		}
		if data == nil {
			debuglog.Log(ctx, "compaction.catch_up.empty", map[string]any{"chunkIndex": chunkIndex, "coversUpToId": coversUpToID})
			if len(observations) == 0 && ctx.Err() == nil {
				// An empty chunk is counted like the background stage's, and proves nothing.
				chunk := observerChunk{branch: branch, serialized: serialized, known: fullMemory.Observations}
				recordEmptyPass(ctx, cfg, h, used.Model, emptyObserverPass(ctx, cfg, h, used.Model, chunk, coversUpToID))
				settlePass(ctx, cfg, h, nil)
			}
			result.StoppedBecause = "empty"
			return result
		}
		if err := h.AppendEntry(ledger.OMObservationsRecorded, data); err != nil {
			debuglog.Log(ctx, "compaction.catch_up.error", map[string]any{"chunkIndex": chunkIndex, "errorMessage": err.Error()})
			notifyUI(h, "Observational memory: catch-up observer failed: "+err.Error(), "warning")
			result.StoppedBecause = "error"
			return result
		}
		result.ChunksRecorded++
		recordPass(ctx, cfg, h, used.Model, pass)
		// Each chunk is a pass: it proves (or reverts) the held definitions it ran with.
		settlePass(ctx, cfg, h, nil)
		debuglog.Log(ctx, "compaction.catch_up.recorded", map[string]any{"chunkIndex": chunkIndex, "count": len(observations), "coversUpToId": coversUpToID})

		// Coverage is positional: everything at or before the marker counts as
		// covered, including entries the serializer skipped for lack of
		// renderable content.
		coveredIndex := ledger.EntryIndexForID(entries, coversUpToID)
		index := ledger.EntryIndexByID(entries)
		next := []ledger.Entry{}
		for _, entry := range remaining {
			if idx, ok := index[entry.ID]; ok && idx > coveredIndex {
				next = append(next, entry)
			}
		}
		remaining = next
		if fresh, err := h.GetBranch(); err == nil && fresh != nil {
			branch = fresh
		}
	}
	if len(remaining) > 0 && result.StoppedBecause == "" {
		result.StoppedBecause = "max_chunks"
	}
	return result
}
