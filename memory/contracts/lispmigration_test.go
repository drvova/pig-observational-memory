package contracts

// The parity of the layer part of a session start (docs/lisp-port-migration.md): over the same
// curated stores, harness.Manager.NoteRetired with harness.MigrateLayerV2 on the Go engine and
// lisp/migration-parity.lisp on the SBCL port, with the clock fixed, write the same files byte
// for byte (the layer files, blobs, held index and manifest lines, and the legacy definitions'
// renames), tell a host with a UI the same messages and build the same skip list; a second start
// changes nothing on either side. The files written validate against contracts/schemas. Like the
// other Lisp parity tests it runs only with OM_LISP_PARITY=1 and sbcl on PATH.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/agents"
	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/harness"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/tables"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

var (
	migrationBuilt = time.Date(2026, 9, 1, 10, 0, 0, 123456000, time.UTC)
	migrationRun   = time.Date(2026, 11, 6, 8, 30, 0, 0, time.UTC)
)

// migrationCase is a store a session start migrates: the session model's key ("" on om's
// neutral host), memory v1's worker key, and the host's tools.
type migrationCase struct {
	name          string
	model, worker string
	taken         []string
	build         func(t *testing.T, s curated.Store)
}

func mustDef(t *testing.T, s curated.Store, d layer.Definition) {
	t.Helper()
	if _, err := s.Layer().Put(d); err != nil {
		t.Fatalf("put %s/%s: %v", d.Kind, d.Name, err)
	}
}

func instructions(model, name, text string) layer.Definition {
	return layer.Definition{Kind: layer.KindInstructions, Name: name, Model: model, Text: text, Session: "s0"}
}

// preModeFile is a layer file as the store wrote it before instructions modes: no mode column.
func preModeFile(d layer.Definition, fallback string) string {
	row := []string{d.Kind, d.Name, d.Model, d.Time.UTC().Format(time.RFC3339Nano), d.Session, "", fallback}
	absent := make([]bool, len(row))
	for i, v := range row {
		absent[i] = v == ""
	}
	header := tables.Rows.Encode(tables.Table{Name: "layer", Fields: []string{"kind", "name", "model", "time", "session", "overrides", "fallback"}, Rows: [][]string{row}, Absent: [][]bool{absent}})
	return header + "\n\n" + d.Text + "\n"
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// putPreMode records d as written before modes: its file and its put generation.
func putPreMode(t *testing.T, s curated.Store, d layer.Definition) {
	t.Helper()
	ls := s.Layer()
	content := preModeFile(d, "")
	writeFile(t, ls.Path(d.Model, d.Kind, d.Name), content)
	gens, err := ls.Generations(d.Model)
	if err != nil {
		t.Fatal(err)
	}
	line, _ := json.Marshal(layer.Generation{N: len(gens) + 1, Time: migrationBuilt.Format(time.RFC3339Nano), Op: layer.OpPut, Kind: d.Kind, Name: d.Name, Digest: layer.Digest(content), Session: d.Session})
	f, err := os.OpenFile(filepath.Join(ls.Dir, layer.ModelDir(d.Model), "manifest.jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
}

// holdPreMode records d as held before modes: its held file with fallback and its index entry.
func holdPreMode(t *testing.T, s curated.Store, d layer.Definition, fallback string) {
	t.Helper()
	dir := filepath.Join(s.Layer().Dir, layer.ModelDir(d.Model), "held")
	content := preModeFile(d, fallback)
	writeFile(t, filepath.Join(dir, d.Kind, d.Name+".rows"), content)
	index, _ := json.Marshal(map[string]string{d.Kind + "/" + d.Name: layer.Digest(content)})
	writeFile(t, filepath.Join(dir, "index.json"), string(index)+"\n")
}

// legacyDefinition is a definitions/<name>.rows file written before the layer.
func legacyDefinition(t *testing.T, s curated.Store, name, query string) {
	t.Helper()
	text := tables.Rows.Encode(tables.Table{Name: "definition", Fields: []string{"name", "query", "description", "created", "sources"},
		Rows: [][]string{{name, query, "What it returns.", migrationBuilt.Format(time.RFC3339Nano), "s-legacy s-other"}}})
	writeFile(t, filepath.Join(s.Dir, "definitions", name+".rows"), text+"\n")
}

var migrationCases = []migrationCase{
	{name: "v1 session", model: "p/m", taken: []string{"bash", "taken-one"}, build: func(t *testing.T, s curated.Store) {
		mustDef(t, s, instructions("p/m", agents.InstructionsObserver, "Mode: extend\nAlways record the port of every service."))
		mustDef(t, s, instructions("p/m", agents.InstructionsReflector, "Mode: replace\nReflect only on deploys."))
		mustDef(t, s, instructions("p/m", agents.InstructionsDropper, "Never drop a port."))
		for _, d := range []curated.Definition{
			{Name: "deploys", Query: `notes | search "deploy"`, Description: "The deploy notes."},
			{Name: "taken-one", Query: "notes | limit 3", Description: "Three notes."},
		} {
			if _, err := s.Define(d, "s1"); err != nil {
				t.Fatal(err)
			}
		}
		// A layer tool too long to be a definition is not listed (parseDefinition's bounds).
		long := tables.Rows.Encode(tables.Table{Name: "definition", Fields: []string{"name", "query", "description", "created", "sources"},
			Rows: [][]string{{"too-long", "notes | " + strings.Repeat("limit 3 | ", 30), "Long.", "", "s1"}}})
		mustDef(t, s, layer.Definition{Kind: layer.KindTool, Name: "too-long", Model: layer.AllModels, Text: long, Session: "s1"})
		legacyDefinition(t, s, "old-one", "notes | count")
		legacyDefinition(t, s, "notes", "notes | limit 1")     // kernel-owned: refused, kept .refused
		legacyDefinition(t, s, "deploys", "history | limit 2") // the layer holds the name: renamed only
	}},
	{name: "all models", model: "p/m", build: func(t *testing.T, s curated.Store) {
		mustDef(t, s, instructions(layer.AllModels, agents.InstructionsObserver, "Note every port."))
		mustDef(t, s, instructions(layer.AllModels, agents.InstructionsConsolidation, "Keep versions whole."))
		mustDef(t, s, instructions(layer.AllModels, agents.InstructionsDropper, "Never drop a plan."))
		mustDef(t, s, instructions("p/m", agents.InstructionsReflector, "Only deploys, & <never> \"quotes\"."))
	}},
	{name: "worker", model: "p/m", worker: "w/x", build: func(t *testing.T, s curated.Store) {
		mustDef(t, s, instructions("w/x", agents.InstructionsObserver, "Worker notes ports."))
		mustDef(t, s, instructions("w/x", agents.InstructionsReflector, "mode: replace\nWorker reflects."))
		mustDef(t, s, instructions("w/x", agents.InstructionsDropper, "Worker drops."))
		mustDef(t, s, instructions("p/m", agents.InstructionsConsolidation, "Keep it short."))
	}},
	{name: "before modes", model: "p/m", build: func(t *testing.T, s curated.Store) {
		putPreMode(t, s, layer.Definition{Kind: layer.KindInstructions, Name: agents.InstructionsConsolidation, Model: "p/m", Text: agents.ConsolidationInstructions + "Also keep versions.", Session: "s0", Time: migrationBuilt})
		putPreMode(t, s, layer.Definition{Kind: layer.KindInstructions, Name: agents.InstructionsObserver, Model: layer.AllModels, Text: "Record ports.", Session: "s0", Time: migrationBuilt})
		holdPreMode(t, s, layer.Definition{Kind: layer.KindInstructions, Name: agents.InstructionsReflector, Model: "p/m", Text: "Keep every plan.", Session: "s0", Time: migrationBuilt}, "none")
		// Untracked: no generation recorded it, so it is never read.
		writeFile(t, s.Layer().Path("p/m", layer.KindInstructions, agents.InstructionsDropper),
			preModeFile(layer.Definition{Kind: layer.KindInstructions, Name: agents.InstructionsDropper, Model: "p/m", Text: "Crafted.", Time: migrationBuilt}, ""))
	}},
	{name: "held consolidation", model: "p/m", build: func(t *testing.T, s curated.Store) {
		mustDef(t, s, instructions("p/m", agents.InstructionsObserver, "Note hosts."))
		mustDef(t, s, instructions("p/m", agents.InstructionsDropper, "Drop nothing."))
		if _, err := s.Layer().Hold(instructions("p/m", agents.InstructionsConsolidation, "Held addition.")); err != nil {
			t.Fatal(err)
		}
	}},
	{name: "om", build: func(t *testing.T, s curated.Store) {
		mustDef(t, s, instructions(layer.AllModels, agents.InstructionsObserver, "Note every port."))
		mustDef(t, s, instructions(layer.AllModels, agents.InstructionsReflector, "mode: replace\nOnly deploys."))
		legacyDefinition(t, s, "old-one", "notes | count")
	}},
}

func fixedStore(dir string, at time.Time) curated.Store {
	s := curated.New(dir)
	s.Now = func() time.Time { return at }
	return s
}

// goStart is engine.SessionStart's NoteRetired and MigrateLayer on the Go engine: the messages
// a host with a UI is told and the skip list.
func goStart(c migrationCase, dir string) []string {
	s := fixedStore(dir, migrationRun)
	h := testfix.NewFakeHost()
	for _, name := range c.taken {
		h.Tools = append(h.Tools, host.ToolInfo{Name: name, Source: "builtin"})
	}
	manager := harness.NewManager(func(host.ToolHost) (curated.Call, bool) { return curated.Call{Store: s}, true })
	manager.NoteRetired(h)
	harness.MigrateLayerV2(h, s, c.model, c.worker)
	out := []string{}
	for _, n := range h.Notifications {
		out = append(out, "notice: "+n.Message)
	}
	return append(out, "skip: "+strings.Join(manager.SkipNames(), ","))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func copyStore(t *testing.T, from, to string) {
	t.Helper()
	for rel, content := range storeFiles(t, from) {
		writeFile(t, filepath.Join(to, filepath.FromSlash(rel)), content)
	}
}

func TestLispLayerMigrationMatchesGo(t *testing.T) {
	sbcl, _ := lispParity(t)
	script, err := filepath.Abs(filepath.Join("..", "..", "lisp", "migration-parity.lisp"))
	if err != nil {
		t.Fatal(err)
	}
	set := loadSchemas(t)
	for _, c := range migrationCases {
		t.Run(c.name, func(t *testing.T) {
			goHome, lispHome := t.TempDir(), t.TempDir()
			goDir, lispDir := curated.DefaultDir(goHome), curated.DefaultDir(lispHome)
			c.build(t, fixedStore(goDir, migrationBuilt))
			copyStore(t, goDir, lispDir)
			before := storeFiles(t, goDir)
			for run := 1; run <= 2; run++ {
				want := goStart(c, goDir)
				got := runLisp(t, sbcl, script, lispDir, orDash(c.model), orDash(c.worker), migrationRun.Format(time.RFC3339Nano), orDash(strings.Join(c.taken, ",")))
				if !reflect.DeepEqual(want, got) {
					t.Errorf("run %d: Go\n  %s\nLisp\n  %s", run, strings.Join(want, "\n  "), strings.Join(got, "\n  "))
				}
				goFiles, lispFiles := storeFiles(t, goDir), storeFiles(t, lispDir)
				for rel, content := range goFiles {
					if lispFiles[rel] != content {
						t.Errorf("run %d: %s differs\nGo:\n%s\nLisp:\n%s", run, rel, content, lispFiles[rel])
					}
				}
				for rel := range lispFiles {
					if _, ok := goFiles[rel]; !ok {
						t.Errorf("run %d: the port wrote %s, which Go does not", run, rel)
					}
				}
				if run == 1 && reflect.DeepEqual(before, goFiles) && c.name != "held consolidation" {
					t.Errorf("the start changed nothing: the case tests nothing")
				}
			}
			// Every layer file the port wrote validates (the legacy definitions are rows files).
			for rel, content := range storeFiles(t, lispDir) {
				if strings.HasPrefix(rel, "definitions/") {
					continue
				}
				f, ok := classify("observational-memory/memories/" + rel)
				if !ok {
					t.Errorf("%s: no schema covers this stored file", rel)
					continue
				}
				switch f.mode {
				case "json":
					for _, e := range set.ValidateJSON(f.schema, []byte(content)) {
						t.Errorf("%s (%s): %v", rel, f.schema, e)
					}
				case "lines":
					for i, line := range strings.Split(strings.TrimSuffix(content, "\n"), "\n") {
						for _, e := range set.ValidateJSON(f.schema, []byte(line)) {
							t.Errorf("%s:%d (%s): %v", rel, i+1, f.schema, e)
						}
					}
				}
			}
		})
	}
}

// TestLispConsolidationSeed checks the port's copy of the consolidation seed, whose digest a
// written consolidation definition records (layer.Store.stampLifted), against Go's.
func TestLispConsolidationSeed(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "lisp", "src", "layer", "go-layer.lisp"))
	if err != nil {
		t.Fatal(err)
	}
	const open = "(defparameter +consolidation-instructions+ \""
	_, rest, ok := strings.Cut(string(data), open)
	if !ok {
		t.Fatal("go-layer.lisp has no +consolidation-instructions+")
	}
	text, _, ok := strings.Cut(rest, "\")")
	if !ok || text != agents.ConsolidationInstructions {
		t.Fatalf("the port's consolidation seed differs from agents.ConsolidationInstructions:\n%q\n%q", text, agents.ConsolidationInstructions)
	}
}
