package engine

import (
	"context"

	"github.com/drvova/pig-observational-memory/memory/hooks"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/runtime"
	"github.com/drvova/pig-observational-memory/memory/session"
)

// BeforeCompact answers session_before_compact from its payload, as the PiG adapter does: it
// reads only request's cancellation, never the agent run's signal. encodes reports whether
// a result encodes in the harness's JSON.
//
// A panic anywhere in the handler (decoding, the host, the configuration, the hook) is
// recovered into hooks.RecoveredCompactionV2, and a result that would not encode is replaced
// by it too: an error or a nil result would send the harness to its own summarizer
// (docs/memory-v2.md, "Owning every compaction PiG prepares"). A panic is re-raised only when
// the payload names no firstKeptEntryId to keep.
func BeforeCompact(request context.Context, rt *runtime.Runtime, h host.Host, data map[string]any, encodes func(any) error) (result map[string]any) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		fallback, ok := RecoveredBeforeCompact(rt, data, encodes)
		if !ok {
			panic(recovered)
		}
		result = fallback
	}()
	event := DecodeBeforeCompact(data)
	outcome := hooks.HandleBeforeCompactRequest(request, rt, h, event)
	answer := outcome.ToResult()
	if answer == nil {
		return nil
	}
	if outcome.Compaction != nil {
		if err := encodes(answer); err != nil {
			if fallback, ok := RecoveredBeforeCompact(rt, data, encodes); ok {
				return fallback
			}
		}
	}
	return answer
}

// RecoveredBeforeCompact is the answer after a panic in the session_before_compact handler,
// built from the payload alone: false when the payload names no firstKeptEntryId to keep.
func RecoveredBeforeCompact(rt *runtime.Runtime, data map[string]any, encodes func(any) error) (result map[string]any, ok bool) {
	event := RecoveredEvent(data)
	if event.FirstKeptEntryID == "" {
		return nil, false
	}
	result = hooks.RecoveredCompactionV2(rt, event).ToResult()
	if err := encodes(result); err != nil {
		// The recovered compaction's details are empty lists and an engine name,
		// so this does not happen; the summary alone still keeps the harness's cut.
		result = map[string]any{"compaction": map[string]any{"summary": hooks.V2FallbackSummary, "firstKeptEntryId": event.FirstKeptEntryID, "tokensBefore": event.TokensBefore}}
	}
	return result, true
}

// RecoveredEvent decodes as much of the payload as decodes without a panic: all of it, else
// the preparation's firstKeptEntryId and tokensBefore alone.
func RecoveredEvent(data map[string]any) (event hooks.BeforeCompactEvent) {
	defer func() {
		if recover() == nil {
			return
		}
		event = hooks.BeforeCompactEvent{}
		func() {
			defer func() { _ = recover() }()
			preparation, _ := data["preparation"].(map[string]any)
			event.FirstKeptEntryID, _ = preparation["firstKeptEntryId"].(string)
			if tokensBefore, ok := preparation["tokensBefore"].(float64); ok {
				event.TokensBefore = int(tokensBefore)
			}
		}()
	}()
	return DecodeBeforeCompact(data)
}

// DecodeBeforeCompact reads PiG's session_before_compact payload: the whole preparation, the
// branch, the reason and the custom instructions.
func DecodeBeforeCompact(data map[string]any) hooks.BeforeCompactEvent {
	event := hooks.BeforeCompactEvent{}
	if preparation, ok := data["preparation"].(map[string]any); ok {
		event.FirstKeptEntryID, _ = preparation["firstKeptEntryId"].(string)
		if tokensBefore, ok := preparation["tokensBefore"].(float64); ok {
			event.TokensBefore = int(tokensBefore)
		}
		event.Preparation = decodePreparation(preparation)
	}
	event.BranchEntries = session.FromAny(data["branchEntries"])
	event.Reason, _ = data["reason"].(string)
	event.CustomInstructions, _ = data["customInstructions"].(string)
	return event
}

// decodePreparation reads the rest of PiG's CompactionPreparation; a missing or malformed
// member stays zero.
func decodePreparation(preparation map[string]any) hooks.CompactionPreparation {
	out := hooks.CompactionPreparation{
		MessagesToSummarize: messageList(preparation["messagesToSummarize"]),
		TurnPrefixMessages:  messageList(preparation["turnPrefixMessages"]),
	}
	out.IsSplitTurn, _ = preparation["isSplitTurn"].(bool)
	out.PreviousSummary, _ = preparation["previousSummary"].(string)
	if ops, ok := preparation["fileOps"].(map[string]any); ok {
		out.FileOps = hooks.CompactionFileOps{Read: stringList(ops["read"]), Written: stringList(ops["written"]), Edited: stringList(ops["edited"])}
	}
	if settings, ok := preparation["settings"].(map[string]any); ok {
		out.Settings.Enabled, _ = settings["enabled"].(bool)
		out.Settings.ReserveTokens = wholeNumber(settings["reserveTokens"])
		out.Settings.KeepRecentTokens = wholeNumber(settings["keepRecentTokens"])
	}
	return out
}

func messageList(v any) []map[string]any {
	list, _ := v.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, item := range list {
		if message, ok := item.(map[string]any); ok {
			out = append(out, message)
		}
	}
	return out
}

func stringList(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func wholeNumber(v any) int {
	if n, ok := v.(float64); ok && n > 0 {
		return int(n)
	}
	return 0
}
