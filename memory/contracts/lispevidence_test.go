package contracts

// The parity checks of the image's evidence path (docs/lisp-port-evidence.md): the usage
// ledger's detect and pass events (rules.RecordDetection, rules.AfterPass with Engine v2), the
// rules evaluate decides and writes into the store's layer, the rules that apply to a model
// (rules.For) and the settle decisions over the resulting ledger (rules.SettleV2), run as one
// script on the Go engine and on lisp/evidence-parity.lisp from the same initial store, compared
// line by line and file by file; the Go engine then reads the Lisp-written ledger with the same
// profile (harness.Compute) and rules; and the files the evidence path writes validate against
// contracts/schemas. Like the other Lisp parity tests, they run only with OM_LISP_PARITY=1 and
// sbcl on PATH.

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

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/harness"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/rules"
)

func evidenceScript(t *testing.T) (sbcl, script string) {
	t.Helper()
	sbcl, _ = lispParity(t)
	script, err := filepath.Abs(filepath.Join("..", "..", "lisp", "evidence-parity.lisp"))
	if err != nil {
		t.Fatal(err)
	}
	return sbcl, script
}

// evidenceStep is one step of the evidence script.
type evidenceStep struct {
	Op       string         `json:"op"`
	Model    string         `json:"model"`
	Time     string         `json:"time"`
	Counts   map[string]int `json:"counts,omitempty"`
	Kinds    map[string]int `json:"kinds,omitempty"`
	Tokens   int            `json:"tokens,omitempty"`
	Line     string         `json:"line,omitempty"`
	Empty    bool           `json:"empty,omitempty"`
	Level1   bool           `json:"level1,omitempty"`
	RanHeld  []string       `json:"ranHeld,omitempty"`
	Kind     string         `json:"kind,omitempty"`
	Name     string         `json:"name,omitempty"`
	HeldID   string         `json:"heldId,omitempty"`
	HeldTime string         `json:"heldTime,omitempty"`
	def      layer.Definition
}

var evidenceModels = []string{"p/a", "p/b", "p/c", "q/d", "q/e"}

// seedEvidenceStore writes the initial store both sides start from: a catalogue override that
// never tries keep-head for q/e, a held v2 keep-head rule for q/d, and a v2 rule established for
// p/c written by hand.
func seedEvidenceStore(t *testing.T, dir string, at time.Time) {
	t.Helper()
	store := curated.New(dir)
	store.Now = func() time.Time { return at }
	catalogue := "catalogue[1]{detector,rank}:\n  uncited-assistant-specifics,0"
	if _, err := store.Layer().Put(layer.Definition{Kind: layer.KindRemedy, Name: rules.RemedyKeepHead, Model: "q/e", Text: catalogue}); err != nil {
		t.Fatal(err)
	}
	if _, err := rules.Add(store, rules.Rule{Detector: rules.DetectUncitedAssistantSpecifics, Remedy: rules.RemedyKeepHead, Models: []string{"p/c"},
		State: rules.StateEstablished, Engine: curated.EngineV2, Reason: "by hand"}); err != nil {
		t.Fatal(err)
	}
	held := rules.Rule{ID: "0123456789ab", Detector: rules.DetectUncitedAssistantSpecifics, Remedy: rules.RemedyKeepHead, Models: []string{"q/d"},
		State: rules.StateProbation, Engine: curated.EngineV2, Proposer: rules.ProposerModel, Created: at, Reason: "held"}
	text := encodeRule(t, held)
	if _, err := store.Layer().Hold(layer.Definition{Kind: layer.KindRemedy, Name: held.ID, Model: "q/d", Text: text}); err != nil {
		t.Fatal(err)
	}
}

// encodeRule is a rule's table as rules writes it: Add stamps it, so a held rule is encoded
// by adding it to a scratch store and reading the file back.
func encodeRule(t *testing.T, r rules.Rule) string {
	t.Helper()
	scratch := curated.New(t.TempDir())
	scratch.Now = func() time.Time { return r.Created }
	added, err := rules.Add(scratch, r)
	if err != nil {
		t.Fatal(err)
	}
	d, err := scratch.Layer().Get(r.Models[0], layer.KindRemedy, added.ID)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Replace(d.Text, added.ID, r.ID, 1)
}

// evidenceSteps is a random script: per model, phases of detector runs with high then low
// counts (so harness rules are written, then established or retired, then promoted to a
// default), decode failures, level-1 counts, empty passes and held ids, then the rules and the
// settle decisions of two held definitions.
func evidenceSteps(rng *rand.Rand, start time.Time) []evidenceStep {
	at := start
	tick := func() string {
		at = at.Add(time.Duration(1+rng.IntN(90)) * time.Minute).Add(time.Duration(rng.IntN(1000)) * time.Nanosecond)
		return at.UTC().Format(time.RFC3339Nano)
	}
	held := []layer.Definition{
		{Kind: layer.KindInstructions, Name: "consolidation", Mode: layer.ModeExtend, Text: "Keep every figure.", Time: start},
		{Kind: layer.KindRemedy, Name: "0123456789ab", Text: "rule[0]{id}:", Time: start},
	}
	steps := []evidenceStep{}
	for round := 0; round < 14; round++ {
		high := round < 5
		for _, model := range evidenceModels {
			for k := rng.IntN(3); k >= 0; k-- {
				counts := map[string]int{}
				if high || rng.IntN(4) == 0 {
					counts[rules.DetectUncitedAssistantSpecifics] = 1 + rng.IntN(4)
				}
				if rng.IntN(3) == 0 {
					counts[rules.DetectDroppedThenQueried] = rng.IntN(2)
				}
				kinds := map[string]int{}
				if counts[rules.DetectDroppedThenQueried] > 0 {
					kinds[[]string{"fact", "plan", rules.Unclassified}[rng.IntN(3)]] = 1
				}
				steps = append(steps, evidenceStep{Op: "detect", Model: model, Time: tick(), Counts: counts, Kinds: kinds, Tokens: []int{0, 80, 400, 1200}[rng.IntN(4)]})
			}
			if rng.IntN(3) == 0 {
				e := curated.ModelEvent{Time: tick(), Model: model, Kind: curated.ModelKindDecode, Outcome: curated.OutcomeError, Codec: "rows", Engine: curated.EngineV2}
				if rng.IntN(4) == 0 {
					e.Engine = ""
				}
				line, _ := json.Marshal(e)
				steps = append(steps, evidenceStep{Op: "raw", Model: model, Time: e.Time, Line: string(line)})
			}
			pass := evidenceStep{Op: "pass", Model: model, Time: tick(), Empty: rng.IntN(7) == 0, Level1: rng.IntN(2) == 0}
			if pass.Level1 {
				pass.Counts = map[string]int{}
				for _, k := range []string{rules.CountLevel1Refused, rules.CountLevel1SpecificsRefused, rules.CountLevel1UnionKept, rules.DetectCrossScopeSupersede} {
					if rng.IntN(4) == 0 {
						pass.Counts[k] = rng.IntN(3)
					}
				}
			}
			for _, d := range held {
				if rng.IntN(2) == 0 {
					pass.RanHeld = append(pass.RanHeld, d.HeldID())
				}
			}
			sort.Strings(pass.RanHeld)
			steps = append(steps, pass)
		}
	}
	for _, model := range evidenceModels {
		steps = append(steps, evidenceStep{Op: "rules", Model: model, Time: tick()})
		for _, d := range held {
			steps = append(steps, evidenceStep{Op: "settle", Model: model, Time: tick(), Kind: d.Kind, Name: d.Name, HeldID: d.HeldID(),
				HeldTime: d.Time.UTC().Format(time.RFC3339Nano), def: d})
		}
	}
	return steps
}

// goEvidence runs the steps on the Go engine and prints what the Lisp side prints.
func goEvidence(t *testing.T, dir string, steps []evidenceStep) []string {
	t.Helper()
	out := []string{}
	for n, s := range steps {
		at, err := time.Parse(time.RFC3339Nano, s.Time)
		if err != nil {
			t.Fatal(err)
		}
		store := curated.New(dir)
		store.Now = func() time.Time { return at }
		switch s.Op {
		case "detect":
			if err := rules.RecordDetection(store, s.Model, rules.Findings{Counts: s.Counts, DroppedKinds: s.Kinds}, s.Tokens); err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprintf("%d detect", n+1))
		case "raw":
			f, err := os.OpenFile(filepath.Join(dir, "models", layer.ModelDir(s.Model)+".jsonl"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
			if err == nil {
				_, err = f.WriteString(s.Line + "\n")
				f.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, fmt.Sprintf("%d raw", n+1))
		case "pass":
			ts, err := rules.AfterPass(store, s.Model, rules.Pass{Counts: s.Counts, Level1: s.Level1, Empty: s.Empty, RanHeld: s.RanHeld})
			if err != nil {
				t.Fatal(err)
			}
			msgs := make([]string, 0, len(ts))
			for _, tr := range ts {
				msgs = append(msgs, tr.Message())
			}
			// Go promotes defaults in map order: both sides print the messages sorted.
			sort.Strings(msgs)
			b, _ := json.Marshal(msgs)
			out = append(out, fmt.Sprintf("%d pass %s", n+1, b))
		case "rules":
			active := rules.For(store, s.Model)
			parts := []string{}
			for _, r := range active.Rules {
				parts = append(parts, r.ID+":"+r.Remedy+":"+r.State)
			}
			has := "no"
			if active.Has(rules.RemedyKeepHead) {
				has = "yes"
			}
			out = append(out, fmt.Sprintf("%d rules %s keep-head=%s", n+1, strings.Join(parts, ","), has))
		case "settle":
			events, err := store.ModelEvents(s.Model)
			if err != nil {
				t.Fatal(err)
			}
			decided, clean, reason := rules.SettleV2(events, s.def)
			state, word := "held", "unclean"
			if decided {
				state = "decided"
			}
			if clean {
				word = "clean"
			}
			out = append(out, fmt.Sprintf("%d settle %s %s %s", n+1, state, word, quoted(reason)))
		}
	}
	return out
}

// evidenceFiles are the files under dir (the lock left out), by relative path.
func evidenceFiles(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || d.Name() == ".lock" {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		out[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	for rel, data := range evidenceFiles(t, from) {
		path := filepath.Join(to, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLispEvidenceParity(t *testing.T) {
	sbcl, script := evidenceScript(t)
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	transitions, rulesWritten := 0, 0
	kinds := map[string]int{}
	for seed := uint64(1); seed <= 6; seed++ {
		rng := rand.New(rand.NewPCG(seed, 17))
		goDir, lispDir := t.TempDir(), t.TempDir()
		seedEvidenceStore(t, goDir, start)
		copyTree(t, goDir, lispDir)
		steps := evidenceSteps(rng, start)
		want := goEvidence(t, goDir, steps)
		path := filepath.Join(t.TempDir(), "steps.json")
		writeJSON(t, path, steps)
		got := runLisp(t, sbcl, script, "rules", lispDir, path)
		diffParity(t, want, got)
		goFiles, lispFiles := evidenceFiles(t, goDir), evidenceFiles(t, lispDir)
		names := []string{}
		for name := range goFiles {
			names = append(names, name)
		}
		for name := range lispFiles {
			if _, ok := goFiles[name]; !ok {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		for _, name := range names {
			if goFiles[name] != lispFiles[name] {
				t.Fatalf("seed %d: %s differs:\n go:   %q\n lisp: %q", seed, name, clipText(goFiles[name]), clipText(lispFiles[name]))
			}
			if strings.Contains(name, "/remedy/") {
				rulesWritten++
			}
		}
		for _, w := range want {
			transitions += strings.Count(w, "Observational memory: rule")
			for _, kind := range []string{"added on probation", "is now established", "is now retired", "a default for all models", "switch-codec", "keep-head=yes"} {
				kinds[kind] += strings.Count(w, kind)
			}
		}
		// The Go engine reads the Lisp-written ledger and layer as its own.
		for _, model := range evidenceModels {
			goStore, lispStore := curated.New(goDir), curated.New(lispDir)
			ge, _ := goStore.ModelEvents(model)
			le, _ := lispStore.ModelEvents(model)
			if len(ge) == 0 || !reflect.DeepEqual(harness.Compute(model, ge), harness.Compute(model, le)) {
				t.Fatalf("seed %d: %s's profile differs over the Lisp ledger (%d events)", seed, model, len(le))
			}
			if !reflect.DeepEqual(rules.For(goStore, model), rules.For(lispStore, model)) {
				t.Fatalf("seed %d: %s's rules differ", seed, model)
			}
		}
	}
	for kind, n := range kinds {
		if n == 0 {
			t.Errorf("the scripts never exercised %q: %v", kind, kinds)
		}
	}
	if rulesWritten < 4 {
		t.Fatalf("the scripts wrote too little: %d rule files", rulesWritten)
	}
	t.Logf("identical: 6 scripts, %d transitions %v, %d rule files", transitions, kinds, rulesWritten)
}

func clipText(s string) string {
	if len(s) > 2000 {
		return s[:2000] + "…"
	}
	return s
}

// The files the evidence path writes validate: the usage ledger against model-event, the Go
// layer's manifest against layer-generation and the image layer's manifest (with a retire
// rule's revert) and units against lisp-layer-generation and lisp-layer-unit.
func TestLispEvidenceFilesValidate(t *testing.T) {
	sbcl, script := evidenceScript(t)
	set := loadSchemas(t)
	store, root := t.TempDir(), t.TempDir()
	runLisp(t, sbcl, script, "files", store, root)
	seen := map[string]int{}
	check := func(base string) {
		for rel, data := range evidenceFiles(t, base) {
			lines := strings.Split(strings.TrimSuffix(data, "\n"), "\n")
			schema := ""
			switch {
			case base == store && strings.HasPrefix(rel, "models/"):
				schema = "model-event.schema.json"
			case base == store && strings.HasSuffix(rel, "/manifest.jsonl"):
				schema = "layer-generation.schema.json"
			case base == store && (strings.Contains(rel, "/remedy/") || strings.Contains(rel, "/blobs/")):
				if _, err := curated.New(store).Layer().Get("acme/files", layer.KindRemedy, strings.TrimSuffix(filepath.Base(rel), ".rows")); err != nil && strings.Contains(rel, "/remedy/") {
					t.Errorf("%s: Go's layer refuses it: %v", rel, err)
				}
				seen["rule"]++
				continue
			case base == root && strings.HasSuffix(rel, "/manifest.jsonl"):
				schema = "lisp-layer-generation.schema.json"
			case base == root && strings.HasSuffix(rel, "/units.jsonl"):
				schema = "lisp-layer-unit.schema.json"
			case base == root && strings.HasSuffix(rel, "/held/index.json"):
				schema, lines = "layer-held-index.schema.json", []string{data}
			case strings.HasSuffix(rel, ".lisp"):
				continue
			default:
				t.Errorf("%s: no schema covers this stored file", rel)
				continue
			}
			for n, line := range lines {
				seen[schema]++
				for _, e := range set.ValidateJSON(schema, []byte(line)) {
					t.Errorf("%s:%d: %v", rel, n+1, e)
				}
			}
		}
	}
	check(store)
	check(root)
	for _, schema := range []string{"model-event.schema.json", "layer-generation.schema.json", "lisp-layer-generation.schema.json", "lisp-layer-unit.schema.json"} {
		if seen[schema] == 0 {
			t.Errorf("nothing validated against %s: %v", schema, seen)
		}
	}
	if seen["rule"] == 0 {
		t.Errorf("no rule written: %v", seen)
	}
}
