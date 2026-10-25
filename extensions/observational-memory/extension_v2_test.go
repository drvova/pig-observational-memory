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

// A panic outside the v2 hook (here the SDK context itself, which a zero sdk.Context makes
// panic) is recovered into a compaction on PiG's cut under memory v2, so PiG never falls
// through to its summarizer; under v1 it propagates exactly as before.
func TestBeforeCompactRecoversPanicUnderV2(t *testing.T) {
	rt := runtime.New()
	cfg := config.Defaults
	cfg.MemoryEngine = config.EngineV2
	rt.SetConfig(cfg)
	result, err := handleBeforeCompact(sdk.Context{}, rt, beforeCompactPayload())
	if err != nil {
		t.Fatal(err)
	}
	answer, _ := result.(map[string]any)
	compaction, _ := answer["compaction"].(map[string]any)
	if compaction["firstKeptEntryId"] != "u2" || compaction["tokensBefore"] != 150_000 || !strings.Contains(compaction["summary"].(string), "user: Fix the deploy script and check") {
		t.Fatalf("result %+v", result)
	}
	if _, err := json.Marshal(result); err != nil {
		t.Fatal(err)
	}

	// A payload that names no cut to keep cannot be answered: the panic propagates.
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("no firstKeptEntryId: the panic was swallowed")
			}
		}()
		_, _ = handleBeforeCompact(sdk.Context{}, rt, map[string]any{"preparation": map[string]any{}})
	}()

	cfg.MemoryEngine = config.EngineV1
	rt.SetConfig(cfg)
	defer func() {
		if recover() == nil {
			t.Fatal("v1: the panic was swallowed")
		}
	}()
	_, _ = handleBeforeCompact(sdk.Context{}, rt, beforeCompactPayload())
}

// Without a usable payload, the recovered event keeps the preparation's cut and size.
func TestRecoveredEventFallsBackToThePreparation(t *testing.T) {
	event := recoveredEvent(beforeCompactPayload())
	if event.FirstKeptEntryID != "u2" || event.TokensBefore != 150_000 || len(event.BranchEntries) != 2 {
		t.Fatalf("event %+v", event)
	}
}
