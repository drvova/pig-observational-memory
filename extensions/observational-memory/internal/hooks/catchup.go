package hooks

import (
	"context"
	"fmt"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/jsfmt"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/serialize"
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
	contextWindow := host.ModelContextWindow(resolved.Model)
	maxChunkTokens := config.ResolveObserverChunkMaxTokens(cfg, contextWindow)
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
	thinking := "low"
	if cfg.Model != nil && cfg.Model.Thinking != "" {
		thinking = string(cfg.Model.Thinking)
	}
	for chunkIndex := 0; chunkIndex < args.MaxChunks && len(remaining) > 0; chunkIndex++ {
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
		memory := ledger.BoundWorkerMemory(fullMemory.Reflections, fullMemory.Observations, config.ResolveWorkerMemoryMaxTokens(cfg, contextWindow), false)
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
		priorObservations := make([]string, 0, len(memory.Observations))
		for _, observation := range memory.Observations {
			priorObservations = append(priorObservations, ledger.ObservationToSummaryLine(observation))
		}
		observations, err := runObserver(ctx, agents.ObserverArgs{
			WorkerArgs: agents.WorkerArgs{
				Model:           resolved.Model,
				APIKey:          resolved.APIKey,
				Headers:         resolved.Headers,
				Env:             resolved.Env,
				MaxTurns:        cfg.AgentMaxTurns,
				MaxOutputTokens: cfg.AgentMaxTokens,
				ThinkingLevel:   thinking,
				Stream:          h.Models().Stream,
			},
			PriorReflections:      priorReflections,
			PriorObservations:     priorObservations,
			Chunk:                 serialized.Text,
			AllowedSourceEntryIDs: serialized.SourceEntryIDs,
		})
		if err != nil {
			if ctx.Err() != nil {
				result.StoppedBecause = "aborted"
				return result
			}
			debuglog.Log(ctx, "compaction.catch_up.error", map[string]any{"chunkIndex": chunkIndex, "errorMessage": err.Error()})
			notifyUI(h, "Observational memory: catch-up observer failed: "+err.Error(), "warning")
			result.StoppedBecause = "error"
			return result
		}
		var data *ledger.ObservationsRecordedData
		if len(observations) > 0 {
			data = ledger.BuildObservationsRecordedData(observations, coversUpToID)
		}
		if data == nil {
			debuglog.Log(ctx, "compaction.catch_up.empty", map[string]any{"chunkIndex": chunkIndex, "coversUpToId": coversUpToID})
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
