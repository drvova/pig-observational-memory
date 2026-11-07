package enginerpc

import (
	"encoding/json"

	"github.com/drvova/pig-observational-memory/memory/host"
)

// ProtocolVersion is the version of the connection's methods and shapes (docs/engine-rpc.md).
// initialize refuses a client that asks for another.
const ProtocolVersion = 1

// Host → engine methods: the harness's events, the memory tool, the commands.
const (
	MethodInitialize = "initialize"
	MethodShutdown   = "shutdown"
	// MethodImageStop stops a Lisp image (every connection); a client sends it to a stale build.
	MethodImageStop            = "image/stop"
	MethodSessionStart         = "event/sessionStart"
	MethodProjectTrust         = "event/projectTrust"
	MethodBeforeAgentStart     = "event/beforeAgentStart"
	MethodAgentStart           = "event/agentStart"
	MethodTurnEnd              = "event/turnEnd"
	MethodAgentSettled         = "event/agentSettled"
	MethodSessionBeforeCompact = "event/sessionBeforeCompact"
	MethodSessionCompact       = "event/sessionCompact"
	MethodContext              = "event/context"
	MethodSessionShutdown      = "event/sessionShutdown"
	MethodToolCall             = "tool/call"
	MethodToolRenderCall       = "tool/renderCall"
	MethodToolRenderResult     = "tool/renderResult"
	MethodCommand              = "command/run"
)

// Engine → host methods: one per memory/host method the engine calls.
const (
	MethodGetBranch                 = "host/getBranch"
	MethodGetEntries                = "host/getEntries"
	MethodAppendEntry               = "host/appendEntry"
	MethodSessionID                 = "host/sessionId"
	MethodSessionFile               = "host/sessionFile"
	MethodConfigHome                = "host/configHome"
	MethodRepo                      = "host/repo"
	MethodStructure                 = "host/structure"
	MethodHarness                   = "host/harness"
	MethodNotify                    = "host/notify"
	MethodIsIdle                    = "host/isIdle"
	MethodCompact                   = "host/compact"
	MethodGetContextUsage           = "host/getContextUsage"
	MethodProjectTrusted            = "host/projectTrusted"
	MethodSessionModel              = "host/sessionModel"
	MethodAllTools                  = "host/allTools"
	MethodActiveTools               = "host/activeTools"
	MethodSetActiveTools            = "host/setActiveTools"
	MethodModelsFind                = "models/find"
	MethodModelsGetApiKeyAndHeaders = "models/getApiKeyAndHeaders"
	MethodModelsIsUsingOAuth        = "models/isUsingOAuth"
	MethodModelsHasConfiguredAuth   = "models/hasConfiguredAuth"
	MethodModelsRefresh             = "models/refresh"
	MethodModelsStream              = "models/stream"
	MethodModelsStreamResult        = "models/streamResult"
)

// Command names command/run takes.
const (
	CommandStatus = "om:status"
	CommandView   = "om:view"
)

// Scope is the host an event's handler runs against: the adapter names it, and every host
// call the engine makes while handling the event (or later, from work the event started)
// carries its id, so the adapter answers it with that event's host. Cwd and HasUI are read
// once when the event arrives, as the in-process host reads them.
type Scope struct {
	ID    string `json:"id"`
	Cwd   string `json:"cwd"`
	HasUI bool   `json:"hasUI"`
}

// InitializeParams open the connection.
type InitializeParams struct {
	ProtocolVersion int `json:"protocolVersion"`
	// AgentDir and ConfigDirName are the harness's directories (memory/hostinfo): its global
	// settings directory and its per-project configuration directory name. Empty leaves the
	// engine's own (om's root).
	AgentDir      string `json:"agentDir,omitempty"`
	ConfigDirName string `json:"configDirName,omitempty"`
	// Env is the adapter's environment the engine reads (config.PassiveEnv), for an engine
	// that is not the adapter's child and so does not inherit it (the Lisp image). An `om
	// serve` engine reads its own environment and ignores it.
	Env map[string]string `json:"env,omitempty"`
}

// ToolSpec is the memory tool as the adapter registers it (memory/omtool).
type ToolSpec struct {
	Name             string         `json:"name"`
	Label            string         `json:"label"`
	Description      string         `json:"description"`
	PromptSnippet    string         `json:"promptSnippet"`
	PromptGuidelines []string       `json:"promptGuidelines"`
	Parameters       map[string]any `json:"parameters"`
}

// InitializeResult answers initialize.
type InitializeResult struct {
	ProtocolVersion int      `json:"protocolVersion"`
	Name            string   `json:"name"`
	Version         string   `json:"version"`
	Tool            ToolSpec `json:"tool"`
	// BuildID identifies the sources a Lisp image was built from (docs/lisp-port-peer.md); a
	// client that knows the id it wants refuses another (Client.RequireBuild).
	BuildID string `json:"buildId,omitempty"`
}

// EventParams are an event's params: its scope, and the payload where the event has one.
type EventParams struct {
	Scope Scope `json:"scope"`
	// Data is session_before_compact's payload as the harness sent it.
	Data map[string]any `json:"data,omitempty"`
}

// EventResult answers an event: Thinning is whether the context handler is wanted (the
// loaded configuration is not passive), for agent_start, turn_end and
// session_before_compact.
type EventResult struct {
	Thinning *bool `json:"thinning,omitempty"`
}

// BeforeAgentStartResult is the system prompt sections to set.
type BeforeAgentStartResult struct {
	Sections map[string]string `json:"sections"`
}

// BeforeCompactResult is the session_before_compact answer in Pi's
// SessionBeforeCompactResult shape ({"compaction": …}), nil to decline.
type BeforeCompactResult struct {
	Result   map[string]any `json:"result"`
	Thinning *bool          `json:"thinning,omitempty"`
}

// ContextParams carry a provider request's messages. A harness sends a request's whole
// list, which between two requests mostly repeats the previous one, so the list is delivered
// incrementally: Reuse leading messages are byte-identical to those of context request Base
// (its Seq), and Messages are the rest. A client that keeps no previous list sends Reuse 0.
// The engine answers CodeStaleContext when it does not hold request Base (it restarted, or
// another request came between), and the client sends the whole list again.
type ContextParams struct {
	Scope    Scope             `json:"scope"`
	Seq      int64             `json:"seq,omitempty"`
	Base     int64             `json:"base,omitempty"`
	Reuse    int               `json:"reuse,omitempty"`
	Messages []json.RawMessage `json:"messages"`
}

// CodeStaleContext answers a context request whose Base the engine does not hold.
const CodeStaleContext = -32001

// ContextEdit is one message the engine changed: the adapter replaces the members of the
// message at Index with Message's, in place.
type ContextEdit struct {
	Index   int            `json:"index"`
	Message map[string]any `json:"message"`
}

// ContextResult lists the changed messages; an unchanged message is not sent back.
type ContextResult struct {
	Edits []ContextEdit `json:"edits"`
}

// ToolCallParams are one memory tool call.
type ToolCallParams struct {
	Scope  Scope          `json:"scope"`
	Name   string         `json:"name"`
	Params map[string]any `json:"params"`
}

// ToolCallResult is the tool's result.
type ToolCallResult struct {
	Text    string `json:"text"`
	IsError bool   `json:"isError"`
	Details any    `json:"details"`
}

// RenderCallParams render a call line.
type RenderCallParams struct {
	Args map[string]any `json:"args"`
}

// RenderResultParams render a result.
type RenderResultParams struct {
	Details  any    `json:"details"`
	Text     string `json:"text"`
	Expanded bool   `json:"expanded"`
}

// LinesResult is rendered text, one string per line.
type LinesResult struct {
	Lines []string `json:"lines"`
}

// CommandParams run a slash command.
type CommandParams struct {
	Scope Scope  `json:"scope"`
	Name  string `json:"name"`
	Args  string `json:"args"`
}

// ScopeParams name the scope of a host call with no other params.
type ScopeParams struct {
	Scope string `json:"scope"`
}

// GetBranchParams ask for the branch after From entries the engine already holds, the last
// of which has id AnchorID. The adapter answers with the entries from From when its branch
// still has AnchorID at index From-1 (an entry's id fixes its whole path, so the prefix is
// unchanged), else with the whole branch and From 0.
type GetBranchParams struct {
	Scope    string `json:"scope"`
	From     int    `json:"from"`
	AnchorID string `json:"anchorId,omitempty"`
}

// BranchResult is a branch or its tail: Entries start at index From.
type BranchResult struct {
	From    int               `json:"from"`
	Entries []json.RawMessage `json:"entries"`
}

// EntriesResult is every entry of the session file.
type EntriesResult struct {
	Entries []json.RawMessage `json:"entries"`
}

// AppendEntryParams append a custom entry.
type AppendEntryParams struct {
	Scope      string `json:"scope"`
	CustomType string `json:"customType"`
	Data       any    `json:"data"`
}

// RepoResult is host/repo's result: the session's repository (host.Repo), null outside one.
type RepoResult struct {
	Repo *host.Repo `json:"repo"`
}

// StructureParams are host/structure's params: the scope and the query (host.StructureQuery).
type StructureParams struct {
	Scope string              `json:"scope"`
	Query host.StructureQuery `json:"query"`
}

// StructureResult is host/structure's result: the HEAD structure view's answer, null outside a
// repository or for a host that reads none.
type StructureResult struct {
	Answer *host.StructureAnswer `json:"answer"`
}

// StringResult is a string answer (session id, session file, config home, harness).
type StringResult struct {
	Value string `json:"value"`
}

// BoolResult is a boolean answer.
type BoolResult struct {
	Value bool `json:"value"`
}

// NotifyParams show a notification.
type NotifyParams struct {
	Scope   string `json:"scope"`
	Message string `json:"message"`
	Level   string `json:"level"`
}

// ContextUsage is host.ContextUsage on the wire.
type ContextUsage struct {
	Tokens        *int `json:"tokens"`
	ContextWindow int  `json:"contextWindow"`
}

// ContextUsageResult is the usage, nil when the host has none.
type ContextUsageResult struct {
	Usage *ContextUsage `json:"usage"`
}

// ModelResult is a model object, nil when none.
type ModelResult struct {
	Model host.Model `json:"model"`
}

// ToolInfo is host.ToolInfo on the wire.
type ToolInfo struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// ToolsResult lists the session's tools.
type ToolsResult struct {
	Tools []ToolInfo `json:"tools"`
}

// NamesParams and NamesResult carry tool names.
type NamesParams struct {
	Scope string   `json:"scope"`
	Names []string `json:"names"`
}

// NamesResult lists tool names.
type NamesResult struct {
	Names []string `json:"names"`
}

// FindParams look a model up.
type FindParams struct {
	Scope    string `json:"scope"`
	Provider string `json:"provider"`
	ID       string `json:"id"`
}

// ModelParams name a model.
type ModelParams struct {
	Scope string     `json:"scope"`
	Model host.Model `json:"model"`
}

// Auth is host.Auth on the wire (the shape AuthFromMap reads).
type Auth struct {
	OK      bool              `json:"ok"`
	APIKey  *string           `json:"apiKey,omitempty"`
	Headers map[string]any    `json:"headers,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	BaseURL string            `json:"baseUrl,omitempty"`
	Error   string            `json:"error,omitempty"`
}

// AuthResult is a model's request authentication.
type AuthResult struct {
	Auth Auth `json:"auth"`
}

// RefreshParams refresh the model registry.
type RefreshParams struct {
	Scope        string   `json:"scope"`
	AllowNetwork *bool    `json:"allowNetwork,omitempty"`
	Providers    []string `json:"providers,omitempty"`
}

// StreamParams start a model stream.
type StreamParams struct {
	Scope   string         `json:"scope"`
	Model   host.Model     `json:"model"`
	Request map[string]any `json:"request"`
}

// StreamResult names the started stream, "" when the host started none (host.Models.Stream
// returned nil).
type StreamResult struct {
	Stream string `json:"stream"`
}

// StreamResultParams drain a started stream.
type StreamResultParams struct {
	Scope  string `json:"scope"`
	Stream string `json:"stream"`
}

// StreamMessageResult is the stream's terminal message (ModelStream.Result), nil when none.
type StreamMessageResult struct {
	Message map[string]any `json:"message"`
	// Events is how many events the host drained.
	Events int `json:"events"`
}

func authToWire(a host.Auth) Auth {
	return Auth{OK: a.OK, APIKey: a.APIKey, Headers: a.Headers, Env: a.Env, BaseURL: a.BaseURL, Error: a.Error}
}

func authFromWire(a Auth) host.Auth {
	return host.Auth{OK: a.OK, APIKey: a.APIKey, Headers: a.Headers, Env: a.Env, BaseURL: a.BaseURL, Error: a.Error}
}
