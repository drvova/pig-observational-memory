package enginerpc

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"sync"

	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/session"
)

// remoteHost is the engine's host.ToolHost for one event: every method is a call to the
// adapter carrying the event's scope, answered with that event's host. The engine may retain
// it past the event (the compaction trigger does), as it retains an in-process host.
type remoteHost struct {
	peer   *Peer
	scope  Scope
	ctx    context.Context
	branch *branchCache
}

var (
	_ host.ToolHost          = (*remoteHost)(nil)
	_ host.ConfigHomer       = (*remoteHost)(nil)
	_ host.SessionFileReader = (*remoteHost)(nil)
)

// call makes one host call. Host calls are not cancelled by the event's cancellation: the
// in-process host's methods take no context either; the two that do (Refresh, the stream's
// drain) pass theirs.
func (h *remoteHost) call(method string, params any, result any) error {
	return h.peer.Call(context.Background(), method, params, result)
}

func (h *remoteHost) scoped() ScopeParams { return ScopeParams{Scope: h.scope.ID} }

// RequestContext is the event's request cancellation: the adapter's $/cancelRequest for the
// event (the harness cancelled the request the event answers) cancels it.
func (h *remoteHost) RequestContext() context.Context { return h.ctx }

func (h *remoteHost) GetBranch() ([]session.Entry, error) {
	return h.branch.get(func(from int, anchor string) (BranchResult, error) {
		var out BranchResult
		err := h.call(MethodGetBranch, GetBranchParams{Scope: h.scope.ID, From: from, AnchorID: anchor}, &out)
		return out, err
	})
}

func (h *remoteHost) GetEntries() ([]session.Entry, error) {
	var out EntriesResult
	if err := h.call(MethodGetEntries, h.scoped(), &out); err != nil {
		return nil, err
	}
	return session.FromJSON(out.Entries)
}

func (h *remoteHost) AppendEntry(customType string, data any) error {
	return h.call(MethodAppendEntry, AppendEntryParams{Scope: h.scope.ID, CustomType: customType, Data: data}, nil)
}

func (h *remoteHost) stringCall(method string) string {
	var out StringResult
	if err := h.call(method, h.scoped(), &out); err != nil {
		return ""
	}
	return out.Value
}

func (h *remoteHost) SessionID() string   { return h.stringCall(MethodSessionID) }
func (h *remoteHost) SessionFile() string { return h.stringCall(MethodSessionFile) }
func (h *remoteHost) ConfigHome() string  { return h.stringCall(MethodConfigHome) }
func (h *remoteHost) HasUI() bool         { return h.scope.HasUI }
func (h *remoteHost) Cwd() string         { return h.scope.Cwd }

func (h *remoteHost) Notify(message, level string) {
	_ = h.call(MethodNotify, NotifyParams{Scope: h.scope.ID, Message: message, Level: level}, nil)
}

func (h *remoteHost) IsIdle() (bool, error) {
	var out BoolResult
	if err := h.call(MethodIsIdle, h.scoped(), &out); err != nil {
		return false, err
	}
	return out.Value, nil
}

// Compact asks the host to compact and returns at once, as the in-process host does; the
// reply arrives when the compaction has finished, and the callback runs then.
func (h *remoteHost) Compact(callbacks host.CompactCallbacks) {
	go func() {
		err := h.call(MethodCompact, h.scoped(), nil)
		if err != nil {
			if callbacks.OnError != nil {
				callbacks.OnError(errors.New(err.Error()))
			}
			return
		}
		if callbacks.OnComplete != nil {
			callbacks.OnComplete()
		}
	}()
}

func (h *remoteHost) GetContextUsage() (*host.ContextUsage, error) {
	var out ContextUsageResult
	if err := h.call(MethodGetContextUsage, h.scoped(), &out); err != nil {
		return nil, err
	}
	if out.Usage == nil {
		return nil, nil
	}
	return &host.ContextUsage{Tokens: out.Usage.Tokens, ContextWindow: out.Usage.ContextWindow}, nil
}

func (h *remoteHost) ProjectTrusted() (bool, error) {
	var out BoolResult
	if err := h.call(MethodProjectTrusted, h.scoped(), &out); err != nil {
		return false, err
	}
	return out.Value, nil
}

func (h *remoteHost) SessionModel() host.Model {
	var out ModelResult
	if err := h.call(MethodSessionModel, h.scoped(), &out); err != nil {
		return nil
	}
	return out.Model
}

func (h *remoteHost) Models() host.Models { return remoteModels{h: h} }

func (h *remoteHost) AllTools() ([]host.ToolInfo, error) {
	var out ToolsResult
	if err := h.call(MethodAllTools, h.scoped(), &out); err != nil {
		return nil, err
	}
	tools := make([]host.ToolInfo, 0, len(out.Tools))
	for _, t := range out.Tools {
		tools = append(tools, host.ToolInfo{Name: t.Name, Source: t.Source})
	}
	return tools, nil
}

func (h *remoteHost) ActiveTools() ([]string, error) {
	var out NamesResult
	if err := h.call(MethodActiveTools, h.scoped(), &out); err != nil {
		return nil, err
	}
	return out.Names, nil
}

func (h *remoteHost) SetActiveTools(names []string) error {
	return h.call(MethodSetActiveTools, NamesParams{Scope: h.scope.ID, Names: names}, nil)
}

// ErrRegisterToolRemote is RegisterTool's answer: a tool's Execute is code, which does not
// cross the connection. Memory v2 registers no tool while a session runs (defined tools are
// retired), so the engine never makes this call.
var ErrRegisterToolRemote = errors.New("enginerpc: registerTool is not available to an engine process")

func (h *remoteHost) RegisterTool(spec host.ToolSpec) error { return ErrRegisterToolRemote }

type remoteModels struct{ h *remoteHost }

func (m remoteModels) Find(provider, id string) host.Model {
	var out ModelResult
	if err := m.h.call(MethodModelsFind, FindParams{Scope: m.h.scope.ID, Provider: provider, ID: id}, &out); err != nil {
		return nil
	}
	return out.Model
}

func (m remoteModels) GetApiKeyAndHeaders(model host.Model) (host.Auth, error) {
	var out AuthResult
	if err := m.h.call(MethodModelsGetApiKeyAndHeaders, ModelParams{Scope: m.h.scope.ID, Model: model}, &out); err != nil {
		return host.Auth{}, err
	}
	return authFromWire(out.Auth), nil
}

func (m remoteModels) boolCall(method string, model host.Model) bool {
	var out BoolResult
	if err := m.h.call(method, ModelParams{Scope: m.h.scope.ID, Model: model}, &out); err != nil {
		return false
	}
	return out.Value
}

func (m remoteModels) IsUsingOAuth(model host.Model) bool {
	return m.boolCall(MethodModelsIsUsingOAuth, model)
}

func (m remoteModels) HasConfiguredAuth(model host.Model) bool {
	return m.boolCall(MethodModelsHasConfiguredAuth, model)
}

func (m remoteModels) Refresh(ctx context.Context, options host.RefreshOptions) error {
	return m.h.peer.Call(ctx, MethodModelsRefresh, RefreshParams{Scope: m.h.scope.ID, AllowNetwork: options.AllowNetwork, Providers: options.Providers}, nil)
}

// Stream starts the stream on the host at once, so a host that starts none answers nil here
// as the in-process host does; Events drains it there.
func (m remoteModels) Stream(model host.Model, request map[string]any) host.ModelStream {
	var out StreamResult
	if err := m.h.call(MethodModelsStream, StreamParams{Scope: m.h.scope.ID, Model: model, Request: request}, &out); err != nil || out.Stream == "" {
		return nil
	}
	return &remoteStream{h: m.h, id: out.Stream}
}

// remoteStream is a model stream the host drains: Events asks the host to drain it under
// ctx (cancelling ctx cancels the host's drain) and delivers one terminal event with the
// message when the host has finished.
type remoteStream struct {
	h  *remoteHost
	id string

	mu      sync.Mutex
	message map[string]any
}

func (s *remoteStream) Events(ctx context.Context) <-chan map[string]any {
	out := make(chan map[string]any, 1)
	go func() {
		defer close(out)
		var result StreamMessageResult
		err := s.h.peer.Call(ctx, MethodModelsStreamResult, StreamResultParams{Scope: s.h.scope.ID, Stream: s.id}, &result)
		if err != nil {
			return
		}
		s.mu.Lock()
		s.message = result.Message
		s.mu.Unlock()
		if result.Message != nil && ctx.Err() == nil {
			out <- map[string]any{"type": "done", "reason": result.Message["stopReason"], "message": result.Message}
		}
	}()
	return out
}

func (s *remoteStream) Result() map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.message
}

// branchCache holds the branch the engine last read, decoded, so a GetBranch asks the
// adapter only for the entries after it (incremental branch delivery) and decodes only
// those. Every read returns a fresh slice over the same decoded entries, as testfix's host
// does: the engine treats a read entry as immutable (session entries are), so readers share
// them.
type branchCache struct {
	mu      sync.Mutex
	entries []session.Entry
}

func (c *branchCache) get(fetch func(from int, anchor string) (BranchResult, error)) ([]session.Entry, error) {
	c.mu.Lock()
	cached := c.entries
	c.mu.Unlock()
	from, anchor := len(cached), ""
	if from > 0 {
		anchor = cached[from-1].ID
	}
	if anchor == "" {
		from = 0
	}
	reply, err := fetch(from, anchor)
	if err != nil {
		return nil, err
	}
	if reply.From < 0 || reply.From > from {
		return nil, errors.New("enginerpc: getBranch answered from " + strconv.Itoa(reply.From) + " for a request from " + strconv.Itoa(from))
	}
	tail, err := session.FromJSON(reply.Entries)
	if err != nil {
		return nil, err
	}
	if len(tail) != len(reply.Entries) {
		// A null entry would shift the positions the next request names; read it whole next time.
		c.mu.Lock()
		c.entries = nil
		c.mu.Unlock()
		return append(slices.Clone(cached[:reply.From]), tail...), nil
	}
	next := append(slices.Clip(cached[:reply.From]), tail...)
	c.mu.Lock()
	c.entries = slices.Clip(next)
	c.mu.Unlock()
	return slices.Clone(next), nil
}
