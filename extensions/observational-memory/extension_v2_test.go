package observationalmemory

import (
	"reflect"
	"strings"
	"testing"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
	"github.com/MichaelKinsy/PiG/extensions/sdk/json"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/hooks"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// The hook decodes PiG's whole preparation for memory v2.
func TestDecodeBeforeCompactReadsWholePreparation(t *testing.T) {
	data := testfix.ToJSONValue(map[string]any{
		"type": "session_before_compact",
		"preparation": map[string]any{
			"firstKeptEntryId": "u3", "tokensBefore": 150_000,
			"messagesToSummarize": []any{map[string]any{"role": "user", "content": "first"}, map[string]any{"role": "assistant", "content": []any{}}},
			"turnPrefixMessages":  []any{map[string]any{"role": "user", "content": "prefix"}},
			"isSplitTurn":         true,
			"previousSummary":     "earlier summary",
			"fileOps":             map[string]any{"read": []any{"a.go"}, "written": []any{"b.go"}, "edited": []any{"c.go", "d.go"}},
			"settings":            map[string]any{"enabled": true, "reserveTokens": 16384, "keepRecentTokens": 20000},
		},
		"branchEntries": []any{},
		"reason":        "overflow",
	}).(map[string]any)

	event := decodeBeforeCompact(data)
	want := hooks.CompactionPreparation{
		MessagesToSummarize: []map[string]any{{"role": "user", "content": "first"}, {"role": "assistant", "content": []any{}}},
		TurnPrefixMessages:  []map[string]any{{"role": "user", "content": "prefix"}},
		IsSplitTurn:         true,
		PreviousSummary:     "earlier summary",
		FileOps:             hooks.CompactionFileOps{Read: []string{"a.go"}, Written: []string{"b.go"}, Edited: []string{"c.go", "d.go"}},
		Settings:            hooks.CompactionSettings{Enabled: true, ReserveTokens: 16384, KeepRecentTokens: 20000},
	}
	if event.FirstKeptEntryID != "u3" || event.TokensBefore != 150_000 || event.Reason != "overflow" || !reflect.DeepEqual(event.Preparation, want) {
		t.Fatalf("event %+v", event)
	}
	// Missing or malformed members stay zero.
	event = decodeBeforeCompact(map[string]any{"preparation": map[string]any{"messagesToSummarize": "x", "fileOps": 3, "settings": map[string]any{"reserveTokens": "big"}}})
	if len(event.Preparation.MessagesToSummarize) != 0 || event.Preparation.FileOps.Read != nil || event.Preparation.Settings.ReserveTokens != 0 {
		t.Fatalf("malformed preparation %+v", event.Preparation)
	}
}

// beforeCompactPayload is a session_before_compact payload whose branch decodes.
func beforeCompactPayload() map[string]any {
	return testfix.ToJSONValue(map[string]any{
		"type": "session_before_compact",
		"preparation": map[string]any{
			"firstKeptEntryId": "u2", "tokensBefore": 150_000,
			"settings": map[string]any{"enabled": true, "reserveTokens": 16384, "keepRecentTokens": 20000},
		},
		"branchEntries": []any{
			testfix.RawMessage("u1", "Fix the deploy script "+strings.Repeat("and check every step ", 20)).Raw,
			testfix.RawMessage("u2", "thanks").Raw,
		},
		"reason": "threshold",
	}).(map[string]any)
}

// The answer to a panic outside the hook is a compaction on PiG's cut built from the payload
// alone, so PiG never falls through to its summarizer; a payload that names no cut to keep
// cannot be answered, and the panic propagates.
func TestBeforeCompactRecoversPanic(t *testing.T) {
	rt := runtime.New()
	rt.SetConfig(config.Defaults)
	result, ok := recoveredBeforeCompact(rt, beforeCompactPayload())
	if !ok {
		t.Fatal("no answer")
	}
	compaction, _ := result["compaction"].(map[string]any)
	if compaction["firstKeptEntryId"] != "u2" || compaction["tokensBefore"] != 150_000 || !strings.Contains(compaction["summary"].(string), "user: Fix the deploy script and check") {
		t.Fatalf("result %+v", result)
	}
	if _, err := json.Marshal(result); err != nil {
		t.Fatal(err)
	}
	if _, ok := recoveredBeforeCompact(rt, map[string]any{"preparation": map[string]any{}}); ok {
		t.Fatal("answered a payload with no firstKeptEntryId")
	}
	// The handler itself answers the same payload with a compaction on the same cut.
	answer, err := handleBeforeCompact(sdk.Context{}, rt, beforeCompactPayload())
	if c, _ := answer.(map[string]any)["compaction"].(map[string]any); err != nil || c["firstKeptEntryId"] != "u2" {
		t.Fatalf("answer %+v, %v", answer, err)
	}
}

// Without a usable payload, the recovered event keeps the preparation's cut and size.
func TestRecoveredEventFallsBackToThePreparation(t *testing.T) {
	event := recoveredEvent(beforeCompactPayload())
	if event.FirstKeptEntryID != "u2" || event.TokensBefore != 150_000 || len(event.BranchEntries) != 2 {
		t.Fatalf("event %+v", event)
	}
}
