// Package observationalmemory is the PiG (pi-in-go) extension factory for
// pig-observational-memory: cache-friendly tiered compaction with observations
// and reflections.
package observationalmemory

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/commands"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/hooks"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/sdkhost"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
)

// Name is the extension identity PiG expects: the factory directory's name.
const Name = "observational-memory"

// Extension is the PiG factory. PiG discovers it by its exact signature.
func Extension() *sdk.Extension {
	ext := sdk.New(Name)
	rt := runtime.New()
	Register(ext, rt)
	return ext
}

// Register wires the extension's hooks, commands and memory tool onto ext.
func Register(ext *sdk.Extension, rt *runtime.Runtime) {
	ext.OnEvent(sdk.EventAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		hooks.OnAgentStart(rt, sdkhost.New(ctx))
		return nil, nil
	})
	ext.OnEvent(sdk.EventTurnEnd, func(ctx sdk.Context, _ map[string]any) (any, error) {
		hooks.OnTurnEnd(rt, sdkhost.New(ctx))
		return nil, nil
	})
	ext.OnEvent(sdk.EventAgentSettled, func(ctx sdk.Context, _ map[string]any) (any, error) {
		// Pi emits agent_settled only after retries, automatic compaction and
		// queued continuation have finished, so retry policy stays owned by Pi.
		// In idle mode the compaction check waits for the memory workers.
		hooks.OnAgentSettledEvent(rt, sdkhost.New(ctx))
		return nil, nil
	})
	// The session is being replaced (and under PiG this process with it):
	// abandon in-flight memory work instead of failing against a stale context.
	// A tree jump changes the branch under a running worker, whose coverage
	// claims would then describe entries that are not on the branch.
	for _, event := range []string{sdk.EventSessionBeforeSwitch, sdk.EventSessionBeforeFork, sdk.EventSessionBeforeTree, sdk.EventSessionShutdown} {
		reason := event
		ext.OnEvent(reason, func(sdk.Context, map[string]any) (any, error) {
			hooks.OnSessionEnd(rt, reason)
			return nil, nil
		})
	}
	// The configuration, and with it the project's trust, is read again for
	// each session: PiG keeps this process across sessions, so settings edited
	// since, or a session resumed in another project (whose trust PiG resolves
	// separately), are picked up. A /trust decision is not: PiG caches trust
	// per working directory for the life of the process and applies a saved
	// decision only after a restart.
	ext.OnEvent(sdk.EventSessionStart, func(sdk.Context, map[string]any) (any, error) {
		rt.ReloadConfig()
		return nil, nil
	})
	// project_trust asks for a trust decision before the session runs. The
	// extension never makes one (it answers undecided); it only drops the
	// configuration loaded under the previous answer, so the next read asks
	// the host again.
	ext.OnProjectTrust(func(sdk.Context, map[string]any) (sdk.ProjectTrustResult, error) {
		rt.ReloadConfig()
		return sdk.ProjectTrustResult{Trusted: sdk.ProjectTrustUndecided}, nil
	})
	ext.OnEvent(sdk.EventSessionBeforeCompact, func(ctx sdk.Context, data map[string]any) (any, error) {
		return handleBeforeCompact(ctx, rt, data)
	})

	ext.RegisterCommand("om:status", sdk.CommandOptions{
		Description: "Show observational memory status",
		Handler: func(ctx sdk.Context, _ string) error {
			return commands.RunStatus(rt, sdkhost.New(ctx))
		},
	})
	ext.RegisterCommand("om:view", sdk.CommandOptions{
		Description: "Print and copy observational memory content (visible by default, full for recorded memory)",
		Handler: func(ctx sdk.Context, args string) error {
			return commands.RunView(rt, sdkhost.New(ctx), args, nil)
		},
	})

	registerMemory(ext, rt)
}

// handleBeforeCompact answers session_before_compact. v1 stops its catch-up on
// the agent run's signal; memory v2 reads only the request's own cancellation,
// which a manual /compact's run abort leaves live.
//
// Under memory v2 a panic anywhere in the handler (decoding, the host, the
// configuration, the hook, the result) is recovered into
// hooks.RecoveredCompactionV2, and a result that would not encode is replaced
// by it too: an error or a nil result would send PiG to its own summarizer,
// whose requests v2 replaces (docs/memory-v2.md, "Owning every compaction PiG
// prepares"). The engine is the configuration the runtime holds when the panic
// is recovered: the one this compaction loaded, or the last one loaded when
// loading it panicked. Under v1 a panic propagates exactly as before.
func handleBeforeCompact(ctx sdk.Context, rt *runtime.Runtime, data map[string]any) (result any, err error) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		fallback, ok := recoveredBeforeCompact(rt, data)
		if !ok {
			panic(recovered)
		}
		result, err = fallback, nil
	}()
	event := decodeBeforeCompact(data)
	h := sdkhost.New(ctx)
	outcome := hooks.HandleBeforeCompactRequest(ctx.Signal(), h.RequestContext(), rt, h, event)
	answer := outcome.ToResult()
	if answer == nil {
		return nil, nil
	}
	if outcome.Compaction != nil && engineV2(rt) {
		if _, err := json.Marshal(answer); err != nil {
			if fallback, ok := recoveredBeforeCompact(rt, data); ok {
				return fallback, nil
			}
		}
	}
	return answer, nil
}

// engineV2 reports whether the runtime's configuration selects memory v2; a
// configuration that cannot be read counts as v2, since only v2 answers then.
func engineV2(rt *runtime.Runtime) (v2 bool) {
	defer func() {
		if recover() != nil {
			v2 = true
		}
	}()
	return rt.Config().V2()
}

// recoveredBeforeCompact is memory v2's answer after a panic in the
// session_before_compact handler, built from PiG's payload alone: false under
// v1, or when the payload names no firstKeptEntryId to keep.
func recoveredBeforeCompact(rt *runtime.Runtime, data map[string]any) (result map[string]any, ok bool) {
	if !engineV2(rt) {
		return nil, false
	}
	event := recoveredEvent(data)
	if event.FirstKeptEntryID == "" {
		return nil, false
	}
	result = hooks.RecoveredCompactionV2(rt, event).ToResult()
	if _, err := json.Marshal(result); err != nil {
		// The recovered compaction's details are empty lists and an engine name,
		// so this does not happen; the summary alone still keeps PiG's cut.
		result = map[string]any{"compaction": map[string]any{"summary": hooks.V2FallbackSummary, "firstKeptEntryId": event.FirstKeptEntryID, "tokensBefore": event.TokensBefore}}
	}
	return result, true
}

// recoveredEvent decodes as much of the payload as decodes without a panic: all
// of it, else the preparation's firstKeptEntryId and tokensBefore alone.
func recoveredEvent(data map[string]any) (event hooks.BeforeCompactEvent) {
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
	return decodeBeforeCompact(data)
}

// decodeBeforeCompact reads PiG's session_before_compact payload: the whole
// preparation (memory v2 reads all of it; v1 only firstKeptEntryId and
// tokensBefore), the branch, the reason and the custom instructions.
func decodeBeforeCompact(data map[string]any) hooks.BeforeCompactEvent {
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

// decodePreparation reads the rest of PiG's CompactionPreparation; a missing or
// malformed member stays zero.
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
