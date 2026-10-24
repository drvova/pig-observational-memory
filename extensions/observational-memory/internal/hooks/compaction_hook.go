package hooks

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/jsfmt"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

const defaultObservationsPoolMaxTokens = 20_000

func observationsPoolMaxTokens(cfg config.Config) float64 {
	if cfg.ObservationsPoolMaxTokens > 0 {
		return float64(cfg.ObservationsPoolMaxTokens)
	}
	return defaultObservationsPoolMaxTokens
}

// CompactionCut is where a compaction cuts.
type CompactionCut struct {
	// FirstKeptEntryID is the first entry Pi keeps in context.
	FirstKeptEntryID string
	// FoldThroughEntryID is the entry through which the memory ledger is folded into the rendered summary.
	FoldThroughEntryID string
}

// CutResolution is the hook's decision: a cut, or a delegation to Pi's native summarizer.
type CutResolution struct {
	// Kind is "cut" or "delegate".
	Kind string
	Cut  CompactionCut
	// Gap is the unobserved source before Pi's cut, when any.
	Gap *ledger.UnobservedSourceSpan
	// RetainedTokens is the estimated source kept when the cut moved; -1 when it did not.
	RetainedTokens int
	Reason         string
}

// CutOptions bound a cut decision.
type CutOptions struct {
	MaxRetainedTokens int
	Reason            string
}

func cutAt(firstKept, foldThrough string) CutResolution {
	return CutResolution{Kind: "cut", Cut: CompactionCut{FirstKeptEntryID: firstKept, FoldThroughEntryID: foldThrough}, RetainedTokens: -1}
}

func delegate(reason string, gap *ledger.UnobservedSourceSpan) CutResolution {
	return CutResolution{Kind: "delegate", Reason: reason, Gap: gap, RetainedTokens: -1}
}

// ResolveCompactionCut decides where this compaction cuts. Pi proposes
// firstKeptEntryID from its own keepRecentTokens budget; the observer may not
// have reached it yet. When unobserved source sits before the cut, retain it by
// moving the boundary back to the nearest valid cut point at or before the
// first unobserved entry and fold every recorded observation into the summary.
// If retaining is impossible or exceeds the budget, delegate to Pi's native
// summarizer so the pre-cut context is summarized instead of dropped.
func ResolveCompactionCut(entries []ledger.Entry, firstKeptEntryID string, options CutOptions) CutResolution {
	cutIndex := ledger.EntryIndexForID(entries, firstKeptEntryID)
	if cutIndex == -1 {
		return cutAt(firstKeptEntryID, firstKeptEntryID)
	}
	// Entries the observer could never read (no renderable content, such as
	// an aborted assistant turn) need no coverage; counting them would force a
	// moved cut onto them with a "~0 tokens" notice.
	gap := ledger.UnobservedSourceSpanBeforeWhere(entries, cutIndex, serialize.ObserverRenders)
	if gap == nil {
		// Coverage reaches Pi's cut, so fold through the cut. A marker that
		// straddles it also read source this compaction removes; folding
		// through the cut would leave its observations of that source out of
		// the summary, so fold through that marker instead. Later markers
		// cover only retained source and wait for the next compaction rather
		// than take summary budget from the source being removed.
		foldThrough := firstKeptEntryID
		if straddling := ledger.StraddlingCoverageMarkerID(entries, cutIndex, ledger.OMObservationsRecorded, serialize.ObserverRenders); straddling != "" {
			foldThrough = straddling
		}
		// Never fold through less than the latest full fold did: later
		// compactions apply drops and reflections through the fold boundary.
		if fullFold := ledger.EntryIndexForID(entries, ledger.LatestFullFoldBoundaryID(entries)); fullFold > ledger.EntryIndexForID(entries, foldThrough) {
			foldThrough = entries[fullFold].ID
		}
		return cutAt(firstKeptEntryID, foldThrough)
	}
	// Overflow recovery must free context now; retaining more than Pi asked
	// for risks overflowing again on the retried turn.
	if options.Reason == "overflow" {
		return delegate("overflow recovery", gap)
	}
	coverageMarkerID := ledger.LatestCoverageMarkerID(entries, ledger.OMObservationsRecorded)
	if coverageMarkerID == "" {
		return delegate("no observation coverage", gap)
	}
	// Only source the observer has covered inside the current compaction range
	// can be folded. Compare source tokens, not indices: Pi's retention
	// boundary may sit on a metadata entry that carries no context tokens.
	rangeStart := ledger.CompactionRangeStartIndex(entries)
	coverageIndex := ledger.LatestCoverageIndex(entries, ledger.OMObservationsRecorded)
	if coverageIndex < rangeStart {
		return delegate("nothing observed can be compacted", gap)
	}
	safeCutIndex := findCutPointAtOrBefore(entries, gap.FirstIndex, rangeStart)
	if safeCutIndex <= rangeStart {
		return delegate("nothing observed can be compacted", gap)
	}
	freed := ledger.RawTokensAfterIndex(entries, rangeStart-1) - ledger.RawTokensAfterIndex(entries, safeCutIndex-1)
	if freed <= 0 {
		return delegate("nothing observed can be compacted", gap)
	}
	retained := ledger.RawTokensAfterIndex(entries, safeCutIndex-1)
	if retained > options.MaxRetainedTokens {
		return delegate(fmt.Sprintf("retaining ~%s tokens exceeds the ~%s-token budget", jsfmt.Locale(retained), jsfmt.Locale(options.MaxRetainedTokens)), gap)
	}
	resolution := cutAt(entries[safeCutIndex].ID, coverageMarkerID)
	resolution.Gap = gap
	resolution.RetainedTokens = retained
	return resolution
}

func gapLabel(gap ledger.UnobservedSourceSpan) string {
	noun := "entries"
	if gap.EntryCount == 1 {
		noun = "entry"
	}
	return fmt.Sprintf("%d source %s (~%s tokens)", gap.EntryCount, noun, jsfmt.Locale(gap.Tokens))
}

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
}

// CompactionResult is the extension-provided compaction.
type CompactionResult struct {
	Summary          string
	FirstKeptEntryID string
	TokensBefore     int
	Details          ledger.MemoryDetails
}

// ToMap is the result in Pi's CompactionResult wire shape.
func (r CompactionResult) ToMap() map[string]any {
	return map[string]any{
		"summary":          r.Summary,
		"firstKeptEntryId": r.FirstKeptEntryID,
		"tokensBefore":     r.TokensBefore,
		"details":          r.Details,
	}
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

// HandleBeforeCompact is the session_before_compact handler. ctx carries the
// event's cancellation.
func HandleBeforeCompact(ctx context.Context, rt *runtime.Runtime, h host.Host, event BeforeCompactEvent) BeforeCompactOutcome {
	if !rt.TryEnterCompactHook() {
		notifyUI(h, "Observational memory: another compaction is already in progress; cancelling duplicate", "warning")
		return BeforeCompactOutcome{Cancel: true}
	}
	defer rt.LeaveCompactHook()
	rt.EnsureConfig(h.Cwd())
	return handleCompaction(debuglog.With(ctx, debugScope(rt, h)), rt, h, event)
}

// branchContextTokens estimates what the branch puts into Pi's context: the
// source in the current compaction range plus the latest compaction's summary.
func branchContextTokens(entries []ledger.Entry) int {
	total := ledger.RawTokensSinceLastCompaction(entries)
	if index := ledger.FindLastCompactionIndex(entries); index >= 0 {
		if summary, ok := entries[index].Summary.(string); ok {
			total += tokens.EstimateStringTokens(summary)
		}
	}
	return total
}

// summaryBudget is the rendered summary budget for a cut: the room the next
// context has once the cut lands (config.ResolveSummaryBudget). The retained
// tail is the source from the first kept entry on; the overhead is Pi's
// tokensBefore minus the branch's own context estimate, 0 when either is
// unknown. A moved cut also keeps retained source plus summary within
// maxRetainedTokens.
func summaryBudget(cfg config.Config, contextWindow, tokensBefore int, entries []ledger.Entry, resolution CutResolution, maxRetainedTokens int) int {
	retained := 0
	if index := ledger.EntryIndexForID(entries, resolution.Cut.FirstKeptEntryID); index >= 0 {
		retained = ledger.RawTokensAfterIndex(entries, index-1)
	}
	overhead := 0
	if branch := branchContextTokens(entries); tokensBefore > 0 && branch > 0 {
		overhead = max(0, tokensBefore-branch)
	}
	budget := config.ResolveSummaryBudget(cfg, contextWindow, overhead, retained)
	if resolution.Gap != nil && resolution.RetainedTokens >= 0 {
		budget = max(1, min(budget, maxRetainedTokens-resolution.RetainedTokens))
	}
	return budget
}

func handleCompaction(ctx context.Context, rt *runtime.Runtime, h host.Host, event BeforeCompactEvent) BeforeCompactOutcome {
	cfg := rt.Config()
	entries := event.BranchEntries
	contextWindow := host.ModelContextWindow(h.SessionModel())
	maxRetainedTokens := config.ResolveCompactionMaxRetainedTokens(cfg, contextWindow)
	resolution := ResolveCompactionCut(entries, event.FirstKeptEntryID, CutOptions{MaxRetainedTokens: maxRetainedTokens, Reason: event.Reason})

	// The background observer is behind Pi's cut. Rather than retaining the
	// tail or handing the whole range to Pi's native summarizer, observe the
	// gap now: Pi waits for this hook, so the memory model has the server to
	// itself, and observations are durable where a native summary is not.
	if resolution.Gap != nil && cfg.CompactionCatchUpMaxChunks > 0 && !rt.ConsolidationInFlight() && ledger.LatestCoverageMarkerID(entries, ledger.OMObservationsRecorded) != "" {
		catchUp := CatchUpObserver(ctx, CatchUpArgs{Runtime: rt, Host: h, Entries: entries, Gap: *resolution.Gap, MaxChunks: cfg.CompactionCatchUpMaxChunks})
		data := map[string]any{"chunksRecorded": catchUp.ChunksRecorded, "gapEntries": resolution.Gap.EntryCount, "gapTokens": resolution.Gap.Tokens}
		if catchUp.StoppedBecause != "" {
			data["stoppedBecause"] = catchUp.StoppedBecause
		}
		debuglog.Log(ctx, "compaction.catch_up", data)
		if catchUp.ChunksRecorded > 0 {
			if fresh, err := h.GetBranch(); err == nil && fresh != nil {
				entries = fresh
			}
			resolution = ResolveCompactionCut(entries, event.FirstKeptEntryID, CutOptions{MaxRetainedTokens: maxRetainedTokens, Reason: event.Reason})
		}
	}

	var projection *ledger.CompactionProjection
	summary := ""
	if resolution.Kind == "cut" {
		built := ledger.BuildCompactionProjection(entries, resolution.Cut.FoldThroughEntryID, ledger.CompactionProjectionConfig{ObservationsPoolMaxTokens: observationsPoolMaxTokens(cfg)})
		projection = &built
		// The session model reads the summary: its harness profile picks the codec and
		// layout, and may give the summary less than the room (never more), and its layer
		// may select what the summary shows (its summary query; the seed is the native
		// tables layout), never what the kernel discloses as omitted.
		// The layer is loaded once, so the profile's codec and the summary come from one
		// generation of it.
		// The compaction is the summary's pass: it proves a held summary definition the
		// session model runs with (settlePass) once the summary becomes the compaction, and
		// reverts it when the kernel had to fall back to the seed layout or the summary made
		// the hook delegate to Pi. It settles only once the outcome is decided, below.
		passCtx := withPass(ctx, passCompaction)
		active := layerFor(passCtx, h, cfg, h.SessionModel())
		profile := harness.Default(harness.ModelKey(h.SessionModel()))
		if l, ok := harness.For(h, cfg, h.SessionModel()); ok {
			profile = l.ProfileWith(active)
		}
		room := summaryBudget(cfg, contextWindow, event.TokensBefore, entries, resolution, maxRetainedTokens)
		summaryMaxTokens := profile.ApplySummary(room)
		if summaryMaxTokens < room {
			debuglog.Log(ctx, "compaction.summary_profile", map[string]any{"model": profile.Model, "roomTokens": room, "summaryMaxTokens": summaryMaxTokens, "summaryHalvings": profile.SummaryHalvings})
		}
		// Instructions attached to this compaction ride along after the
		// memory, so the memory render gets the budget minus their share.
		instructionsBlock := ""
		memoryBudget := summaryMaxTokens
		if instructions := strings.TrimSpace(event.CustomInstructions); instructions != "" {
			instructionsBlock = "\n\n---\n\n" + instructions
			// The memory keeps at least half the budget: instructions are the
			// caller's explicit request and ride along in full, but they must
			// not crowd the memory out of its own summary.
			memoryBudget = max(summaryMaxTokens/2, summaryMaxTokens-tokens.EstimateStringTokens(instructionsBlock))
		}
		options := ledger.RenderOptions{ReflectionDates: ledger.ReflectionDateRanges(ledger.FoldLedger(entries, "")), Format: profile.SummaryFormat(cfg), Codec: profile.CodecValue(), Aliases: projection.Aliases}
		rendered, report := active.RenderSummaryReport(projection.Reflections, projection.Observations, memoryBudget, options)
		if report != "" {
			debuglog.Log(ctx, "compaction.summary_definition_fallback", map[string]any{"model": active.Model(), "report": report})
			notifyUI(h, "Observational memory: "+report, "warning")
			failPass(passCtx, nil, "an invariant refusal: "+report)
		}
		summary = rendered.Text
		// The summary decodes later (harness.VisibilityOf) in the codec that wrote it,
		// recorded with its declaration when the model declared it.
		if options.Format != ledger.FormatLines && options.Codec != nil {
			projection.Details.SummaryCodec = options.Codec.Name()
			if f, declared := tables.DeclaredFormat(options.Codec); declared {
				projection.Details.SummaryFormat = f.Declaration()
			}
		}
		// A summary that kept no record would replace older context with only
		// instructions and an omission notice. Decline instead, so Pi's native
		// summarizer preserves the facts.
		hadRecords := len(projection.Reflections)+len(projection.Observations) > 0
		summaryFailed := false
		if hadRecords && len(rendered.Reflections)+len(rendered.Observations) == 0 {
			debuglog.Log(ctx, "compaction.summary_budget_empty", map[string]any{"summaryMaxTokens": summaryMaxTokens, "memoryBudget": memoryBudget, "reflections": len(projection.Reflections), "observations": len(projection.Observations)})
			budgetText := fmt.Sprintf("the ~%s-token summary budget", jsfmt.Locale(summaryMaxTokens))
			if memoryBudget < summaryMaxTokens {
				budgetText = fmt.Sprintf("its ~%s-token share of the ~%s-token summary budget (the compaction instructions take the rest)", jsfmt.Locale(memoryBudget), jsfmt.Locale(summaryMaxTokens))
			}
			notifyUI(h, "Observational memory: no memory record fits "+budgetText+"; delegating to Pi's native summarizer", "warning")
			failPass(passCtx, nil, "the summary kept no record within "+budgetText+", so the compaction delegated to Pi")
			summaryFailed = true
			summary = ""
		}
		// Instructions another extension or the user attached to this compaction
		// ride along after the memory, so they reach the next context the way
		// they would in Pi's native summary. They count toward the budget below.
		if summary != "" && instructionsBlock != "" {
			summary += instructionsBlock
		}
		// A moved cut keeps the unobserved tail AND adds the rendered memory on
		// top of it. Both must fit the budget, or Pi's threshold fires again
		// right after this compaction and the next hook call can only delegate.
		if resolution.Gap != nil && resolution.RetainedTokens >= 0 {
			summaryTokens := tokens.EstimateStringTokens(summary)
			if resolution.RetainedTokens+summaryTokens > maxRetainedTokens {
				resolution = delegate(fmt.Sprintf("retaining ~%s tokens plus a ~%s-token memory summary exceeds the ~%s-token budget", jsfmt.Locale(resolution.RetainedTokens), jsfmt.Locale(summaryTokens), jsfmt.Locale(maxRetainedTokens)), resolution.Gap)
				failPass(passCtx, nil, "the summary made the compaction delegate to Pi: "+resolution.Reason)
				summaryFailed = true
			}
		}
		// Only a summary that becomes the compaction is proven: a pass with nothing to
		// summarize proves nothing either way, and its held summary waits for the next.
		if summary == "" && !summaryFailed {
			incompletePass(passCtx)
		} else if summary != "" && !summaryFailed {
			recordedPass(passCtx)
		}
		settlePass(passCtx, cfg, h, nil)
	}

	if resolution.Gap != nil {
		data := map[string]any{
			"proposedFirstKeptEntryId": event.FirstKeptEntryID,
			"unobservedEntries":        resolution.Gap.EntryCount,
			"unobservedTokens":         resolution.Gap.Tokens,
			"outcome":                  resolution.Kind,
		}
		if resolution.Kind == "cut" {
			data["firstKeptEntryId"] = resolution.Cut.FirstKeptEntryID
			data["foldThroughEntryId"] = resolution.Cut.FoldThroughEntryID
			if resolution.RetainedTokens >= 0 {
				data["retainedTokens"] = resolution.RetainedTokens
			}
			data["summaryTokens"] = tokens.EstimateStringTokens(summary)
			data["maxRetainedTokens"] = maxRetainedTokens
		} else {
			data["reason"] = resolution.Reason
		}
		debuglog.Log(ctx, "compaction.observer_behind", data)
	}

	if resolution.Kind == "delegate" {
		gap := ledger.UnobservedSourceSpan{}
		if resolution.Gap != nil {
			gap = *resolution.Gap
		}
		notifyUI(h, fmt.Sprintf("Observational memory: observer has not reached %s before the compaction cut and they cannot be retained (%s); delegating to Pi's native summarizer", gapLabel(gap), resolution.Reason), "warning")
		return BeforeCompactOutcome{}
	}
	if projection == nil || summary == "" {
		return BeforeCompactOutcome{}
	}
	if resolution.Gap != nil {
		notifyUI(h, fmt.Sprintf("Observational memory: observer has not reached %s before the compaction cut; keeping them in context until they are observed", gapLabel(*resolution.Gap)), "info")
	}
	return BeforeCompactOutcome{Compaction: &CompactionResult{
		Summary:          summary,
		FirstKeptEntryID: resolution.Cut.FirstKeptEntryID,
		TokensBefore:     event.TokensBefore,
		Details:          projection.Details,
	}}
}
