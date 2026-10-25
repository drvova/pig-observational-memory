package hooks

import (
	"context"
	"strconv"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
)

// BeforeCompactEvent is Pi's session_before_compact event as this hook reads it.
type BeforeCompactEvent struct {
	FirstKeptEntryID string
	TokensBefore     int
	BranchEntries    []ledger.Entry
	Reason           string
	// CustomInstructions is what the caller of compact({customInstructions})
	// asked the summary to carry; appended to the rendered memory (upstream
	// issue #55).
	CustomInstructions string
	// Preparation is the rest of PiG's CompactionPreparation.
	Preparation CompactionPreparation
}

// CompactionPreparation is PiG's CompactionPreparation beyond firstKeptEntryId and
// tokensBefore, which BeforeCompactEvent carries itself.
type CompactionPreparation struct {
	// MessagesToSummarize are the messages PiG would summarize (decoded JSON).
	MessagesToSummarize []map[string]any
	// TurnPrefixMessages are the messages of a split turn's kept prefix.
	TurnPrefixMessages []map[string]any
	// IsSplitTurn is set when the cut falls inside a turn.
	IsSplitTurn bool
	// PreviousSummary is the previous compaction's summary, "" when none.
	PreviousSummary string
	// FileOps are the paths read, written and edited in the summarized range.
	FileOps CompactionFileOps
	// Settings are PiG's compaction settings, with model overrides applied.
	Settings CompactionSettings
}

// CompactionFileOps are PiG's FileOperations, each set as a sorted path list.
type CompactionFileOps struct {
	Read, Written, Edited []string
}

// CompactionSettings are PiG's CompactionSettings.
type CompactionSettings struct {
	Enabled          bool
	ReserveTokens    int
	KeepRecentTokens int
}

// CompactionResult is the extension-provided compaction.
type CompactionResult struct {
	Summary          string
	FirstKeptEntryID string
	TokensBefore     int
	Details          ledger.MemoryDetails
	// Usage is the provider usage of the request that built the summary, in Pi's Usage
	// shape; nil when no request did (level 0).
	Usage map[string]any
}

// ToMap is the result in Pi's CompactionResult wire shape.
func (r CompactionResult) ToMap() map[string]any {
	result := map[string]any{
		"summary":          r.Summary,
		"firstKeptEntryId": r.FirstKeptEntryID,
		"tokensBefore":     r.TokensBefore,
		"details":          r.Details,
	}
	if r.Usage != nil {
		result["usage"] = r.Usage
	}
	return result
}

// BeforeCompactOutcome is the hook's answer: cancel, own the compaction, or
// (neither) decline so Pi's native summarizer runs.
type BeforeCompactOutcome struct {
	Cancel     bool
	Compaction *CompactionResult
}

// ToResult is the outcome in Pi's SessionBeforeCompactResult wire shape, or nil to decline.
func (o BeforeCompactOutcome) ToResult() map[string]any {
	if o.Cancel {
		return map[string]any{"cancel": true}
	}
	if o.Compaction != nil {
		return map[string]any{"compaction": o.Compaction.ToMap()}
	}
	return nil
}

func debugScope(rt *runtime.Runtime, h host.Host) debuglog.Context {
	return debuglog.Context{
		Enabled:     rt.Config().DebugLog,
		Cwd:         h.Cwd(),
		SessionID:   h.SessionID(),
		SessionFile: h.SessionFile(),
		RunID:       "compaction-" + strconv.FormatInt(Now().UnixMilli(), 36),
	}
}

// HandleBeforeCompact is the session_before_compact handler with ctx as the request's own
// cancellation (HandleBeforeCompactRequest).
func HandleBeforeCompact(ctx context.Context, rt *runtime.Runtime, h host.Host, event BeforeCompactEvent) BeforeCompactOutcome {
	return HandleBeforeCompactRequest(ctx, rt, h, event)
}

// HandleBeforeCompactRequest is the session_before_compact handler (handleBeforeCompactV2).
// request is the session_before_compact request's own cancellation, never the agent run's
// signal: a manual /compact aborts the run, never the request.
func HandleBeforeCompactRequest(request context.Context, rt *runtime.Runtime, h host.Host, event BeforeCompactEvent) BeforeCompactOutcome {
	rt.EnsureConfig(h)
	return handleBeforeCompactV2(request, rt, h, event)
}
