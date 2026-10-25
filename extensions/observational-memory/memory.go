package observationalmemory

import (
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/memquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/sdkhost"
)

// memorySection names the system prompt section the index occupies.
const memorySection = "curated-memory"

// resumeNoteLine is memory v2's one system-prompt line asking the session model to say,
// before a tool call, what it would need to resume: level 0 keeps the text before an assistant
// message's first tool call verbatim on that call's record (docs/memory-v2.md, "The model's
// own words"). It costs no request.
const resumeNoteLine = "Before a tool call, say in a sentence what you would need to resume this work if the conversation were compacted; memory keeps those words with the call."

// memorySectionText is the extension's system-prompt section: the curated index when curated
// memory is on, led under memory v2 by resumeNoteLine (a fixed line, so the prompt prefix stays
// stable). set is false when there is nothing to show and the section is removed.
func memorySectionText(index string, curatedOn, v2 bool) (text string, set bool) {
	if !v2 {
		return index, curatedOn
	}
	if !curatedOn || index == "" {
		return resumeNoteLine, true
	}
	return resumeNoteLine + "\n\n" + index, true
}

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

// memoryToolSpec is the memory tool with description, run by the harness's manager.
func memoryToolSpec(manager *harness.Manager, description string) host.ToolSpec {
	return host.ToolSpec{
		Name:             memquery.ToolName,
		Label:            memquery.Label,
		Description:      description,
		PromptSnippet:    memquery.PromptSnippet,
		PromptGuidelines: memquery.PromptGuidelines,
		Parameters:       memquery.Parameters,
		Execute: func(h host.ToolHost, params map[string]any) host.ToolResult {
			query, _ := params["query"].(string)
			result := manager.RunMemory(h, query)
			return host.ToolResult{Text: result.Text, IsError: result.IsError, Details: result.Details}
		},
		RenderCall: func(args map[string]any) []string {
			query, _ := args["query"].(string)
			return []string{memquery.FormatCallForTui(query)}
		},
		RenderResult: func(details any, text string, expanded bool) []string {
			return strings.Split(memquery.FormatRenderedResultForTui(memquery.DetailsFrom(details), text, expanded), "\n")
		},
	}
}

// registerMemory wires the memory tool (docs/memory-interface.md, section 5), the
// per-model harness and the tools the agent defines (section 6), and the
// curated memory index section of the system prompt. rt reads the defined
// tools the manager registered, the memory tools every worker leaves out of
// what it reads (harness.MemoryToolNames).
func registerMemory(ext *sdk.Extension, rt *runtime.Runtime) {
	manager := harness.NewManager(func(h host.ToolHost) (curated.Call, bool) {
		call, _, on := curatedCall(h)
		return call, on
	})
	manager.MemoryTool = func(description string) host.ToolSpec { return memoryToolSpec(manager, description) }
	manager.IndexShown = memoryPin.Shown
	manager.Config = func(h host.ToolHost) config.Config {
		_, cfg, _ := curatedCall(h)
		return cfg
	}
	rt.SetDefinedTools(manager.Registered)

	ext.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		h := sdkhost.New(ctx)
		// The session model's profile: the memory tool's codec and spec, and the
		// defined tools it keeps active.
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
		if text, set := memorySectionText(index, on, cfg.V2()); set {
			sections.Set(memorySection, text)
		} else {
			sections.Delete(memorySection)
		}
		return nil, nil
	})
	// A new or resumed conversation, and one restarting from a compaction
	// summary, take a freshly ranked index. A session start also registers the
	// stored definitions as tools (once per process: the host keeps them).
	ext.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		memoryPin.Reset()
		manager.RegisterStored(sdkhost.New(ctx))
		return nil, nil
	})
	ext.OnEvent(sdk.EventSessionCompact, func(sdk.Context, map[string]any) (any, error) {
		memoryPin.Reset()
		return nil, nil
	})

	ext.RegisterTool(sdkhost.Definition(memoryToolSpec(manager, memquery.Description(nil))))
}
