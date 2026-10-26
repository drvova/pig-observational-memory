package hooks

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/consolidate"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ids"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/selection"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/snippettest"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// The v1 records of the migration fixture.
var (
	v1PortID     = "c0ffee000001"
	v1MakefileID = "c0ffee000002"
	v1DBID       = "c0ffee000003"
	v1ReflID     = "c0ffee000004"
)

// v1Session is a realistic session memory v1 left: its observer's records with their coverage
// markers, a reflection, and a v1 compaction that moved PiG's cut back to u2 and folded through
// the coverage marker at a2 (om.folded details, fullFold), then a turn v1 never observed:
//
//	0 u1  1 a1  2 om(A, B; covers a1)  3 u2  4 a2  5 om(C; covers a2)  6 om(reflection)
//	7 c1 (firstKept u2, foldThrough a2)  8 u3  9 a3  10 u4
func v1Session() []ledger.Entry {
	// Long enough that the summary budget (never more than the range removes) holds records.
	pad := strings.Repeat(", after the usual checks", 40)
	port := testfix.Observation(v1PortID, func(o *ledger.Observation) {
		o.Content, o.SourceEntryIDs, o.Kind = "The api deploys to staging on port 8080", []string{"u1"}, "fact"
	})
	makefile := testfix.Observation(v1MakefileID, func(o *ledger.Observation) {
		o.Content, o.SourceEntryIDs, o.Kind = "Deploys run from the Makefile target deploy-staging", []string{"a1"}, "fact"
	})
	db := testfix.Observation(v1DBID, func(o *ledger.Observation) {
		o.Content, o.SourceEntryIDs, o.Kind = "The database is Postgres 15.2", []string{"u2"}, "fact"
	})
	refl := testfix.Reflection(v1ReflID, []string{v1PortID, v1MakefileID}, func(r *ledger.Reflection) {
		r.Content = "Staging deploys run from the Makefile target deploy-staging on port 8080"
	})
	details := testfix.MemoryDetails(testfix.MemoryDetailsArgs{FullFold: true, Observations: []ledger.Observation{port, makefile, db}, Reflections: []ledger.Reflection{refl}, FoldThroughEntryID: "a2"})
	return []ledger.Entry{
		userMessage("u1", "Deploy the api to staging on port 8080"),
		assistantMessage("a1", "Deployed with the Makefile target deploy-staging"),
		testfix.ObservationsRecordedEntry("om-v1-1", []ledger.Observation{port, makefile}, "a1"),
		userMessage("u2", "Use Postgres 15.2 for the database"+pad),
		assistantMessage("a2", "Configured Postgres 15.2"+pad),
		testfix.ObservationsRecordedEntry("om-v1-2", []ledger.Observation{db}, "a2"),
		testfix.ReflectionsRecordedEntry("om-v1-3", []ledger.Reflection{refl}, "a2"),
		testfix.CompactionEntry("c1", testfix.CompactionArgs{Summary: "v1 memory summary", FirstKeptEntryID: "u2", Details: details}),
		userMessage("u3", "The cache runs redis 7.2 on port 6379"+pad),
		assistantMessage("a3", "Noted the redis 7.2 cache on port 6379"+pad),
		userMessage("u4", "Next, the queue"),
	}
}

// appendedCiting are the records appended to h that cite one of entryIDs.
func appendedCiting(h *testfix.FakeHost, entryIDs ...string) []ledger.Observation {
	out := []ledger.Observation{}
	for _, entry := range h.Entries {
		data, ok := ledger.ObservationsRecordedEntry(entry)
		if !ok || !strings.HasPrefix(entry.ID, "appended-") {
			continue
		}
		for _, o := range data.Observations {
			for _, id := range o.SourceEntryIDs {
				if slices.Contains(entryIDs, id) {
					out = append(out, o)
				}
			}
		}
	}
	return out
}

// persist appends the compaction PiG would persist for c to h's branch.
func persist(h *testfix.FakeHost, id string, c CompactionResult) {
	h.Entries = append(h.Entries, testfix.CompactionEntry(id, testfix.CompactionArgs{Summary: c.Summary, FirstKeptEntryID: c.FirstKeptEntryID, Details: testfix.ToJSONValue(c.Details)}))
}

// A realistic v1 session switched to memory v2 (docs/memory-v2.md, "Migration"): the layer's
// v1 worker instructions are migrated (the observer's addition folded into the consolidation
// instructions, the replace-mode reflector and the dropper retired, each once, v1's files kept),
// the first v2 compaction extracts only what neither v1's coverage nor its fold boundary reached
// and keeps every v1 record and reflection, a later native PiG compaction's summary is stored
// once as a host record that level 1 may restate, and the requests are counted: none at turn
// ends or level-0 compactions, exactly one for the level-1 compaction.
func TestV2MigrationFromV1Session(t *testing.T) {
	counter := testfix.CountRequests(t)
	entries := v1Session()
	rt, h := v2Setup(t, entries)
	h.Home = t.TempDir()
	h.UI = true
	store, ok := harness.StoreOf(h, rt.Config())
	if !ok || store.Layer().Engine != curated.EngineV2 {
		t.Fatal("no v2 store")
	}
	model := harness.ModelKey(h.SessionModel())

	// v1's layer and curated store.
	ls := store.Layer()
	for _, d := range []layer.Definition{
		{Kind: layer.KindInstructions, Name: agents.InstructionsObserver, Model: model, Text: "Mode: extend\nAlways record the port of every service."},
		{Kind: layer.KindInstructions, Name: agents.InstructionsReflector, Model: model, Text: "Mode: replace\nReflect only on deploys."},
		{Kind: layer.KindInstructions, Name: agents.InstructionsDropper, Model: model, Text: "Never drop a port."},
	} {
		if _, err := ls.Put(d); err != nil {
			t.Fatalf("put %s: %v", d.Name, err)
		}
	}
	if _, err := store.Save(curated.Memory{Name: "deploy-port", Type: curated.TypeProject, Description: "Staging deploys use port 8080."}, h.SessionID()); err != nil {
		t.Fatal(err)
	}

	// Layer definitions: migrated at session start, once.
	migrated := harness.MigrateLayerV2(h, store, model, "")
	if len(migrated) != 3 || !migrated[0].Folded || migrated[0].Name != agents.InstructionsObserver || migrated[1].Folded || migrated[2].Folded {
		t.Fatalf("migrations %+v", migrated)
	}
	consolidation, err := ls.Get(model, layer.KindInstructions, agents.InstructionsConsolidation)
	if err != nil || consolidation.Mode != layer.ModeExtend || !strings.Contains(consolidation.Text, "Always record the port of every service.") || strings.Contains(consolidation.Text, "Reflect only") {
		t.Fatalf("consolidation %+v (%v)", consolidation, err)
	}
	if again := harness.MigrateLayerV2(h, store, model, ""); len(again) != 0 {
		t.Fatalf("migrated twice: %+v", again)
	}
	for _, name := range []string{agents.InstructionsObserver, agents.InstructionsReflector, agents.InstructionsDropper} {
		if _, err := ls.Get(model, layer.KindInstructions, name); err != nil {
			t.Fatalf("v1's %s instructions were removed: %v", name, err)
		}
	}
	notices := strings.Join(h.Messages(), "\n")
	if !strings.Contains(notices, "folded the instructions/observer addition") || !strings.Contains(notices, "retires the instructions/reflector definition") || !strings.Contains(notices, "retires the instructions/dropper definition") {
		t.Fatalf("notices:\n%s", notices)
	}

	// A turn end under v2 reads only what v1 never observed, with no request.
	extractTurnV2(rt, h)
	if got := appendedCiting(h, "u1", "a1", "u2", "a2"); len(got) != 0 {
		t.Fatalf("re-extracted v1's range: %+v", got)
	}
	if len(appendedCiting(h, "u3")) == 0 {
		t.Fatal("the turn v1 never observed was not extracted")
	}

	// The first v2 compaction: nothing re-extracted, and v1's records fold as today: the one no
	// reflection cites stays active, the reflection is rendered, and the records it cites are
	// dropped by rule, still recallable.
	c := expectV2Compaction(t, runV2Hook(rt, h, v2Event(append([]ledger.Entry{}, h.Entries...), "u4", 20_000, "threshold")), "u4", h)
	if got := appendedCiting(h, "u1", "a1", "u2", "a2"); len(got) != 0 {
		t.Fatalf("re-extracted v1's range: %+v", got)
	}
	if shown := observationIDs(c.Details.Observations); !slices.Contains(shown, v1DBID) {
		t.Fatalf("v1 record %s lost: %v", v1DBID, shown)
	}
	if len(c.Details.Reflections) != 1 || c.Details.Reflections[0].ID != v1ReflID || !strings.Contains(c.Summary, v1ReflID) {
		t.Fatalf("v1 reflection lost: %+v\n%s", c.Details.Reflections, c.Summary)
	}
	folded := ledger.FoldLedger(h.Entries, "")
	for _, id := range []string{v1PortID, v1MakefileID} {
		if _, recorded := folded.ObservationsByID[id]; !recorded || !folded.DroppedObservationIDs[id] {
			t.Fatalf("v1 record %s neither dropped by its rendered reflection nor kept", id)
		}
		if text := snippettest.Recall(t, h.Entries, om.FormatRead(omwire.MRecall, id, omwire.Options{})); !strings.Contains(text, id) {
			t.Fatalf("recall %s: %s", id, text)
		}
	}
	if !strings.Contains(c.Summary, "Postgres 15.2") || !strings.Contains(c.Summary, "6379") {
		t.Fatalf("summary:\n%s", c.Summary)
	}
	persist(h, "c2", c)

	// A native PiG compaction (an overflow while the extension was disabled), then a v2 one.
	h.Entries = append(h.Entries,
		assistantMessage("a4", "Queue set up"),
		userMessage("u5", "Ship release 2.4.1 on Friday"),
		assistantMessage("a5", "Release 2.4.1 is scheduled"),
		userMessage("u6", "Now the docs for version 3"+strings.Repeat(", after the usual checks", 300)),
		assistantMessage("a6", "Docs for version 3 updated"+strings.Repeat(", after the usual checks", 300)),
		testfix.CompactionEntry("c3", testfix.CompactionArgs{Summary: "## Goal\nShip release 2.4.1 on Friday.\n\n## Done\n- the queue is set up", FirstKeptEntryID: "u6", Details: map[string]any{"readFiles": []any{}, "modifiedFiles": []any{}}}),
		userMessage("u7", "And the changelog"),
	)
	hostID := ids.HashID(ledger.OriginHost + "\x00" + "c3")
	cfg := rt.Config()
	cfg.ConsolidateOff = false
	rt.SetConfig(cfg)
	h.ModelsFake.Streams.Replies = []map[string]any{testfix.AssistantToolCall("call-1", consolidate.ToolName, map[string]any{
		"records": []any{map[string]any{"id": hostID, "subject": "release"}},
	})}
	out := level1CompactionOf2(t, runV2Hook(rt, h, v2Event(append([]ledger.Entry{}, h.Entries...), "u7", 20_000, "threshold")), "u7")
	if counter.Total() != 1 || h.ModelsFake.Streams.Calls() != 1 {
		t.Fatalf("requests %v", counter.ByStage())
	}
	system, _ := h.ModelsFake.Streams.Requests[0]["systemPrompt"].(string)
	if !strings.Contains(system, "Always record the port of every service.") {
		t.Fatalf("the folded observer addition is not in level 1's prompt:\n%s", system)
	}
	if !strings.Contains(fmt.Sprint(h.ModelsFake.Streams.Requests[0]["messages"]), hostID) {
		t.Fatal("the host record is not a level-1 candidate")
	}
	hosts := appendedCiting(h, "c3")
	if len(hosts) != 2 || hosts[0].ID != hostID || hosts[0].Origin != ledger.OriginHost || hosts[1].Subject != "release" || hosts[1].Origin != ledger.OriginHost {
		t.Fatalf("host records %+v", hosts)
	}
	if report := out.Details.Consolidation; !report.Applied || report.Dropped != 1 {
		t.Fatalf("report %+v", report)
	}
	if !strings.Contains(out.Summary, "Ship release 2.4.1 on Friday.") || !strings.Contains(out.Summary, "the queue is set up") || strings.Contains(out.Summary, hostID) {
		t.Fatalf("the host summary is not rendered whole as its restatement:\n%s", out.Summary)
	}
	for _, id := range []string{v1DBID} {
		if !slices.Contains(observationIDs(out.Details.Observations), id) {
			t.Fatalf("v1 record %s lost at the second v2 compaction", id)
		}
	}

	// A second hook on the same preparation (a retry of a compaction PiG did not persist) stores
	// no second host record. Level 1 is off for it: this checks the host record's dedup only.
	cfg.ConsolidateOff = true
	rt.SetConfig(cfg)
	runV2Hook(rt, h, v2Event(append([]ledger.Entry{}, h.Entries...), "u7", 20_000, "threshold"))
	if got := appendedCiting(h, "c3"); len(got) != 2 {
		t.Fatalf("host summary stored again: %+v", got)
	}
}

// A native summary too large for the summary budget is an omitted span whose printed memory
// query lists it.
func TestV2HostSummaryOmittedWhenItDoesNotFit(t *testing.T) {
	big := "Goal: " + strings.Repeat("keep the release notes for 2.4.1 current, ", 400)
	// PiG's order: the entries a compaction keeps precede its entry.
	entries := []ledger.Entry{
		userMessage("u1", "Start"),
		assistantMessage("a1", "Started"),
		userMessage("u2", "Ship release 2.4.1 on Friday at 10:00"),
		assistantMessage("a2", "Scheduled release 2.4.1 for Friday at 10:00"),
		testfix.CompactionEntry("c1", testfix.CompactionArgs{Summary: big, FirstKeptEntryID: "u2"}),
		userMessage("u3", "Next"),
	}
	rt, h := v2Setup(t, entries)
	// Room for the instructions and a few rows, never the host summary.
	removed := v2SourceTokens(v2SourceEntries(v2CutRange(entries, v2Event(entries, "u3", 0, "threshold")))) + tokens.EstimateStringTokens(big)
	room := tokens.EstimateStringTokens(selection.TablesInstructions) + 300
	c := expectV2Compaction(t, runV2Hook(rt, h, v2Event(entries, "u3", 200_000-16_384+removed-room, "threshold")), "u3", h)
	hosts := appendedCiting(h, "c1")
	if len(hosts) != 1 || hosts[0].Origin != ledger.OriginHost || hosts[0].Content != strings.TrimSpace(big) {
		t.Fatalf("host records %+v", hosts)
	}
	if strings.Contains(c.Summary, "current, keep the release notes") {
		t.Fatalf("an oversized host summary was rendered:\n%s", c.Summary)
	}
	if !slices.Contains(observationIDs(c.Details.Observations), hosts[0].ID) {
		t.Fatal("the host record is not in the memory the compaction folds")
	}
	after := append(append([]ledger.Entry{}, h.Entries...), testfix.CompactionEntry("c2", testfix.CompactionArgs{Summary: c.Summary, FirstKeptEntryID: "u3", Details: testfix.ToJSONValue(c.Details)}))
	listed := ""
	for _, q := range printedQueries(t, c.Summary) {
		listed += strings.Join(snippettest.Run(t, after, q).IDs, "\n") + "\n"
	}
	if !strings.Contains(listed, hosts[0].ID) {
		t.Fatalf("no printed query lists the host record:\n%s", c.Summary)
	}
}

// level1CompactionOf2 asserts a compaction on firstKept carrying a consolidation report.
func level1CompactionOf2(t *testing.T, outcome BeforeCompactOutcome, firstKept string) CompactionResult {
	t.Helper()
	if outcome.Cancel || outcome.Compaction == nil || outcome.Compaction.FirstKeptEntryID != firstKept || outcome.Compaction.Details.Consolidation == nil {
		t.Fatalf("compaction %+v", outcome)
	}
	return *outcome.Compaction
}

// A native summary is multi-line markdown; its host record is rendered on one line ending with
// its anchor, so its headings never read as sections of the v2 summary (level 1 off, lines
// layout).
func TestV2HostSummaryRenderedOnOneLine(t *testing.T) {
	// Long enough that the summary budget (never more than the range removes) holds the record.
	pad := strings.Repeat(", after the usual checks", 40)
	entries := []ledger.Entry{
		userMessage("u1", "Start"),
		assistantMessage("a1", "Started"),
		userMessage("u2", "Ship release 2.4.1 on Friday"+pad),
		assistantMessage("a2", "Scheduled release 2.4.1"+pad),
		testfix.CompactionEntry("c1", testfix.CompactionArgs{Summary: "## Goal\nShip release 2.4.1 on Friday.\n\n## Done\n- the queue is set up\n", FirstKeptEntryID: "u2"}),
		userMessage("u3", "Next"),
	}
	rt, h := v2Setup(t, entries)
	cfg := rt.Config()
	cfg.MemoryFormat = config.MemoryFormatLines
	rt.SetConfig(cfg)
	c := expectV2Compaction(t, runV2Hook(rt, h, v2Event(entries, "u3", 20_000, "threshold")), "u3", h)
	hosts := appendedCiting(h, "c1")
	want := "## Goal Ship release 2.4.1 on Friday. ## Done - the queue is set up"
	if len(hosts) != 1 || hosts[0].Content != want {
		t.Fatalf("host records %+v", hosts)
	}
	found := false
	for _, line := range strings.Split(c.Summary, "\n") {
		if strings.HasPrefix(line, "## Goal") || strings.HasPrefix(line, "## Done") || strings.HasPrefix(line, "- the queue") {
			t.Fatalf("the native summary's lines leaked into the summary layout:\n%s", c.Summary)
		}
		if strings.Contains(line, want) {
			found = true
			if !strings.HasSuffix(line, " (c1)") {
				t.Fatalf("the host record's line does not end with its anchor: %q", line)
			}
		}
	}
	if !found {
		t.Fatalf("the host record is not rendered whole:\n%s", c.Summary)
	}
}

// A compaction whose cut is not on the branch reads nothing, but still stores the native
// summary it replaces in context, once, so it never leaves both the context and the ledger.
func TestV2HostSummaryStoredWhenTheCutIsNotOnTheBranch(t *testing.T) {
	entries := []ledger.Entry{
		userMessage("u1", "Start"),
		assistantMessage("a1", "Started"),
		userMessage("u2", "Ship release 2.4.1 on Friday"),
		testfix.CompactionEntry("c1", testfix.CompactionArgs{Summary: "## Goal\nShip release 2.4.1 on Friday.", FirstKeptEntryID: "u2"}),
		userMessage("u3", "Next"),
	}
	counter := testfix.CountRequests(t)
	rt, h := v2Setup(t, entries)
	runV2Hook(rt, h, v2Event(entries, "not-on-branch", 20_000, "threshold"))
	hosts := appendedCiting(h, "c1")
	if len(hosts) != 1 || hosts[0].Origin != ledger.OriginHost || hosts[0].Content != "## Goal Ship release 2.4.1 on Friday." {
		t.Fatalf("host records %+v", hosts)
	}
	runV2Hook(rt, h, v2Event(append([]ledger.Entry{}, h.Entries...), "not-on-branch", 20_000, "threshold"))
	if got := appendedCiting(h, "c1"); len(got) != 1 {
		t.Fatalf("host summary stored again: %+v", got)
	}
	if counter.Total() != 0 {
		t.Fatalf("requests %v", counter.ByStage())
	}
}

// The code group the kernel gained for the memory tool's Go code (kernel.OwnsCode) is matched
// exactly and never owned for every kind or for a layer kind, so data that loaded before keeps
// loading: a subject "time" keeps its canonical path and its aliases, and a layer detector
// named "errors" (or "time") is accepted when written and when loaded.
func TestV2CodeNamesStayFreeForSubjectsAndLayer(t *testing.T) {
	for _, subject := range []string{"time", "errors", "time/zones", "strings/builder"} {
		if _, ok := ledger.SubjectPath(subject); !ok {
			t.Errorf("subject %q is refused as a path", subject)
		}
		if stored, refused := ledger.RecordableSubject(subject); refused || stored != subject {
			t.Errorf("subject %q stored as %q, refused %v", subject, stored, refused)
		}
	}
	inForce, refused := ledger.CheckSubjectAliases([]string{"time", "clock"}, []ledger.SubjectAlias{{From: "clock", To: "time"}})
	if len(refused) != 0 || len(inForce) != 1 {
		t.Fatalf("aliases in force %+v, refused %+v", inForce, refused)
	}

	ls := curated.New(t.TempDir()).Layer()
	for _, name := range []string{"errors", "time"} {
		if _, err := ls.Put(layer.Definition{Kind: layer.KindDetector, Name: name, Model: "p/m", Text: "detector[1]{query,description}:\n  \"notes | where kind=plan\",plans recorded"}); err != nil {
			t.Fatalf("detector %q: %v", name, err)
		}
	}
	h, reports, err := ls.Load("p/m")
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reports {
		t.Errorf("load report %+v", r)
	}
	var names []string
	for _, d := range rules.ModelDetectors(h) {
		names = append(names, d.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"errors", "time"}) {
		t.Fatalf("detectors loaded %v", names)
	}
}

// A tables summary written before the code tool ends its instructions with the query language's
// paragraph (ledger.LegacyTablesRecallParagraph, frozen): its tables are still found, so its
// omitted span starts keep classifying a code call's hits as omitted (its printed old queries
// get the removal notice and a snippet hint: harness TestLegacyQueriesNeverRun). So does one the
// code tool's first build wrote, whose paragraph held its first placeholders
// (ledger.FirstCodeTablesRecallParagraph, frozen).
func TestPreUpgradeTablesSummaryStillClassifiesHits(t *testing.T) {
	for _, frozen := range []string{ledger.LegacyTablesRecallParagraph, ledger.FirstCodeTablesRecallParagraph} {
		preUpgradeTablesSummaryClassifies(t, frozen)
	}
	if ledger.FirstCodeTablesRecallParagraph == ledger.TablesRecallParagraph || !strings.Contains(ledger.FirstCodeTablesRecallParagraph, "om.Search(`w`)") {
		t.Fatal("the first build's paragraph is frozen as it was written")
	}
}

func preUpgradeTablesSummaryClassifies(t *testing.T, frozen string) {
	t.Helper()
	body := tables.Rows.Encode(
		tables.Table{Name: "notes", Fields: []string{"id", "when", "relevance", "content"}, Rows: [][]string{{"aaaaaaaaaaa1", "2026-05-02", "high", "Shown."}}},
		tables.Table{Name: "omitted", Fields: []string{"from", "to", "count", "first_id", "query"}, Rows: [][]string{{"2026-05-01", "2026-05-01", "2", "aaaaaaaaaaa2", "notes | from u0 | through u1"}}},
	)
	legacyInstructions := strings.Replace(selection.TablesInstructions, ledger.TablesRecallParagraph, frozen, 1)
	if legacyInstructions == selection.TablesInstructions || !strings.Contains(ledger.LegacyTablesRecallParagraph, "recall <id>") {
		t.Fatal("the fixture needs the frozen paragraph")
	}
	summary := legacyInstructions + "\n\n" + body
	if got, ok := ledger.TablesSummaryBody(summary); !ok || got != strings.TrimSpace(body) {
		t.Fatalf("pre-upgrade body %q %v", got, ok)
	}
	details := testfix.MemoryDetails(testfix.MemoryDetailsArgs{FullFold: true, Observations: []ledger.Observation{testfix.Observation("aaaaaaaaaaa1"), testfix.Observation("aaaaaaaaaaa2"), testfix.Observation("aaaaaaaaaaa3")}})
	entries := []ledger.Entry{testfix.RawMessage("u1", "hi"), testfix.CompactionEntry("c1", testfix.CompactionArgs{FirstKeptEntryID: "u1", Details: details, Summary: summary})}
	v := harness.VisibilityOf(entries, nil)
	if outcome, target := v.Classify([]string{"aaaaaaaaaaa2"}); outcome != curated.OutcomeOmitted || target != curated.TargetSummary {
		t.Fatalf("an omitted span start classified %s %s", outcome, target)
	}
	if outcome, _ := v.Classify([]string{"aaaaaaaaaaa1"}); outcome != curated.OutcomeShown {
		t.Fatalf("a shown record classified %s", outcome)
	}
}
