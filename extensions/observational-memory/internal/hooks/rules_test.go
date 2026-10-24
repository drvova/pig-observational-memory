package hooks

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// workerModel is the memory worker consSetup configures.
const workerModel = "anthropic/memory"

// withRules gives the fixture a store and adds rules for the memory worker.
func withRules(t *testing.T, fx *consFixture, rs ...rules.Rule) curated.Store {
	t.Helper()
	fx.h.Home = t.TempDir()
	store := curated.New(curated.DefaultDir(fx.h.Home))
	for _, r := range rs {
		if len(r.Models) == 0 {
			r.Models = []string{workerModel}
		}
		if _, err := rules.Add(store, r); err != nil {
			t.Fatal(err)
		}
	}
	return store
}

const specificReply = "Try the Kindle Paperwhite at 149 or the Kobo Clara at 139."

func noteOn(id, content string, sources ...string) ledger.Observation {
	return testfix.Observation(id, func(o *ledger.Observation) {
		o.Content, o.SourceEntryIDs, o.TokenCount = content, sources, 10
	})
}

func appendedObservations(t *testing.T, fx *consFixture, index int) ledger.ObservationsRecordedData {
	t.Helper()
	if len(fx.h.Appended) <= index || fx.h.Appended[index].CustomType != ledger.OMObservationsRecorded {
		t.Fatalf("appended %+v", fx.h.Appended)
	}
	data, ok := ledger.ObservationsRecordedDataFrom(fx.h.Appended[index].Data)
	if !ok {
		t.Fatalf("appended data %+v", fx.h.Appended[index].Data)
	}
	return data
}

// prompt-line rules reach the system prompt of the stage their detector judges.
func TestRulePromptLinesReachWorkerPrompts(t *testing.T) {
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, poolTargetTokens: 5, quiet: true})
	withRules(t, fx,
		rules.Rule{Detector: rules.DetectThinChunk, Remedy: rules.RemedyPromptLine, Text: "Keep the figures."},
		rules.Rule{Detector: rules.DetectDroppedThenQueried, Remedy: rules.RemedyPromptLine, Text: "Keep what is asked for."},
		rules.Rule{Detector: rules.DetectThinChunk, Remedy: rules.RemedyPromptLine, Text: "Another model's line.", Models: []string{"other/model"}},
	)
	fx.obs.resolve(agents.Observed([]ledger.Observation{obsA}))
	fx.ref.resolve([]ledger.Reflection{testfix.Reflection("ffffffffffff", []string{"aaaaaaaaaaaa"})})
	fx.drop.resolve(nil)
	fx.run()
	if got := fx.obs.call(0).SystemLines; !reflect.DeepEqual(got, []string{"Keep the figures."}) {
		t.Fatalf("observer lines %v", got)
	}
	if got := fx.ref.call(0).SystemLines; len(got) != 0 {
		t.Fatalf("reflector lines %v", got)
	}
	if got := fx.drop.call(0).SystemLines; !reflect.DeepEqual(got, []string{"Keep what is asked for."}) {
		t.Fatalf("dropper lines %v", got)
	}
}

// require-kind asks the observer once more, allowed to cite only the flagged entries, and
// records both answers under one marker.
func TestRequireKindReasksObserverOnce(t *testing.T) {
	entries := []ledger.Entry{userMessage("u1", "Which e-reader should I buy?"), assistantMessage("a1", specificReply)}
	fx := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999, quiet: true})
	withRules(t, fx, rules.Rule{Detector: rules.DetectUncitedAssistantSpecifics, Remedy: rules.RemedyRequireKind})
	first := noteOn("aaaaaaaaaaaa", "User asked which e-reader to buy.", "u1")
	second := noteOn("bbbbbbbbbbbb", "Assistant suggested the Kindle Paperwhite (149) and Kobo Clara (139).", "a1")
	second.Kind = "assistant"
	fx.obs.resolve(agents.Observed([]ledger.Observation{first}))
	fx.obs.resolve(agents.Observed([]ledger.Observation{second}))
	fx.run()
	if fx.obs.count() != 2 {
		t.Fatalf("observer calls %d", fx.obs.count())
	}
	again := fx.obs.call(1)
	if !reflect.DeepEqual(again.AllowedSourceEntryIDs, []string{"a1"}) || !strings.Contains(again.Instruction, "kind=assistant") || !strings.Contains(again.Instruction, "a1") {
		t.Fatalf("re-ask args: allowed %v instruction %q", again.AllowedSourceEntryIDs, again.Instruction)
	}
	data := appendedObservations(t, fx, 0)
	if len(data.Observations) != 2 || data.CoversUpToID != "a1" {
		t.Fatalf("recorded %+v", data)
	}
}

// keep-head keeps the opening of a flagged assistant entry only inside the coverage boundary:
// a partial run that covers through u1 keeps a1's head and leaves a2 to the next run.
func TestKeepHeadStaysInsideCoverageBoundary(t *testing.T) {
	entries := []ledger.Entry{assistantMessage("a1", specificReply), userMessage("u1", "ok thanks"), assistantMessage("a2", "The PocketBook Era is 279.")}
	fx := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999, quiet: true})
	withRules(t, fx, rules.Rule{Detector: rules.DetectUncitedAssistantSpecifics, Remedy: rules.RemedyKeepHead})
	fx.obs.resolve(agents.ObserverResult{Observations: []ledger.Observation{noteOn("aaaaaaaaaaaa", "User thanked the assistant.", "u1")}, Partial: true})
	fx.run()
	data := appendedObservations(t, fx, 0)
	if data.CoversUpToID != "u1" || len(data.Observations) != 2 {
		t.Fatalf("recorded %+v", data)
	}
	head := data.Observations[1]
	if !reflect.DeepEqual(head.SourceEntryIDs, []string{"a1"}) || head.Relevance != "low" || head.Kind != "assistant" || !strings.Contains(head.Content, "Kindle Paperwhite at 149") {
		t.Fatalf("kept head %+v", head)
	}
	for _, o := range data.Observations {
		if reflect.DeepEqual(o.SourceEntryIDs, []string{"a2"}) {
			t.Fatal("a note cites an entry past the coverage boundary")
		}
	}
}

// keep-head never turns an empty verdict into a record: nothing is appended and coverage stays.
func TestKeepHeadLeavesEmptyVerdictEmpty(t *testing.T) {
	fx := consSetup(t, consArgs{entries: []ledger.Entry{assistantMessage("a1", specificReply)}, reflectAfterTokens: 999, quiet: true})
	store := withRules(t, fx, rules.Rule{Detector: rules.DetectUncitedAssistantSpecifics, Remedy: rules.RemedyKeepHead})
	fx.obs.resolve(agents.ObserverResult{})
	fx.run()
	fx.expectNoAppends()
	// It is counted as an empty pass for probation (rules.CountEmptyPass, with what the kernel's
	// checks found in the unnoted chunk), never as an observer pass a rule is decided by.
	events, _ := store.ModelEvents(workerModel)
	for _, e := range events {
		if e.Kind == curated.ModelKindPass && (e.Outcome != curated.OutcomeEmpty || e.Counts[rules.CountEmptyPass] != 1 || e.Counts[rules.DetectUncitedAssistantSpecifics] == 0) {
			t.Fatalf("an empty verdict was recorded as %+v", e)
		}
	}
}

// The compaction catch-up applies keep-head with the same constraint.
func TestKeepHeadInCatchUp(t *testing.T) {
	entries := []ledger.Entry{
		userMessage("u0"), coverage("om-u0", "u0", "dddddddddddd"),
		assistantMessage("a1", specificReply), userMessage("u1", "thanks"), assistantMessage("a2", "The PocketBook Era is 279."),
	}
	obs, _, _ := mockWorkers(t)
	fx := catchUpSetup(t, catchUpArgs{entries: entries})
	fx.h.Home = t.TempDir()
	store := curated.New(curated.DefaultDir(fx.h.Home))
	if _, err := rules.Add(store, rules.Rule{Detector: rules.DetectUncitedAssistantSpecifics, Remedy: rules.RemedyKeepHead, Models: []string{"gtr7/memory"}}); err != nil {
		t.Fatal(err)
	}
	obs.resolve(agents.ObserverResult{Observations: []ledger.Observation{noteOn("aaaaaaaaaaaa", "User thanked the assistant.", "u1")}, Partial: true})
	obs.resolve(agents.ObserverResult{})
	result := CatchUpObserver(context.Background(), CatchUpArgs{Runtime: fx.rt, Host: fx.h, Entries: entries, Gap: ledger.UnobservedSourceSpan{FirstIndex: 2, LastIndex: 4}, MaxChunks: 2})
	if result.ChunksRecorded != 1 || len(fx.h.Appended) != 1 {
		t.Fatalf("result %+v appended %+v", result, fx.h.Appended)
	}
	data, _ := ledger.ObservationsRecordedDataFrom(fx.h.Appended[0].Data)
	if data.CoversUpToID != "u1" || len(data.Observations) != 2 || !reflect.DeepEqual(data.Observations[1].SourceEntryIDs, []string{"a1"}) {
		t.Fatalf("recorded %+v", data)
	}
	// The second, empty chunk is counted as an empty pass.
	events, _ := store.ModelEvents("gtr7/memory")
	if len(events) != 2 || events[0].Kind != curated.ModelKindPass || events[0].Tokens == 0 || events[0].Outcome == curated.OutcomeEmpty || events[1].Outcome != curated.OutcomeEmpty {
		t.Fatalf("catch-up passes %+v", events)
	}
}

// block-link drops a supersedes link between notes whose scopes differ.
func TestBlockLinkDropsCrossScopeSupersedes(t *testing.T) {
	old := testfix.Observation("aaaaaaaaaaaa", func(o *ledger.Observation) {
		o.Content, o.Scope, o.SourceEntryIDs, o.TokenCount = "User finished in 25 hours.", "normal difficulty", []string{"raw-0"}, 10
	})
	entries := []ledger.Entry{text("raw-0", "normal"), testfix.ObservationsRecordedEntry("om-0", []ledger.Observation{old}, "raw-0"), text("raw-1", "hard took 40 hours")}
	fx := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999, quiet: true})
	store := withRules(t, fx, rules.Rule{Detector: rules.DetectCrossScopeSupersede, Remedy: rules.RemedyBlockLink})
	replacement := noteOn("bbbbbbbbbbbb", "User finished in 40 hours.", "raw-1")
	replacement.Scope, replacement.Supersedes = "hard difficulty", []string{"aaaaaaaaaaaa"}
	fx.obs.resolve(agents.Observed([]ledger.Observation{replacement}))
	fx.run()
	data := appendedObservations(t, fx, 0)
	if len(data.Observations) != 1 || len(data.Observations[0].Supersedes) != 0 {
		t.Fatalf("recorded %+v", data)
	}
	// The detector counts the model's own link, not the harness's edit of it.
	events, _ := store.ModelEvents(workerModel)
	if last := events[len(events)-1]; last.Kind != curated.ModelKindPass || last.Counts[rules.DetectCrossScopeSupersede] != 1 {
		t.Fatalf("pass %+v", last)
	}
}

// protect-kind keeps the protected kind from the dropper's selection below its fullness.
func TestProtectKindKeepsKindFromDropping(t *testing.T) {
	fact := testfix.Observation("aaaaaaaaaaaa", func(o *ledger.Observation) { o.Kind, o.SourceEntryIDs, o.TokenCount = "fact", []string{"raw-1"}, 10 })
	event := testfix.Observation("bbbbbbbbbbbb", func(o *ledger.Observation) { o.Kind, o.SourceEntryIDs, o.TokenCount = "event", []string{"raw-1"}, 10 })
	entries := []ledger.Entry{text("raw-1", "aaaaaaaa"), testfix.ObservationsRecordedEntry("om-1", []ledger.Observation{fact, event}, "raw-1")}
	fx := consSetup(t, consArgs{entries: entries, observeAfterTokens: 999, poolMaxTokens: 60, poolTargetTokens: 30, quiet: true})
	withRules(t, fx, rules.Rule{Detector: rules.DetectDroppedThenQueried, Remedy: rules.RemedyProtectKind, Kind: "fact"})
	fx.ref.resolve([]ledger.Reflection{testfix.Reflection("ffffffffffff", []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb"})})
	fx.drop.resolve([]string{"aaaaaaaaaaaa", "bbbbbbbbbbbb"})
	fx.run()
	var dropped ledger.ObservationsDroppedData
	for _, a := range fx.h.Appended {
		if d, ok := ledger.ObservationsDroppedDataFrom(a.Data); ok && a.CustomType == ledger.OMObservationsDropped {
			dropped = d
		}
	}
	if !reflect.DeepEqual(dropped.ObservationIDs, []string{"bbbbbbbbbbbb"}) {
		t.Fatalf("dropped %+v (appended %+v)", dropped, fx.h.Appended)
	}
}

// split-chunk halves the observer chunk for its model.
func TestSplitChunkHalvesObserverChunk(t *testing.T) {
	entries := []ledger.Entry{}
	for _, id := range []string{"r1", "r2", "r3", "r4"} {
		entries = append(entries, text(id, strings.Repeat("word ", 40)))
	}
	chunk := func(split bool) int {
		fx := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999, chunkMaxTokens: 260, quiet: true})
		if split {
			withRules(t, fx, rules.Rule{Detector: rules.DetectThinChunk, Remedy: rules.RemedySplitChunk})
		}
		fx.obs.resolve(agents.ObserverResult{})
		fx.run()
		return len(fx.obs.call(0).AllowedSourceEntryIDs)
	}
	whole, half := chunk(false), chunk(true)
	if whole < 2 || half >= whole || half < 1 {
		t.Fatalf("chunk entries: %d whole, %d split", whole, half)
	}
}

// switch-codec and long-spec rules reach the observer through the worker's profile.
func TestSwitchRulesReachObserver(t *testing.T) {
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", "aaaaaaaa")}, reflectAfterTokens: 999, quiet: true})
	withRules(t, fx,
		rules.Rule{Detector: rules.DetectDecodeFailure, Remedy: rules.RemedySwitchCodec},
		rules.Rule{Detector: rules.DetectDecodeFailure, Remedy: rules.RemedyLongSpec},
	)
	fx.obs.resolve(agents.Observed([]ledger.Observation{newObservation()}))
	fx.run()
	if call := fx.obs.call(0); call.Codec != tables.JSONLines || !call.LongSpec {
		t.Fatalf("observer codec %v long spec %v", call.Codec, call.LongSpec)
	}
}

// Every observer pass is counted for the worker; a detector that keeps counting makes the
// harness write a rule, and the user is told.
func TestObserverPassesWriteHarnessRule(t *testing.T) {
	entries := []ledger.Entry{userMessage("u1", "Which e-reader?"), assistantMessage("a1", specificReply)}
	fx := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999, quiet: true})
	store := withRules(t, fx)
	for i := 0; i < rules.MinEvidence-1; i++ {
		_ = store.RecordModel(curated.ModelEvent{Model: workerModel, Kind: curated.ModelKindPass, Stage: rules.StageObserver, Tokens: 40, Counts: map[string]int{rules.DetectUncitedAssistantSpecifics: 1}})
	}
	fx.obs.resolve(agents.Observed([]ledger.Observation{noteOn("aaaaaaaaaaaa", "User asked about e-readers.", "u1")}))
	fx.run()
	events, _ := store.ModelEvents(workerModel)
	last := events[len(events)-1]
	if last.Kind != curated.ModelKindPass || last.Stage != rules.StageObserver || last.Tokens == 0 || last.Counts[rules.DetectUncitedAssistantSpecifics] != 1 {
		t.Fatalf("pass %+v", last)
	}
	all, _ := rules.List(store)
	if len(all) != 1 || all[0].Remedy != rules.RemedyRequireKind || all[0].Model() != workerModel {
		t.Fatalf("rules %+v", all)
	}
	if !hasNotificationContaining(fx.h, "added on probation for "+workerModel, "info") {
		t.Fatalf("notifications %+v", fx.h.Notifications)
	}
}

// keep-head's own notes never count for it: a model that keeps leaving assistant specifics
// uncited is counted for them, though keep-head cites every one, and the rule is retired. The
// baseline is the model's rate before (one per 40-token pass): counting the remedied notes
// would make the rate fall and establish the rule.
func TestKeepHeadRetiredWhenModelKeepsLeavingSpecificsUncited(t *testing.T) {
	entries := []ledger.Entry{userMessage("u1", "Which e-reader?"), assistantMessage("a1", specificReply)}
	fx := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999, quiet: true})
	store := withRules(t, fx, rules.Rule{Detector: rules.DetectUncitedAssistantSpecifics, Remedy: rules.RemedyKeepHead, BaselineEvents: 1, BaselineTokens: 40})
	for i := 0; i < rules.MinEvidence-1; i++ {
		_ = store.RecordModel(curated.ModelEvent{Model: workerModel, Kind: curated.ModelKindPass, Stage: rules.StageObserver, Tokens: 40, Counts: map[string]int{rules.DetectUncitedAssistantSpecifics: 1}})
	}
	fx.obs.resolve(agents.Observed([]ledger.Observation{noteOn("aaaaaaaaaaaa", "User asked about e-readers.", "u1")}))
	fx.run()
	data := appendedObservations(t, fx, 0)
	if len(data.Observations) != 2 || !reflect.DeepEqual(data.Observations[1].SourceEntryIDs, []string{"a1"}) {
		t.Fatalf("recorded %+v", data)
	}
	events, _ := store.ModelEvents(workerModel)
	if last := events[len(events)-1]; last.Kind != curated.ModelKindPass || last.Counts[rules.DetectUncitedAssistantSpecifics] != 1 {
		t.Fatalf("pass %+v", last)
	}
	all, _ := rules.List(store)
	var keepHead rules.Rule
	for _, r := range all {
		if r.Remedy == rules.RemedyKeepHead {
			keepHead = r
		}
	}
	if keepHead.State != rules.StateRetired {
		t.Fatalf("keep-head %+v", keepHead)
	}
}

// A held remedy is proven by a pass of the stage it acts in: a rule for dropped-then-queried
// (and the protect-kind catalogue, which answers only it) by a dropper pass, never by the
// observer pass that does not run it; an observer rule and a mixed catalogue by the observer.
func TestHeldRemedyIsProvenInTheStageItActsIn(t *testing.T) {
	store := curated.New(curated.DefaultDir(t.TempDir()))
	for _, r := range []rules.Rule{
		{Detector: rules.DetectDroppedThenQueried, Remedy: rules.RemedyProtectKind, Kind: "fact", Models: []string{workerModel}},
		{Detector: rules.DetectThinChunk, Remedy: rules.RemedyPromptLine, Text: "Keep every figure.", Models: []string{workerModel}},
	} {
		if _, err := rules.Add(store, r); err != nil {
			t.Fatal(err)
		}
	}
	defs, _, err := store.Layer().ListAll(layer.KindRemedy)
	if err != nil || len(defs) != 2 {
		t.Fatalf("rules %+v %v", defs, err)
	}
	for _, d := range defs {
		dropper := strings.Contains(d.Text, rules.DetectDroppedThenQueried)
		if provesDefinition(passObserver, d) == dropper || provesDefinition(passDropper, d) != dropper {
			t.Errorf("rule %s (dropper %v): observer proves %v, dropper proves %v", d.Name, dropper, provesDefinition(passObserver, d), provesDefinition(passDropper, d))
		}
	}
	protect := layer.Definition{Kind: layer.KindRemedy, Name: rules.RemedyProtectKind, Text: rules.CatalogueSeeds[rules.RemedyProtectKind]}
	if provesDefinition(passObserver, protect) || !provesDefinition(passDropper, protect) {
		t.Error("the protect-kind catalogue is not proven by the dropper alone")
	}
	prompt := layer.Definition{Kind: layer.KindRemedy, Name: rules.RemedyPromptLine, Text: rules.CatalogueSeeds[rules.RemedyPromptLine]}
	if !provesDefinition(passObserver, prompt) || provesDefinition(passDropper, prompt) {
		t.Error("the mixed prompt-line catalogue is not proven by the observer")
	}
}
