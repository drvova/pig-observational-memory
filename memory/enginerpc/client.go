package enginerpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"sync"
	"time"

	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/lispimage"
	"github.com/drvova/pig-observational-memory/memory/session"
)

// scopeRetention is how many recent scopes the client keeps answering host calls for. The
// engine retains a host past its event only for work that event started (the compaction
// trigger's goroutine); a call for a scope that has aged out is answered with the most recent
// event's host, the same session's live connection.
const scopeRetention = 256

// Dialer starts an engine and returns the connection's two ends and a stop function that
// ends it (closing its input and waiting for it to exit).
type Dialer func() (r io.Reader, w io.Writer, stop func() error, err error)

// Client is the adapter end of the connection: it forwards the harness's events to the
// engine and answers the engine's host calls with the host of the event each names. It
// starts the engine on first use and starts it again on the next event after it exits.
type Client struct {
	dial   Dialer
	params InitializeParams

	mu         sync.Mutex
	peer       *Peer
	stop       func() error
	tool       ToolSpec
	nextScope  int64
	scopes     map[string]host.Host
	order      []string
	latest     host.Host
	nextStream int64
	streams    map[string]host.ModelStream
	// acted holds the scopes whose event made a host write (actingMethods), until the event's
	// answer arrives: a refusal after one is never run again elsewhere.
	acted map[string]bool

	// fallback answers what the engine could not (SetFallback); requireBuild is the build id
	// the engine must report (RequireBuild).
	fallback    *Client
	fallbackLog io.Writer
	// failovers counts the events the fallback ran (Failovers).
	failovers    int
	requireBuild string

	// ctxMu serializes context requests; ctxPrev is the last one's encoded messages, sent as
	// request ctxPrevSeq.
	ctxMu      sync.Mutex
	ctxSeq     int64
	ctxPrevSeq int64
	ctxPrev    []json.RawMessage
}

// NewClient is a client that starts its engine with dial and opens the connection with params
// (ProtocolVersion is filled in).
func NewClient(dial Dialer, params InitializeParams) *Client {
	params.ProtocolVersion = ProtocolVersion
	return &Client{dial: dial, params: params, scopes: map[string]host.Host{}, streams: map[string]host.ModelStream{}, acted: map[string]bool{}}
}

// SetFallback makes fallback answer each event the engine could not, before it acted: when the
// engine cannot be started, reached or initialized, or refuses the event as not ported
// (CodeNotPorted) without having made a host write for it (host/appendEntry, models/stream,
// host/compact, host/notify). After a host write nothing is run again: the engine's error is
// the event's. Each fallback writes one line with its reason to log (nil writes none). The
// PiG adapter's "engine": "lisp" mode runs the Lisp image with the Go engine as its fallback
// (docs/lisp-port-peer.md).
func (c *Client) SetFallback(fallback *Client, log io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.fallback, c.fallbackLog = fallback, log
}

// Failovers is how many events the fallback engine ran since the client was made: the PiG
// adapter's /om:status shows it beside the image's own refusal counter, which misses the
// events the image never saw (it could not be started or reached).
func (c *Client) Failovers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failovers
}

// RequireBuild makes the connection refuse an engine whose initialize reports another build id
// (InitializeResult.BuildID): a stale image is a failure to start, never a conversation across
// versions. "" accepts any.
func (c *Client) RequireBuild(id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requireBuild = id
}

// FailoverError is an event the engine failed before it acted: it could not be started,
// reached or initialized, or it refused the event as not ported before any host write. Its
// message is the underlying error's.
type FailoverError struct {
	// Reason says why, in a few words.
	Reason string
	Err    error
}

func (e *FailoverError) Error() string { return e.Err.Error() }
func (e *FailoverError) Unwrap() error { return e.Err }

// actingMethods are the host calls that change what the harness holds or shows, or start a
// model request: once the engine has made one for an event, the event is never run again by a
// fallback engine (a second level-0 batch, coverage marker or model request).
var actingMethods = map[string]bool{MethodAppendEntry: true, MethodModelsStream: true, MethodCompact: true, MethodNotify: true}

// ProcessDialer starts `<path> serve` as a child process speaking on its stdin and stdout;
// its stderr goes to stderr (os.Stderr when nil).
func ProcessDialer(path string, stderr io.Writer) Dialer {
	return func() (io.Reader, io.Writer, func() error, error) {
		cmd := exec.Command(path, "serve")
		if stderr == nil {
			stderr = os.Stderr
		}
		cmd.Stderr = stderr
		in, err := cmd.StdinPipe()
		if err != nil {
			return nil, nil, nil, err
		}
		out, err := cmd.StdoutPipe()
		if err != nil {
			return nil, nil, nil, err
		}
		if err := cmd.Start(); err != nil {
			return nil, nil, nil, fmt.Errorf("start %s serve: %w", path, err)
		}
		var once sync.Once
		var waitErr error
		stop := func() error {
			once.Do(func() {
				_ = in.Close()
				waitErr = cmd.Wait()
			})
			return waitErr
		}
		return out, in, stop, nil
	}
}

// PipeDialer runs serve in this process over in-memory pipes (tests, and a harness that
// wants the boundary without a child process). serve must return when its reader ends.
func PipeDialer(serve func(r io.Reader, w io.Writer) error) Dialer {
	return func() (io.Reader, io.Writer, func() error, error) {
		toEngine, engineIn := io.Pipe()
		engineOut, fromEngine := io.Pipe()
		done := make(chan error, 1)
		go func() {
			err := serve(toEngine, fromEngine)
			_ = fromEngine.Close()
			done <- err
		}()
		var once sync.Once
		var serveErr error
		stop := func() error {
			once.Do(func() {
				_ = engineIn.Close()
				serveErr = <-done
			})
			return serveErr
		}
		return engineOut, engineIn, stop, nil
	}
}

// SocketDialer connects to the user's Lisp engine image on its unix socket, starting the image
// when the socket is absent or refuses (memory/lispimage, the launcher the om CLI's Lisp mode
// uses too). stop closes this connection only: the image serves other clients and outlives it.
func SocketDialer(l *lispimage.Launcher) Dialer {
	return func() (io.Reader, io.Writer, func() error, error) {
		conn, err := l.Connect()
		if err != nil {
			return nil, nil, nil, err
		}
		var once sync.Once
		var closeErr error
		stop := func() error {
			once.Do(func() { closeErr = conn.Close() })
			return closeErr
		}
		return conn, conn, stop, nil
	}
}

// Start connects now (otherwise the first event does) and returns the memory tool's spec
// the engine reports.
func (c *Client) Start() (ToolSpec, error) {
	if _, err := c.connection(); err != nil {
		return ToolSpec{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.tool, nil
}

// connection is the live peer, starting the engine when there is none or it has exited.
func (c *Client) connection() (*Peer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.peer != nil {
		select {
		case <-c.peer.Done():
			if c.stop != nil {
				_ = c.stop()
			}
			c.peer, c.stop = nil, nil
		default:
			return c.peer, nil
		}
	}
	r, w, stop, err := c.dial()
	if err != nil {
		return nil, err
	}
	peer := NewPeer(r, w, c.handle)
	go func() { _ = peer.Run() }()
	var result InitializeResult
	if err := peer.Call(context.Background(), MethodInitialize, c.params, &result); err != nil {
		_ = stop()
		<-peer.Done()
		return nil, fmt.Errorf("enginerpc: initialize: %w", err)
	}
	if c.requireBuild != "" && result.BuildID != c.requireBuild {
		// A stale image (om upgraded under it) is stopped, so the next event starts the
		// current one instead of failing over until the stale one idles out.
		stopCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		_ = peer.Call(stopCtx, MethodImageStop, struct{}{}, nil)
		cancel()
		_ = stop()
		<-peer.Done()
		return nil, fmt.Errorf("enginerpc: the engine reports build %q, not %q: a stale image (stopped; the next event starts the current one)", result.BuildID, c.requireBuild)
	}
	c.peer, c.stop, c.tool = peer, stop, result.Tool
	return peer, nil
}

// Close shuts the engine down and waits for it.
func (c *Client) Close() error {
	c.mu.Lock()
	peer, stop := c.peer, c.stop
	c.peer, c.stop = nil, nil
	c.mu.Unlock()
	if peer == nil {
		return nil
	}
	_ = peer.Call(context.Background(), MethodShutdown, struct{}{}, nil)
	var err error
	if stop != nil {
		err = stop()
	}
	<-peer.Done()
	return err
}

// requestContext is h's request cancellation (the SDK host's RequestContext), else none.
func requestContext(h host.Host) context.Context {
	if rc, ok := h.(interface{ RequestContext() context.Context }); ok {
		if ctx := rc.RequestContext(); ctx != nil {
			return ctx
		}
	}
	return context.Background()
}

// scope registers h for the engine's host calls during (and after) one event.
func (c *Client) scope(h host.Host) Scope {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextScope++
	id := strconv.FormatInt(c.nextScope, 10)
	c.scopes[id] = h
	c.order = append(c.order, id)
	if len(c.order) > scopeRetention {
		delete(c.scopes, c.order[0])
		delete(c.acted, c.order[0])
		c.order = c.order[1:]
	}
	c.latest = h
	return Scope{ID: id, Cwd: h.Cwd(), HasUI: h.HasUI()}
}

func (c *Client) hostOf(id string) (host.Host, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if h, ok := c.scopes[id]; ok {
		return h, nil
	}
	if c.latest != nil {
		return c.latest, nil
	}
	return nil, errors.New("enginerpc: no host for scope " + id)
}

// event sends one event under h's request cancellation, and runs it on the fallback engine
// when the engine failed it before acting (SetFallback).
func (c *Client) event(h host.Host, method string, params func(Scope) any, result any) error {
	err := c.send(h, method, params, result)
	if fallback, ok := c.failover(method, err); ok {
		return fallback.event(h, method, params, result)
	}
	return err
}

// send sends one event; an error from before the engine acted is a *FailoverError.
func (c *Client) send(h host.Host, method string, params func(Scope) any, result any) error {
	peer, err := c.connection()
	if err != nil {
		return &FailoverError{Reason: "the engine is unreachable", Err: err}
	}
	scope := c.scope(h)
	err = peer.Call(requestContext(h), method, params(scope), result)
	c.mu.Lock()
	acted := c.acted[scope.ID]
	delete(c.acted, scope.ID)
	c.mu.Unlock()
	var rpcErr *Error
	if err != nil && !acted && errors.As(err, &rpcErr) && rpcErr.Code == CodeNotPorted {
		return &FailoverError{Reason: "not ported", Err: err}
	}
	return err
}

// failover is the fallback client for err, after logging why, when err is a failover and a
// fallback is set.
func (c *Client) failover(method string, err error) (*Client, bool) {
	var fo *FailoverError
	if err == nil || !errors.As(err, &fo) {
		return nil, false
	}
	c.mu.Lock()
	fallback, log := c.fallback, c.fallbackLog
	if fallback != nil {
		c.failovers++
	}
	c.mu.Unlock()
	if fallback == nil {
		return nil, false
	}
	if log != nil {
		fmt.Fprintf(log, "observational-memory: %s: %s (%v); running it on the Go engine\n", method, fo.Reason, fo.Err)
	}
	return fallback, true
}

func eventParams(scope Scope) any { return EventParams{Scope: scope} }

// SessionStart forwards session_start.
func (c *Client) SessionStart(h host.Host) error {
	return c.event(h, MethodSessionStart, eventParams, nil)
}

// ProjectTrust forwards project_trust; the engine makes no decision, it reloads its
// configuration.
func (c *Client) ProjectTrust(h host.Host) error {
	return c.event(h, MethodProjectTrust, eventParams, nil)
}

// BeforeAgentStart forwards before_agent_start and returns the system prompt sections to set.
func (c *Client) BeforeAgentStart(h host.Host) (map[string]string, error) {
	var out BeforeAgentStartResult
	err := c.event(h, MethodBeforeAgentStart, eventParams, &out)
	return out.Sections, err
}

func thinning(r EventResult) (bool, bool) {
	if r.Thinning == nil {
		return false, false
	}
	return *r.Thinning, true
}

// AgentStart forwards agent_start; thinning is whether the context handler is wanted (known
// false when the call failed).
func (c *Client) AgentStart(h host.Host) (wanted, known bool, err error) {
	var out EventResult
	err = c.event(h, MethodAgentStart, eventParams, &out)
	wanted, known = thinning(out)
	return wanted, known, err
}

// TurnEnd forwards turn_end (level-0 extraction).
func (c *Client) TurnEnd(h host.Host) (wanted, known bool, err error) {
	var out EventResult
	err = c.event(h, MethodTurnEnd, eventParams, &out)
	wanted, known = thinning(out)
	return wanted, known, err
}

// AgentSettled forwards agent_settled (the compaction trigger, the curated evidence check).
func (c *Client) AgentSettled(h host.Host) error {
	return c.event(h, MethodAgentSettled, eventParams, nil)
}

// SessionCompact forwards session_compact.
func (c *Client) SessionCompact(h host.Host) error {
	return c.event(h, MethodSessionCompact, eventParams, nil)
}

// SessionShutdown forwards session_shutdown. The engine keeps running: the harness process
// (and with it the client) outlives its sessions.
func (c *Client) SessionShutdown(h host.Host) error {
	return c.event(h, MethodSessionShutdown, eventParams, nil)
}

// BeforeCompact forwards session_before_compact's payload and returns the engine's answer in
// Pi's SessionBeforeCompactResult shape (nil declines). Cancelling h's request context sends
// $/cancelRequest; the engine still answers with a compaction on the payload's cut.
func (c *Client) BeforeCompact(h host.Host, data map[string]any) (result map[string]any, wanted, known bool, err error) {
	var out BeforeCompactResult
	err = c.event(h, MethodSessionBeforeCompact, func(scope Scope) any { return EventParams{Scope: scope, Data: data} }, &out)
	if out.Thinning != nil {
		wanted, known = *out.Thinning, true
	}
	return out.Result, wanted, known, err
}

// Context forwards a provider request's messages and applies the engine's edits in place:
// each changed message keeps its identity and takes the engine's members.
//
// The list is delivered incrementally (ContextParams): the leading messages byte-identical to
// the previous request's are not sent again. Context requests are serialized, so the engine
// holds the request the next one builds on.
func (c *Client) Context(h host.Host, messages []map[string]any) error {
	raw := make([]json.RawMessage, len(messages))
	for i, m := range messages {
		encoded, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("enginerpc: context message %d: %w", i, err)
		}
		raw[i] = encoded
	}
	c.ctxMu.Lock()
	defer c.ctxMu.Unlock()
	reuse := 0
	for reuse < len(raw) && reuse < len(c.ctxPrev) && bytes.Equal(raw[reuse], c.ctxPrev[reuse]) {
		reuse++
	}
	c.ctxSeq++
	seq, base := c.ctxSeq, c.ctxPrevSeq
	if base == 0 {
		reuse = 0
	}
	var out ContextResult
	err := c.send(h, MethodContext, func(scope Scope) any {
		return ContextParams{Scope: scope, Seq: seq, Base: base, Reuse: reuse, Messages: raw[reuse:]}
	}, &out)
	var rpcErr *Error
	if errors.As(err, &rpcErr) && rpcErr.Code == CodeStaleContext {
		err = c.send(h, MethodContext, func(scope Scope) any { return ContextParams{Scope: scope, Seq: seq, Messages: raw} }, &out)
	}
	if err != nil {
		c.ctxPrev, c.ctxPrevSeq = nil, 0
		// The fallback keeps its own previous request: the list is sent to it as it stands.
		if fallback, ok := c.failover(MethodContext, err); ok {
			return fallback.Context(h, messages)
		}
		return err
	}
	c.ctxPrev, c.ctxPrevSeq = raw, seq
	for _, edit := range out.Edits {
		if edit.Index < 0 || edit.Index >= len(messages) || messages[edit.Index] == nil || edit.Message == nil {
			continue
		}
		target := messages[edit.Index]
		clear(target)
		for k, v := range edit.Message {
			target[k] = v
		}
	}
	return nil
}

// ToolCall runs one memory tool call.
func (c *Client) ToolCall(h host.ToolHost, name string, params map[string]any) (host.ToolResult, error) {
	var out ToolCallResult
	err := c.event(h, MethodToolCall, func(scope Scope) any { return ToolCallParams{Scope: scope, Name: name, Params: params} }, &out)
	if err != nil {
		return host.ToolResult{}, err
	}
	return host.ToolResult{Text: out.Text, IsError: out.IsError, Details: out.Details}, nil
}

// Command runs one slash command (CommandStatus, CommandView); its output reaches the user
// through the host's Notify.
func (c *Client) Command(h host.Host, name, args string) error {
	return c.event(h, MethodCommand, func(scope Scope) any { return CommandParams{Scope: scope, Name: name, Args: args} }, nil)
}

// handle answers the engine's host calls.
func (c *Client) handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	var head struct {
		Scope string `json:"scope"`
	}
	if err := json.Unmarshal(params, &head); err != nil {
		return nil, &Error{Code: CodeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	h, err := c.hostOf(head.Scope)
	if err != nil {
		return nil, err
	}
	if actingMethods[method] {
		c.mu.Lock()
		c.acted[head.Scope] = true
		c.mu.Unlock()
	}
	switch method {
	case MethodGetBranch:
		p, err := decode[GetBranchParams](params)
		if err != nil {
			return nil, err
		}
		entries, err := h.GetBranch()
		if err != nil {
			return nil, err
		}
		from := 0
		if p.From > 0 && p.AnchorID != "" && len(entries) >= p.From && entries[p.From-1].ID == p.AnchorID {
			from = p.From
		}
		raw, err := encodeEntries(entries[from:])
		if err != nil {
			return nil, err
		}
		return BranchResult{From: from, Entries: raw}, nil
	case MethodGetEntries:
		reader, ok := h.(host.SessionFileReader)
		if !ok {
			return nil, errors.New("the host does not read the session file")
		}
		entries, err := reader.GetEntries()
		if err != nil {
			return nil, err
		}
		raw, err := encodeEntries(entries)
		if err != nil {
			return nil, err
		}
		return EntriesResult{Entries: raw}, nil
	case MethodAppendEntry:
		var p struct {
			CustomType string          `json:"customType"`
			Data       json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &Error{Code: CodeInvalidParams, Message: err.Error()}
		}
		return struct{}{}, h.AppendEntry(p.CustomType, p.Data)
	case MethodSessionID:
		return StringResult{Value: h.SessionID()}, nil
	case MethodSessionFile:
		return StringResult{Value: h.SessionFile()}, nil
	case MethodConfigHome:
		if homer, ok := h.(host.ConfigHomer); ok {
			return StringResult{Value: homer.ConfigHome()}, nil
		}
		return StringResult{}, nil
	case MethodNotify:
		p, err := decode[NotifyParams](params)
		if err != nil {
			return nil, err
		}
		h.Notify(p.Message, p.Level)
		return struct{}{}, nil
	case MethodIsIdle:
		idle, err := h.IsIdle()
		return BoolResult{Value: idle}, err
	case MethodCompact:
		done := make(chan error, 1)
		h.Compact(host.CompactCallbacks{
			OnComplete: func() { done <- nil },
			OnError: func(err error) {
				if err == nil {
					err = errors.New("unknown error")
				}
				done <- err
			},
		})
		return struct{}{}, <-done
	case MethodGetContextUsage:
		usage, err := h.GetContextUsage()
		if err != nil {
			return nil, err
		}
		if usage == nil {
			return ContextUsageResult{}, nil
		}
		return ContextUsageResult{Usage: &ContextUsage{Tokens: usage.Tokens, ContextWindow: usage.ContextWindow}}, nil
	case MethodProjectTrusted:
		trusted, err := h.ProjectTrusted()
		return BoolResult{Value: trusted}, err
	case MethodSessionModel:
		return ModelResult{Model: h.SessionModel()}, nil
	case MethodAllTools, MethodActiveTools, MethodSetActiveTools:
		return c.tools(h, method, params)
	}
	return c.models(ctx, h, method, params)
}

func encodeEntries(entries []session.Entry) ([]json.RawMessage, error) {
	raw := make([]json.RawMessage, len(entries))
	for i, entry := range entries {
		encoded, err := json.Marshal(entry.ToMap())
		if err != nil {
			return nil, fmt.Errorf("entry %s: %w", entry.ID, err)
		}
		raw[i] = encoded
	}
	return raw, nil
}

func (c *Client) tools(h host.Host, method string, params json.RawMessage) (any, error) {
	tools, ok := h.(host.Tools)
	if !ok {
		return nil, errors.New("the host does not manage tools")
	}
	switch method {
	case MethodAllTools:
		all, err := tools.AllTools()
		if err != nil {
			return nil, err
		}
		out := ToolsResult{Tools: make([]ToolInfo, 0, len(all))}
		for _, t := range all {
			out.Tools = append(out.Tools, ToolInfo{Name: t.Name, Source: t.Source})
		}
		return out, nil
	case MethodActiveTools:
		names, err := tools.ActiveTools()
		return NamesResult{Names: names}, err
	}
	p, err := decode[NamesParams](params)
	if err != nil {
		return nil, err
	}
	return struct{}{}, tools.SetActiveTools(p.Names)
}

func (c *Client) models(ctx context.Context, h host.Host, method string, params json.RawMessage) (any, error) {
	switch method {
	case MethodModelsFind:
		p, err := decode[FindParams](params)
		if err != nil {
			return nil, err
		}
		return ModelResult{Model: h.Models().Find(p.Provider, p.ID)}, nil
	case MethodModelsGetApiKeyAndHeaders:
		p, err := decode[ModelParams](params)
		if err != nil {
			return nil, err
		}
		auth, err := h.Models().GetApiKeyAndHeaders(p.Model)
		if err != nil {
			return nil, err
		}
		return AuthResult{Auth: authToWire(auth)}, nil
	case MethodModelsIsUsingOAuth, MethodModelsHasConfiguredAuth:
		p, err := decode[ModelParams](params)
		if err != nil {
			return nil, err
		}
		if method == MethodModelsIsUsingOAuth {
			return BoolResult{Value: h.Models().IsUsingOAuth(p.Model)}, nil
		}
		return BoolResult{Value: h.Models().HasConfiguredAuth(p.Model)}, nil
	case MethodModelsRefresh:
		p, err := decode[RefreshParams](params)
		if err != nil {
			return nil, err
		}
		return struct{}{}, h.Models().Refresh(ctx, host.RefreshOptions{AllowNetwork: p.AllowNetwork, Providers: p.Providers})
	case MethodModelsStream:
		p, err := decode[StreamParams](params)
		if err != nil {
			return nil, err
		}
		stream := h.Models().Stream(p.Model, p.Request)
		if stream == nil {
			return StreamResult{}, nil
		}
		c.mu.Lock()
		c.nextStream++
		id := strconv.FormatInt(c.nextStream, 10)
		c.streams[id] = stream
		c.mu.Unlock()
		return StreamResult{Stream: id}, nil
	case MethodModelsStreamResult:
		p, err := decode[StreamResultParams](params)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		stream := c.streams[p.Stream]
		delete(c.streams, p.Stream)
		c.mu.Unlock()
		if stream == nil {
			return nil, errors.New("enginerpc: unknown stream " + p.Stream)
		}
		events := 0
		for range stream.Events(ctx) {
			events++
		}
		if ctx.Err() != nil {
			return StreamMessageResult{Events: events}, nil
		}
		return StreamMessageResult{Message: stream.Result(), Events: events}, nil
	}
	return nil, &Error{Code: CodeMethodNotFound, Message: "method not found: " + method}
}
