package serialize_test

import (
	"strings"
	"testing"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/session"
	tf "github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/testfix"
)

// Missing or null message content renders as empty text instead of crashing
// (upstream issue #6).
func TestNullMessageContentDoesNotPanic(t *testing.T) {
	entries := []session.Entry{
		tf.MessageEntry("null-user", map[string]any{"role": "user", "content": nil}),
		tf.MessageEntry("no-content-user", map[string]any{"role": "user"}),
		tf.MessageEntry("null-block-result", map[string]any{"role": "toolResult", "toolName": "bash", "content": []any{nil, map[string]any{"type": "text", "text": "ok"}}}),
		tf.MessageEntry("null-assistant", map[string]any{"role": "assistant", "content": nil}),
	}
	text := serialize.SerializeBranchEntries(entries)
	if !strings.Contains(text, "[User @ ") || !strings.Contains(text, "[Tool result for bash @ ") || !strings.Contains(text, "ok") {
		t.Fatalf("text %q", text)
	}
	for _, entry := range entries {
		_, _ = serialize.RenderRecallSourceEntry(entry)
	}
	_ = serialize.SerializeSourceAddressedBranchEntries(entries, serialize.SourceAddressedOptions{MaxTokens: 100})
}
