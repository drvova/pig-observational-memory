package hooks

import (
	"fmt"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// The observer writes rows in the worker model's profile codec, and every rows decode is
// counted in that model's usage ledger.
func TestObserverUsesWorkerProfile(t *testing.T) {
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999})
	fx.h.Home = t.TempDir()
	store := curated.New(curated.DefaultDir(fx.h.Home))
	for i := 0; i < harness.MinEvidence; i++ {
		_ = store.RecordModel(curated.ModelEvent{Model: "anthropic/memory", Kind: curated.ModelKindDecode, Outcome: curated.OutcomeError, Codec: tables.Rows.Name()})
	}
	fx.obs.resolve(agents.Observed([]ledger.Observation{newObservation()}))

	fx.run()

	call := fx.obs.call(0)
	if call.Codec != tables.JSONLines || call.RecordDecode == nil || !call.LongSpec {
		t.Fatalf("observer codec %v, recorder set %v, long spec %v", call.Codec, call.RecordDecode != nil, call.LongSpec)
	}
	call.RecordDecode(agents.DecodeColumns)
	events, _ := store.ModelEvents("anthropic/memory")
	last := events[len(events)-1]
	if last.Kind != curated.ModelKindDecode || last.Outcome != curated.OutcomeColumns || last.Codec != tables.JSONLines.Name() {
		t.Fatalf("recorded %+v", last)
	}
}

// Without a store the observer keeps the default codec and records nothing.
func TestObserverWithoutStoreUsesDefaults(t *testing.T) {
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999})
	fx.obs.resolve(agents.Observed([]ledger.Observation{newObservation()}))
	fx.run()
	if call := fx.obs.call(0); call.Codec != nil || call.RecordDecode != nil || call.LongSpec {
		t.Fatalf("observer args %+v", call)
	}
}

// The compaction hook gives the summary the session model's share of the room, never more than
// the room, in the profile's codec.
func TestHookAppliesSessionModelProfile(t *testing.T) {
	observations := []ledger.Observation{}
	for i := 0; i < 1000; i++ {
		content := fmt.Sprintf("User noted fact number %d about the deployment pipeline and its staging servers.", i)
		observations = append(observations, testfix.Observation(fmt.Sprintf("%012x", 0xa00000000000+i), func(o *ledger.Observation) {
			o.SourceEntryIDs, o.Content = []string{"raw-1"}, content
			o.TokenCount = tokens.EstimateStringTokens(content) + 8
		}))
	}
	entries := []ledger.Entry{text("raw-1", "aaaa"), testfix.ObservationsRecordedEntry("om-1", observations, "raw-1")}
	model := testfix.Model("p", "m", map[string]any{"contextWindow": 100_000})
	summaryTokens := func(halvings int) (int, string) {
		rt, h := hookSetup(hookArgs{entries: entries, poolMaxTokens: 1_000_000, compactAfter: 50_000, model: model})
		h.Home = t.TempDir()
		store := curated.New(curated.DefaultDir(h.Home))
		for i := 0; i < harness.MinEvidence*halvings; i++ {
			_ = store.RecordModel(curated.ModelEvent{Model: "p/m", Kind: curated.ModelKindMemory, Outcome: curated.OutcomeShown, Target: curated.TargetSummary})
		}
		summary := compactionOf(t, runHook(rt, h, entries, "raw-1", "")).Summary
		return tokens.EstimateStringTokens(summary), summary
	}
	whole, _ := summaryTokens(0)
	quarter, summary := summaryTokens(2)
	if whole < 20_000 || quarter > 50_000/4+500 || quarter >= whole {
		t.Fatalf("whole room %d tokens, a quarter %d tokens", whole, quarter)
	}
	if !strings.Contains(summary, "omitted[") {
		t.Fatal("the smaller summary does not disclose what it omitted")
	}
}
