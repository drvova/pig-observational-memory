package thin

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
)

func ms(s string) float64 {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return float64(t.UnixMilli())
}

func userAt(text string, at string) map[string]any {
	return map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": text}}, "timestamp": ms(at)}
}

func firstBlock(m map[string]any) string {
	text, _ := firstText(m["content"].([]any))
	return text
}

// The first user message of a request and each one that starts a new day get a header; one
// on the same day, an assistant message and a message with no timestamp do not, and a string
// content becomes blocks.
func TestDayHeaders(t *testing.T) {
	old := serialize.Location
	serialize.Location = time.UTC
	defer func() { serialize.Location = old }()
	summary := map[string]any{"role": "compactionSummary", "summary": "s", "timestamp": ms("2023-05-29T10:00:00Z")}
	sameDay := userAt("and another thing", "2023-05-30T18:00:00Z")
	noTime := map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "undated"}}}
	asString := map[string]any{"role": "user", "content": "how many weeks ago was that?", "timestamp": ms("2023-06-20T09:00:00Z")}
	messages := []map[string]any{
		summary,
		userAt("I went to the workshop today", "2023-05-30T09:00:00Z"),
		{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "Nice!"}}, "timestamp": ms("2023-05-31T09:00:00Z")},
		sameDay,
		noTime,
		asString,
	}
	if n := DayHeaders(messages); n != 2 {
		t.Fatalf("marked %d", n)
	}
	if got := firstBlock(messages[1]); got != "[Sent on Tuesday 2023-05-30]" {
		t.Fatalf("first header %q", got)
	}
	if got := firstBlock(sameDay); got != "and another thing" {
		t.Fatalf("same day marked: %q", got)
	}
	if got := firstBlock(noTime); got != "undated" {
		t.Fatalf("undated marked: %q", got)
	}
	blocks := asString["content"].([]any)
	if len(blocks) != 2 || firstBlock(asString) != "[Sent on Tuesday 2023-06-20]" || blocks[1].(map[string]any)["text"] != "how many weeks ago was that?" {
		t.Fatalf("string content %v", blocks)
	}
	if _, ok := messages[0]["content"]; ok {
		t.Fatal("summary touched")
	}
	// Already marked messages are left as they are.
	if n := DayHeaders(messages); n != 0 {
		t.Fatalf("marked again: %d", n)
	}
}

// A message keeps the same bytes when later messages are appended, so earlier request bytes
// never change between requests.
func TestDayHeadersAreStableAcrossRequests(t *testing.T) {
	old := serialize.Location
	serialize.Location = time.UTC
	defer func() { serialize.Location = old }()
	first := []map[string]any{userAt("a", "2023-05-30T09:00:00Z"), userAt("b", "2023-05-31T09:00:00Z")}
	DayHeaders(first)
	before, _ := json.Marshal(first)
	second := []map[string]any{userAt("a", "2023-05-30T09:00:00Z"), userAt("b", "2023-05-31T09:00:00Z"), userAt("c", "2023-05-31T10:00:00Z"), userAt("d", "2023-06-02T10:00:00Z")}
	DayHeaders(second)
	after, _ := json.Marshal(second[:2])
	if string(before) != string(after) {
		t.Fatalf("earlier messages changed:\n%s\n%s", before, after)
	}
	if firstBlock(second[2]) != "c" || firstBlock(second[3]) != "[Sent on Friday 2023-06-02]" {
		t.Fatalf("appended %v %v", second[2], second[3])
	}
}
