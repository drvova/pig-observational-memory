package hooks

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// thinBranch is a coding turn retained by a compaction (PiG kept it whole), followed by a new
// turn whose long result landed after the compaction.
func thinBranch() []ledger.Entry {
	entries := append([]ledger.Entry{userMessage("u0", "start"), assistantMessage("a0", "hello")}, codingTurn("p", "fix the loader")...)
	entries = append(entries,
		assistantMessage("pdone", "Fixed the loader."),
		testfix.CompactionEntry("k1", testfix.CompactionArgs{FirstKeptEntryID: "pu", Summary: "summary"}),
	)
	return append(entries, codingTurn("q", "and the saver")...)
}

// contextMessages is the context PiG sends for a request on entries: the compaction summary,
// then every message from the latest compaction's firstKeptEntryId on, as fresh copies.
func contextMessages(t *testing.T, entries []ledger.Entry) []host.ContextMessage {
	t.Helper()
	start := ledger.CompactionRangeStartIndex(entries)
	out := []host.ContextMessage{{"role": "compactionSummary", "summary": "summary"}}
	for _, entry := range entries[start:] {
		if entry.Type != "message" {
			continue
		}
		raw, err := json.Marshal(entry.Message)
		if err != nil {
			t.Fatal(err)
		}
		var message host.ContextMessage
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatal(err)
		}
		out = append(out, message)
	}
	return out
}

func resultTextOf(messages []host.ContextMessage, callID string) string {
	for _, m := range messages {
		if m["role"] == "toolResult" && m["toolCallId"] == callID {
			blocks, _ := m["content"].([]any)
			block, _ := blocks[0].(map[string]any)
			text, _ := block["text"].(string)
			return text
		}
	}
	return ""
}

func TestV2ThinsAgedToolResultsWithoutRequests(t *testing.T) {
	counter := testfix.CountRequests(t)
	rt, h := v2Setup(t, thinBranch())
	h.Model = testfix.Model("anthropic", "session", map[string]any{"contextWindow": 2_000, "maxTokens": 1_000})
	entries := thinBranch()

	first := contextMessages(t, entries)
	if n := OnContext(rt, h, first); n != 1 {
		t.Fatalf("thinned %d", n)
	}
	thinned := resultTextOf(first, "pc1")
	if !strings.HasPrefix(thinned, "[thinned] bash \"go test ./...\" failed: --- FAIL: TestLoad") || !strings.Contains(thinned, `memory "recall pt1"`) {
		t.Fatalf("thinned to %q", thinned)
	}
	// The fresh turn after the compaction, and the original on the branch, stay whole.
	if !strings.Contains(resultTextOf(first, "qc1"), "loader_test.go:12") {
		t.Fatal("a result after the compaction was thinned")
	}
	branch, _ := h.GetBranch()
	if !strings.Contains(resultTextOf(contextMessages(t, branch), "pc1"), "loader_test.go:12") {
		t.Fatal("the session entry was edited")
	}

	// Byte-stable between requests.
	second := contextMessages(t, entries)
	OnContext(rt, h, second)
	a, _ := json.Marshal(first)
	b, _ := json.Marshal(second)
	if string(a) != string(b) {
		t.Fatal("two requests got different bytes")
	}
	if counter.Total() != 0 || h.ModelsFake.Streams.Calls() != 0 {
		t.Fatalf("requests %v", counter.ByStage())
	}
}

func TestThinningOffUnderV1AndPassive(t *testing.T) {
	for _, mode := range []string{"v1", "passive"} {
		rt, h := v2Setup(t, thinBranch())
		h.Model = testfix.Model("anthropic", "session", map[string]any{"contextWindow": 2_000, "maxTokens": 1_000})
		cfg := rt.Config()
		if mode == "v1" {
			cfg.MemoryEngine = config.EngineV1
		} else {
			cfg.Passive = true
		}
		rt.SetConfig(cfg)
		messages := contextMessages(t, thinBranch())
		before, _ := json.Marshal(messages)
		if n := OnContext(rt, h, messages); n != 0 {
			t.Fatalf("%s thinned %d", mode, n)
		}
		after, _ := json.Marshal(messages)
		if string(before) != string(after) {
			t.Fatalf("%s changed the context", mode)
		}
	}
}

// autoOffSetup is memory v2 with PiG's auto-compaction off, a 200,000-token window and the
// reported context at tokens.
func autoOffSetup(t *testing.T, tokens *int) (*testfix.FakeHost, func() <-chan struct{}) {
	t.Helper()
	rt, h := v2Setup(t, []ledger.Entry{userMessage("u1", "hello"), assistantMessage("a1", "hi")})
	cfg := rt.Config()
	cfg.PiAutoCompactionEnabled = false
	rt.SetConfig(cfg)
	h.Usage = &host.ContextUsage{Tokens: tokens, ContextWindow: 200_000}
	h.CompactFn = func(callbacks host.CompactCallbacks) { callbacks.OnComplete() }
	return h, func() <-chan struct{} { return MaybeTriggerCompaction(rt, h) }
}

func intPtr(n int) *int { return &n }

// With auto-compaction off, memory v2 compacts at PiG's own inequality: context > window -
// reserveTokens (PiG's default reserve here), never while usage is unknown, with no request.
func TestV2AutoCompactionOffTriggersAtPiGsThreshold(t *testing.T) {
	limit := 200_000 - config.PiGDefaultReserveTokens
	for _, c := range []struct {
		name   string
		tokens *int
		fires  bool
	}{
		{"over", intPtr(limit + 1), true},
		{"at", intPtr(limit), false},
		{"under", intPtr(limit - 10_000), false},
		{"unknown usage", nil, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			counter := testfix.CountRequests(t)
			h, trigger := autoOffSetup(t, c.tokens)
			done := trigger()
			waitDone(t, done)
			if (done != nil) != c.fires || (h.CompactCount() == 1) != c.fires {
				t.Fatalf("fired %v, compact calls %d", done != nil, h.CompactCount())
			}
			if counter.Total() != 0 {
				t.Fatalf("requests %v", counter.ByStage())
			}
		})
	}
}

// reserveTokens comes from PiG's settings with the session model's override applied, and the
// trigger never fires with PiG's auto-compaction on.
func TestV2TriggerReadsTheModelsReserveAndStaysOffWithAutoCompaction(t *testing.T) {
	rt, h := v2Setup(t, []ledger.Entry{userMessage("u1", "hello")})
	cfg := rt.Config()
	cfg.PiAutoCompactionEnabled = false
	cfg.PiCompaction = config.PiCompactionTokens{ReserveOverrides: map[string]int{"anthropic/session": 5_000}}
	rt.SetConfig(cfg)
	h.Usage = &host.ContextUsage{Tokens: intPtr(190_000), ContextWindow: 200_000}
	if done := MaybeTriggerCompaction(rt, h); done != nil {
		t.Fatal("fired under the model's own threshold (195,000)")
	}
	h.Usage = &host.ContextUsage{Tokens: intPtr(195_001), ContextWindow: 200_000}
	h.CompactFn = func(callbacks host.CompactCallbacks) { callbacks.OnComplete() }
	waitDone(t, MaybeTriggerCompaction(rt, h))
	if h.CompactCount() != 1 {
		t.Fatalf("compact calls %d", h.CompactCount())
	}
	cfg.PiAutoCompactionEnabled = true
	rt.SetConfig(cfg)
	if done := MaybeTriggerCompaction(rt, h); done != nil {
		t.Fatal("fired with PiG's auto-compaction on")
	}
}

// "Nothing to compact" suspends the trigger until the branch tip changes.
func TestV2NothingToCompactSuspendsUntilTheTipChanges(t *testing.T) {
	rt, h := v2Setup(t, []ledger.Entry{userMessage("u1", "hello"), assistantMessage("a1", "hi")})
	cfg := rt.Config()
	cfg.PiAutoCompactionEnabled = false
	rt.SetConfig(cfg)
	h.Usage = &host.ContextUsage{Tokens: intPtr(199_000), ContextWindow: 200_000}
	h.CompactFn = func(callbacks host.CompactCallbacks) {
		callbacks.OnError(errors.New("Nothing to compact (session too small)"))
	}
	waitDone(t, MaybeTriggerCompaction(rt, h))
	if h.CompactCount() != 1 {
		t.Fatalf("compact calls %d", h.CompactCount())
	}
	if done := MaybeTriggerCompaction(rt, h); done != nil {
		t.Fatal("asked again with the same tip")
	}
	branch, _ := h.GetBranch()
	if ActiveV2CompactionSuspension(rt, h, branch) == nil {
		t.Fatal("no suspension recorded")
	}
	// A v1 backoff check never reads a v2 suspension.
	if ActiveCompactionBackoff(rt, h, branch) != nil {
		t.Fatal("v1 read the v2 suspension")
	}
	h.SetBranch(append(branch, userMessage("u2", "more")))
	waitDone(t, MaybeTriggerCompaction(rt, h))
	if h.CompactCount() != 2 {
		t.Fatalf("compact calls %d after the tip changed", h.CompactCount())
	}
}

// The plan is derived once per compaction: a change between two compactions (here the window,
// as a reserveTokens edit or a forgotten defined tool would be) leaves every request's bytes as
// they were; the next compaction derives a new plan.
func TestV2ThinPlanFixedUntilTheNextCompaction(t *testing.T) {
	rt, h := v2Setup(t, thinBranch())
	h.Model = testfix.Model("anthropic", "session", map[string]any{"contextWindow": 2_000, "maxTokens": 1_000})
	entries := thinBranch()
	if n := OnContext(rt, h, contextMessages(t, entries)); n != 1 {
		t.Fatalf("thinned %d", n)
	}
	h.Model = testfix.Model("anthropic", "session", map[string]any{"contextWindow": 2_000_000, "maxTokens": 1_000})
	if n := OnContext(rt, h, contextMessages(t, entries)); n != 1 {
		t.Fatalf("the plan changed without a compaction: thinned %d", n)
	}
	compacted := append(append([]ledger.Entry{}, entries...), testfix.CompactionEntry("k2", testfix.CompactionArgs{FirstKeptEntryID: "pu", Summary: "summary two"}))
	h.SetBranch(compacted)
	if n := OnContext(rt, h, contextMessages(t, compacted)); n != 0 {
		t.Fatalf("the next compaction kept the old plan: thinned %d", n)
	}
}

// PiG rejecting its own invalid compaction setting suspends the trigger like "Nothing to
// compact": the same tip asks no more.
func TestV2InvalidSettingSuspendsUntilTheTipChanges(t *testing.T) {
	rt, h := v2Setup(t, []ledger.Entry{userMessage("u1", "hello"), assistantMessage("a1", "hi")})
	cfg := rt.Config()
	cfg.PiAutoCompactionEnabled = false
	rt.SetConfig(cfg)
	h.Usage = &host.ContextUsage{Tokens: intPtr(199_000), ContextWindow: 200_000}
	h.CompactFn = func(callbacks host.CompactCallbacks) {
		callbacks.OnError(errors.New(`Invalid compaction.reserveTokens setting: "20k". Expected a non-negative safe integer.`))
	}
	waitDone(t, MaybeTriggerCompaction(rt, h))
	if done := MaybeTriggerCompaction(rt, h); done != nil || h.CompactCount() != 1 {
		t.Fatalf("asked again with the same tip (%d calls)", h.CompactCount())
	}
}
