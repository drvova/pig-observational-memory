package observationalmemory

import (
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/sdkhost"
	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/engine"
	"github.com/drvova/pig-observational-memory/memory/harness"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/omtool"
	"github.com/drvova/pig-observational-memory/memory/runtime"
)

// memorySection names the system prompt section the index occupies.
const memorySection = "curated-memory"

// The engine's shared setup (memory/engine), under the names this adapter's tests use.
const toolNoteLine = engine.ToolNoteLine

var (
	memorySectionText = engine.SectionText
	curatedCall       = engine.CuratedCall
	settleCurated     = engine.SettleCurated
	migrateLayerV2    = engine.MigrateLayer
	statusInfo        = engine.StatusInfo
)

// memoryPin is the index the current conversation was given. PiG keeps the
// extension process across /new, /resume, /fork and /reload, so the pin is
// reset at every session_start as well as after a compaction.
var memoryPin curated.Pin

// projectRoot is the directory a project-scoped memory is filed under (harness.ProjectRoot).
func projectRoot(cwd string) string { return harness.ProjectRoot(cwd) }

// memoryToolSpec is the memory tool (docs/code-tool.md, "The tool"): fixed text, the same for
// every model and every store state, registered once; the harness's manager runs each call on
// the namespace child.
func memoryToolSpec(manager *harness.Manager) host.ToolSpec {
	return host.ToolSpec{
		Name:             omtool.ToolName,
		Label:            omtool.Label,
		Description:      omtool.Description,
		PromptSnippet:    omtool.PromptSnippet,
		PromptGuidelines: omtool.PromptGuidelines,
		Parameters:       omtool.Parameters,
		Execute: func(h host.ToolHost, params map[string]any) host.ToolResult {
			result := manager.RunMemory(h, params)
			return host.ToolResult{Text: result.Text, IsError: result.IsError, Details: result.Details}
		},
		RenderCall: func(args map[string]any) []string {
			return strings.Split(omtool.FormatCallForTui(args), "\n")
		},
		RenderResult: func(details any, text string, expanded bool) []string {
			return strings.Split(omtool.FormatRenderedResultForTui(details, text, expanded), "\n")
		},
	}
}

// registerMemory wires the memory tool (docs/code-tool.md), the one tool the
// extension registers, and the curated memory index section of the system
// prompt. The tools the agent defined with the removed query language are
// retired: none is registered, run or (de)activated. rt reads the memory
// plumbing the manager knows (the memory tool and the retired defined queries'
// names), the tools level-0 extraction leaves out of what it reads
// (harness.MemoryToolNames).
func registerMemory(ext *sdk.Extension, rt *runtime.Runtime) {
	manager := engine.NewManager(rt, &memoryPin)

	ext.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		h := sdkhost.New(ctx)
		options, _ := data["systemPromptOptions"].(map[string]any)
		sections, _ := options["sections"].(*sdk.SystemPromptSections)
		if sections == nil {
			return nil, nil
		}
		index, on := engine.Index(h, &memoryPin)
		// A section, not a returned systemPrompt: a returned prompt is forced
		// for the whole run and drops the sections other extensions set.
		sections.Set(memorySection, memorySectionText(index, on))
		return nil, nil
	})
	// A new or resumed conversation, and one restarting from a compaction
	// summary, take a freshly ranked index. A session start also reads the
	// retired defined queries' names into the skip list.
	ext.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		// The namespace resets: only kept definitions reach the next conversation; a detector
		// pass still running from the last compaction is stopped (engine.SessionStart).
		engine.SessionStart(rt, manager, &memoryPin, sdkhost.New(ctx))
		return nil, nil
	})
	// The evidence for staged memories, and the user corrections that flag saved ones, are
	// checked when the agent settles; the index is not re-rendered until its next render.
	ext.OnEvent(sdk.EventAgentSettled, func(ctx sdk.Context, _ map[string]any) (any, error) {
		settleCurated(sdkhost.New(ctx))
		return nil, nil
	})
	ext.OnEvent(sdk.EventSessionCompact, func(sdk.Context, map[string]any) (any, error) {
		memoryPin.Reset()
		return nil, nil
	})

	ext.OnEvent(sdk.EventSessionShutdown, func(sdk.Context, map[string]any) (any, error) {
		manager.Namespace().Reset()
		rt.StopDetectorPass(runtime.DetectorStopSessionEnd)
		return nil, nil
	})

	ext.RegisterTool(sdkhost.Definition(memoryToolSpec(manager)))
}
