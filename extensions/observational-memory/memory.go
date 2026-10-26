package observationalmemory

import (
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/commands"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omtool"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/sdkhost"
)

// memorySection names the system prompt section the index occupies.
const memorySection = "curated-memory"

// toolNoteLine is the extension's one system-prompt line asking the session model to say, before a
// tool call, what it is doing and why: level 0 keeps the text before an assistant message's first
// tool call verbatim on that call's record (docs/memory-v2.md, "The model's own words"). The
// model writes those words to the user, so the line names no memory, compaction or resuming,
// which the model would otherwise repeat to the user before every call. It costs no request.
const toolNoteLine = "Before each tool call, say in one short sentence what you are doing and why."

// memorySectionText is the extension's system-prompt section: toolNoteLine (a fixed line, so
// the prompt prefix stays stable), followed by the curated index when curated memory is on and
// the index is not empty. The section is always set.
func memorySectionText(index string, curatedOn bool) string {
	if !curatedOn || index == "" {
		return toolNoteLine
	}
	return toolNoteLine + "\n\n" + index
}

// memoryPin is the index the current conversation was given. PiG keeps the
// extension process across /new, /resume, /fork and /reload, so the pin is
// reset at every session_start as well as after a compaction.
var memoryPin curated.Pin

// projectRoot is the directory a project-scoped memory is filed under (harness.ProjectRoot).
func projectRoot(cwd string) string { return harness.ProjectRoot(cwd) }

// curatedCall is the store, project and session a memory query acts on, and
// whether curated memory is on.
func curatedCall(h host.Host) (curated.Call, config.Config, bool) {
	// Project settings only from a trusted project, as PiG reads them.
	cfg := config.Load(h.Cwd(), config.DefaultPaths(), config.TrustFrom(h.ProjectTrusted()), nil)
	store, on := harness.StoreOf(h, cfg)
	if !on {
		return curated.Call{}, cfg, false
	}
	return curated.Call{Store: store, Project: projectRoot(h.Cwd()), Source: h.SessionID()}, cfg, true
}

// settleCurated runs the curated evidence check (harness.SettleCurated) at session_start and
// agent_settled: code only, no request. With curated memory off it does nothing.
func settleCurated(h host.Host) {
	call, _, on := curatedCall(h)
	if !on {
		return
	}
	_, _ = harness.SettleCurated(h, call)
}

// migrateLayerV2 migrates the written v1 worker instructions to memory v2 at session_start
// (harness.MigrateLayerV2): code only, no request. Without a store it does nothing.
func migrateLayerV2(h host.Host) {
	call, cfg, on := curatedCall(h)
	if !on {
		return
	}
	harness.MigrateLayerV2(h, call.Store, harness.ModelKey(h.SessionModel()), harness.WorkerModelKey(h, cfg))
}

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

// statusInfo is the closure a code tool call answers om.Info with (docs/code-tool.md, "Order of
// work"): commands.Info over the call's host, so om.Info and /om:status print one set of shared
// figures. An unreadable branch is reported in the figures rather than failing the call.
func statusInfo(rt *runtime.Runtime, h host.Host) func() om.StatusInfo {
	return func() om.StatusInfo {
		info, err := commands.Info(rt, h)
		if err != nil {
			return om.StatusInfo{Lines: []string{"Status unavailable: " + err.Error()}}
		}
		return info
	}
}

// registerMemory wires the memory tool (docs/code-tool.md), the per-model
// harness and the tools the agent defined before it (docs/memory-interface.md,
// section 6), and the curated memory index section of the system prompt. rt
// reads the memory plumbing the manager knows (the defined tools it registered
// and the retired defined queries' names), the tools level-0 extraction leaves
// out of what it reads (harness.MemoryToolNames).
func registerMemory(ext *sdk.Extension, rt *runtime.Runtime) {
	manager := harness.NewManager(func(h host.ToolHost) (curated.Call, bool) {
		call, _, on := curatedCall(h)
		return call, on
	})
	manager.Info = func(h host.ToolHost) func() om.StatusInfo { return statusInfo(rt, h) }
	manager.IndexShown = memoryPin.Shown
	rt.SetDefinedTools(manager.SkipNames)
	rt.SetCodeStatus(manager.CodeStatusLines)

	ext.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		h := sdkhost.New(ctx)
		// The session model's profile: the defined tools it keeps active. The memory
		// tool itself is the same for every model.
		manager.BeforeAgentStart(h)
		options, _ := data["systemPromptOptions"].(map[string]any)
		sections, _ := options["sections"].(*sdk.SystemPromptSections)
		if sections == nil {
			return nil, nil
		}
		call, cfg, on := curatedCall(h)
		index := ""
		if on {
			index, _, _ = memoryPin.Get(func() (string, []string) {
				return curated.IndexFor(call.Store, call.Project, cfg.CuratedMemoryIndexTokens)
			})
		}
		// A section, not a returned systemPrompt: a returned prompt is forced
		// for the whole run and drops the sections other extensions set.
		sections.Set(memorySection, memorySectionText(index, on))
		return nil, nil
	})
	// A new or resumed conversation, and one restarting from a compaction
	// summary, take a freshly ranked index. A session start also registers the
	// stored definitions as tools (once per process: the host keeps them).
	ext.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		memoryPin.Reset()
		// The namespace resets: only kept definitions reach the next conversation.
		manager.Namespace().Reset()
		h := sdkhost.New(ctx)
		manager.RegisterStored(h)
		migrateLayerV2(h)
		settleCurated(h)
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
		return nil, nil
	})

	ext.RegisterTool(sdkhost.Definition(memoryToolSpec(manager)))
}
