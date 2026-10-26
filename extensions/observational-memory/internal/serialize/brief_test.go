package serialize_test

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

func briefRange() []session.Entry {
	return []session.Entry{
		testfix.RawMessage("u1", "Fix the failing\ndeploy script please"),
		testfix.MessageEntry("a1", map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []any{
			map[string]any{"type": "thinking", "thinking": "secret plan"},
			map[string]any{"type": "text", "text": "Looking at it."},
			map[string]any{"type": "toolCall", "id": "c1", "name": "bash", "arguments": map[string]any{"command": "ls scripts", "timeout": 5}},
			map[string]any{"type": "toolCall", "id": "c2", "name": "memory", "arguments": map[string]any{"query": "notes"}},
		}}),
		testfix.MessageEntry("t1", map[string]any{"role": "toolResult", "toolCallId": "c1", "toolName": "bash", "content": []any{map[string]any{"type": "text", "text": "deploy.sh huge output"}}}),
		testfix.MessageEntry("a2", map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []any{
			map[string]any{"type": "toolCall", "id": "c3", "name": "edit", "arguments": map[string]any{"path": "scripts/deploy.sh", "oldText": "a", "newText": "b"}},
			map[string]any{"type": "toolCall", "id": "c4", "name": "custom", "arguments": map[string]any{"zeta": "z", "alpha": "first"}},
		}}),
		testfix.RawMessage("u2", "thanks"),
	}
}

// The brief is the opening request and one line per tool call; thinking, text, tool bodies
// and the memory tool's calls are dropped.
func TestMinimalBriefFull(t *testing.T) {
	text, kind := serialize.MinimalBrief(briefRange(), []string{"memory"}, 10_000, 0)
	want := serialize.BriefHeading + "\n" +
		"user: Fix the failing deploy script please (u1)\n" +
		`* bash "ls scripts" (a1)` + "\n" +
		`* edit "scripts/deploy.sh" (a2)` + "\n" +
		`* custom "first" (a2)`
	if kind != serialize.BriefFull || text != want {
		t.Fatalf("kind %q\n%s\nwant\n%s", kind, text, want)
	}
	for _, dropped := range []string{"secret plan", "Looking at it", "huge output", "notes", "thanks"} {
		if strings.Contains(text, dropped) {
			t.Fatalf("brief kept %q", dropped)
		}
	}
}

// When the brief does not fit, the opening request line, clipped to fit, and an omitted
// pointer to the whole range remain; with less room, shorter pointers, down to BriefSmallest.
func TestMinimalBriefPointer(t *testing.T) {
	entries := briefRange()
	text, kind := serialize.MinimalBrief(entries, []string{"memory"}, 40, 0)
	if kind != serialize.BriefPointer || !strings.HasPrefix(text, "user: Fix the") || !strings.Contains(text, "omitted: 5 entries from 2026-05-02 to 2026-05-02 (u1 to u2)") || strings.Contains(text, "* bash") {
		t.Fatalf("kind %q text %q", kind, text)
	}
	if tokens.EstimateStringTokens(text) > 40 {
		t.Fatalf("pointer form over budget: %d tokens", tokens.EstimateStringTokens(text))
	}
	for _, c := range []struct {
		max  int
		want string
	}{
		{33, "omitted: 5 entries from 2026-05-02 to 2026-05-02 (u1 to u2); the memory tool searches them (om.History(om.Search(`<words>`)))."},
		{9, "omitted: 5 entries (u1 to u2)."},
		{5, "omitted: 5 entries."},
		{3, serialize.BriefSmallest},
	} {
		if text, kind := serialize.MinimalBrief(entries, nil, c.max, 0); kind != serialize.BriefPointer || text != c.want {
			t.Fatalf("max %d: kind %q text %q", c.max, kind, text)
		}
	}
	// No budget: the pointer form with a bounded request line.
	long := append([]session.Entry{testfix.RawMessage("u0", strings.Repeat("word ", 200))}, entries...)
	text, kind = serialize.MinimalBrief(long, nil, 0, 0)
	if kind != serialize.BriefPointer || len([]rune(strings.SplitN(text, "\n", 2)[0])) > serialize.BriefKeyArgRunes+len("user:  (u0)") {
		t.Fatalf("unbounded: kind %q text %q", kind, text)
	}
	if text, _ := serialize.MinimalBrief(nil, nil, 0, 0); text == "" {
		t.Fatal("empty range rendered nothing")
	}
}

// The ceiling (what the range carries) bounds every form, a budget of 0 or less included: a
// tiny range never gets a brief larger than itself, except BriefSmallest for a range of a
// token or two.
func TestMinimalBriefCeiling(t *testing.T) {
	entries := briefRange()
	for _, maxTokens := range []int{0, -100, 1_000} {
		text, _ := serialize.MinimalBrief(entries, nil, maxTokens, 12)
		if got := tokens.EstimateStringTokens(text); got > 12 {
			t.Fatalf("max %d: %d tokens over a ceiling of 12: %q", maxTokens, got, text)
		}
	}
	// The ceiling under the budget wins, and a budget under the ceiling too.
	if text, _ := serialize.MinimalBrief(entries, nil, 1_000, 6); text != "omitted: 5 entries." {
		t.Fatalf("ceiling under budget: %q", text)
	}
	if text, _ := serialize.MinimalBrief(entries, nil, 6, 1_000); text != "omitted: 5 entries." {
		t.Fatalf("budget under ceiling: %q", text)
	}
	tiny := []session.Entry{testfix.RawMessage("u1", "hi")}
	if text, _ := serialize.MinimalBrief(tiny, nil, -5, 1); text != serialize.BriefSmallest {
		t.Fatalf("tiny range: %q", text)
	}
}
