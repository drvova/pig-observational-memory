package hooks

import (
	"fmt"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/consolidate"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// ownConsolidation is the consolidation instructions addition the fixtures' level 1 defines.
const ownConsolidation = "Prefer one merged line per deploy target over several partial ones."

// level1StoreSetup is level1Setup with a store, so the harness records, offers and settles.
func level1StoreSetup(t *testing.T) (*runtime.Runtime, *testfix.FakeHost, curated.Store, string) {
	t.Helper()
	rt, h, _ := level1Setup(t)
	h.Home = t.TempDir()
	store, ok := harness.StoreOf(h, rt.Config())
	if !ok || store.Layer().Engine != curated.EngineV2 {
		t.Fatal("no v2 store")
	}
	return rt, h, store, harness.ModelKey(h.Model)
}

// seedV2Evidence records a memory v2 compaction pass that counted what level 1 lost, so the
// model's harness evidence differs from none.
func seedV2Evidence(t *testing.T, store curated.Store, model string) {
	t.Helper()
	if err := store.RecordModel(curated.ModelEvent{Model: model, Kind: curated.ModelKindPass, Stage: rules.StageCompaction, Engine: curated.EngineV2, Tokens: 400, Level1: true, Counts: map[string]int{rules.CountLevel1UnionKept: 2}}); err != nil {
		t.Fatal(err)
	}
}

// compactLevel1 runs one level-1 compaction of a fresh copy of the level-1 fixture branch, with
// reply as the one response, and returns the request it made.
func compactLevel1(t *testing.T, rt *runtime.Runtime, h *testfix.FakeHost, reply map[string]any) map[string]any {
	t.Helper()
	h.Entries = level1Branch()
	before := h.ModelsFake.Streams.Calls()
	h.ModelsFake.Streams.Replies = []map[string]any{reply}
	level1CompactionOf(t, runV2Hook(rt, h, v2Event(h.Entries, "u2", 20_000, "threshold")))
	if h.ModelsFake.Streams.Calls() != before+1 {
		t.Fatalf("a compaction made %d requests, want exactly 1", h.ModelsFake.Streams.Calls()-before)
	}
	return h.ModelsFake.Streams.Requests[before]
}

// compactEmpty runs one compaction whose cut range level 0 records nothing from: an empty pass,
// with no level-1 request.
func compactEmpty(t *testing.T, rt *runtime.Runtime, h *testfix.FakeHost) {
	t.Helper()
	h.Entries = []ledger.Entry{userMessage("e1", "ok"), assistantMessage("e2", "ok"), userMessage("e3", "next")}
	before := h.ModelsFake.Streams.Calls()
	if outcome := runV2Hook(rt, h, v2Event(h.Entries, "e3", 20_000, "threshold")); outcome.Compaction == nil || outcome.Compaction.FirstKeptEntryID != "e3" {
		t.Fatalf("expected a compaction on PiG's cut, got %+v", outcome)
	}
	if h.ModelsFake.Streams.Calls() != before {
		t.Fatal("an empty compaction made a request")
	}
}

// level1Reply is the fixture's clean merge, with definitions when defs is not nil.
func level1Reply(defs ...map[string]any) map[string]any {
	args := map[string]any{"merges": []any{map[string]any{"ids": []any{l1PortID, l1MakefileID}, "content": "Staging deploys run from the Makefile on port 8080", "subject": "deploy"}}}
	if len(defs) > 0 {
		items := []any{}
		for _, d := range defs {
			items = append(items, d)
		}
		args["definitions"] = items
	}
	return testfix.AssistantToolCall("call-1", consolidate.ToolName, args)
}

// consolidationDefinition is the definitions item for the fixtures' consolidation addition.
func consolidationDefinition() map[string]any {
	return map[string]any{"kind": layer.KindInstructions, "name": agents.InstructionsConsolidation, "text": ownConsolidation}
}

// heldConsolidation reports the model's consolidation instructions' state: held, written or
// none.
func consolidationState(t *testing.T, store curated.Store, model string) string {
	t.Helper()
	h, _, err := store.Layer().Load(model)
	if err != nil {
		t.Fatal(err)
	}
	d, ok := h.Get(layer.KindInstructions, agents.InstructionsConsolidation)
	if !ok {
		return "none"
	}
	return d.State
}

// offered reports whether a level-1 request offered definitions: the tool takes them and the
// input shows the harness evidence.
func offered(request map[string]any) bool {
	tools := fmt.Sprint(request["tools"])
	shown := strings.Contains(fmt.Sprint(request["messages"]), "HARNESS EVIDENCE")
	return strings.Contains(tools, "definitions") && shown
}

// Definitions are offered only when the harness evidence changed since the last offer: not
// with no evidence, then once evidence differs from none, and not again while it is unchanged.
// Every compaction makes exactly one request, and only for consolidation.
func TestV2DefinitionsOfferedOnlyWhenEvidenceChanged(t *testing.T) {
	counter := testfix.CountRequests(t)
	rt, h, store, model := level1StoreSetup(t)

	if offered(compactLevel1(t, rt, h, level1Reply())) {
		t.Fatal("definitions offered with no evidence")
	}
	if _, ok, _ := store.Layer().LastProposal(model); ok {
		t.Fatal("an offer was marked though none was made")
	}

	seedV2Evidence(t, store, model)
	if !offered(compactLevel1(t, rt, h, level1Reply())) {
		t.Fatal("definitions not offered after the evidence changed")
	}
	last, ok, err := store.Layer().LastProposal(model)
	if err != nil || !ok || last.Evidence == "" {
		t.Fatalf("the answered offer was not marked: %+v %v", last, err)
	}

	if offered(compactLevel1(t, rt, h, level1Reply())) {
		t.Fatal("definitions offered again with unchanged evidence")
	}
	if counter.Total() != 3 || counter.Stage(consolidate.Stage) != 3 {
		t.Fatalf("requests %v, want one per compaction", counter.ByStage())
	}
}

// A definition level 1 proposes is held, never proven by the compaction that proposed it: the
// next compaction runs level 1 under it (the kernel's rules first and whole, then the seed,
// then the addition) and, its code checks clean, writes it. One request per compaction.
func TestV2HeldDefinitionWrittenOnLaterEvidence(t *testing.T) {
	counter := testfix.CountRequests(t)
	rt, h, store, model := level1StoreSetup(t)
	seedV2Evidence(t, store, model)

	first := compactLevel1(t, rt, h, level1Reply(consolidationDefinition()))
	if !offered(first) {
		t.Fatal("definitions not offered")
	}
	if state := consolidationState(t, store, model); state != layer.StateHeld {
		t.Fatalf("after the proposing compaction: %s, want held (its own output never proves it)", state)
	}

	second := compactLevel1(t, rt, h, level1Reply())
	system, _ := second["systemPrompt"].(string)
	rulesText := strings.TrimRight(kernel.Text(kernel.TextConsolidateRules), "\n")
	if !strings.HasPrefix(system, rulesText+"\n\n"+strings.TrimRight(agents.ConsolidationInstructions, "\n")) || !strings.Contains(system, agents.AdditionsHeading+"\n"+ownConsolidation) {
		t.Fatalf("level 1 did not run under the held addition after the kernel's rules:\n%s", system)
	}
	if offered(second) {
		t.Fatal("definitions offered while one waits for its proof")
	}
	if state := consolidationState(t, store, model); state != layer.StateWritten {
		t.Fatalf("after a clean later compaction: %s, want written", state)
	}
	if counter.Total() != 2 || counter.Stage(consolidate.Stage) != 2 {
		t.Fatalf("requests %v, want one per compaction", counter.ByStage())
	}
}

// A held definition whose later compaction's level-1 output under it lost something (a merge
// that would drop a specific, nothing valid recorded) is reverted, and the seed stands again.
func TestV2HeldDefinitionRevertedOnLoss(t *testing.T) {
	rt, h, store, model := level1StoreSetup(t)
	seedV2Evidence(t, store, model)
	compactLevel1(t, rt, h, level1Reply(consolidationDefinition()))
	compactLevel1(t, rt, h, mergeReply("Staging deploys run from the Makefile"))
	if state := consolidationState(t, store, model); state != "none" {
		t.Fatalf("after a lossy later compaction: %s, want reverted", state)
	}
	if system := consolidate.SystemPromptFor(harness.LayerFor(h, rt.Config(), h.Model).Instructions(agents.InstructionsConsolidation)); strings.Contains(system, ownConsolidation) {
		t.Fatal("the reverted addition still reaches the prompt")
	}
}

// An empty compaction is counted, never proof: a held definition stays held through one, and is
// written only by a later compaction whose level 1 ran under it.
func TestV2EmptyPassIsNoEvidence(t *testing.T) {
	counter := testfix.CountRequests(t)
	rt, h, store, model := level1StoreSetup(t)
	seedV2Evidence(t, store, model)
	compactLevel1(t, rt, h, level1Reply(consolidationDefinition()))

	compactEmpty(t, rt, h)
	passes := modelEventsOf(t, store, model, curated.ModelKindPass)
	if last := passes[len(passes)-1]; last.Outcome != curated.OutcomeEmpty {
		t.Fatalf("the empty compaction was not counted as empty: %+v", last)
	}
	if state := consolidationState(t, store, model); state != layer.StateHeld {
		t.Fatalf("after an empty compaction: %s, want held", state)
	}

	compactLevel1(t, rt, h, level1Reply())
	if state := consolidationState(t, store, model); state != layer.StateWritten {
		t.Fatalf("after a clean compaction: %s, want written", state)
	}
	if counter.Total() != 2 {
		t.Fatalf("requests %v, want one per level-1 compaction and none for the empty one", counter.ByStage())
	}
}

// What level 1 may define is checked like every definition from the model: a kind that changes
// nothing under v2, text quoted from tool output and a kernel-owned name are refused; a rule
// goes through propose and is held for v2.
func TestV2Level1DefinitionsAreChecked(t *testing.T) {
	rt, h, store, model := level1StoreSetup(t)
	seedV2Evidence(t, store, model)
	reply := level1Reply(
		map[string]any{"kind": layer.KindSchema, "name": "observation", "text": "schema[1]{field,type,values,max,description}:\n  mood,text,,,How the user feels"},
		map[string]any{"kind": layer.KindDetector, "name": kernel.MemoryToolName, "text": "detector[1]{check,query,description}:\n  ,notes | count,counts"},
		consolidationDefinition(),
	)
	args := reply["content"].([]any)[0].(map[string]any)["arguments"].(map[string]any)
	args["rules"] = []any{map[string]any{"detector": rules.DetectUncitedAssistantSpecifics, "remedy": rules.RemedyKeepHead}}
	compactLevel1(t, rt, h, reply)

	held, err := store.Layer().Held(model)
	if err != nil {
		t.Fatal(err)
	}
	kinds := []string{}
	for _, d := range held {
		kinds = append(kinds, d.Kind+"/"+d.Name)
		if rules.IsRule(d.Kind, d.Name) && !rules.SettlesUnderV2(d) {
			t.Fatalf("a level-1 rule was not held for v2: %+v", d)
		}
	}
	if len(held) != 2 || consolidationState(t, store, model) != layer.StateHeld {
		t.Fatalf("held %v, want the consolidation instructions and one rule", kinds)
	}
}

// Only memory v2's holds apply. A definition memory v1 held (a store whose layer reads holds
// without an engine, as v1's did) never reaches the level-1 prompt, never blocks an offer and is
// never settled by a compaction (a v2 proposal of the same name replaces it); and a v2 hold is
// invisible to such a store and its Claim.
func TestV1HoldsNeverRun(t *testing.T) {
	counter := testfix.CountRequests(t)
	rt, h, store, model := level1StoreSetup(t)
	v1 := store.Layer()
	v1.Engine = ""
	const v1Text = "Merge aggressively; prefer reflections."
	for _, d := range []layer.Definition{
		{Kind: layer.KindInstructions, Name: agents.InstructionsConsolidation, Model: model, Text: v1Text},
		{Kind: layer.KindDetector, Name: "missed-paths", Model: model, Text: "detector[1]{check,query,description}:\n  ,notes | count,notes counted"},
	} {
		if _, err := v1.Hold(d); err != nil {
			t.Fatal(err)
		}
	}
	if held, _ := store.Layer().Held(model); len(held) != 0 {
		t.Fatalf("v2 sees v1's holds: %+v", held)
	}

	seedV2Evidence(t, store, model)
	first := compactLevel1(t, rt, h, level1Reply(consolidationDefinition()))
	if !offered(first) {
		t.Fatal("v1's holds blocked the v2 offer")
	}
	if system, _ := first["systemPrompt"].(string); strings.Contains(system, v1Text) {
		t.Fatalf("v1's held addition reached the level-1 prompt:\n%s", system)
	}
	second := compactLevel1(t, rt, h, level1Reply())
	if system, _ := second["systemPrompt"].(string); strings.Contains(system, v1Text) || !strings.Contains(system, ownConsolidation) {
		t.Fatalf("level 1 ran under the wrong addition:\n%s", system)
	}
	if state := consolidationState(t, store, model); state != layer.StateWritten {
		t.Fatalf("v2's own addition: %s, want written", state)
	}
	held, err := v1.Held(model)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 1 || held[0].Name != "missed-paths" {
		t.Fatalf("v1's holds after v2 compactions: %+v, want its detector still held", held)
	}
	// v2's proposal of the name v1 held replaced v1's unproven hold (one held file per name) and
	// was written on v2's evidence; v1 never sees a v2 hold, nor settles one.
	if _, err := store.Layer().Hold(layer.Definition{Kind: layer.KindDetector, Name: "v2-only", Model: model, Text: "detector[1]{check,query,description}:\n  ,notes | count,v2 counted"}); err != nil {
		t.Fatal(err)
	}
	if held, _ := v1.Held(model); len(held) != 1 || held[0].Name != "missed-paths" {
		t.Fatalf("v1 sees a v2 hold: %+v", held)
	}
	notices, err := v1.Claim(model, true, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notices {
		if n.Name == "v2-only" {
			t.Fatalf("v1's Claim settled a v2 hold: %+v", n)
		}
	}
	if held, _ := store.Layer().Held(model); len(held) != 1 || held[0].Name != "v2-only" {
		t.Fatalf("v2's holds after v1's Claim: %+v", held)
	}
	if counter.Total() != 2 {
		t.Fatalf("requests %v, want one per compaction", counter.ByStage())
	}
}

// Level 1 may only extend the consolidation seed, and may not define a format: a replacement
// and a format are refused and nothing is held. The refused answer is the offer's own output,
// so it never counts as level1-refused and never makes the next offer due.
func TestV2Level1RefusesReplacementAndFormat(t *testing.T) {
	counter := testfix.CountRequests(t)
	rt, h, store, model := level1StoreSetup(t)
	seedV2Evidence(t, store, model)
	request := compactLevel1(t, rt, h, level1Reply(
		map[string]any{"kind": layer.KindInstructions, "name": agents.InstructionsConsolidation, "text": "mode: replace\nAlways merge every pair of records on one subject."},
		map[string]any{"kind": layer.KindFormat, "name": "pipes", "text": "format[1]{header,delimiter,quote,escape,escapes,special,indent}:\n  counted,|,\",\\\\,nrt,:,2"},
	))
	if !offered(request) {
		t.Fatal("definitions not offered")
	}
	if tools := fmt.Sprint(request["tools"]); strings.Contains(tools, layer.KindFormat+" ") || strings.Contains(tools, "replace the seed") {
		t.Fatalf("the offer still invites a format or a replacement: %s", tools)
	}
	if held, _ := store.Layer().Held(model); len(held) != 0 {
		t.Fatalf("held %+v, want nothing", held)
	}
	passes := modelEventsOf(t, store, model, curated.ModelKindPass)
	if last := passes[len(passes)-1]; last.Counts[rules.CountLevel1Refused] != 0 {
		t.Fatalf("the offer's refused answer counted as level1-refused: %+v", last)
	}
	if offered(compactLevel1(t, rt, h, level1Reply())) {
		t.Fatal("the offer's own refused answer made the next offer due")
	}
	if counter.Total() != 2 {
		t.Fatalf("requests %v, want one per compaction", counter.ByStage())
	}
}
