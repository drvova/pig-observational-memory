package serialize_test

import (
	"strings"
	"testing"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/session"
	tf "github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/testfix"
)

// Shell executions keep their command and output for the observer and recall.
func TestSerializeBashExecution(t *testing.T) {
	entry := tf.MessageEntry("bash-1", map[string]any{"role": "bashExecution", "command": "ls -la", "output": "total 0", "exitCode": 1, "truncated": true, "timestamp": 1777716000000})
	text := serialize.SerializeBranchEntries([]session.Entry{entry})
	if !strings.Contains(text, "[Bash @ ") || !strings.Contains(text, "$ ls -la\ntotal 0\n(exit code 1; output truncated)") {
		t.Fatalf("observer text %q", text)
	}
	recall, ok := serialize.RenderRecallSourceEntry(entry)
	if !ok || !strings.Contains(recall, "$ ls -la") {
		t.Fatalf("recall %q ok %v", recall, ok)
	}
	if !serialize.ObserverRenders(entry) {
		t.Fatal("bash execution should render for the observer")
	}
	empty := tf.RawMessage("empty", "", map[string]any{"message": map[string]any{"role": "assistant", "content": []any{}, "stopReason": "aborted"}})
	if serialize.ObserverRenders(empty) || !serialize.ObserverRenders(tf.TextCustomMessage("raw-1", "aaaa")) {
		t.Fatal("renderability misjudged")
	}
}
