package memeval

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/drvova/pig-observational-memory/memory/config"
	"github.com/drvova/pig-observational-memory/memory/hooks"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/runtime"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/testfix"
	"github.com/drvova/pig-observational-memory/memory/tokens"
)

// Window is the simulated session's context: the model's window, PiG's reserve and the
// system prompt and tools' overhead counted in every context. A compaction runs after the turn
// whose context passes Window-Reserve and keeps that turn.
type Window struct {
	Context, Reserve, Overhead, MaxOutput int
	// SummaryMaxTokens caps the summary as the compactionSummaryMaxTokens setting does; 0 is
	// the default (no cap beyond the window's).
	SummaryMaxTokens int
}

// SmallWindow is the default: small enough that the ledger outgrows the summary budget within a
// few compactions.
var SmallWindow = Window{Context: 12_000, Reserve: 2_000, Overhead: 1_500, MaxOutput: 4_000}

// Snapshot is the session right after compaction N (1-based): its summary, the compaction PiG
// persisted, and the branch with it appended.
type Snapshot struct {
	N         int
	Turn      int
	FirstKept string
	Summary   string
	Result    hooks.CompactionResult
	// Before is the branch the hook was handed; Branch is the branch after the compaction entry
	// was appended (what later snippets run over).
	Before []session.Entry
	Branch []session.Entry
}

// Run is a simulated session.
type Run struct {
	Script    Script
	Window    Window
	Snapshots []Snapshot
}

// TurnEntries are turn i's branch entries: the user message, an assistant message and a tool
// result per call, and the final reply.
func TurnEntries(t Turn, i int) []session.Entry {
	at := func(step int) map[string]any {
		return map[string]any{"timestamp": TurnTime(i).Add(time.Duration(step) * time.Second).Format("2006-01-02T15:04:05.000Z")}
	}
	out := []session.Entry{testfix.RawMessage(UserID(i), t.User, at(0))}
	for j, text := range t.Custom {
		out = append(out, testfix.TextCustomMessage(fmt.Sprintf("c%03d-%d", i, j), text, at(0)))
	}
	for j, c := range t.Calls {
		callID := fmt.Sprintf("call-%03d-%d", i, j)
		content := []any{}
		if c.Note != "" {
			content = append(content, map[string]any{"type": "text", "text": c.Note})
		}
		content = append(content, map[string]any{"type": "toolCall", "id": callID, "name": c.Name, "arguments": c.Args})
		out = append(out,
			testfix.MessageEntry(fmt.Sprintf("a%03d-%d", i, j), map[string]any{"role": "assistant", "content": content, "stopReason": "toolUse"}, at(1+2*j)),
			testfix.MessageEntry(fmt.Sprintf("r%03d-%d", i, j), map[string]any{"role": "toolResult", "toolCallId": callID, "toolName": c.Name, "content": []any{map[string]any{"type": "text", "text": c.Result}}, "isError": c.IsError}, at(2+2*j)))
	}
	out = append(out, testfix.MessageEntry(fmt.Sprintf("a%03d-end", i), map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": t.Reply}}, "stopReason": "end_turn"}, at(2+2*len(t.Calls))))
	return out
}

// UserID is turn i's user message id.
func UserID(i int) string { return fmt.Sprintf("u%03d", i) }

// Simulate runs script through the engine at level 0, a turn end after every turn and a
// compaction whenever the context passes the threshold, until maxCompactions or the script ends.
func Simulate(ctx context.Context, script Script, w Window, maxCompactions int) (Run, error) {
	return SimulateWith(ctx, script, w, maxCompactions, Sim{})
}

// Sim is what a simulated session runs with besides its script and window: Tier 0 leaves it
// empty (no store, level 0 only); the forgetting and sharing suites give it a store, a project and
// scripted level-1 replies.
type Sim struct {
	// Home is PiG's config root, where the curated store and the usage ledger live; "" is none.
	Home string
	// Cwd is the session's project directory; Session its id.
	Cwd, Session string
	// Level1 is the scripted level-1 reply for compaction n (1-based) over the branch the hook is
	// handed; nil keeps level 0 only (ConsolidateOff). The fake host serves it: no model is
	// called, but the engine logs the request as it logs any.
	Level1 func(n int, branch []session.Entry) map[string]any
	// Between runs after turn i's entries are on the branch, before its turn end (another
	// writer's change, an injected write the model makes).
	Between func(i int, branch []session.Entry)
	// Host, when set, is the fake host to run on (its entries are the branch); nil makes one.
	Host *testfix.FakeHost
}

// scriptedServed counts the scripted level-1 replies fake hosts served, process-wide: the
// requests the engine logs for them are the only requests a suite may log.
var scriptedServed atomic.Int64

// SimulateWith is Simulate with sim.
func SimulateWith(ctx context.Context, script Script, w Window, maxCompactions int, sim Sim) (run Run, err error) {
	cfg := config.Defaults
	cfg.ConsolidateOff = sim.Level1 == nil
	if w.SummaryMaxTokens > 0 {
		cfg.CompactionSummaryMaxTokens = w.SummaryMaxTokens
	}
	rt := runtime.New()
	rt.SetConfig(cfg)
	h := sim.Host
	if h == nil {
		h = testfix.NewFakeHost()
	}
	h.UI = false
	h.Home, h.ID = sim.Home, sim.Session
	if sim.Cwd != "" {
		h.WorkDir = sim.Cwd
	}
	h.Model = testfix.Model("eval", "session", map[string]any{"contextWindow": w.Context, "maxTokens": w.MaxOutput})
	h.ModelsFake.Add(h.Model, testfix.KeyAuth("none"))
	defer func() { scriptedServed.Add(int64(h.ModelsFake.Streams.Calls())) }()
	run = Run{Script: script, Window: w}
	summary := ""
	for i, t := range script.Turns {
		if ctx.Err() != nil {
			return run, ctx.Err()
		}
		h.Entries = append(h.Entries, TurnEntries(t, i)...)
		if sim.Between != nil {
			sim.Between(i, h.Entries)
		}
		hooks.OnTurnEnd(rt, h)
		used := w.Overhead + tokens.EstimateStringTokens(summary) + activeTokens(h.Entries)
		if used <= w.Context-w.Reserve {
			continue
		}
		before := append([]session.Entry{}, h.Entries...)
		event := hooks.BeforeCompactEvent{
			FirstKeptEntryID: UserID(i),
			TokensBefore:     used,
			BranchEntries:    before,
			Reason:           "threshold",
			Preparation:      hooks.CompactionPreparation{Settings: hooks.CompactionSettings{Enabled: true, ReserveTokens: w.Reserve, KeepRecentTokens: w.Context / 10}},
		}
		if sim.Level1 != nil {
			h.ModelsFake.Streams.Replies = nil
			if reply := sim.Level1(len(run.Snapshots)+1, before); reply != nil {
				h.ModelsFake.Streams.Replies = []map[string]any{reply}
			}
		}
		outcome := hooks.HandleBeforeCompact(ctx, rt, h, event)
		if outcome.Compaction == nil {
			return run, fmt.Errorf("memeval: no compaction at turn %d", i)
		}
		c := *outcome.Compaction
		n := len(run.Snapshots) + 1
		h.Entries = append(h.Entries, testfix.CompactionEntry(fmt.Sprintf("cmp%02d", n), testfix.CompactionArgs{Summary: c.Summary, FirstKeptEntryID: c.FirstKeptEntryID, Details: testfix.ToJSONValue(c.Details)}, map[string]any{"tokensBefore": c.TokensBefore, "timestamp": TurnTime(i).Add(time.Minute).Format("2006-01-02T15:04:05.000Z")}))
		summary = c.Summary
		run.Snapshots = append(run.Snapshots, Snapshot{N: n, Turn: i, FirstKept: c.FirstKeptEntryID, Summary: c.Summary, Result: c, Before: before, Branch: append([]session.Entry{}, h.Entries...)})
		if n >= maxCompactions {
			break
		}
	}
	return run, nil
}

// activeTokens is the estimate of the messages after the latest compaction's boundary.
func activeTokens(branch []session.Entry) int {
	total := 0
	for _, e := range branch[ledger.CompactionRangeStartIndex(branch):] {
		if e.Type == "message" {
			total += tokens.EstimateEntryTokens(e)
		}
	}
	return total
}
