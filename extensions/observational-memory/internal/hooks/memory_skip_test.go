package hooks

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/memquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// definedTool is a tool the agent defined with the memory tool in these tests.
const definedTool = "half-plan"

const savedReply = "Saved memory lisbon-half-training: half marathon in Lisbon on 2027-03-14, 4 runs a week."

// toolCallMessage is an assistant message holding text (none when "") and one call of tool.
func toolCallMessage(id, text, tool string, args map[string]any) ledger.Entry {
	content := []any{}
	if text != "" {
		content = append(content, map[string]any{"type": "text", "text": text})
	}
	content = append(content, map[string]any{"type": "toolCall", "id": "call-" + id, "name": tool, "arguments": args})
	return testfix.MessageEntry(id, map[string]any{"role": "assistant", "content": content, "stopReason": "toolUse"})
}

// toolResultOf is the result of a call of tool.
func toolResultOf(id, tool, body string) ledger.Entry {
	return testfix.MessageEntry(id, map[string]any{"role": "toolResult", "toolCallId": "call-" + id, "toolName": tool, "content": []any{map[string]any{"type": "text", "text": body}}})
}

func memorySave(id, text string) ledger.Entry {
	return toolCallMessage(id, text, memquery.ToolName, map[string]any{"query": `save lisbon-half-training "half marathon in Lisbon on 2027-03-14"`})
}

// registerDefined defines each of names in a curated store under h's home and registers them as
// the extension does (harness.Manager.RegisterStored), with rt reading the manager's registered
// tools as registerMemory wires it. It returns the store and the manager.
func registerDefined(t *testing.T, rt *runtime.Runtime, h *testfix.FakeHost, names ...string) (curated.Store, *harness.Manager) {
	t.Helper()
	if h.Home == "" {
		h.Home = t.TempDir()
	}
	store := curated.New(curated.DefaultDir(h.Home))
	for _, name := range names {
		if _, err := store.Define(curated.Definition{Name: name, Query: "notes", Description: "The half marathon plan."}, "s0"); err != nil {
			t.Fatal(err)
		}
	}
	m := harness.NewManager(func(host.ToolHost) (curated.Call, bool) { return curated.Call{Store: store, Source: "s0"}, true })
	m.RegisterStored(h)
	rt.SetDefinedTools(m.Registered)
	return store, m
}

func expectNoMemoryPlumbing(t *testing.T, chunk string) {
	t.Helper()
	for _, leaked := range []string{"Saved memory", "lisbon-half-training", "[" + memquery.ToolName + "(", definedTool, "Plan rows"} {
		if strings.Contains(chunk, leaked) {
			t.Fatalf("chunk carries memory plumbing %q:\n%s", leaked, chunk)
		}
	}
}

// The observer chunk leaves out the memory tool's calls and results and those of a tool the
// agent defined, keeps the assistant's text beside a memory call and an unrelated tool's call
// and result, and covers through the chunk's last entry even when only memory plumbing is there.
func TestObserverChunkSkipsMemoryToolCalls(t *testing.T) {
	entries := []ledger.Entry{
		userMessage("u1", "I'm training for the Lisbon half in March."),
		memorySave("a1", "Good luck with Lisbon."),
		toolResultOf("t1", memquery.ToolName, savedReply),
		userMessage("u2", "What's in the folder?"),
		toolCallMessage("a2", "", "bash", map[string]any{"command": "ls"}),
		toolResultOf("t2", "bash", "plan.md"),
		toolCallMessage("a3", "", definedTool, map[string]any{}),
		toolResultOf("t3", definedTool, "Plan rows"),
	}
	fx := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999, quiet: true})
	registerDefined(t, fx.rt, fx.h, definedTool)
	fx.obs.resolve(agents.Observed([]ledger.Observation{noteOn("aaaaaaaaaaaa", "User is training for the Lisbon half marathon in March.", "u1")}))
	fx.run()

	call := fx.obs.call(0)
	expectNoMemoryPlumbing(t, call.Chunk)
	for _, kept := range []string{"Good luck with Lisbon.", `[bash({"command":"ls"})]`, "[Tool result for bash", "plan.md"} {
		if !strings.Contains(call.Chunk, kept) {
			t.Fatalf("chunk lost %q:\n%s", kept, call.Chunk)
		}
	}
	if !reflect.DeepEqual(call.AllowedSourceEntryIDs, []string{"u1", "a1", "u2", "a2", "t2"}) {
		t.Fatalf("allowed %v", call.AllowedSourceEntryIDs)
	}
	if data := appendedObservations(t, fx, 0); data.CoversUpToID != "t3" {
		t.Fatalf("coverage %+v", data)
	}
}

// An uncovered backlog of memory plumbing alone gives the observer nothing to read: it does not
// make the observer due and counts nothing toward its backlog; an unrelated tool still does.
func TestMemoryPlumbingDoesNotMakeObserverDue(t *testing.T) {
	entries := []ledger.Entry{
		userMessage("u1"), coverage("om-u1", "u1", "aaaaaaaaaaaa"),
		memorySave("a1", ""), toolResultOf("t1", memquery.ToolName, savedReply),
	}
	fx := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999, quiet: true})
	tools := []string{memquery.ToolName}
	if got := ObserverBacklog(entries, tools); got != 0 {
		t.Fatalf("backlog %d", got)
	}
	if ObserverBacklog(entries, nil) == 0 {
		t.Fatal("without the memory tools the plumbing would count")
	}
	fx.runIfDue()
	if fx.obs.count() != 0 {
		t.Fatalf("observer ran %d times on memory plumbing", fx.obs.count())
	}
	withBash := append(append([]ledger.Entry{}, entries...), toolCallMessage("a2", "", "bash", map[string]any{"command": "ls"}))
	if ObserverBacklog(withBash, tools) == 0 {
		t.Fatal("an unrelated tool call does not count")
	}
}

// Compaction does not wait on memory plumbing the observer never reads: coverage reaching the
// last entry it reads lets Pi's cut stand, and the cut never moves onto the plumbing.
func TestCompactionDoesNotWaitOnMemoryPlumbing(t *testing.T) {
	entries := []ledger.Entry{
		userMessage("u0"), assistantMessage("a0"), coverage("om-a0", "a0", "aaaaaaaaaaaa"),
		memorySave("a1", ""), toolResultOf("t1", memquery.ToolName, savedReply),
		toolCallMessage("a2", "", definedTool, map[string]any{}), toolResultOf("t2", definedTool, "Plan rows"),
		userMessage("u1"), assistantMessage("a3"),
	}
	tools := []string{memquery.ToolName, definedTool}
	resolution := ResolveCompactionCut(entries, "u1", CutOptions{MaxRetainedTokens: 1000, MemoryTools: tools})
	if resolution.Kind != "cut" || resolution.Cut != (CompactionCut{FirstKeptEntryID: "u1", FoldThroughEntryID: "u1"}) || resolution.Gap != nil {
		t.Fatalf("resolution %+v", resolution)
	}
	// Without the memory tools the plumbing would read as unobserved source and hold the cut.
	if held := ResolveCompactionCut(entries, "u1", CutOptions{MaxRetainedTokens: 1000}); held.Gap == nil {
		t.Fatalf("resolution without memory tools %+v", held)
	}

	rt, h := hookSetup(hookArgs{entries: entries})
	registerDefined(t, rt, h, definedTool)
	result := compactionOf(t, runHook(rt, h, entries, "u1", "threshold"))
	if result.FirstKeptEntryID != "u1" || result.Details.FoldThroughEntryID != "u1" {
		t.Fatalf("result %+v", result)
	}
}

// The compaction catch-up serializes its chunks the same way.
func TestCatchUpSkipsMemoryToolCalls(t *testing.T) {
	entries := []ledger.Entry{
		userMessage("u0"), coverage("om-u0", "u0", "dddddddddddd"),
		userMessage("u1", "I'm training for the Lisbon half."),
		memorySave("a1", "Good luck with Lisbon."), toolResultOf("t1", memquery.ToolName, savedReply),
		toolCallMessage("a2", "", definedTool, map[string]any{}), toolResultOf("t2", definedTool, "Plan rows"),
		toolCallMessage("a3", "", "bash", map[string]any{"command": "ls"}), toolResultOf("t3", "bash", "plan.md"),
		userMessage("u2"),
	}
	obs, _, _ := mockWorkers(t)
	fx := catchUpSetup(t, catchUpArgs{entries: entries})
	registerDefined(t, fx.rt, fx.h, definedTool)
	obs.resolve(agents.Observed([]ledger.Observation{gapObservation("u1")}))
	result := CatchUpObserver(context.Background(), CatchUpArgs{Runtime: fx.rt, Host: fx.h, Entries: entries, Gap: ledger.UnobservedSourceSpan{FirstIndex: 2, LastIndex: 9}, MaxChunks: 2})
	if result.ChunksRecorded != 1 || obs.count() != 1 {
		t.Fatalf("result %+v observer calls %d", result, obs.count())
	}
	call := obs.call(0)
	expectNoMemoryPlumbing(t, call.Chunk)
	if !strings.Contains(call.Chunk, "Good luck with Lisbon.") || !strings.Contains(call.Chunk, "plan.md") {
		t.Fatalf("chunk %s", call.Chunk)
	}
	if !reflect.DeepEqual(call.AllowedSourceEntryIDs, []string{"u1", "a1", "a3", "t3", "u2"}) {
		t.Fatalf("allowed %v", call.AllowedSourceEntryIDs)
	}
	data, _ := ledger.ObservationsRecordedDataFrom(fx.h.Appended[0].Data)
	if data.CoversUpToID != "u2" {
		t.Fatalf("recorded %+v", data)
	}
}

// The require-kind re-ask reads the same chunk, without the memory plumbing, and the marker
// covers the trailing memory result it skipped.
func TestRequireKindReaskSkipsMemoryToolCalls(t *testing.T) {
	entries := []ledger.Entry{
		userMessage("u1", "Which e-reader should I buy?"),
		memorySave("a1", specificReply),
		toolResultOf("t1", memquery.ToolName, savedReply),
	}
	fx := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999, quiet: true})
	withRules(t, fx, rules.Rule{Detector: rules.DetectUncitedAssistantSpecifics, Remedy: rules.RemedyRequireKind})
	fx.obs.resolve(agents.Observed([]ledger.Observation{noteOn("aaaaaaaaaaaa", "User asked which e-reader to buy.", "u1")}))
	second := noteOn("bbbbbbbbbbbb", "Assistant suggested the Kindle Paperwhite (149) and Kobo Clara (139).", "a1")
	second.Kind = "assistant"
	fx.obs.resolve(agents.Observed([]ledger.Observation{second}))
	fx.run()
	if fx.obs.count() != 2 {
		t.Fatalf("observer calls %d", fx.obs.count())
	}
	again := fx.obs.call(1)
	expectNoMemoryPlumbing(t, again.Chunk)
	if !strings.Contains(again.Chunk, specificReply) || !reflect.DeepEqual(again.AllowedSourceEntryIDs, []string{"a1"}) {
		t.Fatalf("re-ask chunk %q allowed %v", again.Chunk, again.AllowedSourceEntryIDs)
	}
	if data := appendedObservations(t, fx, 0); len(data.Observations) != 2 || data.CoversUpToID != "t1" {
		t.Fatalf("recorded %+v", data)
	}
}

// The detectors still read the memory calls a marker covers (dropped-then-queried counts them),
// and the pass's tokens are those of the entries the observer read text of.
func TestCoveredKeepsSkippedMemoryCallsForDetectors(t *testing.T) {
	entries := []ledger.Entry{
		userMessage("u1"), memorySave("a1", ""), toolResultOf("t1", memquery.ToolName, savedReply), userMessage("u2"),
	}
	serialized := serialize.SerializeSourceAddressedBranchEntries(entries, serialize.SourceAddressedOptions{SkipTools: []string{memquery.ToolName}})
	chunk := observerChunk{branch: entries, serialized: serialized}
	covered := chunk.covered(serialized.CoversUpToID)
	ids := make([]string, len(covered))
	for i, e := range covered {
		ids[i] = e.ID
	}
	if !reflect.DeepEqual(ids, []string{"u1", "a1", "t1", "u2"}) {
		t.Fatalf("covered %v", ids)
	}
	if got, want := chunk.coveredTokens(covered), rules.SourceTokens([]ledger.Entry{entries[0], entries[3]}); got != want {
		t.Fatalf("pass tokens %d, want %d", got, want)
	}
}

// A stored definition whose name another tool holds is never registered, so that tool's calls
// and results stay conversation: the observer reads them and they need coverage.
func TestCollidingDefinitionIsNotSkipped(t *testing.T) {
	entries := []ledger.Entry{
		userMessage("u1", "Add milk to my list."),
		toolCallMessage("a1", "", "todo", map[string]any{"item": "milk"}),
		toolResultOf("t1", "todo", "Added milk"),
	}
	fx := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999, quiet: true})
	fx.h.Tools = []host.ToolInfo{{Name: "todo", Source: "todo-extension"}}
	registerDefined(t, fx.rt, fx.h, "todo")
	if got := harness.MemoryToolNames(fx.rt); !reflect.DeepEqual(got, []string{memquery.ToolName}) {
		t.Fatalf("memory tools %v", got)
	}
	fx.obs.resolve(agents.Observed([]ledger.Observation{noteOn("aaaaaaaaaaaa", "User added milk to the todo list.", "u1")}))
	fx.run()
	call := fx.obs.call(0)
	if !strings.Contains(call.Chunk, "[Tool result for todo") || !strings.Contains(call.Chunk, "Added milk") {
		t.Fatalf("chunk lost the todo tool:\n%s", call.Chunk)
	}
	if !reflect.DeepEqual(call.AllowedSourceEntryIDs, []string{"u1", "a1", "t1"}) {
		t.Fatalf("allowed %v", call.AllowedSourceEntryIDs)
	}
}

// Forgetting a definition keeps its tool registered, so calls of it already on the branch stay
// memory plumbing: the chunk still leaves them out and compaction still does not wait on them.
func TestForgottenDefinitionStaysSkipped(t *testing.T) {
	entries := []ledger.Entry{
		userMessage("u1", "How is my Lisbon plan going?"),
		toolCallMessage("a1", "", definedTool, map[string]any{}),
		toolResultOf("t1", definedTool, "Plan rows lisbon-half-training"),
	}
	fx := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999, quiet: true})
	store, _ := registerDefined(t, fx.rt, fx.h, definedTool)
	if err := store.ForgetDefinition(definedTool); err != nil {
		t.Fatal(err)
	}
	if got := harness.MemoryToolNames(fx.rt); !reflect.DeepEqual(got, []string{memquery.ToolName, definedTool}) {
		t.Fatalf("memory tools after forget %v", got)
	}
	fx.obs.resolve(agents.Observed([]ledger.Observation{noteOn("aaaaaaaaaaaa", "User asked how the Lisbon plan is going.", "u1")}))
	fx.run()
	call := fx.obs.call(0)
	expectNoMemoryPlumbing(t, call.Chunk)
	if !reflect.DeepEqual(call.AllowedSourceEntryIDs, []string{"u1"}) {
		t.Fatalf("allowed %v", call.AllowedSourceEntryIDs)
	}
	if data := appendedObservations(t, fx, 0); data.CoversUpToID != "t1" {
		t.Fatalf("coverage %+v", data)
	}

	// The cut is stable across the forget: the plumbing still needs no coverage.
	compacting := []ledger.Entry{userMessage("u0"), coverage("om-u0", "u0", "aaaaaaaaaaaa"), entries[1], entries[2], userMessage("u2")}
	resolution := ResolveCompactionCut(compacting, "u2", CutOptions{MaxRetainedTokens: 1000, MemoryTools: harness.MemoryToolNames(fx.rt)})
	if resolution.Gap != nil || resolution.Cut.FirstKeptEntryID != "u2" {
		t.Fatalf("resolution %+v", resolution)
	}
}

// A run that stops early covers only through the last entry its observations cite, never
// through the memory plumbing after it that the full chunk would have covered.
func TestPartialRunCoverageStopsBeforeTrailingPlumbing(t *testing.T) {
	entries := []ledger.Entry{
		userMessage("u1", "I'm training for the Lisbon half."),
		userMessage("u2", "Four runs a week."),
		memorySave("a1", ""),
		toolResultOf("t1", memquery.ToolName, savedReply),
	}
	fx := consSetup(t, consArgs{entries: entries, reflectAfterTokens: 999, quiet: true})
	fx.obs.resolve(agents.ObserverResult{Observations: []ledger.Observation{noteOn("aaaaaaaaaaaa", "User is training for the Lisbon half.", "u1")}, Partial: true})
	fx.run()
	if !reflect.DeepEqual(fx.obs.call(0).AllowedSourceEntryIDs, []string{"u1", "u2"}) {
		t.Fatalf("allowed %v", fx.obs.call(0).AllowedSourceEntryIDs)
	}
	if data := appendedObservations(t, fx, 0); data.CoversUpToID != "u1" {
		t.Fatalf("coverage %+v", data)
	}
}

// Memory plumbing alone never lifts the deliberate-empty observer backoff: it adds nothing the
// observer could read.
func TestMemoryPlumbingDoesNotLiftObserverBackoff(t *testing.T) {
	fx := consSetup(t, consArgs{entries: []ledger.Entry{text("raw-1", strings.Repeat("a", 40))}, observeAfterTokens: 10, reflectAfterTokens: 999})
	registerDefined(t, fx.rt, fx.h, definedTool)
	fx.run()
	if fx.obs.count() != 1 || fx.rt.ObserverEmptyBackoff() == nil {
		t.Fatalf("observer calls %d, backoff %+v", fx.obs.count(), fx.rt.ObserverEmptyBackoff())
	}
	big := strings.Repeat("lisbon-half-training ", 40)
	fx.addEntries(memorySave("a1", ""), toolResultOf("t1", memquery.ToolName, big), toolCallMessage("a2", "", definedTool, map[string]any{}), toolResultOf("t2", definedTool, big))
	fx.runIfDue()
	if fx.obs.count() != 1 {
		t.Fatalf("observer ran again on memory plumbing: %d calls", fx.obs.count())
	}
	entries, _ := fx.h.GetBranch()
	cfg := fx.rt.Config()
	if ActiveObserverBackoff(fx.rt, fx.h, entries, cfg, harness.MemoryToolNames(fx.rt)) == nil {
		t.Fatal("memory plumbing lifted the backoff")
	}
	// The same growth as conversation would lift it.
	if ActiveObserverBackoff(fx.rt, fx.h, entries, cfg, nil) != nil {
		t.Fatal("the growth is too small to test the backoff")
	}
}
