package ledger_test

import (
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

func usageAssistant(id string, input, cacheRead, cacheWrite int, extra ...map[string]any) ledger.Entry {
	message := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "ok"}}, "stopReason": "stop",
		"usage": map[string]any{"input": input, "output": 10, "cacheRead": cacheRead, "cacheWrite": cacheWrite, "totalTokens": input + cacheRead + cacheWrite + 10}}
	for _, e := range extra {
		for k, v := range e {
			message[k] = v
		}
	}
	return testfix.MessageEntry(id, message)
}

// The headroom is the largest growth between the last assistant messages of consecutive runs
// since the previous compaction, counting input plus cache read and cache write.
func TestNextRunHeadroom(t *testing.T) {
	cases := []struct {
		name    string
		entries []ledger.Entry
		want    int
	}{
		{"no usage", []ledger.Entry{testfix.RawMessage("u1", "hi"), testfix.RawMessage("u2", "again")}, 0},
		{"one run", []ledger.Entry{testfix.RawMessage("u1", "hi"), usageAssistant("a1", 100, 0, 0)}, 0},
		{"largest growth, last message of each run", []ledger.Entry{
			testfix.RawMessage("u1", "a"), usageAssistant("a1", 50, 0, 0), usageAssistant("a1b", 100, 0, 0),
			testfix.RawMessage("u2", "b"), usageAssistant("a2", 100, 300, 100), // 500: +400
			testfix.RawMessage("u3", "c"), usageAssistant("a3", 100, 500, 0), // 600: +100
		}, 400},
		{"negative growth ignored", []ledger.Entry{
			testfix.RawMessage("u1", "a"), usageAssistant("a1", 900, 0, 0),
			testfix.RawMessage("u2", "b"), usageAssistant("a2", 100, 0, 0),
		}, 0},
		{"failed messages carry no usage", []ledger.Entry{
			testfix.RawMessage("u1", "a"), usageAssistant("a1", 100, 0, 0),
			testfix.RawMessage("u2", "b"), usageAssistant("a2", 300, 0, 0), usageAssistant("a2e", 9000, 0, 0, map[string]any{"stopReason": "error"}),
		}, 200},
		{"only since the previous compaction", []ledger.Entry{
			testfix.RawMessage("u1", "a"), usageAssistant("a1", 100, 0, 0),
			testfix.RawMessage("u2", "b"), usageAssistant("a2", 5000, 0, 0),
			testfix.CompactionEntry("c1", testfix.CompactionArgs{FirstKeptEntryID: "u2"}),
			testfix.RawMessage("u3", "c"), usageAssistant("a3", 1000, 0, 0),
			testfix.RawMessage("u4", "d"), usageAssistant("a4", 1300, 0, 0),
		}, 300},
	}
	for _, c := range cases {
		if got := ledger.NextRunHeadroom(c.entries); got != c.want {
			t.Errorf("%s: headroom %d, want %d", c.name, got, c.want)
		}
	}
}
