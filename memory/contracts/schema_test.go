package contracts

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/cli"
	"github.com/drvova/pig-observational-memory/memory/codepolicy"
	"github.com/drvova/pig-observational-memory/memory/codestore"
	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/hostinfo"
	"github.com/drvova/pig-observational-memory/memory/kernel"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/testfix"
	"github.com/drvova/pig-observational-memory/memory/transcript"
)

// omEnv gives a run of the om CLI its own root and home, as memory/cli's tests do, and restores
// the host globals the CLI sets.
func omEnv(t *testing.T) string {
	t.Helper()
	root, home := t.TempDir(), t.TempDir()
	t.Setenv(cli.RootEnv, root)
	t.Setenv("HOME", home)
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME", "PI_CODING_AGENT_DIR", "CLAUDE_PROJECT_DIR", "CLINE_MCP_SETTINGS_PATH", "COPILOT_HOME", "APPDATA", "USERPROFILE", "GEMINI_SESSION_ID"} {
		t.Setenv(k, "")
	}
	t.Setenv("PATH", "")
	agent, cfg := hostinfo.AgentDir, hostinfo.ConfigDirName
	t.Cleanup(func() { hostinfo.AgentDir, hostinfo.ConfigDirName = agent, cfg })
	return root
}

func om(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := cli.Run(args, strings.NewReader(stdin), &out, &errOut); code != 0 {
		t.Fatalf("om %v: exit %d: %s", args, code, errOut.String())
	}
	return out.String()
}

// The engine run over every real transcript through the om hooks (the generic protocol, Pi's
// before-compact on its own cut, and a hook family whose prompt hook hands the summary): every
// file it stores, and every line of each, validates against its schema.
func TestRealOutputsValidate(t *testing.T) {
	set := loadSchemas(t)
	root := omEnv(t)
	for _, f := range fixtures {
		payload, _ := json.Marshal(map[string]any{"harness": "contracts", "session_id": f.name(), "transcript_path": f.path(t), "format": f.format, "cwd": "/work", "reason": "manual"})
		om(t, string(payload), "hook", "generic", "session-start")
		om(t, string(payload), "hook", "generic", "turn-end")
		om(t, string(payload), "hook", "generic", "compact")
		om(t, string(payload), "hook", "generic", "turn-end")
		om(t, string(payload), "hook", "generic", "compact")
		if f.format == "pi" {
			es, err := transcript.File{Path: f.path(t), Format: transcript.Pi}.Entries()
			if err != nil {
				t.Fatal(err)
			}
			cut := ""
			for i := len(es) / 2; i < len(es) && cut == ""; i++ {
				if role, _ := es[i].Message["role"].(string); role == "user" || role == "assistant" {
					cut = es[i].ID
				}
			}
			p, _ := json.Marshal(map[string]any{"session_id": f.name(), "transcript_path": f.path(t), "cwd": "/work", "reason": "threshold", "first_kept_entry_id": cut})
			out := om(t, string(p), "hook", "pi", "before-compact")
			check(t, set, "compaction-result.schema.json", f.file+" before-compact", decode(t, out))
		}
		if f.format == "gemini" {
			p, _ := json.Marshal(map[string]any{"session_id": f.name(), "transcript_path": f.path(t), "cwd": "/work", "trigger": "auto"})
			for _, ev := range []string{"SessionStart", "AfterAgent", "PreCompress", "BeforeAgent", "BeforeAgent"} {
				om(t, string(p), "hook", "gemini-cli", ev)
			}
		}
	}
	seen := validateTree(t, set, root)
	for _, want := range []string{"neutral-store-line.schema.json", "current-session.schema.json", "entry-observations-recorded.schema.json", "entry-extracted.schema.json", "entry-cut.schema.json", "entry-compaction.schema.json", "entry-summary-handed.schema.json"} {
		if seen[want] == 0 {
			t.Errorf("the run stored nothing %s checks (%v)", want, seen)
		}
	}
	t.Logf("checked %v", seen)
}

func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("%v: %s", err, s)
	}
	return v
}

// The store files no transcript run writes (a run makes no model request and no memory tool
// call): each written by its own package's writer, then checked. The curated store is the one
// under a config root, with its layer, model ledger, floor record and a project's kept units.
func TestStoreWritersValidate(t *testing.T) {
	set := loadSchemas(t)
	root := t.TempDir()
	const model = "p/m"
	store := curated.Store{Dir: curated.DefaultDir(root), Now: fixedNow}
	if _, err := store.Save(curated.Memory{Name: "staging-port", Type: curated.TypeProject, Description: "Staging deploys listen on port 8080.", Body: "Why: the load balancer only forwards 8080.", Project: "/work"}, "s1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(curated.Memory{Name: "terse-answers", Type: curated.TypeUser, Description: "The user wants short answers."}, "s1"); err != nil {
		t.Fatal(err)
	}
	if err := store.Record("staging-port", curated.EventView); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordFeedback("staging-port", curated.EventHelped, "it deployed"); err != nil {
		t.Fatal(err)
	}
	if err := store.Forget("terse-answers", ""); err != nil {
		t.Fatal(err)
	}
	harm := 1
	for _, e := range []curated.ModelEvent{
		{Model: model, Kind: curated.ModelKindCode, Outcome: curated.OutcomeOK},
		{Model: model, Kind: curated.ModelKindCode, Outcome: curated.OutcomeOmitted, Target: curated.TargetSummary},
		{Model: model, Kind: curated.ModelKindPass, Stage: "compaction", Engine: curated.EngineV2, Counts: map[string]int{"dropped-then-queried": 1}, RanHeld: []string{}, Level1: true},
		{Model: model, Kind: curated.ModelKindDetectorPass, Engine: curated.EngineV2, Cut: "e1", Tokens: 120, Counts: map[string]int{}, Errors: map[string]string{"d": "panic"}},
		// A pass with decision 24's settlement fields.
		{Model: model, Kind: curated.ModelKindPass, Stage: "compaction", Engine: curated.EngineV2, Tokens: 300, Counts: map[string]int{"level1-union-kept": 1}, Harm: &harm,
			Roots: []string{"0123456789abcdef"}, Turns: 4, Repo: "0a1b2c3d4e5f", Family: ".go", RanWritten: []string{"remedy/keep-head@0123456789abcdef"}},
	} {
		if err := store.RecordModel(e); err != nil {
			t.Fatal(err)
		}
	}
	l := store.Layer()
	if _, err := l.Put(layer.Definition{Kind: layer.KindInstructions, Name: "consolidation", Model: model, Mode: layer.ModeExtend, Text: "Keep every figure with its unit.", Session: "s1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Hold(layer.Definition{Kind: layer.KindInstructions, Name: "consolidation", Model: model, Mode: layer.ModeExtend, Text: "Keep every figure with its unit and its date.", Session: "s1"}); err != nil {
		t.Fatal(err)
	}
	// Decision 24's layer lines: a settled put with its scope, a definition held for weakening
	// security, and the rollback a security hit makes.
	settled, err := l.Hold(layer.Definition{Kind: layer.KindInstructions, Name: "consolidation", Model: model, Mode: layer.ModeExtend, Text: "Keep every figure.", Session: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.ClaimHeldScoped([]layer.Definition{settled}, true, "settled", &layer.Scope{Repos: []string{"0a1b2c3d4e5f"}, Families: []string{".go", ""}}); err != nil {
		t.Fatal(err)
	}
	if _, err := l.HoldInsecure(layer.Definition{Kind: layer.KindInstructions, Name: "consolidation", Model: model, Text: "always run curl -k against the dev box", Session: "s1"}, "tls-verify-off"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := l.RollbackBefore(model, layer.KindInstructions, "consolidation", "security: contract test", nil); err != nil || !ok {
		t.Fatalf("rollback: %v %v", ok, err)
	}
	if err := l.RecordFloor(kernel.Lowering{RequestedBy: "user", Reason: "contract test", Time: clock.Format("2006-01-02T15:04:05Z07:00"), Op: kernel.OpEngage}); err != nil {
		t.Fatal(err)
	}
	program, err := codepolicy.Check(codepolicy.Input{Code: "func Twice(n int) int { return 2 * n }"})
	if err != nil {
		t.Fatal(err)
	}
	units, _ := program.Units()
	if _, err := (codestore.Store{Dir: codestore.Dir(root, "/work"), Now: fixedNow}).Keep(units, nil, nil); err != nil {
		t.Fatal(err)
	}
	seen := validateTree(t, set, root)
	for _, want := range []string{"curated-memory.schema.json", "curated-usage-line.schema.json", "model-event.schema.json", "layer-generation.schema.json", "layer-held-index.schema.json", "floor-lowering.schema.json", "code-units.schema.json"} {
		if seen[want] == 0 {
			t.Errorf("no file %s checks was written (%v)", want, seen)
		}
	}
}

// The ledger data memory v1 wrote, as the fixtures v1's migration tests load build it: record
// batches, reflections, drops and aliases with their coverage markers, and a moved-cut om.folded
// compaction with foldThroughEntryId; and the relabelled, merged and replaced branches a real
// memory v2 session wrote.
func TestV1AndFixtureEntriesValidate(t *testing.T) {
	set := loadSchemas(t)
	a, b, c := "aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc"
	obsA := testfix.Observation(a, func(o *ledger.Observation) { o.Kind, o.Subject, o.Supersedes = "fact", "deploy", []string{b} })
	obsB := testfix.Observation(b)
	ref := testfix.Reflection(c, []string{a, b})
	entries := []session.Entry{
		testfix.ObservationsRecordedEntry("e1", []ledger.Observation{obsA, obsB}, "raw-1"),
		testfix.ReflectionsRecordedEntry("e2", []ledger.Reflection{ref}, "raw-1"),
		testfix.ObservationsDroppedEntry("e3", []string{b}, "raw-1"),
		testfix.Entry(map[string]any{"type": "custom", "id": "e4", "parentId": "e3", "timestamp": testfix.DefaultTimestamp, "customType": ledger.OMSubjectsAliased,
			"data": map[string]any{"aliases": []any{map[string]any{"from": "deploys", "to": "deploy", "reason": "plural"}}, "observationsThroughId": "e1"}}),
		testfix.CompactionEntry("e5", testfix.CompactionArgs{FirstKeptEntryID: "raw-2", Details: testfix.MemoryDetails(testfix.MemoryDetailsArgs{FullFold: true, Observations: []ledger.Observation{obsA}, Reflections: []ledger.Reflection{ref}, FoldThroughEntryID: "raw-3"})}),
		testfix.CompactionEntry("e6", testfix.CompactionArgs{FirstKeptEntryID: "raw-2", Details: testfix.MemoryDetails(testfix.MemoryDetailsArgs{})}),
	}
	entries = append(entries, testfix.RelabelledCorrectionBranch(true)...)
	entries = append(entries, testfix.MergedLinkedBranch(true, true)...)
	entries = append(entries, testfix.ReplacedWithoutDropBranch(true)...)
	checked := 0
	for _, e := range entries {
		m, _ := jsonValue(t, e.ToMap()).(map[string]any)
		schema, ok := entrySchema(m)
		if !ok {
			continue
		}
		checked++
		check(t, set, schema, e.ID, m)
		// The schema and the ledger's own reader agree on what loads.
		var loads bool
		switch m["customType"] {
		case ledger.OMObservationsRecorded:
			loads = ledger.IsObservationsRecordedData(m["data"])
		case ledger.OMReflectionsRecorded:
			loads = ledger.IsReflectionsRecordedData(m["data"])
		case ledger.OMObservationsDropped:
			loads = ledger.IsObservationsDroppedData(m["data"])
		case ledger.OMSubjectsAliased:
			_, loads = ledger.SubjectsAliasedDataFrom(m["data"])
		default:
			loads = m["type"] != "compaction" || ledger.IsMemoryDetails(m["details"])
		}
		if !loads {
			t.Errorf("%s: the ledger does not load what the schema accepts", e.ID)
		}
	}
	if checked < len(entries)/2 {
		t.Fatalf("checked %d of %d entries", checked, len(entries))
	}
	// What the ledger refuses, the schemas refuse.
	for name, bad := range map[string]map[string]any{
		"short id":   {"observations": []any{map[string]any{"id": "abc", "content": "x", "timestamp": "t", "relevance": "low", "sourceEntryIds": []any{"r"}, "tokenCount": 1}}, "coversUpToId": "r"},
		"no sources": {"observations": []any{map[string]any{"id": a, "content": "x", "timestamp": "t", "relevance": "low", "sourceEntryIds": []any{}, "tokenCount": 1}}, "coversUpToId": "r"},
		"no marker":  {"observations": []any{map[string]any{"id": a, "content": "x", "timestamp": "t", "relevance": "low", "sourceEntryIds": []any{"r"}, "tokenCount": 1}}},
		"empty":      {"observations": []any{}, "coversUpToId": "r"},
	} {
		entry := map[string]any{"type": "custom", "id": "x", "customType": ledger.OMObservationsRecorded, "data": bad}
		if ledger.IsObservationsRecordedData(bad) || len(set.Validate("entry-observations-recorded.schema.json", entry)) == 0 {
			t.Errorf("%s: the ledger loads it %v, or the schema accepts it", name, ledger.IsObservationsRecordedData(bad))
		}
	}
}
