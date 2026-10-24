package hooks

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/declare"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

func layerHookBranch() []ledger.Entry {
	observations := []ledger.Observation{}
	for i := 0; i < 40; i++ {
		kind := []ledger.ObservationKind{"fact", "plan"}[i%2]
		observations = append(observations, testfix.Observation(fmt.Sprintf("%012x", 0xb00000000000+i), func(o *ledger.Observation) {
			o.SourceEntryIDs, o.Kind, o.Subject = []string{"raw-1"}, kind, []string{"baking", "travel", ""}[i%3]
			o.Content = fmt.Sprintf("User noted detail %d about the plan.", i)
		}))
	}
	return []ledger.Entry{text("raw-1", "aaaa"), testfix.ObservationsRecordedEntry("om-1", observations, "raw-1")}
}

// With only the seeds standing, the layer changes nothing: the compaction summary is the one
// rendered without any store, and the observer gets the built-in fields, no instructions of
// its own and the shipped codec. A definition in the session or worker model's layer then
// reaches the same surfaces.
func TestSeedsOnlyRenderAndObserveIdentically(t *testing.T) {
	entries := layerHookBranch()
	model := testfix.Model("p", "m", map[string]any{"contextWindow": 100_000})
	render := func(home string) string {
		rt, h := hookSetup(hookArgs{entries: entries, poolMaxTokens: 1_000_000, compactAfter: 50_000, model: model})
		h.Home = home
		return compactionOf(t, runHook(rt, h, entries, "raw-1", "")).Summary
	}
	without := render("")
	home := t.TempDir()
	store := curated.New(curated.DefaultDir(home))
	if err := store.RecordModel(curated.ModelEvent{Model: "p/m", Kind: curated.ModelKindMemory, Outcome: curated.OutcomeOK}); err != nil {
		t.Fatal(err)
	}
	if with := render(home); with != without {
		t.Fatalf("a store with only seeds changed the summary:\n%s\n---\n%s", with, without)
	}
	if !strings.HasPrefix(without, ledger.TablesUsageInstructions) {
		t.Fatalf("the seed summary is not the native tables layout:\n%s", without)
	}

	observe := func(home string) agents.ObserverArgs {
		fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999})
		fx.h.Home = home
		fx.obs.resolve(agents.Observed([]ledger.Observation{newObservation()}))
		fx.run()
		return fx.obs.call(0)
	}
	bare, seeded := observe(""), observe(t.TempDir())
	for name, call := range map[string]agents.ObserverArgs{"without a store": bare, "with seeds only": seeded} {
		if call.Schema == nil || !reflect.DeepEqual(call.Schema.Fields, agents.DefaultSchemaFields()) || call.Instructions != "" {
			t.Fatalf("%s: schema %+v instructions %q", name, call.Schema, call.Instructions)
		}
	}
	if seeded.Codec != tables.Rows {
		t.Fatalf("seeded codec %v", seeded.Codec)
	}

	// The session model's summary query and the worker model's format and schema reach the
	// hook and the observer.
	ls := store.Layer()
	if _, err := ls.Put(layer.Definition{Kind: layer.KindSummary, Name: declare.SummaryDefault, Model: "p/m", Text: "summary[1]{table,query,fields}:\n  plans,\"notes | where kind=plan\",id content"}); err != nil {
		t.Fatal(err)
	}
	custom := render(home)
	if !strings.Contains(custom, "summary query") || !strings.Contains(custom, "plans[20]{id,content}") || !strings.Contains(custom, "omitted[") {
		t.Fatalf("custom summary:\n%s", custom)
	}
	semi := tables.Format{Name: "rows", Header: tables.HeaderCounted, Delimiter: ';', Quote: '\'', Escape: '^', Escapes: "nr", Indent: 2}
	if _, err := ls.Put(layer.Definition{Kind: layer.KindFormat, Name: "rows", Model: "anthropic/memory", Text: semi.Declaration()}); err != nil {
		t.Fatal(err)
	}
	if _, err := ls.Put(layer.Definition{Kind: layer.KindSchema, Name: declare.SchemaNotes, Model: "anthropic/memory", Text: "schema[1]{field,type,values}:\n  mood,enum,happy sad"}); err != nil {
		t.Fatal(err)
	}
	defined := observe(home)
	if f, ok := tables.DeclaredFormat(defined.Codec); !ok || f.Delimiter != ';' {
		t.Fatalf("the worker's format did not reach the observer: %v", defined.Codec)
	}
	if defined.Schema == nil || strings.Join(defined.Schema.Names(), ",") != "mood" {
		t.Fatalf("the worker's schema did not reach the observer: %+v", defined.Schema)
	}
}

// A session model's summary that selects no record gives way to the seed layout for that
// compaction, and the user is told the definition caused it; the details record the codec the
// summary was written in, so it decodes later whatever the layer holds.
func TestSummaryDefinitionThatSelectsNothingFallsBack(t *testing.T) {
	entries := layerHookBranch()
	model := testfix.Model("p", "m", map[string]any{"contextWindow": 100_000})
	home := t.TempDir()
	store := curated.New(curated.DefaultDir(home))
	if _, err := store.Layer().Put(layer.Definition{Kind: layer.KindSummary, Name: declare.SummaryDefault, Model: "p/m", Text: "notes | where kind = correction"}); err != nil {
		t.Fatal(err)
	}
	rt, h := hookSetup(hookArgs{entries: entries, poolMaxTokens: 1_000_000, compactAfter: 50_000, model: model})
	h.Home = home
	result := compactionOf(t, runHook(rt, h, entries, "raw-1", ""))
	if !strings.HasPrefix(result.Summary, ledger.TablesUsageInstructions) {
		t.Fatalf("the seed layout did not stand in:\n%s", result.Summary)
	}
	found := false
	for _, n := range h.Notifications {
		found = found || (strings.Contains(n.Message, "summary/default") && strings.Contains(n.Message, "selects no record"))
	}
	if !found {
		t.Fatalf("no notice names the definition: %+v", h.Notifications)
	}
	if result.Details.SummaryCodec != "rows" || result.Details.SummaryFormat != "" {
		t.Fatalf("details codec %q format %q", result.Details.SummaryCodec, result.Details.SummaryFormat)
	}
}

// One pass reads one layer snapshot per model: a write in the middle of the pass reaches the
// next pass, not the rest of this one.
func TestPassReadsOneLayerSnapshot(t *testing.T) {
	home := t.TempDir()
	store := curated.New(curated.DefaultDir(home))
	rt, h := hookSetup(hookArgs{entries: layerHookBranch(), poolMaxTokens: 1_000_000, compactAfter: 50_000})
	h.Home = home
	cfg := rt.Config()
	worker := testfix.Model("p", "w", nil)
	put := func(text string) {
		if _, err := store.Layer().Put(layer.Definition{Kind: layer.KindInstructions, Name: "observer", Model: "p/w", Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	put("First instructions.")
	ctx := withPassLayers(context.Background())
	if got := instructionsFor(ctx, h, cfg, worker, "observer"); got != "First instructions." {
		t.Fatalf("instructions %q", got)
	}
	put("Second instructions.")
	if got := instructionsFor(ctx, h, cfg, worker, "observer"); got != "First instructions." {
		t.Fatalf("a write mid-pass reached the pass: %q", got)
	}
	if got := instructionsFor(withPassLayers(context.Background()), h, cfg, worker, "observer"); got != "Second instructions." {
		t.Fatalf("the next pass did not see the write: %q", got)
	}
}

// A held summary is proven only by a compaction it becomes: one that renders and is used is
// written, and one whose compaction delegated to Pi because the summary kept no record is
// reverted, never written.
func TestHeldSummarySettlesOnTheCompactionOutcome(t *testing.T) {
	summary := "summary[1]{table,query,fields}:\n  all,\"notes\",id content"
	model := testfix.Model("p", "m", map[string]any{"contextWindow": 100_000})
	hold := func(t *testing.T) (string, layer.Store) {
		home := t.TempDir()
		ls := curated.New(curated.DefaultDir(home)).Layer()
		if _, err := ls.Hold(layer.Definition{Kind: layer.KindSummary, Name: declare.SummaryDefault, Model: "p/m", Text: summary}); err != nil {
			t.Fatal(err)
		}
		return home, ls
	}

	t.Run("used", func(t *testing.T) {
		home, ls := hold(t)
		entries := layerHookBranch()
		rt, h := hookSetup(hookArgs{entries: entries, poolMaxTokens: 1_000_000, compactAfter: 50_000, model: model})
		h.Home = home
		compactionOf(t, runHook(rt, h, entries, "raw-1", ""))
		if held, _ := ls.Held("p/m"); len(held) != 0 {
			t.Fatalf("still held: %+v", held)
		}
		if d, err := ls.Get("p/m", layer.KindSummary, declare.SummaryDefault); err != nil || d.State != layer.StateWritten {
			t.Fatalf("not written: %+v %v", d, err)
		}
	})

	t.Run("delegated", func(t *testing.T) {
		home, ls := hold(t)
		// The summary renders, but the retained tail plus the summary (with the instructions
		// riding along) exceed the retention budget, so the hook delegates to Pi.
		entries := laggingBranch()
		rt, h := lagSetup(lagArgs{entries: entries, maxRetainedTokens: 700, model: model})
		h.Home = home
		instructions := strings.TrimSpace(strings.Repeat("Keep the deploy checklist and the open questions. ", 40))
		expectDelegated(t, HandleBeforeCompact(context.Background(), rt, h, BeforeCompactEvent{FirstKeptEntryID: "u2", TokensBefore: 123, BranchEntries: entries, Reason: "threshold", CustomInstructions: instructions}))
		if held, _ := ls.Held("p/m"); len(held) != 0 {
			t.Fatalf("still held: %+v", held)
		}
		if _, err := ls.Get("p/m", layer.KindSummary, declare.SummaryDefault); err == nil {
			t.Fatalf("a summary whose compaction delegated was written: %+v", h.Notifications)
		}
	})
}

// An observer pass that recorded nothing proves no held definition: instructions that make
// the observer record nothing stay held (never written) through the empty passes, each counted
// in the usage ledger, and are reverted once they have run through MinEvidence of them in a
// row, with the user told why.
func TestEmptyObserverPassesNeverProveAHeldDefinition(t *testing.T) {
	home := t.TempDir()
	store := curated.New(curated.DefaultDir(home))
	ls := store.Layer()
	if _, err := ls.Hold(layer.Definition{Kind: layer.KindInstructions, Name: "observer", Model: workerModel, Text: "Record nothing; reply done."}); err != nil {
		t.Fatal(err)
	}
	var last *consFixture
	for i := 0; i < rules.MinEvidence; i++ {
		fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999, quiet: true})
		fx.h.Home = home
		fx.obs.resolve(agents.ObserverResult{})
		fx.run()
		fx.expectNoAppends()
		if _, err := ls.Get(workerModel, layer.KindInstructions, "observer"); err == nil {
			t.Fatalf("pass %d: an empty pass wrote the held definition", i+1)
		}
		held, _ := ls.Held(workerModel)
		if still := len(held) == 1; still != (i < rules.MinEvidence-1) {
			t.Fatalf("pass %d: held %+v", i+1, held)
		}
		last = fx
	}
	events, _ := store.ModelEvents(workerModel)
	empty := 0
	for _, e := range events {
		if e.Kind == curated.ModelKindPass && e.Outcome == curated.OutcomeEmpty && e.Counts[rules.CountEmptyPass] == 1 {
			empty++
		}
	}
	if empty != rules.MinEvidence {
		t.Fatalf("counted %d empty passes in %+v", empty, events)
	}
	found := false
	for _, n := range last.h.Notifications {
		found = found || (strings.Contains(n.Message, "was reverted") && strings.Contains(n.Message, "recorded nothing"))
	}
	if !found {
		t.Fatalf("no revert notice: %+v", last.h.Notifications)
	}
}
