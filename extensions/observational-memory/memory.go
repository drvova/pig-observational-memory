package observationalmemory

import (
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/sdkhost"
)

// memoryPin is the index this process's conversation was given. A PiG
// extension process serves one session, so one pin per process is one per
// conversation.
var memoryPin curated.Pin

// curatedCall is the store, project and session a memory handler acts on, and
// whether curated memory is on.
func curatedCall(ctx sdk.Context) (curated.Call, config.Config, bool) {
	h := sdkhost.New(ctx)
	cfg := config.Load(h.Cwd(), config.DefaultPaths(), nil)
	home := h.ConfigHome()
	if !cfg.CuratedMemory || home == "" {
		return curated.Call{}, cfg, false
	}
	return curated.Call{Store: curated.New(curated.DefaultDir(home)), Project: h.Cwd(), Source: h.SessionID()}, cfg, true
}

// registerCuratedMemory wires the agent's own long-lived memory: the memory
// tools, the index at the head of each conversation and the /om:memory
// command.
func registerCuratedMemory(ext *sdk.Extension) {
	ext.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		call, cfg, on := curatedCall(ctx)
		if !on {
			return nil, nil
		}
		index, _, _ := memoryPin.Get(func() (string, []string) {
			return curated.IndexFor(call.Store, call.Project, cfg.CuratedMemoryIndexTokens)
		})
		if index == "" {
			return nil, nil
		}
		prompt, err := sdkhost.SystemPrompt(ctx, data)
		if err != nil {
			return nil, nil
		}
		return map[string]any{"systemPrompt": prompt + "\n\n" + index}, nil
	})
	// After a compaction the conversation starts over from the summary, so the
	// next request takes a freshly ranked index.
	ext.OnEvent(sdk.EventSessionCompact, func(sdk.Context, map[string]any) (any, error) {
		memoryPin.Reset()
		return nil, nil
	})

	for _, spec := range curated.Tools {
		spec := spec
		ext.RegisterTool(sdk.ToolDefinition{
			Name:        spec.Name,
			Label:       spec.Label,
			Description: spec.Description,
			Parameters:  spec.Parameters,
			Execute: func(ctx sdk.Context, params map[string]any) (any, error) {
				call, _, on := curatedCall(ctx)
				if !on {
					return sdk.ToolResult{Content: "Curated memory is off (curatedMemory: false), or PiG's config directory could not be found."}, nil
				}
				text, err := call.Run(spec.Name, params)
				if err != nil {
					text = "Error: " + err.Error()
				}
				return sdk.ToolResult{Content: text}, nil
			},
		})
	}

	ext.RegisterCommand("om:memory", sdk.CommandOptions{
		Description: "List curated memories, show one (/om:memory <name>), or re-rank the index (/om:memory reload)",
		Handler: func(ctx sdk.Context, args string) error {
			call, cfg, on := curatedCall(ctx)
			if !on {
				ctx.Notify("Curated memory is off (curatedMemory: false).", "info")
				return nil
			}
			arg := strings.TrimSpace(args)
			switch arg {
			case "reload":
				memoryPin.Reset()
				ctx.Notify("The memory index will be re-ranked on the next request.", "info")
				return nil
			case "", "list":
				text, _ := curated.IndexFor(call.Store, call.Project, 0)
				if text == "" {
					text = "No curated memories yet. The agent saves them with memory_save."
				}
				ctx.Notify(text+"\n\nStore: "+call.Store.Dir+" (index budget ~"+strconv.Itoa(cfg.CuratedMemoryIndexTokens)+" tokens)", "info")
				return nil
			}
			m, err := call.Store.Get(arg)
			if err != nil {
				ctx.Notify("No memory named "+arg+".", "warning")
				return nil
			}
			ctx.Notify(curated.Describe(m), "info")
			return nil
		},
	})
}
