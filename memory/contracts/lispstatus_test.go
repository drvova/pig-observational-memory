package contracts

// The parity of /om:status and the harness reads the image ported with it
// (docs/lisp-port-status.md): the status report over the stores the migration and evidence parity
// tests build, the profile over random usage ledgers, the migration of rules written before the
// layer, and the frozen query language's reader (legacyquery.Hint, Substitute, what an old call
// asked for). The Go engine and lisp/status-parity.lisp run over copies of the same files with
// the clock fixed. Like the other Lisp parity tests it runs only with OM_LISP_PARITY=1 and sbcl.

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/commands"
	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/harness"
	"github.com/drvova/pig-observational-memory/memory/hostinfo"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/legacyquery"
	"github.com/drvova/pig-observational-memory/memory/rules"
	"github.com/drvova/pig-observational-memory/memory/runtime"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

func statusScript(t *testing.T) (sbcl, script string) {
	t.Helper()
	sbcl, _ = lispParity(t)
	script, err := filepath.Abs(filepath.Join("..", "..", "lisp", "status-parity.lisp"))
	if err != nil {
		t.Fatal(err)
	}
	return sbcl, script
}

const statusParitySettings = `{"observational-memory": {"observeAfterTokens": 5000, "memoryFormat": "tables"}, "compaction": {"enabled": false}}`

// goStatus is commands.StatusLines over a host whose config home is home, with an empty
// branch and the session model key (none for ""), its project root home.
func goStatus(t *testing.T, home, model, agentDir string) []string {
	t.Helper()
	old := hostinfo.AgentDir
	hostinfo.AgentDir = func() (string, error) { return agentDir, nil }
	defer func() { hostinfo.AgentDir = old }()
	h := testfix.NewFakeHost()
	h.Home, h.WorkDir = home, home
	if provider, id, ok := strings.Cut(model, "/"); ok {
		h.Model = testfix.Model(provider, id)
	}
	lines, err := commands.StatusLines(runtime.New(), h)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

func compareStatus(t *testing.T, sbcl, script, name, home, model string, at time.Time) {
	t.Helper()
	agentDir := t.TempDir()
	settings := filepath.Join(agentDir, "settings.json")
	writeFile(t, settings, statusParitySettings)
	lispHome := t.TempDir()
	copyTree(t, home, lispHome)
	want := goStatus(t, home, model, agentDir)
	got := runLisp(t, sbcl, script, "status", lispHome, orDash(model), settings, at.Format(time.RFC3339Nano))
	if !reflect.DeepEqual(want, got) {
		for i := 0; i < len(want) || i < len(got); i++ {
			var w, g string
			if i < len(want) {
				w = want[i]
			}
			if i < len(got) {
				g = got[i]
			}
			if w != g {
				t.Fatalf("%s: status line %d differs:\n go:   %s\n lisp: %s\n\nGo report:\n%s", name, i+1, w, g, strings.Join(want, "\n"))
			}
		}
	}
	// Status moves the same files on both sides (the legacy definitions into the layer; Go's
	// StoreOf stamps those with the wall clock, so only the names are compared).
	names := func(files map[string]string) []string {
		out := []string{}
		for rel := range files {
			if !strings.Contains(rel, "/blobs/") { // a blob is named by its content's digest
				out = append(out, rel)
			}
		}
		sort.Strings(out)
		return out
	}
	if g, l := names(storeFiles(t, home)), names(storeFiles(t, lispHome)); !reflect.DeepEqual(g, l) {
		t.Fatalf("%s: the files after status differ:\n go:   %v\n lisp: %v", name, g, l)
	}
}

// TestLispStatusOverParityStores compares the status report over each layer migration store
// (before and after its session start's migration) and each evidence script's store.
func TestLispStatusOverParityStores(t *testing.T) {
	sbcl, script := statusScript(t)
	migrationScript, err := filepath.Abs(filepath.Join("..", "..", "lisp", "migration-parity.lisp"))
	if err != nil {
		t.Fatal(err)
	}
	lines := 0
	for _, c := range migrationCases {
		home := t.TempDir()
		dir := curated.DefaultDir(home)
		c.build(t, fixedStore(dir, migrationBuilt))
		compareStatus(t, sbcl, script, c.name+" (before the start)", home, c.model, migrationRun)
		runLisp(t, sbcl, migrationScript, dir, orDash(c.model), orDash(c.worker), migrationRun.Format(time.RFC3339Nano), orDash(strings.Join(c.taken, ",")))
		compareStatus(t, sbcl, script, c.name+" (after the start)", home, c.model, migrationRun)
		lines += len(goStatus(t, home, c.model, t.TempDir()))
	}
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	for seed := uint64(1); seed <= 3; seed++ {
		home := t.TempDir()
		dir := curated.DefaultDir(home)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		seedEvidenceStore(t, dir, start)
		goEvidence(t, dir, evidenceSteps(rand.New(rand.NewPCG(seed, 17)), start))
		for _, model := range []string{"", "p/a", "q/d"} {
			compareStatus(t, sbcl, script, fmt.Sprintf("evidence seed %d, model %q", seed, model), home, model, start)
		}
		lines += len(goStatus(t, home, "p/a", t.TempDir()))
	}
	t.Logf("identical: %d migration stores twice, 3 evidence stores under 3 session models (%d lines in the last reports)", len(migrationCases), lines)
}

// randomLedger is a usage ledger of model whose votes switch every setting now and then.
func randomLedger(rng *rand.Rand, model string, start time.Time) []curated.ModelEvent {
	out := []curated.ModelEvent{}
	at := start
	for range 20 + rng.IntN(60) {
		at = at.Add(time.Duration(1+rng.IntN(90)) * time.Minute)
		e := curated.ModelEvent{Time: at.Format(time.RFC3339), Model: model, Engine: curated.EngineV2}
		if rng.IntN(10) == 0 {
			e.Engine = ""
		}
		switch rng.IntN(4) {
		case 0:
			e.Kind, e.Codec = curated.ModelKindDecode, []string{"rows", "rows", "jsonl"}[rng.IntN(3)]
			e.Outcome = []string{curated.OutcomeError, curated.OutcomeOK, curated.OutcomeColumns}[rng.IntN(3)]
		case 1:
			e.Kind, e.Target = curated.ModelKindCode, []string{curated.TargetSummary, curated.TargetSummary, ""}[rng.IntN(3)]
			e.Outcome = []string{curated.OutcomeShown, curated.OutcomeOmitted, curated.OutcomeError}[rng.IntN(3)]
		default:
			e.Kind, e.Stage, e.Tokens = "pass", "compaction", 100+rng.IntN(900)
			if rng.IntN(5) == 0 {
				e.Outcome = "empty"
			}
		}
		out = append(out, e)
	}
	return out
}

// TestLispProfileParity compares harness.Ledger.Profile with the port over random ledgers,
// with and without a switch-codec rule that applies.
func TestLispProfileParity(t *testing.T) {
	sbcl, script := statusScript(t)
	start := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	switches := 0
	for seed := uint64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewPCG(seed, 3))
		dir := t.TempDir()
		s := curated.New(dir)
		s.Now = func() time.Time { return start }
		model := "p/m"
		for _, e := range randomLedger(rng, model, start) {
			if err := s.RecordModel(e); err != nil {
				t.Fatal(err)
			}
		}
		if seed%4 == 0 {
			if _, err := rules.Add(s, rules.Rule{Detector: rules.DetectDecodeFailure, Remedy: rules.RemedySwitchCodec, Models: []string{model},
				State: rules.StateEstablished, Engine: curated.EngineV2, Reason: "test"}); err != nil {
				t.Fatal(err)
			}
		}
		p := harness.Ledger{Store: s, Model: model}.Profile()
		want := []string{fmt.Sprintf("codec %s layout %s halvings %d events %d", p.Codec, p.Layout, p.SummaryHalvings, p.Events)}
		for _, sw := range p.Switches {
			want = append(want, sw.String())
		}
		switches += len(p.Switches)
		got := runLisp(t, sbcl, script, "profile", dir, model)
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("seed %d: Go\n  %s\nLisp\n  %s", seed, strings.Join(want, "\n  "), strings.Join(got, "\n  "))
		}
	}
	if switches < 20 {
		t.Fatalf("the ledgers switched too little: %d switches", switches)
	}
	t.Logf("identical: 40 ledgers, %d switches", switches)
}

// TestLispRuleMigrationParity runs rules.migrate on the Go engine (through rules.List) and the
// port over the same legacy rule files: a v2 rule, a v1 default rule, a file that is not a rule,
// a rule the layer already holds, and a rule that applies to no model.
func TestLispRuleMigrationParity(t *testing.T) {
	sbcl, script := statusScript(t)
	at := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	goDir, lispDir := t.TempDir(), t.TempDir()
	s := curated.New(goDir)
	s.Now = func() time.Time { return at }
	held, err := rules.Add(s, rules.Rule{Detector: rules.DetectUncitedAssistantSpecifics, Remedy: rules.RemedyKeepHead, Models: []string{"p/a"},
		State: rules.StateEstablished, Engine: curated.EngineV2, Reason: "in the layer"})
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(goDir, "rules")
	v2 := rules.Rule{ID: "0123456789ab", Detector: rules.DetectDecodeFailure, Remedy: rules.RemedySwitchCodec, Models: []string{"p/b"},
		State: rules.StateProbation, Engine: curated.EngineV2, Reason: "legacy v2", Created: at.Add(-time.Hour)}
	v1 := rules.Rule{ID: "ba9876543210", Detector: rules.DetectUncitedAssistantSpecifics, Remedy: rules.RemedyRequireKind, Models: []string{rules.AllModels},
		State: rules.StateEstablished, Reason: "legacy v1", Created: at.Add(-2 * time.Hour)}
	writeFile(t, filepath.Join(legacy, v2.ID+".rows"), encodeRule(t, v2)+"\n")
	writeFile(t, filepath.Join(legacy, v1.ID+".rows"), encodeRule(t, v1)+"\n")
	writeFile(t, filepath.Join(legacy, "junk.rows"), "not a rule\n")
	same := held
	same.Reason = "the legacy copy"
	writeFile(t, filepath.Join(legacy, held.ID+".rows"), strings.Replace(encodeRule(t, same), same.ID, held.ID, 1)+"\n")
	writeFile(t, filepath.Join(legacy, "notes.txt"), "not a rows file\n")
	copyTree(t, goDir, lispDir)
	if _, err := rules.List(s); err != nil {
		t.Fatal(err)
	}
	runLisp(t, sbcl, script, "migrate-rules", lispDir, at.Format(time.RFC3339Nano))
	goFiles, lispFiles := storeFiles(t, goDir), storeFiles(t, lispDir)
	if !reflect.DeepEqual(goFiles, lispFiles) {
		for rel, content := range goFiles {
			if lispFiles[rel] != content {
				t.Errorf("%s differs\nGo:\n%s\nLisp:\n%s", rel, content, lispFiles[rel])
			}
		}
		for rel := range lispFiles {
			if _, ok := goFiles[rel]; !ok {
				t.Errorf("the port wrote %s, which Go does not", rel)
			}
		}
	}
	for _, name := range []string{v2.ID + ".rows.migrated", v1.ID + ".rows.migrated", "junk.rows.refused", held.ID + ".rows.migrated"} {
		if _, ok := goFiles["rules/"+name]; !ok {
			t.Errorf("Go did not leave rules/%s: the case tests nothing", name)
		}
	}
	if _, err := os.Stat(filepath.Join(goDir, "harness", layer.ModelDir("p/b"), "remedy", v2.ID+".rows")); err != nil {
		t.Errorf("the v2 rule was not migrated: %v", err)
	}
}

// legacyQueries are queries of the removed language: reads every hint path takes, writes, and
// queries that do not parse.
var legacyQueries = []string{
	`notes | search "deploy"`, `notes | search deploy | sort id desc | limit 5`, `notes | where kind=fact | where subject~"api"`,
	`notes | since 2024-01-01 event | until 2024-02-28 event`, `notes | since 2024-01-01 event | until 2024-02-28`,
	`notes | until 2024-02-30`, `notes | through 0123456789ab asof | until 2024-01-01`, `notes | through 0123456789ab asof`,
	`notes | from abcdef12 | through 0123456789ab`, `notes | from not-an-id`, `history | where content~"x" | count`,
	`history | sort content`, `notes | count by kind`, `notes | group by subject`, `notes | limit 3 | count`,
	`notes | sort id | search "late"`, `notes | limit 2 | sort id`, `notes | count | limit 2`, `notes | limit 0`, `notes | limit 201`,
	`notes | sort id | sort kind`, `notes | where Kind=fact`, `recall 0123456789ab`, `recall 0123456789ab offset 10`, `recall "my-note"`,
	`recall "My Note"`, `recall abc | limit 2`, `saved | search "tabs"`, `reflections | search "x\"y\\z"`, `harness`, `harness | limit 3`,
	`save name=x type=user description="hi"`, `edit name=x description="new one"`, `forget name=x`, `feedback name=x outcome=helped quote="q"`,
	`define d = notes | search "y" "what it returns"`, `propose detector=thin-chunk remedy=keep-head`, `harness kind=format name=x text="y"`,
	`harness forget kind=format name=x`, `rollback 3`, `rollback 3 model="p/m"`, `rollback x`, `save name=x`, `save name=x name=y`,
	`bogus`, `| notes`, `notes |`, `notes | search`, `notes | where kind`, `notes | where kind=`, `notes | since yesterday`,
	`notes "unterminated`, `notes | search "$input"`, `notes | where id=$input`, ``, `   `, `notes | search "ünïcödé wörds"`,
	`notes | search "tab	inside" | where subject~"a\nb"`, `recall 0123456789ab offset -1`, `notes | until 2024-01-01 event asof`,
}

// TestLispLegacyQueryParity compares legacyquery.Hint, Substitute and what an old call asked
// for (rules.MemoryAsks' legacyAsk) with the port, query by query.
func TestLispLegacyQueryParity(t *testing.T) {
	sbcl, script := statusScript(t)
	path := filepath.Join(t.TempDir(), "queries.json")
	writeJSON(t, path, legacyQueries)
	got := runLisp(t, sbcl, script, "hint", path)
	if len(got) != len(legacyQueries) {
		t.Fatalf("%d answers for %d queries", len(got), len(legacyQueries))
	}
	snippets := 0
	for i, q := range legacyQueries {
		snippet, reason := legacyquery.Hint(q)
		recalled, searches, ok := []string{}, []string{}, false
		if strings.TrimSpace(q) != "" {
			if parsed, err := legacyquery.Parse(q); err == nil && parsed.Write == "" {
				ok = true
				if parsed.Source == legacyquery.SourceRecall {
					recalled = append(recalled, parsed.ID)
				}
				for _, s := range parsed.Stages {
					if s.Kind == legacyquery.StageSearch {
						searches = append(searches, s.Words)
					}
				}
			}
		}
		want := map[string]any{"snippet": snippet, "reason": reason, "substituted": legacyquery.Substitute(q, `a b"c`), "ok": ok, "recalled": recalled, "searches": searches}
		var answer map[string]any
		if err := json.Unmarshal([]byte(got[i]), &answer); err != nil {
			t.Fatalf("%q: %v: %s", q, err, got[i])
		}
		wantJSON, _ := json.Marshal(want)
		gotJSON, _ := json.Marshal(answer)
		if string(wantJSON) != string(gotJSON) {
			t.Errorf("%q:\n go:   %s\n lisp: %s", q, wantJSON, gotJSON)
		}
		if snippet != "" {
			snippets++
		}
	}
	t.Logf("identical: %d queries, %d snippets", len(legacyQueries), snippets)
}
