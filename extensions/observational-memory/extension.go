// Package observationalmemory is the PiG (pi-in-go) extension factory for
// pig-observational-memory: cache-friendly tiered compaction with observations
// and reflections.
package observationalmemory

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

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
	ext.OnEvent(sdk.EventSessionBeforeCompact, func(ctx sdk.Context, data map[string]any) (any, error) {
		event := decodeBeforeCompact(data)
		outcome := hooks.HandleBeforeCompact(ctx.Signal(), rt, sdkhost.New(ctx), event)
		result := outcome.ToResult()
		if result == nil {
			return nil, nil
		}
		return result, nil
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

	registerMemory(ext)
}

func decodeBeforeCompact(data map[string]any) hooks.BeforeCompactEvent {
	event := hooks.BeforeCompactEvent{}
	if preparation, ok := data["preparation"].(map[string]any); ok {
		event.FirstKeptEntryID, _ = preparation["firstKeptEntryId"].(string)
		if tokensBefore, ok := preparation["tokensBefore"].(float64); ok {
			event.TokensBefore = int(tokensBefore)
		}
	}
	event.BranchEntries = session.FromAny(data["branchEntries"])
	event.Reason, _ = data["reason"].(string)
	event.CustomInstructions, _ = data["customInstructions"].(string)
	return event
}
