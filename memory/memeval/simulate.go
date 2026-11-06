package memeval

import (
	"context"
	"fmt"
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
	cfg := config.Defaults
	cfg.ConsolidateOff = true
	rt := runtime.New()
	rt.SetConfig(cfg)
	h := testfix.NewFakeHost()
	h.UI = false
	h.Model = testfix.Model("eval", "session", map[string]any{"contextWindow": w.Context, "maxTokens": w.MaxOutput})
	h.ModelsFake.Add(h.Model, testfix.KeyAuth("none"))
	run := Run{Script: script, Window: w}
	summary := ""
	for i, t := range script.Turns {
		if ctx.Err() != nil {
			return run, ctx.Err()
		}
		h.Entries = append(h.Entries, TurnEntries(t, i)...)
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
