package hooks

import (
	"context"
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/extract"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/selection"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
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
const V2FallbackSummary = "omitted: the compacted conversation; the memory tool searches it (" + om.SnippetHistorySearch + ")."

// renderV2Records renders the ledger's records for a memory v2 summary; tests replace it.
var renderV2Records = selection.Render

// handleBeforeCompactV2 is memory v2's session_before_compact hook (docs/memory-v2.md, "Owning
// every compaction PiG prepares"). It returns a compaction for every preparation PiG hands it,
// never declining and never cancelling: overflow, an empty ledger and a budget too small for
// any record all get one. It keeps PiG's cut exactly (firstKeptEntryId, with the fold boundary
// equal to it), makes no model request at level 0, and recovers a panic into the minimal
// brief, carrying the level-1 request's usage and report when one was made. Only a cancelled
// request context makes it return nothing.
func handleBeforeCompactV2(request context.Context, rt *runtime.Runtime, h host.Host, event BeforeCompactEvent) (outcome BeforeCompactOutcome) {
	if request == nil {
		request = context.Background()
	}
	if request.Err() != nil {
		return BeforeCompactOutcome{}
	}
	// A concurrent hook is not cancelled, and it appends its extraction and drops like any
	// other: extractCutV2 measures what is left to extract on a fresh branch under one lock, so
	// two compactions rendering at once never append the same records twice, and whichever one
	// PiG persists finds its range's records on the session. The slot only shows the hook as
	// running in /om:status.
	owner := rt.TryEnterCompactHook()
	if owner {
		rt.SetCompactHookRequest(request)
		defer rt.LeaveCompactHook()
	}
	ctx := request
	var level1 level1Result
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
		// A request made before the panic was paid for: PiG's compaction_end and /om:status
		// still show it.
		report := level1.report
		if !report.Applied && report.Reason == "" {
			report.Reason = fmt.Sprint("panic: ", recovered)
		}
		compaction.Details.Consolidation = &report
		compaction.Usage = level1.usage
		outcome = BeforeCompactOutcome{Compaction: &compaction}
	}()
	ctx = debuglog.With(request, debugScope(rt, h))
	compaction := buildCompactionV2(ctx, request, rt, h, event, owner, &level1)
	if request.Err() != nil {
		return BeforeCompactOutcome{}
	}
	return BeforeCompactOutcome{Compaction: &compaction}
}

// buildCompactionV2 is memory v2's compaction: it extracts whatever of the cut range no turn end
// read (extractCutV2), runs level 1 when it may (consolidateV2, at most one request, on the
// request context), renders the ledger's records under the summary budget, else the minimal
// brief of the cut range, and drops by rule what the rendered records supersede or the
// rendered reflections cite. The session model's harness shapes the render (its profile's
// summary size, layout and codec, the codec read from its layer) and, while the request is live,
// the compaction is recorded as its evidence unit (recordCompactionV2) by the hook holding the
// compaction slot (owner), or by a concurrent hook once PiG cancelled the owner's request
// (runtime.Runtime.CompactHookOwnerLive): the compaction PiG persists is counted when its slot
// owner was the one cancelled, and two live hooks never count one compaction twice. spent
// receives level 1's result as it stands, even on a panic.
func buildCompactionV2(ctx, request context.Context, rt *runtime.Runtime, h host.Host, event BeforeCompactEvent, owner bool, spent *level1Result) CompactionResult {
	cfg := rt.Config()
	cut := v2SourceEntries(v2CutRange(event.BranchEntries, event))
	// The records of the cut range, appended before rendering so the protection of unextracted
	// entries holds by construction (docs/memory-v2.md, "PiG's cut, exactly").
	entries := extractCutV2(ctx, rt, h, event)
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
	// The session model reads the summary: its profile (harness.ComputeWith over memory v2's own
	// events, one unit per compaction) may give it less than the budget, never more, and picks
	// its layout and codec, a written format read from its layer (docs/memory-interface.md,
	// section 6; level 1 defines no format). With no evidence and no definitions the profile is the default and the render is
	// unchanged.
	active := harness.LayerFor(h, cfg, model)
	profile := harness.Default(harness.ModelKey(model))
	if l, ok := harness.For(h, cfg, model); ok {
		profile = l.ProfileWith(active)
	}
	if scaled := profile.ApplySummary(budget); scaled < budget {
		debuglog.Log(ctx, "compaction.summary_profile", map[string]any{"model": profile.Model, "roomTokens": budget, "summaryMaxTokens": scaled, "summaryHalvings": profile.SummaryHalvings})
		budget = scaled
	}
	format := profile.SummaryFormat(cfg)
	var codec tables.Codec
	if format != ledger.FormatLines {
		codec = profile.CodecValue()
	}

	// PiG's customInstructions reach the level-1 input, or a level-0 summary as a block after the
	// memory when it fits (docs/memory-v2.md, "The rule", 2).
	instructionsBlock := ""
	if instructions := strings.TrimSpace(event.CustomInstructions); instructions != "" {
		instructionsBlock = "\n\n---\n\n" + instructions
	}
	memoryBudget := budget
	if instructionsBlock != "" && budget-tokens.EstimateStringTokens(instructionsBlock) > 0 {
		memoryBudget = budget - tokens.EstimateStringTokens(instructionsBlock)
	}

	// Level 1: at most one request, in place of PiG's summarizer; its records, reflections,
	// aliases and drops are appended before rendering, so the summary folds them. On any
	// failure it appends nothing and level 0 stands. Its current memory is bounded by the room
	// the render has, the instructions' block left out.
	// The layer the compaction ran with, read before level 1: the held definitions it settles
	// once its pass is recorded (settleDefinitionsV2), so one this level 1 holds is never
	// settled by the compaction that proposed it.
	snapshot := active.Harness()
	level1 := consolidateV2(ctx, request, rt, h, event, entries, cut, memoryBudget, active, spent)
	if len(level1.entries) > 0 {
		entries = append(append(make([]ledger.Entry, 0, len(entries)+len(level1.entries)), entries...), level1.entries...)
	}
	instructionsToLevel1 := level1.report.Applied && instructionsBlock != ""
	if level1.report.Applied {
		// The instructions shaped level 1's output; appended too, they would sit in every later
		// context as a stale directive, so the memory gets their room.
		instructionsBlock, memoryBudget = "", budget
	}

	// The fold boundary is PiG's cut: every record covered through it is folded, drops and
	// reflections included, with no normal-versus-full split. A reflection level 1 replaced
	// leaves the memory (ledger.ActiveReflections).
	projection := ledger.FullProjection(entries, event.FirstKeptEntryID)
	projection.Reflections = ledger.ActiveReflections(projection.Reflections)
	summary, kind := "", ""
	var rendered selection.Result
	if memoryBudget > 0 && len(projection.Reflections)+len(projection.Observations) > 0 {
		// Selection's ordered classes are the kernel's: a summary definition (a model's summary
		// query, which v1 lays out through declare) does not select under v2, since only
		// selection keeps every line anchored, every omission a runnable query and the drops
		// exactly what it shows. The model's format and codec do apply.
		folded := ledger.FoldLedger(entries, "")
		rendered = renderV2Records(selection.Input{
			Reflections:     projection.Reflections,
			Observations:    projection.Observations,
			Folded:          folded,
			Branch:          entries,
			Request:         v2SplitTurnRequest(cut, event),
			Budget:          memoryBudget,
			Format:          format,
			Codec:           codec,
			Aliases:         projection.Aliases,
			ReflectionDates: ledger.ReflectionDateRanges(folded),
		})
		if rendered.Text != "" && tokens.EstimateStringTokens(rendered.Text) <= memoryBudget {
			summary, kind = rendered.Text, v2SummaryRecords
			// Dropping by rule: a record superseded by a rendered record, or cited by a
			// rendered reflection, leaves the memory. Selection already let those go, so they
			// are neither shown nor in an omitted span.
			dropped := extract.Drops(projection.Observations, rendered.Observations, rendered.Reflections, folded.ObservationsByID)
			projection.Observations = withoutObservations(projection.Observations, dropped)
			if len(cut) > 0 {
				dropByRule(ctx, h, dropped, cut[len(cut)-1].ID)
			}
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
		"level":            level1.report.Level(),
		"level1Requested":  level1.report.Requested,
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
	if kind == v2SummaryRecords {
		data["shown"] = len(rendered.Reflections) + len(rendered.Observations)
		data["omitted"] = rendered.Omitted
		data["spans"] = rendered.Spans
		data["foldedSpans"] = rendered.Folded
		// Not "request": that key marks a model request event (debuglog.IsRequest).
		data["splitTurnRequest"] = rendered.Request
	}
	if instructionsBlock != "" {
		data["instructionsKept"] = instructionsKept
	}
	if instructionsToLevel1 {
		data["instructionsToLevel1"] = true
	}
	if level1.report.Reason != "" {
		data["level1Reason"] = level1.report.Reason
	}
	debuglog.Log(ctx, "compaction.v2", data)

	details := v2Details(projection, event.FirstKeptEntryID)
	report := level1.report
	details.Consolidation = &report
	if kind == v2SummaryRecords {
		details.Shown = shownIDs(rendered)
		// The summary decodes later in the codec that wrote it, with its declaration when the
		// model declared it; the native rows codec is the default and is not recorded.
		if codec != nil && (codec.Name() != tables.Rows.Name() || isDeclared(codec)) {
			details.SummaryCodec = codec.Name()
			if f, declared := tables.DeclaredFormat(codec); declared {
				details.SummaryFormat = f.Declaration()
			}
		}
	}
	if request.Err() == nil && (owner || !rt.CompactHookOwnerLive()) {
		recordCompactionV2(ctx, rt, h, entries, cut, level1.counts, snapshot)
	}
	return CompactionResult{
		Summary:          summary,
		FirstKeptEntryID: event.FirstKeptEntryID,
		TokensBefore:     event.TokensBefore,
		Details:          details,
		// The level-1 request's provider usage, failed or not, so PiG's compaction_end shows
		// what the compaction cost; nil at level 0, which made no request.
		Usage: level1.usage,
	}
}

// shownIDs are the ids of the records a rendered summary shows, reflections first.
func shownIDs(rendered selection.Result) []string {
	out := make([]string, 0, len(rendered.Reflections)+len(rendered.Observations))
	for _, r := range rendered.Reflections {
		out = append(out, r.ID)
	}
	for _, o := range rendered.Observations {
		out = append(out, o.ID)
	}
	return out
}

// isDeclared reports whether codec is a format a model declared (tables.Declared).
func isDeclared(codec tables.Codec) bool {
	_, declared := tables.DeclaredFormat(codec)
	return declared
}

// withoutObservations is observations without the records whose ids are in ids.
func withoutObservations(observations []ledger.Observation, ids []string) []ledger.Observation {
	remove := make(map[string]bool, len(ids))
	for _, id := range ids {
		remove[id] = true
	}
	out := make([]ledger.Observation, 0, len(observations))
	for _, o := range observations {
		if !remove[o.ID] {
			out = append(out, o)
		}
	}
	return out
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

// v2SplitTurnRequest is the split turn's opening request when PiG's cut falls inside a turn
// (docs/memory-v2.md, "The whole preparation"): the first user message of the prefix range,
// which is the cut range's last len(turnPrefixMessages) source entries; without prefix
// messages, the cut range's last user message. nil when the cut is between turns or no user
// text is found. cut is the range's source entries (v2SourceEntries).
func v2SplitTurnRequest(cut []ledger.Entry, event BeforeCompactEvent) *selection.Request {
	if !event.Preparation.IsSplitTurn || len(cut) == 0 {
		return nil
	}
	userText := func(entry ledger.Entry) (string, bool) {
		if entry.Type != "message" || entry.Message == nil || entry.Message["role"] != "user" {
			return "", false
		}
		_, text, ok := serialize.HistoryText(entry)
		return text, ok && strings.TrimSpace(text) != ""
	}
	if n := len(event.Preparation.TurnPrefixMessages); n > 0 && n <= len(cut) {
		for _, entry := range cut[len(cut)-n:] {
			if text, ok := userText(entry); ok {
				return &selection.Request{EntryID: entry.ID, Text: text}
			}
		}
	}
	for i := len(cut) - 1; i >= 0; i-- {
		if text, ok := userText(cut[i]); ok {
			return &selection.Request{EntryID: cut[i].ID, Text: text}
		}
	}
	return nil
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
