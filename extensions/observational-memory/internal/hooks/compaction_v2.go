package hooks

import (
	"context"
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// The kinds of memory v2 summary (the compaction.v2 debug event's "summary").
const (
	// v2SummaryRecords is the ledger's records rendered under the summary budget.
	v2SummaryRecords = "records"
	// v2SummaryFallback is the last resort when even the minimal brief failed.
	v2SummaryFallback = "fallback"
)

// V2FallbackSummary is the memory v2 summary when building every other one panicked: it
// still points at what the compaction removed, so PiG never falls through to its summarizer.
const V2FallbackSummary = `omitted: the compacted conversation; the memory tool searches it (history | search "words").`

// renderV2Records renders the ledger's records for a memory v2 summary; tests replace it.
var renderV2Records = ledger.RenderSummaryWithOptions

// handleBeforeCompactV2 is memory v2's session_before_compact hook (docs/memory-v2.md, "Owning
// every compaction PiG prepares"). It returns a compaction for every preparation PiG hands it,
// never declining and never cancelling: overflow, an empty ledger and a budget too small for
// any record all get one. It keeps PiG's cut exactly (firstKeptEntryId, with the fold boundary
// equal to it), makes no model request at level 0, and recovers a panic into the minimal
// brief. Only a cancelled request context makes it return nothing.
func handleBeforeCompactV2(request context.Context, rt *runtime.Runtime, h host.Host, event BeforeCompactEvent) (outcome BeforeCompactOutcome) {
	if request == nil {
		request = context.Background()
	}
	if request.Err() != nil {
		return BeforeCompactOutcome{}
	}
	// A concurrent hook is not cancelled: the hook appends nothing, so two compactions
	// rendering at once cannot interfere.
	if rt.TryEnterCompactHook() {
		defer rt.LeaveCompactHook()
	}
	ctx := request
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		debuglog.Log(ctx, "compaction.v2.recovered", map[string]any{"panic": fmt.Sprint(recovered)})
		if request.Err() != nil {
			outcome = BeforeCompactOutcome{}
			return
		}
		compaction := v2RecoveredCompaction(rt, event)
		outcome = BeforeCompactOutcome{Compaction: &compaction}
	}()
	ctx = debuglog.With(request, debugScope(rt, h))
	compaction := buildCompactionV2(ctx, rt, h, event)
	if request.Err() != nil {
		return BeforeCompactOutcome{}
	}
	return BeforeCompactOutcome{Compaction: &compaction}
}

// buildCompactionV2 is level 0's compaction: the ledger's records under the summary budget,
// else the minimal brief of the cut range.
func buildCompactionV2(ctx context.Context, rt *runtime.Runtime, h host.Host, event BeforeCompactEvent) CompactionResult {
	cfg := rt.Config()
	entries := event.BranchEntries
	cut := v2SourceEntries(v2CutRange(entries, event))
	previousSummary := v2PreviousSummary(entries, event)
	removed := v2SourceTokens(cut) + tokens.EstimateStringTokens(previousSummary)
	model := h.SessionModel()
	headroom := ledger.NextRunHeadroom(entries)
	budget := config.ResolveV2SummaryBudget(cfg, config.V2SummaryBudgetInputs{
		ContextWindow:   host.ModelContextWindow(model),
		ReserveTokens:   event.Preparation.Settings.ReserveTokens,
		TokensBefore:    event.TokensBefore,
		RemovedTokens:   removed,
		MaxOutputTokens: host.ModelMaxTokens(model),
		NextRunHeadroom: headroom,
	})

	// PiG's customInstructions reach a level-0 summary as a block after the memory, when it fits.
	instructionsBlock := ""
	if instructions := strings.TrimSpace(event.CustomInstructions); instructions != "" {
		instructionsBlock = "\n\n---\n\n" + instructions
	}
	memoryBudget := budget
	if instructionsBlock != "" && budget-tokens.EstimateStringTokens(instructionsBlock) > 0 {
		memoryBudget = budget - tokens.EstimateStringTokens(instructionsBlock)
	}

	// The fold boundary is PiG's cut: every record covered through it is folded, drops and
	// reflections included, with no normal-versus-full split.
	projection := ledger.FullProjection(entries, event.FirstKeptEntryID)
	summary, kind := "", ""
	if memoryBudget > 0 && len(projection.Reflections)+len(projection.Observations) > 0 {
		// The native layout in memoryFormat, with no codec: memory v2 bypasses the per-model
		// layer's summary definitions (harness.LayerFor, which v1 renders through) until the
		// harness moves to the new path (docs/memory-v2.md, "Order of work", step 5).
		options := ledger.RenderOptions{ReflectionDates: ledger.ReflectionDateRanges(ledger.FoldLedger(entries, "")), Format: cfg.MemoryFormat, Aliases: projection.Aliases}
		rendered := renderV2Records(projection.Reflections, projection.Observations, memoryBudget, options)
		if len(rendered.Reflections)+len(rendered.Observations) > 0 && tokens.EstimateStringTokens(rendered.Text) <= memoryBudget {
			summary, kind = rendered.Text, v2SummaryRecords
		}
	}
	if summary == "" {
		// Bounded by what the range carries too, so even a budget of 0 or less (a tiny range)
		// never gets a summary larger than what it replaces.
		summary, kind = serialize.MinimalBrief(cut, harness.MemoryToolNames(rt), memoryBudget, removed)
	}
	instructionsKept := false
	if instructionsBlock != "" && tokens.EstimateStringTokens(summary+instructionsBlock) <= budget {
		summary += instructionsBlock
		instructionsKept = true
	}

	data := map[string]any{
		"level":            0,
		"summary":          kind,
		"budgetTokens":     budget,
		"removedTokens":    removed,
		"headroomTokens":   headroom,
		"tokensBefore":     event.TokensBefore,
		"reserveTokens":    event.Preparation.Settings.ReserveTokens,
		"summaryTokens":    tokens.EstimateStringTokens(summary),
		"firstKeptEntryId": event.FirstKeptEntryID,
		"cutEntries":       len(cut),
		"reason":           event.Reason,
		"isSplitTurn":      event.Preparation.IsSplitTurn,
		"records":          len(projection.Reflections) + len(projection.Observations),
	}
	if instructionsBlock != "" {
		data["instructionsKept"] = instructionsKept
	}
	debuglog.Log(ctx, "compaction.v2", data)

	return CompactionResult{
		Summary:          summary,
		FirstKeptEntryID: event.FirstKeptEntryID,
		TokensBefore:     event.TokensBefore,
		Details:          v2Details(projection, event.FirstKeptEntryID),
	}
}

// v2Details are a memory v2 compaction's om.folded details: the full projection through
// PiG's cut, which is also the fold boundary.
func v2Details(projection ledger.Projection, firstKeptEntryID string) ledger.MemoryDetails {
	observations, reflections := projection.Observations, projection.Reflections
	if observations == nil {
		observations = []ledger.Observation{}
	}
	if reflections == nil {
		reflections = []ledger.Reflection{}
	}
	return ledger.MemoryDetails{
		Type:               ledger.OMFolded,
		Version:            1,
		FullFold:           true,
		Observations:       observations,
		Reflections:        reflections,
		FoldThroughEntryID: firstKeptEntryID,
		Aliases:            projection.Aliases,
		Engine:             config.EngineV2,
	}
}

// RecoveredCompactionV2 is memory v2's answer after a panic anywhere in the
// session_before_compact handler: the minimal brief of the cut range in its pointer form,
// bounded by what the range carries, or V2FallbackSummary when building that panics too. It
// never panics, so PiG never falls through to its summarizer. rt may be nil, and event may
// carry only the preparation's firstKeptEntryId and tokensBefore.
func RecoveredCompactionV2(rt *runtime.Runtime, event BeforeCompactEvent) BeforeCompactOutcome {
	compaction := v2RecoveredCompaction(rt, event)
	return BeforeCompactOutcome{Compaction: &compaction}
}

// v2RecoveredCompaction is the compaction after a panic: the minimal brief of the cut range
// with no budget (the pointer form, bounded by the range), or V2FallbackSummary when that
// panics too.
func v2RecoveredCompaction(rt *runtime.Runtime, event BeforeCompactEvent) (compaction CompactionResult) {
	compaction = CompactionResult{
		Summary:          V2FallbackSummary,
		FirstKeptEntryID: event.FirstKeptEntryID,
		TokensBefore:     event.TokensBefore,
		Details:          v2Details(ledger.Projection{}, event.FirstKeptEntryID),
	}
	defer func() { _ = recover() }()
	var skip []string
	if rt != nil {
		skip = harness.MemoryToolNames(rt)
	}
	cut := v2SourceEntries(v2CutRange(event.BranchEntries, event))
	removed := v2SourceTokens(cut) + tokens.EstimateStringTokens(v2PreviousSummary(event.BranchEntries, event))
	if summary, _ := serialize.MinimalBrief(cut, skip, 0, removed); summary != "" {
		compaction.Summary = summary
	}
	return compaction
}

// v2CutRange is what the compaction removes from the branch: the entries from the previous
// compaction's boundary up to PiG's firstKeptEntryId. When that id is not on the branch PiG
// handed over, the range is read from the preparation's messages instead.
func v2CutRange(entries []ledger.Entry, event BeforeCompactEvent) []ledger.Entry {
	start := ledger.CompactionRangeStartIndex(entries)
	if cut := ledger.EntryIndexForID(entries, event.FirstKeptEntryID); cut >= start {
		return entries[start:cut]
	}
	messages := append(append([]map[string]any{}, event.Preparation.MessagesToSummarize...), event.Preparation.TurnPrefixMessages...)
	out := make([]ledger.Entry, 0, len(messages))
	for _, message := range messages {
		out = append(out, session.Entry{Type: "message", Message: message})
	}
	return out
}

// v2PreviousSummary is the summary the compaction replaces: the preparation's, else the
// latest compaction's on the branch.
func v2PreviousSummary(entries []ledger.Entry, event BeforeCompactEvent) string {
	if event.Preparation.PreviousSummary != "" {
		return event.Preparation.PreviousSummary
	}
	if index := ledger.FindLastCompactionIndex(entries); index >= 0 {
		summary, _ := entries[index].Summary.(string)
		return summary
	}
	return ""
}

// v2SourceEntries are the source entries of entries (ledger.IsSourceEntry): the
// conversation a compaction removes, without the om.* records, coverage markers, the previous
// compaction entry or model and setting changes.
func v2SourceEntries(entries []ledger.Entry) []ledger.Entry {
	out := make([]ledger.Entry, 0, len(entries))
	for _, entry := range entries {
		if ledger.IsSourceEntry(entry) {
			out = append(out, entry)
		}
	}
	return out
}

// v2SourceTokens is the estimated context the source entries of entries carry.
func v2SourceTokens(entries []ledger.Entry) int {
	total := 0
	for _, entry := range entries {
		if ledger.IsSourceEntry(entry) {
			total += tokens.EstimateEntryTokens(entry)
		}
	}
	return total
}
