// Package host abstracts what the extension needs from the PiG host, so the
// memory logic runs against the SDK in production and against fakes in tests.
package host

import (
	"context"

	"github.com/drvova/pig-observational-memory/memory/session"
)

// Model is a Pi model object as the registry serves it (provider, id,
// contextWindow, maxTokens, reasoning, api, baseUrl, …).
type Model = map[string]any

// ModelString reads a string member of a model.
func ModelString(model Model, key string) string {
	if model == nil {
		return ""
	}
	switch v := model[key].(type) {
	case string:
		return v
	case map[string]any:
		// A provider may arrive as an object with an id.
		if id, ok := v["id"].(string); ok {
			return id
		}
	}
	return ""
}

// ModelInt reads a numeric member of a model as an int (0 when absent or not a number).
func ModelInt(model Model, key string) int {
	if model == nil {
		return 0
	}
	switch v := model[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case int64:
		return int(v)
	}
	return 0
}

// ModelProvider is the model's provider id.
func ModelProvider(model Model) string { return ModelString(model, "provider") }

// ModelID is the model's id.
func ModelID(model Model) string {
	if id := ModelString(model, "id"); id != "" {
		return id
	}
	return ModelString(model, "modelId")
}

// ModelContextWindow is the model's context window, 0 when unknown.
func ModelContextWindow(model Model) int { return ModelInt(model, "contextWindow") }

// ModelMaxTokens is the model's output limit, 0 when unknown.
func ModelMaxTokens(model Model) int {
	if n := ModelInt(model, "maxTokens"); n > 0 {
		return n
	}
	return ModelInt(model, "maxOutputTokens")
}

// ModelReasoning reports whether the model supports reasoning.
func ModelReasoning(model Model) bool {
	if model == nil {
		return false
	}
	switch v := model["reasoning"].(type) {
	case bool:
		return v
	case nil:
		return false
	}
	return true
}

// Auth is the host's resolved request authentication for a model.
type Auth struct {
	OK      bool
	APIKey  *string
	Headers map[string]any
	Env     map[string]string
	BaseURL string
	Error   string
}

// AuthFromMap reads the host's getModelAuth reply.
func AuthFromMap(m map[string]any) Auth {
	auth := Auth{}
	if m == nil {
		return auth
	}
	if ok, isBool := m["ok"].(bool); isBool {
		auth.OK = ok
	}
	if key, ok := m["apiKey"].(string); ok {
		auth.APIKey = &key
	}
	if headers, ok := m["headers"].(map[string]any); ok {
		auth.Headers = headers
	}
	if env, ok := m["env"].(map[string]any); ok {
		auth.Env = map[string]string{}
		for k, v := range env {
			if s, ok := v.(string); ok {
				auth.Env[k] = s
			}
		}
	} else if env, ok := m["env"].(map[string]string); ok {
		auth.Env = env
	}
	auth.BaseURL, _ = m["baseUrl"].(string)
	auth.Error, _ = m["error"].(string)
	return auth
}

// RefreshOptions are the model registry refresh options.
type RefreshOptions struct {
	AllowNetwork *bool
	Providers    []string
}

// ModelStream is a model call in flight.
type ModelStream interface {
	Events(ctx context.Context) <-chan map[string]any
	Result() map[string]any
}

// Models is the model registry the level-1 request resolves the session model in.
type Models interface {
	Find(provider, id string) Model
	GetApiKeyAndHeaders(model Model) (Auth, error)
	IsUsingOAuth(model Model) bool
	HasConfiguredAuth(model Model) bool
	Refresh(ctx context.Context, options RefreshOptions) error
	// Stream starts a model request through the host's provider runtime.
	// request is Pi's stream request (messages, tools, systemPrompt plus the
	// request options).
	Stream(model Model, request map[string]any) ModelStream
}

// Session reads and appends the current session branch.
type Session interface {
	GetBranch() ([]session.Entry, error)
	AppendEntry(customType string, data any) error
	SessionID() string
	SessionFile() string
}

// SessionFileReader is a host that reads every entry of the session file, not only the current
// branch: memory v2's memory tool falls back to them when the branch has nothing
// (docs/memory-v2.md, "Branches").
type SessionFileReader interface {
	// GetEntries returns every entry of the session file, in file order.
	GetEntries() ([]session.Entry, error)
}

// ContextMessage is one message of a provider request's context, as PiG's context event
// carries it before each request: a Pi agent message (user, assistant, toolResult,
// compactionSummary, …) as decoded JSON, with no entry id. A handler edits it in place; the
// session file keeps the message as it was.
type ContextMessage = map[string]any

// UI notifies the user.
type UI interface {
	HasUI() bool
	Notify(message, level string)
}

// ContextUsage is the host's context-window usage.
type ContextUsage struct {
	Tokens        *int
	ContextWindow int
}

// CompactCallbacks receive a compaction's outcome.
type CompactCallbacks struct {
	OnComplete func()
	OnError    func(err error)
}

// Agent controls the session's agent.
type Agent interface {
	IsIdle() (bool, error)
	Compact(callbacks CompactCallbacks)
	GetContextUsage() (*ContextUsage, error)
}

// Host is everything the extension reads from and writes to PiG for one event.
type Host interface {
	Session
	UI
	Agent
	Cwd() string
	// ProjectTrusted reports whether the host trusts the current project, so
	// its settings may be read. An error means the answer is unknown, which
	// callers treat as untrusted.
	ProjectTrusted() (bool, error)
	// SessionModel is the session's active model, nil when none is set.
	SessionModel() Model
	Models() Models
}

// ConfigHomer is a host that knows PiG's config root, where the curated store and the
// per-model usage ledgers live. A host without one runs with no store: curated memory, the
// usage ledgers and the retired defined queries are then off and every model gets the default profile.
type ConfigHomer interface {
	// ConfigHome is PiG's config root, "" when it cannot be found.
	ConfigHome() string
}

// ToolInfo is one tool in the session's registry, active or not.
type ToolInfo struct {
	Name string
	// Source is what registered it: "builtin", an extension's name, or a declared source.
	Source string
}

// ToolResult is a tool call's result.
type ToolResult struct {
	// Text is what the model sees.
	Text    string
	IsError bool
	// Details are structured details for the renderers; they must survive a JSON round trip.
	Details any
}

// ToolSpec is a model-callable tool the extension registers: at load, or while a session runs
// (a tool the agent defined with the memory tool, docs/memory-interface.md section 6).
type ToolSpec struct {
	Name, Label, Description string
	// PromptSnippet is the tool's line in the system prompt's tool list; "" leaves it out.
	PromptSnippet    string
	PromptGuidelines []string
	// Parameters is the JSON schema of the arguments.
	Parameters map[string]any
	// Execute runs one call against the live session.
	Execute func(h ToolHost, params map[string]any) ToolResult
	// RenderCall, when set, renders the call line in the TUI.
	RenderCall func(args map[string]any) []string
	// RenderResult, when set, renders the result in the TUI from the result's details (as
	// decoded JSON, nil when none), its text, and whether the view is expanded.
	RenderResult func(details any, text string, expanded bool) []string
}

// Tools reads and changes the session's tools.
type Tools interface {
	// AllTools lists every tool in the session's registry, active or not.
	AllTools() ([]ToolInfo, error)
	// ActiveTools lists the active tool names.
	ActiveTools() ([]string, error)
	// SetActiveTools replaces the active tool list.
	SetActiveTools(names []string) error
	// RegisterTool registers spec, or replaces the extension's earlier tool of its name.
	RegisterTool(spec ToolSpec) error
}

// ToolHost is a host that also manages the session's tools: the memory tool and the tools
// the agent defines run against one.
type ToolHost interface {
	Host
	Tools
}

// Repo is the git repository a session runs in, as the adapter reads it by running git in the
// session's working directory (memory/gitrepo; docs/brain-memory.md, "Project identity"). The
// engine never runs git itself: level 0 stays a pure function of what the host hands it.
type Repo struct {
	// ID is the repository's identity (gitrepo.ID): the hash of the normalized remote URL and the
	// root commit, else of the root commit, else of the common git directory.
	ID string `json:"repoId"`
	// Root is the canonical real path of the work tree's root.
	Root string `json:"root"`
	// CommonDir is the canonical git common directory, shared by every worktree of one clone.
	CommonDir string `json:"commonDir"`
	// Remote is the normalized URL of the origin remote (else the first remote), "" for none.
	Remote string `json:"remote"`
	// RootCommit is the hash of the oldest root commit of HEAD, "" before the first commit.
	RootCommit string `json:"rootCommit"`
	// Branch is the checked-out branch, "" for a detached HEAD.
	Branch string `json:"branch"`
	// Head is HEAD's commit hash, "" before the first commit.
	Head string `json:"head"`
}

// RepoReader is a host that knows the session's git repository. ok is false outside one (or
// when git is missing); an error is a failed read, which callers treat as none.
type RepoReader interface {
	Repo() (repo Repo, ok bool, err error)
}

// The HEAD structure view (docs/brain-memory.md, decision 20; contracts/encoding.md, "Project
// records"): what the session's repository holds at HEAD, derived on demand by the adapter and
// cached per (repository, HEAD). It is never stored as fact: the engine reads it to judge its
// project records' freshness and to answer the memory tool, and writes no line of it.

// StructureQuery is one batch of questions about HEAD. Every part is optional.
type StructureQuery struct {
	// Paths asks whether each repository-relative path exists at HEAD, and its blob hash.
	Paths []string `json:"paths,omitempty"`
	// Symbols asks whether each symbol appears as a whole word in a path at HEAD ("" path: in
	// any file).
	Symbols []SymbolQuery `json:"symbols,omitempty"`
	// Blobs asks for each path's blob hash at a commit (the hash a record written there would
	// have carried).
	Blobs []BlobQuery `json:"blobs,omitempty"`
	// Diffs asks for each path's diff from a commit to HEAD, bounded.
	Diffs []DiffQuery `json:"diffs,omitempty"`
	// List asks for a page of the file list at HEAD.
	List *ListQuery `json:"list,omitempty"`
}

// SymbolQuery asks whether Symbol appears in Path at HEAD.
type SymbolQuery struct {
	Path   string `json:"path"`
	Symbol string `json:"symbol"`
}

// BlobQuery asks for Path's blob hash at commit At.
type BlobQuery struct {
	Path string `json:"path"`
	At   string `json:"at"`
}

// BlobAnswer is one path's blob at a commit; Known is false when it could not be read (an
// unknown commit, a path the commit does not hold).
type BlobAnswer struct {
	Path  string `json:"path"`
	At    string `json:"at"`
	Known bool   `json:"known"`
	Blob  string `json:"blob,omitempty"`
}

// DiffQuery asks for Path's diff from commit From to HEAD.
type DiffQuery struct {
	Path string `json:"path"`
	From string `json:"from"`
}

// ListQuery is a page of HEAD's files whose path starts with Prefix, in path order.
type ListQuery struct {
	Prefix string `json:"prefix,omitempty"`
	Offset int    `json:"offset,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// StructureAnswer answers a StructureQuery at one HEAD, its parts in the query's order.
type StructureAnswer struct {
	// RepoID and Head name the repository and the commit answered.
	RepoID  string         `json:"repoId"`
	Head    string         `json:"head"`
	Paths   []PathAnswer   `json:"paths,omitempty"`
	Symbols []SymbolAnswer `json:"symbols,omitempty"`
	Blobs   []BlobAnswer   `json:"blobs,omitempty"`
	Diffs   []DiffAnswer   `json:"diffs,omitempty"`
	// Files is the page List asked for; Total counts HEAD's files under the prefix.
	Files []string `json:"files,omitempty"`
	Total int      `json:"total,omitempty"`
	// Truncated is true when HEAD holds more files than the adapter lists: a path missing from
	// the list is then unknown, never gone.
	Truncated bool `json:"truncated,omitempty"`
}

// PathAnswer is one path at HEAD. Known is false when the adapter could not tell (a truncated
// list, a path outside the work tree); Exists and Blob are then meaningless.
type PathAnswer struct {
	Path   string `json:"path"`
	Known  bool   `json:"known"`
	Exists bool   `json:"exists"`
	Blob   string `json:"blob,omitempty"`
}

// SymbolAnswer is one symbol at HEAD; Known is false when the search failed or timed out.
type SymbolAnswer struct {
	Path   string `json:"path"`
	Symbol string `json:"symbol"`
	Known  bool   `json:"known"`
	Found  bool   `json:"found"`
}

// DiffAnswer is one path's diff, at most the adapter's bound (Truncated past it); Known is false
// when the diff could not be read (an unknown commit).
type DiffAnswer struct {
	Path      string `json:"path"`
	From      string `json:"from"`
	Known     bool   `json:"known"`
	Diff      string `json:"diff,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
}

// StructureReader is a host that answers the HEAD structure view of the session's repository.
// ok is false outside a repository, before the first commit, or when the host reads none.
type StructureReader interface {
	Structure(q StructureQuery) (answer StructureAnswer, ok bool, err error)
}

// HarnessNamer is a host that names its harness ("pig", or the om registry's harness name), which
// records carry as their writer.
type HarnessNamer interface {
	Harness() string
}
