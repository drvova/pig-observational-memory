package serialize_test

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/session"
	tf "github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/testfix"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/tokens"
)

func customEntry(id, content string) session.Entry {
	return tf.Entry(map[string]any{"type": "custom_message", "id": id, "timestamp": "2026-05-02T10:00:00.000Z", "content": content})
}

func toolResultEntry(id, text string) session.Entry {
	ts, _ := time.Parse(time.RFC3339, "2026-05-02T10:00:00.000Z")
	return tf.Entry(map[string]any{"type": "message", "id": id, "timestamp": "2026-05-02T10:00:00.000Z", "message": map[string]any{
		"role": "toolResult", "toolCallId": "tool-1", "toolName": "bash", "content": []any{map[string]any{"type": "text", "text": text}}, "isError": false, "timestamp": ts.UnixMilli(),
	}})
}

func TestBudgetPreservesFittingBlocks(t *testing.T) {
	result := serialize.SerializeSourceAddressedBranchEntries([]session.Entry{customEntry("raw-1", "first"), customEntry("raw-2", "second")}, serialize.SourceAddressedOptions{MaxTokens: 1000})
	if !reflect.DeepEqual(result.SourceEntryIDs, []string{"raw-1", "raw-2"}) || len(result.TruncatedSourceEntryIDs) != 0 {
		t.Fatalf("ids %v truncated %v", result.SourceEntryIDs, result.TruncatedSourceEntryIDs)
	}
	if !strings.Contains(result.Text, "[Source entry id: raw-1]") || !strings.Contains(result.Text, "[Source entry id: raw-2]") || result.EstimatedTokens != tokens.EstimateStringTokens(result.Text) {
		t.Fatal("text or estimate wrong")
	}
}

func TestBudgetKeepsLaterEntriesForNextRun(t *testing.T) {
	first := customEntry("raw-1", strings.Repeat("a", 80))
	second := customEntry("raw-2", strings.Repeat("b", 80))
	firstOnly := serialize.SerializeSourceAddressedBranchEntries([]session.Entry{first}, serialize.SourceAddressedOptions{})
	result := serialize.SerializeSourceAddressedBranchEntries([]session.Entry{first, second}, serialize.SourceAddressedOptions{MaxTokens: firstOnly.EstimatedTokens})
	if !reflect.DeepEqual(result.SourceEntryIDs, []string{"raw-1"}) || len(result.TruncatedSourceEntryIDs) != 0 || strings.Contains(result.Text, "raw-2") {
		t.Fatalf("result %+v", result)
	}
}

func TestBudgetTooSmallForLabel(t *testing.T) {
	result := serialize.SerializeSourceAddressedBranchEntries([]session.Entry{customEntry("raw-1", "content")}, serialize.SourceAddressedOptions{MaxTokens: 1})
	if result.Text != "" || len(result.SourceEntryIDs) != 0 || result.EstimatedTokens != 0 || len(result.TruncatedSourceEntryIDs) != 0 {
		t.Fatalf("result %+v", result)
	}
}

func TestBudgetHeadTailExcerpt(t *testing.T) {
	source := "HEAD:" + strings.Repeat("m", 2000) + ":TAIL"
	huge := toolResultEntry("raw-huge", source)
	result := serialize.SerializeSourceAddressedBranchEntries([]session.Entry{huge, customEntry("raw-next", "later")}, serialize.SourceAddressedOptions{MaxTokens: 100})
	if !reflect.DeepEqual(result.SourceEntryIDs, []string{"raw-huge"}) || !reflect.DeepEqual(result.TruncatedSourceEntryIDs, []string{"raw-huge"}) || result.EstimatedTokens > 100 {
		t.Fatalf("result %+v", result)
	}
	for _, want := range []string{"[Source entry id: raw-huge]", "[Tool result for bash", "HEAD:", ":TAIL", "middle omitted: source exceeds observer input budget", "original source remains in the session ledger"} {
		if !strings.Contains(result.Text, want) {
			t.Fatalf("text missing %q", want)
		}
	}
	if strings.Contains(result.Text, "raw-next") {
		t.Fatal("later entry leaked into the capped chunk")
	}
	recalled, ok := serialize.RenderRecallSourceEntry(huge)
	if !ok || !strings.Contains(recalled, source) || len(recalled) <= len(result.Text) {
		t.Fatal("recall should render the original entry in full")
	}
}

func TestSerializeConversation(t *testing.T) {
	old := serialize.Location
	serialize.Location = time.UTC
	defer func() { serialize.Location = old }()
	ts, _ := time.Parse(time.RFC3339, "2026-01-15T14:30:00Z")
	messages := []map[string]any{
		tf.ToJSONValue(map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "hello"}}, "timestamp": ts.UnixMilli()}).(map[string]any),
		tf.ToJSONValue(map[string]any{"role": "assistant", "content": []any{
			map[string]any{"type": "thinking", "thinking": "hidden", "redacted": true},
			map[string]any{"type": "thinking", "thinking": "plan"},
			map[string]any{"type": "text", "text": "hi"},
			map[string]any{"type": "toolCall", "name": "bash", "arguments": map[string]any{"command": "ls"}},
		}, "timestamp": ts.UnixMilli()}).(map[string]any),
		tf.ToJSONValue(map[string]any{"role": "toolResult", "toolName": "bash", "content": []any{map[string]any{"type": "text", "text": "out"}}, "timestamp": ts.UnixMilli()}).(map[string]any),
	}
	got := serialize.SerializeConversation(messages)
	// Thinking blocks never reach the workers (upstream issue #97).
	want := "[User @ 2026-01-15 14:30]: hello\n\n[Assistant @ 2026-01-15 14:30]: hi\n[bash({\"command\":\"ls\"})]\n\n[Tool result for bash @ 2026-01-15 14:30]: out"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
	if serialize.FormatTimestamp(nil) != "????-??-?? ??:??" || serialize.FormatTimestamp("garbage") != "????-??-?? ??:??" || serialize.FormatRecallTimestamp(nil, "garbage") != "Unknown time" {
		t.Fatal("unknown timestamps wrong")
	}
	if got := serialize.FormatRecallTimestamp(nil, "2026-01-15T14:30:00.000Z"); got != "2026-01-15 14:30" {
		t.Fatalf("recall timestamp %q", got)
	}
}

func TestTruncateRecordContent(t *testing.T) {
	short := strings.Repeat("a", serialize.MaxRecordContentChars)
	if serialize.TruncateRecordContent(short) != short {
		t.Fatal("content at the limit should be untouched")
	}
	long := strings.Repeat("a", serialize.MaxRecordContentChars+5)
	got := serialize.TruncateRecordContent(long)
	if !strings.HasSuffix(got, " … [truncated 5 chars]") || !strings.HasPrefix(got, short) {
		t.Fatalf("truncated %q", got[len(got)-40:])
	}
}
