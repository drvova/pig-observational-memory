package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/consolidate"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// The reasons level 1 makes no request at a compaction (ledger.ConsolidationReport.Reason).
const (
	level1Off          = "consolidate is false"
	level1Passive      = "passive mode"
	level1OwnTrigger   = "the extension's own compaction (PiG's auto-compaction is off) runs level 0 only"
	level1Overflow     = "an overflow compaction runs level 0 only, so recovery is not delayed"
	level1NoCut        = "the cut range is not on the branch"
	level1Cancelled    = "the compaction was cancelled before the request"
	level1NoWindow     = "the session model's context window is unknown"
	level1NoOutputCap  = "no output cap: the model's output limit is unknown and the summary budget is 0"
	level1NoCandidates = "no level-0 records in the cut range"
	level1NoRoom       = "the input does not fit: est(prompt + input) + output cap + PiG's 4096 safety margin exceeds the window"
)

// level1Result is what level 1 left for the compaction to fold: the entries it appended (as
// branch entries), the request's usage, and its report.
type level1Result struct {
	entries []ledger.Entry
	usage   map[string]any
	report  ledger.ConsolidationReport
}

// consolidateV2 is memory v2's level 1 (docs/memory-v2.md, "Level 1: one request at
// compaction"): at most one request, on the session model, with the level-0 records of the cut
// range as candidates, made only in place of PiG's summarizer and only while the request
// context is live. It makes no request when consolidate is false, in passive mode, for the
// extension's own compaction with PiG's auto-compaction off, on overflow, or when the input and
// the output cap do not fit the window with PiG's safety margin. Any failure (no tool call, a
// length or stream error, an abort, output with nothing valid) leaves the level-0 records and
// the reflections as they are; nothing is retried. entries is the branch with level 0's
// extraction folded in, cut the cut range's source entries, budget the summary budget the
// memory is rendered under. spent, when not nil, receives out as it stands when consolidateV2
// returns or panics, so a panic after the request still reports the request and its usage.
func consolidateV2(ctx, request context.Context, rt *runtime.Runtime, h host.Host, event BeforeCompactEvent, entries, cut []ledger.Entry, budget int, spent *level1Result) (out level1Result) {
	if spent != nil {
		defer func() { *spent = out }()
	}
	cfg := rt.Config()
	skip := func(reason string) level1Result {
		out.report.Reason = reason
		return out
	}
	switch {
	case !cfg.Consolidates():
		return skip(level1Off)
	case cfg.Passive:
		return skip(level1Passive)
	case rt.CompactInFlight():
		// Under v2 only maybeTriggerCompactionV2 claims the slot, and only with PiG's
		// auto-compaction off: this compaction is the extension's own.
		return skip(level1OwnTrigger)
	case event.Reason == "overflow":
		return skip(level1Overflow)
	case len(cut) == 0 || ledger.EntryIndexForID(entries, event.FirstKeptEntryID) < 0:
		return skip(level1NoCut)
	case request.Err() != nil:
		return skip(level1Cancelled)
	}

	resolved := rt.ResolveSessionModel(ctx, runtime.ResolveCtx{Model: h.SessionModel(), Models: h.Models(), HasUI: h.HasUI(), Notify: h.Notify})
	if !resolved.OK {
		return skip("the session model is unavailable: " + resolved.Reason)
	}
	resolved = workerHeadersFor(h, resolved)
	window := host.ModelContextWindow(resolved.Model)
	if window <= 0 {
		return skip(level1NoWindow)
	}
	outputCap := host.ModelMaxTokens(resolved.Model)
	if outputCap <= 0 {
		outputCap = budget
	}
	if outputCap <= 0 {
		return skip(level1NoOutputCap)
	}

	projection := ledger.FullProjection(entries, event.FirstKeptEntryID)
	cutIDs := make(map[string]bool, len(cut))
	for _, entry := range cut {
		cutIDs[entry.ID] = true
	}
	candidates, active := []ledger.Observation{}, []ledger.Observation{}
	for _, o := range projection.Observations {
		if o.Origin == ledger.OriginCode && citesAny(o.SourceEntryIDs, cutIDs) {
			candidates = append(candidates, o)
		} else {
			active = append(active, o)
		}
	}
	if len(candidates) == 0 {
		return skip(level1NoCandidates)
	}

	tool := consolidate.Tool()
	system := consolidate.SystemPrompt()
	fixedTokens := requestOverheadTokens(system, tool)
	prompt, ok := consolidate.BuildPrompt(consolidate.PromptInput{
		Cut:          cut,
		SkipTools:    harness.MemoryToolNames(rt),
		Candidates:   candidates,
		Active:       active,
		Reflections:  ledger.ActiveReflections(projection.Reflections),
		Instructions: event.CustomInstructions,
		Available:    window - config.PiGContextSafetyTokens - outputCap - fixedTokens,
		MemoryBudget: max(0, budget),
	})
	estimated := fixedTokens + prompt.Tokens
	if !ok || estimated+outputCap+config.PiGContextSafetyTokens > window {
		return skip(level1NoRoom)
	}
	if request.Err() != nil {
		return skip(level1Cancelled)
	}

	answer, err := agents.RunSingleRequest(ctx, h.Models().Stream, agents.SingleRequestConfig{
		Stage:     consolidate.Stage,
		Engine:    cfg.Engine(),
		Model:     resolved.Model,
		APIKey:    resolved.APIKey,
		Headers:   resolved.Headers,
		Env:       resolved.Env,
		MaxTokens: outputCap,
	}, system, prompt.Text, tool)
	// Counted only once the stream started: a cancel landing before the call starts none.
	out.report.Requested = answer.Made
	out.usage = answer.Usage
	logData := map[string]any{
		"candidates":         len(prompt.Candidates),
		"omittedCandidates":  prompt.OmittedCandidates,
		"active":             len(prompt.Active),
		"omittedActive":      prompt.OmittedActive,
		"reflections":        len(prompt.Reflections),
		"omittedReflections": prompt.OmittedReflections,
		"cutShown":           prompt.CutShown,
		"cutEntries":         prompt.CutTotal,
		"inputTokens":        estimated,
		"outputCap":          outputCap,
		"window":             window,
	}
	fail := func(reason string) level1Result {
		out.report.Reason = reason
		logData["failure"] = reason
		debuglog.Log(ctx, "compaction.v2.level1", logData)
		return out
	}
	switch {
	case errors.Is(err, agents.ErrAborted) && answer.Made:
		return fail("aborted: the compaction was cancelled during the request")
	case errors.Is(err, agents.ErrAborted):
		return fail(level1Cancelled)
	case err != nil:
		return fail(err.Error())
	case answer.StreamError != nil:
		return fail(answer.StreamError.Error())
	case answer.Message != nil && answer.Message["stopReason"] == "length":
		return fail("the response reached the output cap (stopReason length)")
	}

	in := consolidate.Input{
		Candidates:  prompt.Candidates,
		Active:      prompt.Active,
		Reflections: prompt.Reflections,
		Ledger:      ledger.FoldLedger(entries, ""),
		Subjects:    ledger.SubjectsOf(projection.Observations),
	}
	result := consolidate.Validate(answer.Arguments, in)
	if len(result.Refusals) > 0 {
		logData["refusals"] = consolidate.RefusalSummary(result.Refusals)
	}
	out.report.Refused = len(result.Refusals)
	if result.Failure != "" {
		return fail(result.Failure)
	}
	drop, reflections, keptBack := consolidate.Union(result, projection.Observations)
	result.Reflections = reflections
	out.entries = appendLevel1(ctx, h, result, drop, cut[len(cut)-1].ID)
	out.report.Applied = true
	out.report.Records = len(result.Records) - result.Merges
	out.report.Merges = result.Merges
	out.report.Reflections = len(result.Reflections)
	out.report.Aliases = len(result.Aliases)
	out.report.Dropped = len(drop)
	logData["records"], logData["merges"], logData["newReflections"] = out.report.Records, out.report.Merges, out.report.Reflections
	logData["aliases"], logData["dropped"], logData["unchanged"], logData["keptByUnion"] = out.report.Aliases, len(drop), result.Unchanged, keptBack
	debuglog.Log(ctx, "compaction.v2.level1", logData)
	return out
}

// requestOverheadTokens is the estimated part of the level-1 request that is not its user text:
// the system prompt with the single-tool instruction RunSingleRequest appends, and the tool's
// schema.
func requestOverheadTokens(system string, tool agents.Tool) int {
	schema, _ := json.Marshal(map[string]any{"name": tool.Name, "description": tool.Description, "parameters": tool.Parameters})
	full := strings.TrimRight(system, "\n") + "\n\n" + strings.ReplaceAll(agents.SingleToolInstruction, "%s", tool.Name) + "\n"
	return tokens.EstimateStringTokens(full) + tokens.EstimateStringTokens(string(schema))
}

// citesAny reports whether any of sourceIDs is in ids.
func citesAny(sourceIDs []string, ids map[string]bool) bool {
	for _, id := range sourceIDs {
		if ids[id] {
			return true
		}
	}
	return false
}

// appendLevel1 appends level 1's records, reflections, aliases and the drops of what its records
// replace, each covering through coversUpToID (the last source entry PiG removes, which level 0
// already read), and returns them as branch entries, so the compaction folds them through PiG's
// cut whether or not an append failed.
func appendLevel1(ctx context.Context, h host.Host, result consolidate.Result, drop []string, coversUpToID string) []ledger.Entry {
	type pending struct {
		customType string
		data       any
	}
	queue := []pending{}
	if data := ledger.BuildObservationsRecordedData(result.Records, coversUpToID); data != nil {
		queue = append(queue, pending{ledger.OMObservationsRecorded, data})
	}
	if data := ledger.BuildReflectionsRecordedData(result.Reflections, coversUpToID); data != nil {
		queue = append(queue, pending{ledger.OMReflectionsRecorded, data})
	}
	if data := ledger.BuildSubjectsAliasedData(result.Aliases, coversUpToID); data != nil {
		queue = append(queue, pending{ledger.OMSubjectsAliased, data})
	}
	if data := ledger.BuildObservationsDroppedData(drop, coversUpToID); data != nil {
		queue = append(queue, pending{ledger.OMObservationsDropped, data})
	}
	out := make([]ledger.Entry, 0, len(queue))
	for n, p := range queue {
		if err := h.AppendEntry(p.customType, p.data); err != nil {
			debuglog.Log(ctx, "compaction.v2.level1.append_failed", map[string]any{"customType": p.customType, "errorMessage": err.Error()})
		}
		out = append(out, session.Entry{Type: "custom", ID: fmt.Sprintf("om-v2-level1-%d", n), CustomType: p.customType, Data: jsonValue(p.data)})
	}
	return out
}
