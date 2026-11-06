package enginerpc_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/enginerpc"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// pair is two peers over in-memory pipes.
func pair(t *testing.T, a, b enginerpc.Handler) (*enginerpc.Peer, *enginerpc.Peer) {
	t.Helper()
	ar, bw := io.Pipe()
	br, aw := io.Pipe()
	pa, pb := enginerpc.NewPeer(ar, aw, a), enginerpc.NewPeer(br, bw, b)
	go func() { _ = pa.Run() }()
	go func() { _ = pb.Run() }()
	t.Cleanup(func() {
		_ = aw.Close()
		_ = bw.Close()
		<-pa.Done()
		<-pb.Done()
	})
	return pa, pb
}

// Requests run concurrently in both directions: a handler that calls back while its own
// request is pending gets its answer, and a cancelled request is cancelled in the callee,
// which still answers.
func TestPeerCallbacksAndCancellation(t *testing.T) {
	var engine *enginerpc.Peer
	release := make(chan struct{})
	host := func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		switch method {
		case "double":
			var n int
			_ = json.Unmarshal(params, &n)
			return n * 2, nil
		case "block":
			<-ctx.Done()
			close(release)
			return nil, ctx.Err()
		}
		return nil, errors.New("no " + method)
	}
	engineHandler := func(ctx context.Context, method string, params json.RawMessage) (any, error) {
		var n int
		if err := engine.Call(ctx, "double", 21, &n); err != nil {
			return nil, err
		}
		return n + 1, nil
	}
	hostPeer, enginePeer := pair(t, host, engineHandler)
	engine = enginePeer

	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var n int
			if err := hostPeer.Call(context.Background(), "event", nil, &n); err != nil || n != 43 {
				t.Errorf("event: %d %v", n, err)
			}
		}()
	}
	wg.Wait()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- enginePeer.Call(ctx, "block", nil, nil) }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled call: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled call did not return")
	}
	<-release

	var rpcErr *enginerpc.Error
	if err := enginePeer.Call(context.Background(), "missing", nil, nil); !errors.As(err, &rpcErr) || rpcErr.Message != "no missing" {
		t.Fatalf("host error: %v", err)
	}
}

// pipeClient is a client of an in-process Server over pipes.
func pipeClient(t *testing.T) *enginerpc.Client {
	t.Helper()
	settingsDir(t)
	c := enginerpc.NewClient(enginerpc.PipeDialer(func(r io.Reader, w io.Writer) error { return enginerpc.NewServer().Serve(r, w) }), enginerpc.InitializeParams{})
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// The branch is delivered incrementally, and in full again when it is no longer an extension
// of what the engine holds (another session, a fork): level 0 over the second branch is what
// it is in-process.
func TestBranchDeliveryFollowsForks(t *testing.T) {
	c := pipeClient(t)
	first := newHost(codingTurn("p", "fix the loader"))
	_, _, err := c.TurnEnd(first)
	must(t, err)
	first.SetBranch(append(first.Entries, codingTurn("q", "and the saver")...))
	_, _, err = c.TurnEnd(first)
	must(t, err)

	// A fork: the same first entries, then a different turn.
	fork := codingTurn("p", "fix the loader")
	fork = append(fork[:4:4], codingTurn("z", "something else")...)
	got := newHost(fork)
	_, _, err = c.TurnEnd(got)
	must(t, err)
	want := newHost(fork)
	d := newInProcess()
	d.turnEnd(want)
	if !slices.Equal(got.AppendedTypes(), want.AppendedTypes()) || len(got.Appended) == 0 {
		t.Fatalf("fork: %v, in-process %v", got.AppendedTypes(), want.AppendedTypes())
	}
	a, _ := json.Marshal(got.Appended)
	b, _ := json.Marshal(want.Appended)
	if string(a) != string(b) {
		t.Fatalf("fork appended\n%s\nin-process\n%s", a, b)
	}
}

// A context request whose base the engine lost (it restarted) is sent again whole, and the
// engine started again answers it.
func TestContextResendsAfterARestart(t *testing.T) {
	settingsDir(t)
	var mu sync.Mutex
	var stops []func() error
	dial := enginerpc.PipeDialer(func(r io.Reader, w io.Writer) error { return enginerpc.NewServer().Serve(r, w) })
	c := enginerpc.NewClient(func() (io.Reader, io.Writer, func() error, error) {
		r, w, stop, err := dial()
		mu.Lock()
		stops = append(stops, stop)
		mu.Unlock()
		return r, w, stop, err
	}, enginerpc.InitializeParams{})
	t.Cleanup(func() { _ = c.Close() })
	entries := append(codingTurn("p", "fix the loader"), testfix.CompactionEntry("k1", testfix.CompactionArgs{FirstKeptEntryID: "pu", Summary: "summary"}))
	entries = append(entries, codingTurn("q", "and the saver")...)
	h := newHost(entries)
	must(t, c.SessionStart(h))
	first := contextMessages(entries)
	must(t, c.Context(h, first))
	mu.Lock()
	_ = stops[0]()
	mu.Unlock()
	second := contextMessages(entries)
	// The first call after the engine exited may find it gone; the next one starts it again.
	if err := c.Context(h, second); err != nil {
		second = contextMessages(entries)
		must(t, c.Context(h, second))
	}
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatalf("after a restart\n%s\nbefore\n%s", clip(b), clip(a))
	}
	var thinned bool
	for _, m := range first {
		if m["role"] == "toolResult" && m["toolCallId"] == "pc1" {
			blocks, _ := m["content"].([]any)
			text, _ := blocks[0].(map[string]any)["text"].(string)
			thinned = len(text) < 1000
		}
	}
	if !thinned {
		t.Fatal("the aged result was not thinned")
	}
}
