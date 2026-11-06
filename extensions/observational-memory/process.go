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
	"github.com/drvova/pig-observational-memory/memory/lispimage"
	"github.com/drvova/pig-observational-memory/memory/omtool"
	"github.com/drvova/pig-observational-memory/memory/runtime"
)

// engineDialer starts the engine process for the om binary at path (tests replace it).
var engineDialer = func(path string) enginerpc.Dialer { return enginerpc.ProcessDialer(path, os.Stderr) }

// lispDialer reaches the Lisp engine image l names (tests replace it).
var lispDialer = func(l *lispimage.Launcher) enginerpc.Dialer { return enginerpc.SocketDialer(l) }

// goEngineDialer is the Go engine the "lisp" mode falls back to: a Server in this process,
// over pipes, so a refused event runs on the same engine functions the in-process path runs.
var goEngineDialer = func() enginerpc.Dialer {
	return enginerpc.PipeDialer(func(r io.Reader, w io.Writer) error { return enginerpc.NewServer().Serve(r, w) })
}

// engineLog is where process-mode failures are reported: the extension's stderr, never the
// model's context.
var engineLog io.Writer = os.Stderr

// processEngine is the client of the engine process when the user's global settings ask for
// one (config.ReadEngine: "engine": "process", "enginePath"), started now so a binary that
// cannot run leaves the extension on the in-process engine; nil otherwise.
func processEngine() *enginerpc.Client {
	setting := config.ReadEngine(config.DefaultPaths())
	if setting.Lisp() {
		return lispEngine(setting)
	}
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
	client := enginerpc.NewClient(engineDialer(path), initializeParams())
	if _, err := client.Start(); err != nil {
		fmt.Fprintf(engineLog, "observational-memory: engine \"process\": %v; using the in-process engine\n", err)
		return nil
	}
	return client
}

// initializeParams name PiG's directories to the engine.
func initializeParams() enginerpc.InitializeParams {
	params := enginerpc.InitializeParams{}
	if dir, err := hostinfo.AgentDir(); err == nil {
		params.AgentDir = dir
	}
	if name, err := hostinfo.ConfigDirName(); err == nil {
		params.ConfigDirName = name
	}
	return params
}

// lispEngine is the client of the user's Lisp engine image ("engine": "lisp",
// docs/lisp-port-peer.md): the image's socket under the om root ($OM_HOME, else ~/.om), the
// image to start from OM_LISP_IMAGE, else lispImage in PiG's or the om root's settings, the
// build id written beside the image required. Each event the image refuses before acting runs
// on the Go engine, logged once per event; OM_LISP_FALLBACK=0 turns that off. An image that
// cannot be started now leaves the extension on the in-process engine (with falling back off,
// the client is kept and every event fails until the image starts).
func lispEngine(setting config.EngineSetting) *enginerpc.Client {
	root, err := lispimage.DefaultRoot()
	if err != nil {
		fmt.Fprintf(engineLog, "observational-memory: engine \"lisp\": %v; using the in-process engine\n", err)
		return nil
	}
	l := lispimage.FromEnv(root)
	if os.Getenv(lispimage.ImageEnv) == "" && setting.Image != "" {
		l.Image = setting.Image
	}
	params := initializeParams()
	// The image is no child of this process: it reads the client's passive switch from here.
	if v, ok := os.LookupEnv(config.PassiveEnv); ok {
		params.Env = map[string]string{config.PassiveEnv: v}
	}
	client := enginerpc.NewClient(lispDialer(l), params)
	client.RequireBuild(l.ExpectedBuild())
	fallback := lispimage.FallbackOn()
	if fallback {
		client.SetFallback(enginerpc.NewClient(goEngineDialer(), params), engineLog)
	}
	if _, err := client.Start(); err != nil {
		if fallback {
			fmt.Fprintf(engineLog, "observational-memory: engine \"lisp\": %v; using the in-process engine\n", err)
			return nil
		}
		fmt.Fprintf(engineLog, "observational-memory: engine \"lisp\": %v; falling back is off (%s=0)\n", err, lispimage.FallbackEnv)
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
