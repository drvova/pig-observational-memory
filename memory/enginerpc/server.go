package enginerpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"

	"github.com/drvova/pig-observational-memory/memory/commands"
	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/engine"
	"github.com/drvova/pig-observational-memory/memory/harness"
	"github.com/drvova/pig-observational-memory/memory/hooks"
	"github.com/drvova/pig-observational-memory/memory/hostinfo"
	"github.com/drvova/pig-observational-memory/memory/omtool"
	"github.com/drvova/pig-observational-memory/memory/runtime"
)

// MemorySection names the system prompt section the engine fills at before_agent_start.
const MemorySection = "curated-memory"

// Server is the engine end of the connection: one runtime, memory tool manager and curated
// index pin for the life of the process, the state an in-process adapter keeps for the life
// of its extension process. Each event runs the engine functions the in-process PiG adapter
// runs for it, in the same order, against a host that forwards every call to the adapter.
type Server struct {
	// Version is reported by initialize.
	Version string
	// DefaultDirs, when set, runs at initialize when the adapter names no directories (om
	// points the engine at its own root).
	DefaultDirs func()

	rt      *runtime.Runtime
	pin     *curated.Pin
	manager *harness.Manager
	branch  branchCache
	peer    *Peer

	mu          sync.Mutex
	initialized bool

	// ctxMu serializes context requests; ctxPristine are request ctxSeq's messages, decoded
	// and unedited, which the next request may reuse (ContextParams).
	ctxMu       sync.Mutex
	ctxSeq      int64
	ctxPristine []map[string]any
}

// NewServer is an engine with fresh state.
func NewServer() *Server {
	s := &Server{rt: runtime.New(), pin: &curated.Pin{}}
	s.manager = engine.NewManager(s.rt, s.pin)
	return s
}

// Runtime is the engine's runtime (tests read its state).
func (s *Server) Runtime() *runtime.Runtime { return s.rt }

// Serve answers the adapter on r and w until r ends, then stops the engine's children (the
// memory tool's namespace, a detector pass) as session_shutdown does.
func (s *Server) Serve(r io.Reader, w io.Writer) error {
	s.peer = NewPeer(r, w, s.handle)
	err := s.peer.Run()
	s.manager.Namespace().Reset()
	s.rt.StopDetectorPass(runtime.DetectorStopSessionEnd)
	return err
}

func (s *Server) host(ctx context.Context, scope Scope) *remoteHost {
	return &remoteHost{peer: s.peer, scope: scope, ctx: ctx, branch: &s.branch}
}

func decode[T any](params json.RawMessage) (T, error) {
	var v T
	if len(params) == 0 {
		return v, nil
	}
	if err := json.Unmarshal(params, &v); err != nil {
		return v, &Error{Code: CodeInvalidParams, Message: "invalid params: " + err.Error()}
	}
	return v, nil
}

func (s *Server) thinning() *bool {
	wanted := !s.rt.Config().Passive
	return &wanted
}

func (s *Server) handle(ctx context.Context, method string, params json.RawMessage) (any, error) {
	if method == MethodInitialize {
		return s.initialize(params)
	}
	if method == MethodShutdown {
		s.manager.Namespace().Reset()
		s.rt.StopDetectorPass(runtime.DetectorStopSessionEnd)
		return struct{}{}, nil
	}
	s.mu.Lock()
	ready := s.initialized
	s.mu.Unlock()
	if !ready {
		return nil, &Error{Code: CodeInvalidRequest, Message: "not initialized"}
	}
	switch method {
	case MethodToolRenderCall:
		p, err := decode[RenderCallParams](params)
		if err != nil {
			return nil, err
		}
		return LinesResult{Lines: strings.Split(omtool.FormatCallForTui(p.Args), "\n")}, nil
	case MethodToolRenderResult:
		p, err := decode[RenderResultParams](params)
		if err != nil {
			return nil, err
		}
		return LinesResult{Lines: strings.Split(omtool.FormatRenderedResultForTui(p.Details, p.Text, p.Expanded), "\n")}, nil
	case MethodContext:
		p, err := decode[ContextParams](params)
		if err != nil {
			return nil, err
		}
		return s.context(ctx, p)
	case MethodToolCall:
		p, err := decode[ToolCallParams](params)
		if err != nil {
			return nil, err
		}
		if p.Name != omtool.ToolName {
			return nil, &Error{Code: CodeInvalidParams, Message: "unknown tool: " + p.Name}
		}
		result := s.manager.RunMemory(s.host(ctx, p.Scope), p.Params)
		return ToolCallResult{Text: result.Text, IsError: result.IsError, Details: result.Details}, nil
	case MethodCommand:
		p, err := decode[CommandParams](params)
		if err != nil {
			return nil, err
		}
		h := s.host(ctx, p.Scope)
		switch p.Name {
		case CommandStatus:
			return struct{}{}, commands.RunStatus(s.rt, h)
		case CommandView:
			return struct{}{}, commands.RunView(s.rt, h, p.Args, nil)
		}
		return nil, &Error{Code: CodeInvalidParams, Message: "unknown command: " + p.Name}
	}
	p, err := decode[EventParams](params)
	if err != nil {
		return nil, err
	}
	h := s.host(ctx, p.Scope)
	switch method {
	case MethodSessionStart:
		// The two session_start handlers of the in-process adapter, in registration order.
		s.rt.ReloadConfig()
		engine.SessionStart(s.rt, s.manager, s.pin, h)
		return EventResult{}, nil
	case MethodProjectTrust:
		s.rt.ReloadConfig()
		return EventResult{}, nil
	case MethodBeforeAgentStart:
		index, on := engine.Index(h, s.pin)
		return BeforeAgentStartResult{Sections: map[string]string{MemorySection: engine.SectionText(index, on)}}, nil
	case MethodAgentStart:
		hooks.OnAgentStart(s.rt, h)
		return EventResult{Thinning: s.thinning()}, nil
	case MethodTurnEnd:
		hooks.OnTurnEnd(s.rt, h)
		return EventResult{Thinning: s.thinning()}, nil
	case MethodAgentSettled:
		hooks.OnAgentSettledEvent(s.rt, h)
		engine.SettleCurated(h)
		return EventResult{}, nil
	case MethodSessionBeforeCompact:
		result := engine.BeforeCompact(ctx, s.rt, h, p.Data, jsonEncodes)
		return BeforeCompactResult{Result: result, Thinning: s.thinning()}, nil
	case MethodSessionCompact:
		s.pin.Reset()
		return EventResult{}, nil
	case MethodSessionShutdown:
		s.manager.Namespace().Reset()
		s.rt.StopDetectorPass(runtime.DetectorStopSessionEnd)
		return EventResult{}, nil
	}
	return nil, &Error{Code: CodeMethodNotFound, Message: "method not found: " + method}
}

func jsonEncodes(v any) error {
	_, err := json.Marshal(v)
	return err
}

func (s *Server) initialize(params json.RawMessage) (any, error) {
	p, err := decode[InitializeParams](params)
	if err != nil {
		return nil, err
	}
	if p.ProtocolVersion != ProtocolVersion {
		return nil, &Error{Code: CodeInvalidParams, Message: fmt.Sprintf("protocol version %d is not %d", p.ProtocolVersion, ProtocolVersion)}
	}
	if p.AgentDir != "" || p.ConfigDirName != "" {
		agentDir, dirName := p.AgentDir, p.ConfigDirName
		hostinfo.AgentDir = func() (string, error) {
			if agentDir == "" {
				return "", hostinfo.ErrUnavailable
			}
			return agentDir, nil
		}
		hostinfo.ConfigDirName = func() (string, error) {
			if dirName == "" {
				return "", hostinfo.ErrUnavailable
			}
			return dirName, nil
		}
	} else if s.DefaultDirs != nil {
		s.DefaultDirs()
	}
	s.mu.Lock()
	s.initialized = true
	s.mu.Unlock()
	return InitializeResult{ProtocolVersion: ProtocolVersion, Name: "om", Version: s.Version, Tool: MemoryToolSpec()}, nil
}

// MemoryToolSpec is the memory tool's fixed text and schema (memory/omtool).
func MemoryToolSpec() ToolSpec {
	return ToolSpec{Name: omtool.ToolName, Label: omtool.Label, Description: omtool.Description, PromptSnippet: omtool.PromptSnippet, PromptGuidelines: omtool.PromptGuidelines, Parameters: omtool.Parameters}
}

// LispMemoryToolSpec is the memory tool's fixed text and schema when the SBCL image serves it
// (memory/omtool's Lisp text, docs/lisp-core.md decision 10): what the image's initialize
// answers, from lisp/src/server/toolspec.json, which TestLispToolSpec keeps equal to this.
func LispMemoryToolSpec() ToolSpec {
	return ToolSpec{Name: omtool.ToolName, Label: omtool.Label, Description: omtool.LispDescription, PromptSnippet: omtool.LispPromptSnippet, PromptGuidelines: omtool.LispPromptGuidelines, Parameters: omtool.LispParameters}
}

// context thins the request's messages (hooks.OnContext edits them in place) and sends back
// only the messages it changed. The messages a request reuses from the previous one are not
// decoded again: the engine keeps the previous request's messages as decoded, unedited, and
// each request edits deep copies (which share their strings, so the copy and the comparison
// with the unedited message cost no parse).
func (s *Server) context(ctx context.Context, p ContextParams) (any, error) {
	s.ctxMu.Lock()
	defer s.ctxMu.Unlock()
	if p.Reuse < 0 || (p.Reuse > 0 && (p.Base != s.ctxSeq || p.Base == 0 || p.Reuse > len(s.ctxPristine))) {
		return nil, &Error{Code: CodeStaleContext, Message: "stale context base"}
	}
	pristine := make([]map[string]any, 0, p.Reuse+len(p.Messages))
	pristine = append(pristine, s.ctxPristine[:p.Reuse]...)
	for _, raw := range p.Messages {
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			m = nil
		}
		pristine = append(pristine, m)
	}
	s.ctxSeq, s.ctxPristine = p.Seq, pristine
	if p.Seq == 0 {
		s.ctxPristine = nil
	}
	messages := make([]map[string]any, len(pristine))
	// The in-process handler sees only the messages that are objects.
	list := make([]map[string]any, 0, len(pristine))
	for i, m := range pristine {
		if m != nil {
			messages[i] = deepCopy(m).(map[string]any)
			list = append(list, messages[i])
		}
	}
	hooks.OnContext(s.rt, s.host(ctx, p.Scope), list)
	result := ContextResult{Edits: []ContextEdit{}}
	for i, m := range messages {
		if m != nil && !reflect.DeepEqual(m, pristine[i]) {
			result.Edits = append(result.Edits, ContextEdit{Index: i, Message: m})
		}
	}
	return result, nil
}

// deepCopy copies decoded JSON's objects and arrays; strings and numbers are shared.
func deepCopy(v any) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			out[k] = deepCopy(item)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			out[i] = deepCopy(item)
		}
		return out
	}
	return v
}
