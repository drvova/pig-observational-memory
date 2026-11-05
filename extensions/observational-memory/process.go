package observationalmemory

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/sdkhost"
	"github.com/drvova/pig-observational-memory/memory/config"
	"github.com/drvova/pig-observational-memory/memory/enginerpc"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/hostinfo"
	"github.com/drvova/pig-observational-memory/memory/omtool"
	"github.com/drvova/pig-observational-memory/memory/runtime"
)

// engineDialer starts the engine process for the om binary at path (tests replace it).
var engineDialer = func(path string) enginerpc.Dialer { return enginerpc.ProcessDialer(path, os.Stderr) }

// engineLog is where process-mode failures are reported: the extension's stderr, never the
// model's context.
var engineLog io.Writer = os.Stderr

// processEngine is the client of the engine process when the user's global settings ask for
// one (config.ReadEngine: "engine": "process", "enginePath"), started now so a binary that
// cannot run leaves the extension on the in-process engine; nil otherwise.
func processEngine() *enginerpc.Client {
	setting := config.ReadEngine(config.DefaultPaths())
	if !setting.Process() {
		return nil
	}
	path := setting.Path
	if path == "" {
		found, err := exec.LookPath("om")
		if err != nil {
			fmt.Fprintf(engineLog, "observational-memory: engine \"process\": no om on PATH (%v); using the in-process engine\n", err)
			return nil
		}
		path = found
	}
	params := enginerpc.InitializeParams{}
	if dir, err := hostinfo.AgentDir(); err == nil {
		params.AgentDir = dir
	}
	if name, err := hostinfo.ConfigDirName(); err == nil {
		params.ConfigDirName = name
	}
	client := enginerpc.NewClient(engineDialer(path), params)
	if _, err := client.Start(); err != nil {
		fmt.Fprintf(engineLog, "observational-memory: engine \"process\": %v; using the in-process engine\n", err)
		return nil
	}
	return client
}

// RegisterProcess wires the same hooks, commands and memory tool as Register, each forwarding
// to the engine process through client (docs/engine-rpc.md) and answering its host calls with
// the event's SDK host. An event the engine cannot answer (it exited; the client starts it
// again on the next event) does what the in-process handler does on failure: nothing, except
// session_before_compact, which still answers with the minimal compaction on PiG's cut.
func RegisterProcess(ext *sdk.Extension, client *enginerpc.Client) {
	report := func(event string, err error) {
		if err != nil {
			fmt.Fprintf(engineLog, "observational-memory: %s: %v\n", event, err)
		}
	}
	thinning := &contextSubscription{subscribe: func() func() {
		return ext.OnEvent(sdk.EventContext, func(ctx sdk.Context, data map[string]any) (any, error) {
			report("context", client.Context(sdkhost.New(ctx), sdkhost.ContextMessages(data)))
			return nil, nil
		})
	}}
	syncThinning := func(wanted, known bool) {
		if known {
			thinning.sync(wanted)
		}
	}
	ext.OnEvent(sdk.EventAgentStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		wanted, known, err := client.AgentStart(sdkhost.New(ctx))
		report("agent_start", err)
		syncThinning(wanted, known)
		return nil, nil
	})
	ext.OnEvent(sdk.EventTurnEnd, func(ctx sdk.Context, _ map[string]any) (any, error) {
		wanted, known, err := client.TurnEnd(sdkhost.New(ctx))
		report("turn_end", err)
		syncThinning(wanted, known)
		return nil, nil
	})
	ext.OnEvent(sdk.EventAgentSettled, func(ctx sdk.Context, _ map[string]any) (any, error) {
		report("agent_settled", client.AgentSettled(sdkhost.New(ctx)))
		return nil, nil
	})
	ext.OnEvent(sdk.EventSessionStart, func(ctx sdk.Context, _ map[string]any) (any, error) {
		report("session_start", client.SessionStart(sdkhost.New(ctx)))
		return nil, nil
	})
	ext.OnProjectTrust(func(ctx sdk.Context, _ map[string]any) (sdk.ProjectTrustResult, error) {
		report("project_trust", client.ProjectTrust(sdkhost.New(ctx)))
		return sdk.ProjectTrustResult{Trusted: sdk.ProjectTrustUndecided}, nil
	})
	// The minimal compaction when the engine cannot answer is built here from the payload,
	// with the default configuration.
	fallback := runtime.New()
	ext.OnEvent(sdk.EventSessionBeforeCompact, func(ctx sdk.Context, data map[string]any) (any, error) {
		h := sdkhost.New(ctx)
		result, wanted, known, err := client.BeforeCompact(h, data)
		syncThinning(wanted, known)
		if err != nil {
			report("session_before_compact", err)
			if h.RequestContext().Err() != nil {
				return nil, nil
			}
			if recovered, ok := recoveredBeforeCompact(fallback, data); ok {
				return recovered, nil
			}
			return nil, nil
		}
		if result == nil {
			return nil, nil
		}
		return result, nil
	})
	ext.RegisterCommand("om:status", sdk.CommandOptions{
		Description: "Show observational memory status",
		Handler: func(ctx sdk.Context, _ string) error {
			return client.Command(sdkhost.New(ctx), enginerpc.CommandStatus, "")
		},
	})
	ext.RegisterCommand("om:view", sdk.CommandOptions{
		Description: "Print and copy observational memory content (visible by default, full for recorded memory)",
		Handler: func(ctx sdk.Context, args string) error {
			return client.Command(sdkhost.New(ctx), enginerpc.CommandView, args)
		},
	})

	ext.OnEvent(sdk.EventBeforeAgentStart, func(ctx sdk.Context, data map[string]any) (any, error) {
		options, _ := data["systemPromptOptions"].(map[string]any)
		sections, _ := options["sections"].(*sdk.SystemPromptSections)
		if sections == nil {
			return nil, nil
		}
		set, err := client.BeforeAgentStart(sdkhost.New(ctx))
		report("before_agent_start", err)
		for name, text := range set {
			sections.Set(name, text)
		}
		return nil, nil
	})
	ext.OnEvent(sdk.EventSessionCompact, func(ctx sdk.Context, _ map[string]any) (any, error) {
		report("session_compact", client.SessionCompact(sdkhost.New(ctx)))
		return nil, nil
	})
	ext.OnEvent(sdk.EventSessionShutdown, func(ctx sdk.Context, _ map[string]any) (any, error) {
		report("session_shutdown", client.SessionShutdown(sdkhost.New(ctx)))
		return nil, nil
	})
	ext.RegisterTool(sdkhost.Definition(processToolSpec(client)))
}

// processToolSpec is memoryToolSpec with each call run by the engine process; the call line
// and result render here, from the same fixed text.
func processToolSpec(client *enginerpc.Client) host.ToolSpec {
	return host.ToolSpec{
		Name:             omtool.ToolName,
		Label:            omtool.Label,
		Description:      omtool.Description,
		PromptSnippet:    omtool.PromptSnippet,
		PromptGuidelines: omtool.PromptGuidelines,
		Parameters:       omtool.Parameters,
		Execute: func(h host.ToolHost, params map[string]any) host.ToolResult {
			result, err := client.ToolCall(h, omtool.ToolName, params)
			if err != nil {
				return host.ToolResult{Text: "The memory engine is unavailable: " + err.Error(), IsError: true}
			}
			return result
		},
		RenderCall: func(args map[string]any) []string {
			return strings.Split(omtool.FormatCallForTui(args), "\n")
		},
		RenderResult: func(details any, text string, expanded bool) []string {
			return strings.Split(omtool.FormatRenderedResultForTui(details, text, expanded), "\n")
		},
	}
}
