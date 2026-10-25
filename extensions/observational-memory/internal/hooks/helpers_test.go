package hooks

import (
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
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
