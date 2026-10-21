package observationalmemory

import (
	"path/filepath"
	goruntime "runtime"
	"testing"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/testfix"
)

// The hook reads Pi's session_before_compact payload as PiG sends it.
func TestDecodeBeforeCompactReadsPiPayload(t *testing.T) {
	data := testfix.ToJSONValue(map[string]any{
		"type": "session_before_compact",
		"preparation": map[string]any{
			"firstKeptEntryId": "u2", "tokensBefore": 123, "messagesToSummarize": []any{}, "turnPrefixMessages": []any{},
			"isSplitTurn": false, "fileOps": map[string]any{}, "settings": map[string]any{},
		},
		"branchEntries":      []any{map[string]any{"type": "message", "id": "u2", "parentId": nil, "timestamp": testfix.DefaultTimestamp, "message": map[string]any{"role": "user", "content": "hi"}}},
		"customInstructions": "Keep the deploy checklist.",
		"reason":             "threshold",
		"willRetry":          false,
	}).(map[string]any)

	event := decodeBeforeCompact(data)

	if event.FirstKeptEntryID != "u2" || event.TokensBefore != 123 || event.Reason != "threshold" || event.CustomInstructions != "Keep the deploy checklist." || len(event.BranchEntries) != 1 || event.BranchEntries[0].ID != "u2" {
		t.Fatalf("event %+v", event)
	}
	if event := decodeBeforeCompact(map[string]any{}); event.FirstKeptEntryID != "" || len(event.BranchEntries) != 0 {
		t.Fatalf("empty payload %+v", event)
	}
}

// PiG identifies a factory by its directory name: Name must match it.
func TestNameMatchesFactoryDirectory(t *testing.T) {
	_, file, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("no caller information")
	}
	if dir := filepath.Base(filepath.Dir(file)); dir != Name {
		t.Fatalf("Name %q does not match the factory directory %q", Name, dir)
	}
}
