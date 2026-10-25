package thin_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/memquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/thin"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

func user(id, text string) session.Entry {
	return session.Entry{Type: "message", ID: id, Message: map[string]any{"role": "user", "content": text}}
}

func call(id, callID, tool string, args map[string]any) session.Entry {
	return session.Entry{Type: "message", ID: id, Message: map[string]any{"role": "assistant", "content": []any{
		map[string]any{"type": "toolCall", "id": callID, "name": tool, "arguments": args},
	}}}
}

func reply(id, text string) session.Entry {
	return session.Entry{Type: "message", ID: id, Message: map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": text}}}}
}

func result(id, callID, tool, text string, isError bool) session.Entry {
	return session.Entry{Type: "message", ID: id, Message: map[string]any{
		"role": "toolResult", "toolCallId": callID, "toolName": tool, "isError": isError,
		"content": []any{map[string]any{"type": "text", "text": text}},
	}}
}

func compaction(id, firstKept, summary string, tokensBefore int) session.Entry {
	return session.Entry{Type: "compaction", ID: id, FirstKeptEntryID: firstKept, Summary: summary, Raw: map[string]any{"tokensBefore": float64(tokensBefore)}}
}

var big = strings.Repeat("output line\n", 400)

// branch is a session with one compaction: u0..r0 were cut, u1..a3 retained, u2..r3 came after.
func branch() []session.Entry {
	return []session.Entry{
		user("u0", "start"), call("a0", "c0", "bash", map[string]any{"command": "ls"}), result("r0", "c0", "bash", big, false),
		user("u1", "run the tests"),
		call("a1", "c1", "bash", map[string]any{"command": "go test ./..."}), result("r1", "c1", "bash", "FAIL x_test.go\n"+big, true),
		call("a2", "c2", "read", map[string]any{"path": "/repo/x.go"}), result("r2", "c2", "read", big, false),
		reply("a3", "Fixed."),
		compaction("k1", "u1", "summary", 0),
		user("u2", "again"), call("a4", "c3", "bash", map[string]any{"command": "go vet"}), result("r3", "c3", "bash", big, false),
	}
}

// tight is an input whose window leaves the post-compaction context inside the headroom, so
// every candidate is thinned.
func tight(entries []session.Entry) thin.Input {
	return thin.Input{Entries: entries, ContextWindow: 1_000, ReserveTokens: 900, Cwd: "/repo"}
}

func contextOf(entries []session.Entry) []map[string]any {
	out := []map[string]any{}
	for _, entry := range entries {
		if entry.Type == "message" {
			out = append(out, deepCopy(entry.Message))
		}
	}
	return out
}

func deepCopy(m map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	return out
}

func TestThinsAnsweredRetainedResultsOnly(t *testing.T) {
	entries := branch()
	plan := thin.Compute(tight(entries))
	// c2 is the newest round: the final "Fixed." answered it, but it stays whole.
	if len(plan) != 1 || plan["c1"] == "" || plan["c2"] != "" {
		t.Fatalf("plan %v", plan)
	}
	if !strings.Contains(plan["c1"], `bash "go test ./..." failed: FAIL x_test.go`) || !strings.Contains(plan["c1"], `memory "recall r1"`) {
		t.Fatalf("c1 thinned to %q", plan["c1"])
	}
	// The read the final answer came from, thinned once a later round answered after it.
	later := append(append([]session.Entry{}, entries[:9]...), call("a9", "c9", "bash", map[string]any{"command": "go test ./..."}), result("r9", "c9", "bash", big, false), reply("a10", "Green."), compaction("k1", "u1", "summary", 0))
	if got := thin.Compute(tight(later)); !strings.Contains(got["c2"], `read "x.go" worked`) || got["c9"] != "" {
		t.Fatalf("plan with a later round %v", got)
	}
	// The query the thinned text names returns the original.
	if res := memquery.Run(memquery.Env{Entries: entries}, "recall r1"); res.IsError || !strings.Contains(res.Text, "FAIL x_test.go") || !strings.Contains(res.Text, "output line") {
		t.Fatalf("recall: %+v", res)
	}

	messages := contextOf(entries)
	if n := thin.Apply(messages, plan); n != 1 {
		t.Fatalf("applied %d", n)
	}
	for _, m := range messages {
		if m["role"] != "toolResult" {
			continue
		}
		id, _ := m["toolCallId"].(string)
		text := m["content"].([]any)[0].(map[string]any)["text"].(string)
		thinned := strings.HasPrefix(text, "[thinned] ")
		if thinned != (id == "c1") {
			t.Fatalf("%s thinned %v: %q", id, thinned, text)
		}
		if id == "c1" && m["isError"] != true {
			t.Fatal("thinning changed a field other than the content")
		}
	}
	// The session's entries are untouched.
	if !strings.Contains(entries[5].Message["content"].([]any)[0].(map[string]any)["text"].(string), "output line") {
		t.Fatal("the branch entry was edited")
	}
}

func TestByteStableUntilTheNextCompaction(t *testing.T) {
	entries := branch()
	first, second := thin.Compute(tight(entries)), thin.Compute(tight(entries))
	if !reflect.DeepEqual(first, second) {
		t.Fatal("two calls differ")
	}
	a, b := contextOf(entries), contextOf(entries)
	thin.Apply(a, first)
	thin.Apply(b, second)
	if !reflect.DeepEqual(a, b) {
		t.Fatal("two applications differ")
	}
	// More rounds after the compaction change nothing: r3 is answered now but landed after it.
	grown := append(append([]session.Entry{}, entries...), reply("a5", "done"), user("u3", "next"), call("a6", "c4", "bash", map[string]any{"command": "make"}), result("r4", "c4", "bash", big, false), reply("a7", "ok"))
	if got := thin.Compute(tight(grown)); !reflect.DeepEqual(got, first) {
		t.Fatalf("plan changed without a compaction: %v", got)
	}
	// A new compaction keeping u2 onward thins what it retained and answered.
	compacted := append(append([]session.Entry{}, grown...), compaction("k2", "u2", "summary two", 0))
	got := thin.Compute(tight(compacted))
	if reflect.DeepEqual(got, first) || got["c3"] == "" || got["c4"] != "" || got["c1"] != "" {
		t.Fatalf("plan after the next compaction %v", got)
	}
}

func TestNeverTheNewestRoundOrAFreshResult(t *testing.T) {
	entries := []session.Entry{
		user("u0", "start"), reply("a0", "hi"),
		user("u1", "go"),
		call("a1", "c1", "bash", map[string]any{"command": "make"}), result("r1", "c1", "bash", big, false),
		call("a2", "c2", "bash", map[string]any{"command": "make test"}), result("r2", "c2", "bash", big, false),
		compaction("k1", "u1", "summary", 0),
	}
	plan := thin.Compute(tight(entries))
	if plan["c2"] != "" || plan["c1"] == "" {
		t.Fatalf("plan %v", plan)
	}
}

func TestNoCompactionNoWindowNoThinning(t *testing.T) {
	entries := branch()
	if plan := thin.Compute(tight(entries[:9])); plan != nil {
		t.Fatalf("thinned before any compaction: %v", plan)
	}
	in := tight(entries)
	in.ContextWindow = 0
	if plan := thin.Compute(in); plan != nil {
		t.Fatalf("thinned with an unknown window: %v", plan)
	}
}

func TestMemoryToolResultsAreNotThinned(t *testing.T) {
	entries := branch()
	entries[6] = call("a2", "c2", memquery.ToolName, map[string]any{"query": "notes"})
	entries[7] = result("r2", "c2", memquery.ToolName, big, false)
	in := tight(entries)
	in.SkipTools = []string{memquery.ToolName}
	if plan := thin.Compute(in); plan["c2"] != "" || plan["c1"] == "" {
		t.Fatalf("plan %v", plan)
	}
}

// Thinning stops once the context after the compaction is out of the headroom: below PiG's
// threshold less the largest per-run growth.
func TestStopsOnceOutOfTheHeadroom(t *testing.T) {
	entries := branch()
	resultTokens := tokens.EstimateEntryTokens(entries[7])
	summary := tokens.EstimateStringTokens("summary")
	retained := 0
	for _, entry := range entries[3:9] {
		retained += tokens.EstimateEntryTokens(entry)
	}
	next := summary + retained
	in := thin.Input{Entries: entries, Cwd: "/repo"}

	// Plenty of room: nothing is thinned.
	in.ContextWindow, in.ReserveTokens = next+1_000, 1_000
	if plan := thin.Compute(in); plan != nil {
		t.Fatalf("thinned with room: %v", plan)
	}
	// Over the threshold by less than the oldest result saves: only that one is thinned.
	in.ContextWindow = next + 1_000 - resultTokens/2
	if plan := thin.Compute(in); len(plan) != 1 || plan["c1"] == "" {
		t.Fatalf("plan %v", plan)
	}

	// With tokensBefore, the context after is tokensBefore - removed + summary.
	withBefore := append([]session.Entry{}, entries...)
	removed := 0
	for _, entry := range entries[:3] {
		removed += tokens.EstimateEntryTokens(entry)
	}
	withBefore[9] = compaction("k1", "u1", "summary", 50_000)
	in.Entries = withBefore
	in.ContextWindow, in.ReserveTokens = 50_000-removed+summary+1_000, 1_000
	if plan := thin.Compute(in); plan != nil {
		t.Fatalf("thinned with room: %v", plan)
	}
	in.ContextWindow -= 1
	if plan := thin.Compute(in); len(plan) != 1 {
		t.Fatalf("plan %v", plan)
	}
}

// A result that lands after the compaction never changes the plan, even when it reuses a
// retained call id; a request holding that id twice thins neither.
func TestReusedCallIDAfterTheCompaction(t *testing.T) {
	entries := branch()
	first := thin.Compute(tight(entries))
	reused := append(append([]session.Entry{}, entries...), reply("a5", "ok"), call("a6", "c1", "bash", map[string]any{"command": "make"}), result("r4", "c1", "bash", big, false))
	if got := thin.Compute(tight(reused)); !reflect.DeepEqual(got, first) {
		t.Fatalf("a reused id after the compaction changed the plan: %v", got)
	}
	messages := contextOf(reused)
	if n := thin.Apply(messages, first); n != 0 {
		t.Fatalf("thinned %d results of a repeated id", n)
	}
}
