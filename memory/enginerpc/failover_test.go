package enginerpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/enginerpc"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// fakeEngine is an engine over pipes whose events answer as script says; its own host calls go
// through call. It records the events it received.
type fakeEngine struct {
	mu     sync.Mutex
	events []string
	build  string
	script func(e *fakeEngine, peer *enginerpc.Peer, method, scope string) (any, error)
}

func (e *fakeEngine) dialer() enginerpc.Dialer {
	return enginerpc.PipeDialer(func(r io.Reader, w io.Writer) error {
		var peer *enginerpc.Peer
		peer = enginerpc.NewPeer(r, w, func(ctx context.Context, method string, params json.RawMessage) (any, error) {
			if method == enginerpc.MethodInitialize {
				return enginerpc.InitializeResult{ProtocolVersion: 1, Name: "om", BuildID: e.build}, nil
			}
			if method == enginerpc.MethodShutdown {
				return struct{}{}, nil
			}
			var p enginerpc.EventParams
			_ = json.Unmarshal(params, &p)
			e.mu.Lock()
			e.events = append(e.events, method)
			e.mu.Unlock()
			return e.script(e, peer, method, p.Scope.ID)
		})
		return peer.Run()
	})
}

func (e *fakeEngine) received() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.events...)
}

func notPorted(method string) error {
	return &enginerpc.Error{Code: enginerpc.CodeNotPorted, Message: "om engine: " + method + " is not ported to the Lisp engine yet"}
}

// The fallback answers only what the engine failed before it acted: a not-ported refusal with
// no host write, an engine that cannot be reached, a stale build. After a host write the
// engine's error stands and the fallback never runs. Each fallback logs one line.
func TestFailoverBeforeTheEngineActed(t *testing.T) {
	primary := &fakeEngine{build: "b1", script: func(e *fakeEngine, peer *enginerpc.Peer, method, scope string) (any, error) {
		switch method {
		case enginerpc.MethodTurnEnd:
			return nil, notPorted(method)
		case enginerpc.MethodAgentSettled:
			// Acts, then refuses: never run again.
			if err := peer.Call(context.Background(), enginerpc.MethodAppendEntry, enginerpc.AppendEntryParams{Scope: scope, CustomType: "om.test", Data: map[string]any{}}, nil); err != nil {
				return nil, err
			}
			return nil, notPorted(method)
		case enginerpc.MethodSessionStart:
			// Reads, then refuses: a read is no act.
			if err := peer.Call(context.Background(), enginerpc.MethodSessionID, enginerpc.ScopeParams{Scope: scope}, nil); err != nil {
				return nil, err
			}
			return nil, notPorted(method)
		}
		return enginerpc.EventResult{}, nil
	}}
	fallback := &fakeEngine{script: func(*fakeEngine, *enginerpc.Peer, string, string) (any, error) { return enginerpc.EventResult{}, nil }}
	var log strings.Builder
	var logMu sync.Mutex
	lockedLog := writerFunc(func(p []byte) (int, error) { logMu.Lock(); defer logMu.Unlock(); return log.Write(p) })
	fb := enginerpc.NewClient(fallback.dialer(), enginerpc.InitializeParams{})
	c := enginerpc.NewClient(primary.dialer(), enginerpc.InitializeParams{})
	c.SetFallback(fb, lockedLog)
	t.Cleanup(func() { _ = c.Close(); _ = fb.Close() })

	h := testfix.NewFakeHost(testfix.RawMessage("u1", "hello"))
	if _, _, err := c.TurnEnd(h); err != nil {
		t.Fatalf("turn end: %v", err)
	}
	must(t, c.SessionStart(h))
	err := c.AgentSettled(h)
	var rpcErr *enginerpc.Error
	if !errors.As(err, &rpcErr) || rpcErr.Code != enginerpc.CodeNotPorted {
		t.Fatalf("agent settled after a host write: %v", err)
	}
	if len(h.Appended) != 1 {
		t.Fatalf("appended %d entries", len(h.Appended))
	}
	if got := strings.Join(fallback.received(), ","); got != "event/turnEnd,event/sessionStart" {
		t.Fatalf("the fallback ran %q", got)
	}
	logMu.Lock()
	lines := strings.Split(strings.TrimSpace(log.String()), "\n")
	logMu.Unlock()
	if len(lines) != 2 || !strings.Contains(lines[0], "event/turnEnd: not ported") || !strings.Contains(lines[1], "event/sessionStart: not ported") {
		t.Fatalf("log %q", lines)
	}

	// A stale build is a failure to start: everything runs on the fallback.
	stale := enginerpc.NewClient(primary.dialer(), enginerpc.InitializeParams{})
	stale.RequireBuild("b2")
	stale.SetFallback(fb, nil)
	t.Cleanup(func() { _ = stale.Close() })
	if _, _, err := stale.AgentStart(h); err != nil {
		t.Fatalf("stale image: %v", err)
	}
	if got := fallback.received(); got[len(got)-1] != enginerpc.MethodAgentStart {
		t.Fatalf("the fallback ran %q", got)
	}

	// An engine that cannot be reached, without a fallback, is the dial's error.
	down := enginerpc.NewClient(func() (io.Reader, io.Writer, func() error, error) { return nil, nil, nil, errors.New("no socket") }, enginerpc.InitializeParams{})
	if err := down.SessionStart(h); err == nil || err.Error() != "no socket" {
		t.Fatalf("unreachable: %v", err)
	}
	down.SetFallback(fb, nil)
	must(t, down.SessionStart(h))
}

type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }
