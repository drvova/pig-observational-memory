package observationalmemory

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/sdkhost"
)

// memorySection names the system prompt section the index occupies.
const memorySection = "curated-memory"

// memoryPin is the index the current conversation was given. PiG keeps the
// extension process across /new, /resume, /fork and /reload, so the pin is
// reset at every session_start as well as after a compaction.
var memoryPin curated.Pin

// projectRoot is the directory a project-scoped memory is filed under: the
// nearest ancestor of cwd holding .git, else cwd itself, with symlinks
// resolved, so a session started in a subdirectory sees the project's
// memories.
func projectRoot(cwd string) string {
	if cwd == "" {
		return ""
	}
	dir := filepath.Clean(cwd)
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for d := dir; ; {
		if _, err := os.Stat(filepath.Join(d, ".git")); err == nil {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			return dir
		}
		d = parent
	}
}

// curatedCall is the store, project and session a memory handler acts on, and
// whether curated memory is on.
func curatedCall(ctx sdk.Context) (curated.Call, config.Config, bool) {
	h := sdkhost.New(ctx)
	cfg := config.Load(h.Cwd(), config.DefaultPaths(), nil)
	home := h.ConfigHome()
	if !cfg.CuratedMemory || home == "" {
		return curated.Call{}, cfg, false
	}
	return curated.Call{Store: curated.New(curated.DefaultDir(home)), Project: projectRoot(h.Cwd()), Source: h.SessionID()}, cfg, true
}

// registerCuratedMemory wires the agent's own long-lived memory: the memory
// tools, the index section of the system prompt and the /om:memory command.
func registerCuratedMemory(ext *sdk.Extension) {
	ext.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		options, _ := data["systemPromptOptions"].(map[string]any)
		sections, _ := options["sections"].(*sdk.SystemPromptSections)
		if sections == nil {
			return nil, nil
		}
		call, cfg, on := curatedCall(ctx)
		if !on {
			sections.Delete(memorySection)
			return nil, nil
		}
		// A section, not a returned systemPrompt: a returned prompt is forced
		// for the whole run and drops the sections other extensions set.
		index, _, _ := memoryPin.Get(func() (string, []string) {
			return curated.IndexFor(call.Store, call.Project, cfg.CuratedMemoryIndexTokens)
		})
		sections.Set(memorySection, index)
		return nil, nil
	})
	// A new or resumed conversation, and one restarting from a compaction
	// summary, take a freshly ranked index.
	for _, event := range []string{sdk.EventSessionStart, sdk.EventSessionCompact} {
		ext.OnEvent(event, func(sdk.Context, map[string]any) (any, error) {
			memoryPin.Reset()
			return nil, nil
		})
	}

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
					return sdk.ToolResult{Content: "Curated memory is off (curatedMemory: false), or PiG's config directory could not be found.", IsError: true}, nil
				}
				text, err := call.Run(spec.Name, params)
				if err != nil {
					return sdk.ToolResult{Content: "Error: " + err.Error(), IsError: true}, nil
				}
				// Saves outlive the session and reach every later system prompt,
				// so the user sees each one, including any an injected
				// instruction asked for.
				if spec.Name == curated.ToolSave || spec.Name == curated.ToolEdit {
					ctx.Notify(text, "info")
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
				text, shown := curated.IndexFor(call.Store, call.Project, 0)
				if len(shown) == 0 {
					text = "No curated memories yet. The agent saves them with memory_save."
				}
				ctx.Notify(text+"\n\nStore: "+call.Store.Dir+" (index budget ~"+strconv.Itoa(cfg.CuratedMemoryIndexTokens)+" tokens)", "info")
				return nil
			}
			m, err := call.Store.GetIn(arg, call.Project)
			if err != nil {
				ctx.Notify("No memory named "+arg+" here.", "warning")
				return nil
			}
			ctx.Notify(curated.Describe(m), "info")
			return nil
		},
	})
}
