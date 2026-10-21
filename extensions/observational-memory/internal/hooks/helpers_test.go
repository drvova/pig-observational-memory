package hooks

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/testfix"
)

const testTimeout = 10 * time.Second

// The idle re-poll waits between attempts in production; tests do not.
func init() { IdleRecheckDelay = 0 }

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	if done == nil {
		return
	}
	select {
	case <-done:
	case <-time.After(testTimeout):
		t.Fatal("timed out waiting for the background run")
	}
}

// expectNeverClosed asserts ch stays open for a short grace period: the
// goroutine that would close it has had time to run and chose not to.
func expectNeverClosed(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
		t.Fatal("expected the channel to stay open")
	case <-time.After(100 * time.Millisecond):
	}
}

func text(id, content string) ledger.Entry { return testfix.TextCustomMessage(id, content) }

func userMessage(id string, content ...string) ledger.Entry {
	body := "user " + id
	if len(content) > 0 {
		body = content[0]
	}
	return testfix.RawMessage(id, body)
}

func assistantMessage(id string, content ...string) ledger.Entry {
	body := "assistant " + id
	if len(content) > 0 {
		body = content[0]
	}
	return testfix.RawMessage(id, body, map[string]any{
		"message": map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": body}}, "stopReason": "end_turn"},
	})
}

func toolResultMessage(id string, content ...string) ledger.Entry {
	body := "tool result " + id
	if len(content) > 0 {
		body = content[0]
	}
	return testfix.RawMessage(id, body, map[string]any{
		"message": map[string]any{"role": "toolResult", "toolCallId": "call-" + id, "toolName": "bash", "content": []any{map[string]any{"type": "text", "text": body}}},
	})
}

func coverage(id, coversUpToID, observationID string) ledger.Entry {
	return testfix.ObservationsRecordedEntry(id, []ledger.Observation{testfix.Observation(observationID, func(o *ledger.Observation) {
		o.SourceEntryIDs = []string{coversUpToID}
		o.TokenCount = 10
	})}, coversUpToID)
}

// laggingBranch is two full turns, then a third turn whose tail the observer
// has not reached:
//
//	0 u0  1 a0  2 u1  3 a1  4 t1  5 a2  6 u2  7 om(covers a0)  8 om(covers a1)
//
// Pi proposes to keep from u2; t1 and a2 are unobserved.
func laggingBranch(t1Text ...string) []ledger.Entry {
	return []ledger.Entry{
		userMessage("u0"),
		assistantMessage("a0"),
		userMessage("u1"),
		assistantMessage("a1"),
		toolResultMessage("t1", t1Text...),
		assistantMessage("a2"),
		userMessage("u2"),
		coverage("om-a0", "a0", "aaaaaaaaaaaa"),
		coverage("om-a1", "a1", "bbbbbbbbbbbb"),
	}
}

func observationIDs(observations []ledger.Observation) []string { return testfix.IDs(observations) }

func hasNotification(h *testfix.FakeHost, message, level string) bool {
	for _, n := range h.Notifications {
		if n.Message == message && n.Level == level {
			return true
		}
	}
	return false
}

func hasNotificationContaining(h *testfix.FakeHost, fragment, level string) bool {
	for _, n := range h.Notifications {
		if strings.Contains(n.Message, fragment) && (level == "" || n.Level == level) {
			return true
		}
	}
	return false
}

func expectNotification(t *testing.T, h *testfix.FakeHost, message, level string) {
	t.Helper()
	if !hasNotification(h, message, level) {
		t.Fatalf("missing notification %q (%s); got %+v", message, level, h.Notifications)
	}
}

func recordedObservations(observations []ledger.Observation, coversUpToID string) any {
	return testfix.ToJSONValue(map[string]any{"observations": observations, "coversUpToId": coversUpToID})
}

func recordedReflections(reflections []ledger.Reflection, coversUpToID string) any {
	return testfix.ToJSONValue(map[string]any{"reflections": reflections, "coversUpToId": coversUpToID})
}

func droppedObservations(ids []string, coversUpToID string) any {
	return testfix.ToJSONValue(map[string]any{"observationIds": ids, "coversUpToId": coversUpToID})
}

func expectAppended(t *testing.T, h *testfix.FakeHost, index int, customType string, data any) {
	t.Helper()
	if len(h.Appended) <= index {
		t.Fatalf("appended %d entries, want index %d: %+v", len(h.Appended), index, h.Appended)
	}
	got := h.Appended[index]
	if got.CustomType != customType || !reflect.DeepEqual(got.Data, data) {
		t.Fatalf("appended[%d] = %s %v, want %s %v", index, got.CustomType, got.Data, customType, data)
	}
}

// stageMock scripts a worker stage one call at a time and records its calls.
type stageMock[A any, R any] struct {
	mu    sync.Mutex
	calls []A
	ctxs  []context.Context
	queue []func(context.Context, A) (R, error)
}

func (m *stageMock[A, R]) run(ctx context.Context, args A) (R, error) {
	m.mu.Lock()
	m.calls = append(m.calls, args)
	m.ctxs = append(m.ctxs, ctx)
	var fn func(context.Context, A) (R, error)
	if len(m.queue) > 0 {
		fn = m.queue[0]
		m.queue = m.queue[1:]
	}
	m.mu.Unlock()
	if fn == nil {
		var zero R
		return zero, nil
	}
	return fn(ctx, args)
}

func (m *stageMock[A, R]) resolve(value R) {
	m.queue = append(m.queue, func(context.Context, A) (R, error) { return value, nil })
}

func (m *stageMock[A, R]) reject(err error) {
	m.queue = append(m.queue, func(context.Context, A) (R, error) { var zero R; return zero, err })
}

func (m *stageMock[A, R]) impl(fn func(context.Context, A) (R, error)) { m.queue = append(m.queue, fn) }

func (m *stageMock[A, R]) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.calls)
}

func (m *stageMock[A, R]) call(i int) A {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls[i]
}

type (
	observerMock  = stageMock[agents.ObserverArgs, agents.ObserverResult]
	reflectorMock = stageMock[agents.ReflectorArgs, []ledger.Reflection]
	dropperMock   = stageMock[agents.DropperArgs, []string]
)

// mockWorkers replaces the worker entry points for one test.
func mockWorkers(t *testing.T) (*observerMock, *reflectorMock, *dropperMock) {
	t.Helper()
	obs, ref, drop := &observerMock{}, &reflectorMock{}, &dropperMock{}
	oldObserver, oldReflector, oldDropper := runObserver, runReflector, runDropper
	runObserver, runReflector, runDropper = obs.run, ref.run, drop.run
	t.Cleanup(func() { runObserver, runReflector, runDropper = oldObserver, oldReflector, oldDropper })
	return obs, ref, drop
}
