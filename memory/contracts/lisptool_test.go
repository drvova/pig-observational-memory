package contracts

// W4's parity checks of the Lisp port (docs/lisp-port-tool-layer.md): the curated store's writes
// (curated.Call.Run, Describe, List and every file they write) and the evidence rule
// (rules.SettleV2) run on the Go engine and on lisp/tool-parity.lisp, compared line by line and
// file by file; and the files the Lisp store and layer write validate against contracts/schemas.
// Like memory/selection/lispparity_test.go, they run only with OM_LISP_PARITY=1 and sbcl on PATH.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/rules"
)

func lispParity(t *testing.T) (sbcl, script string) {
	t.Helper()
	if os.Getenv("OM_LISP_PARITY") != "1" {
		t.Skip("set OM_LISP_PARITY=1 to diff the Go engine against the SBCL port in lisp/")
	}
	sbcl, err := exec.LookPath("sbcl")
	if err != nil {
		t.Skip("sbcl is not on PATH")
	}
	script, err = filepath.Abs(filepath.Join("..", "..", "lisp", "tool-parity.lisp"))
	if err != nil {
		t.Fatal(err)
	}
	return sbcl, script
}

func runLisp(t *testing.T, sbcl string, args ...string) []string {
	t.Helper()
	cmd := exec.Command(sbcl, append([]string{"--script"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("sbcl: %v\n%s", err, stderr.String())
	}
	return strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
}

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func quoted(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// curatedStep is one write (or read) of the curated script, with its context.
type curatedStep struct {
	Op           string            `json:"op"`
	Args         map[string]string `json:"args"`
	Session      string            `json:"session"`
	Project      string            `json:"project"`
	Time         string            `json:"time"`
	User         []string          `json:"user"`
	Conversation []string          `json:"conversation,omitempty"`
}

// quotedIn is omhost's quotedIn over plain texts: what both sides' evidence reads.
func quotedIn(texts []string) func(string) bool {
	return func(quote string) bool {
		needle := strings.ToLower(strings.Join(strings.Fields(quote), " "))
		for _, text := range texts {
			if strings.Contains(strings.ToLower(strings.Join(strings.Fields(text), " ")), needle) {
				return true
			}
		}
		return false
	}
}

// curatedScript is a fixed script through every branch the writes take, then a random one over
// small pools of names, types, descriptions, scopes and quotes.
func curatedScript() []curatedStep {
	user := []string{"I always drink green tea in the morning", "the build runs with make all"}
	conversation := append([]string{"The assistant said: deploys go out on Tuesdays"}, user...)
	at := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	var steps []curatedStep
	add := func(op, session, project string, args map[string]string) {
		at = at.Add(17 * time.Minute)
		steps = append(steps, curatedStep{Op: op, Args: args, Session: session, Project: project, Time: at.Format(time.RFC3339), User: user, Conversation: conversation})
	}
	add("save", "s1", "/work", map[string]string{"name": "tea", "type": "user", "description": "Drinks  green tea", "quote": "always drink GREEN tea"})
	add("save", "s1", "/work", map[string]string{"name": "tea", "type": "user", "description": "again"})
	add("save", "s1", "/work", map[string]string{"name": "Tea", "type": "user", "description": "bad name"})
	add("save", "s1", "/work", map[string]string{"name": "cafe", "type": "drink", "description": "bad type"})
	add("save", "s1", "/work", map[string]string{"name": "cafe", "type": "user", "description": "   "})
	add("save", "s1", "/work", map[string]string{"name": "cafe", "type": "user", "description": "x", "quote": "short"})
	add("save", "s1", "/work", map[string]string{"name": "cafe", "type": "user", "description": "x", "quote": "not the user's words at all"})
	add("save", "s1", "/work", map[string]string{"name": "build", "type": "project", "description": "Builds with make", "scope": "project", "body": "  Run make all.\n\nIt is the only target.  "})
	add("save", "s2", "/work", map[string]string{"name": "build", "type": "project", "description": "builds WITH  make", "scope": "project"})
	add("save", "s3", "/work", map[string]string{"name": "build", "type": "project", "description": "another description", "scope": "project"})
	add("save", "s1", "", map[string]string{"name": "scoped", "type": "user", "description": "x", "scope": "project"})
	add("save", "s1", "/work", map[string]string{"name": "scoped", "type": "user", "description": "x", "scope": "everywhere"})
	add("save", "s1", "/work", map[string]string{"name": "long", "type": "lesson", "description": "Too long", "body": strings.Repeat("word ", 2100)})
	add("describe", "s1", "/work", map[string]string{"name": "build"})
	add("describe", "s1", "/work", map[string]string{"name": "tea"})
	add("list", "s1", "/work", map[string]string{"project": "/work"})
	add("list", "s1", "", map[string]string{"project": ""})
	add("edit", "s1", "/work", map[string]string{"name": "tea", "type": "", "description": " "})
	add("edit", "s1", "/work", map[string]string{"name": "tea", "type": "feedback", "description": "Drinks green tea daily", "body": "With no sugar."})
	add("edit", "s3", "/work", map[string]string{"name": "build", "body": "x"})
	add("edit", "s1", "/work", map[string]string{"name": "build", "description": "Builds with make all"})
	add("edit", "s1", "/elsewhere", map[string]string{"name": "build", "body": ""})
	add("edit", "s1", "/work", map[string]string{"name": "tea", "type": "nope"})
	add("feedback", "s1", "/work", map[string]string{"name": "tea", "outcome": "helped", "quote": "drink green tea in the morning"})
	add("feedback", "s1", "/work", map[string]string{"name": "build", "outcome": "helped", "quote": "the build runs with make"})
	add("feedback", "s1", "/work", map[string]string{"name": "tea", "outcome": "harmed", "quote": "deploys go out on Tuesdays"})
	add("feedback", "s1", "/work", map[string]string{"name": "tea", "outcome": "loved", "quote": "deploys go out on Tuesdays"})
	add("feedback", "s1", "/work", map[string]string{"name": "tea", "outcome": "helped", "quote": "nowhere in this conversation"})
	add("feedback", "s1", "/work", map[string]string{"name": "ghost", "outcome": "helped", "quote": "drink green tea in the morning"})
	add("describe", "s1", "/work", map[string]string{"name": "tea"})
	add("forget", "s3", "/work", map[string]string{"name": "build"})
	add("forget", "s1", "/work", map[string]string{"name": "tea"})
	add("forget", "s1", "/work", map[string]string{"name": "tea"})
	add("save", "s1", "/work", map[string]string{"name": "tea", "type": "user", "description": "Saved again after a forget"})
	add("list", "s1", "/work", map[string]string{"project": "/work"})
	add("frobnicate", "s1", "/work", map[string]string{"name": "tea"})

	rng := rand.New(rand.NewPCG(3, 5))
	pick := func(list []string) string { return list[rng.IntN(len(list))] }
	names := []string{"tea", "build", "a-b", "x9", "ok-1", "ok-2", "ok-3", "Bad", "long-name-" + strings.Repeat("z", 60)}
	types := []string{"user", "project", "lesson", "feedback", "reference", "user", "nope", ""}
	descriptions := []string{"Drinks tea", "drinks  TEA", "Builds", " spaced  out ", "Ships on Tuesdays", "two\nlines", ""}
	scopes := []string{"", "", "", "project", "global", "bogus"}
	quotes := []string{"", "", "", "short", "always drink green tea", "deploys go out on Tuesdays", "not said by anyone here"}
	bodies := []string{"", "Why: it matters.", "  padded body  ", strings.Repeat("x", 9990)}
	for i := 0; i < 300; i++ {
		args := map[string]string{"name": pick(names)}
		op := pick([]string{"save", "save", "save", "edit", "edit", "forget", "feedback", "describe", "list"})
		switch op {
		case "save":
			args["type"], args["description"], args["scope"] = pick(types), pick(descriptions), pick(scopes)
			if q := pick(quotes); q != "" {
				args["quote"] = q
			}
			if b := pick(bodies); b != "" {
				args["body"] = b
			}
		case "edit":
			for _, k := range []string{"type", "description", "body"} {
				if rng.IntN(2) == 0 {
					switch k {
					case "type":
						args[k] = pick(types)
					case "description":
						args[k] = pick(descriptions)
					default:
						args[k] = pick(bodies)
					}
				}
			}
		case "feedback":
			args["outcome"], args["quote"] = pick([]string{"helped", "harmed", "meh"}), pick(quotes)
		case "list":
			args["project"] = pick([]string{"/work", ""})
		}
		add(op, pick([]string{"s1", "s2"}), pick([]string{"/work", "/work", ""}), args)
	}
	return steps
}

// goCurated runs the script on the Go store at dir, one line per step as tool-parity.lisp
// prints it.
func goCurated(t *testing.T, dir string, steps []curatedStep) []string {
	t.Helper()
	var lines []string
	for n, s := range steps {
		at, err := time.Parse(time.RFC3339, s.Time)
		if err != nil {
			t.Fatal(err)
		}
		store := curated.Store{Dir: dir, Now: func() time.Time { return at }}
		call := curated.Call{Store: store, Project: s.Project, Source: s.Session, UserEvidence: quotedIn(s.User)}
		if s.Conversation != nil {
			call.Evidence = quotedIn(s.Conversation)
		}
		switch s.Op {
		case "describe":
			m, err := store.Get(s.Args["name"])
			if err != nil {
				lines = append(lines, fmt.Sprintf("%d error %s", n+1, quoted(err.Error())))
				continue
			}
			lines = append(lines, fmt.Sprintf("%d describe %s", n+1, quoted(curated.Describe(m))))
		case "list":
			ms, err := store.List(s.Args["project"])
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, m := range ms {
				names = append(names, m.Name)
			}
			lines = append(lines, fmt.Sprintf("%d list %s", n+1, strings.Join(names, ",")))
		default:
			text, err := call.Run(s.Op, s.Args)
			if err != nil {
				lines = append(lines, fmt.Sprintf("%d error %s", n+1, quoted(err.Error())))
				continue
			}
			lines = append(lines, fmt.Sprintf("%d ok %s", n+1, quoted(text)))
		}
	}
	return lines
}

// storeFiles are the files under dir but the lock, by relative path.
func storeFiles(t *testing.T, dir string) map[string]string {
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

func diffParity(t *testing.T, want, got []string) {
	t.Helper()
	for i := 0; i < len(want) || i < len(got); i++ {
		w, g := "", ""
		if i < len(want) {
			w = want[i]
		}
		if i < len(got) {
			g = got[i]
		}
		if w != g {
			t.Fatalf("line %d differs (Go %d lines, Lisp %d lines):\n go:   %s\n lisp: %s", i+1, len(want), len(got), w, g)
		}
	}
}

func TestLispCuratedParity(t *testing.T) {
	sbcl, script := lispParity(t)
	root := t.TempDir()
	steps := curatedScript()
	scriptPath := filepath.Join(root, "script.json")
	writeJSON(t, scriptPath, steps)
	goDir := filepath.Join(root, "go", "observational-memory", "memories")
	lispDir := filepath.Join(root, "lisp", "observational-memory", "memories")
	want := goCurated(t, goDir, steps)
	got := runLisp(t, sbcl, script, "curated", lispDir, scriptPath)
	diffParity(t, want, got)
	goFiles, lispFiles := storeFiles(t, goDir), storeFiles(t, lispDir)
	names := []string{}
	for name := range goFiles {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if lispFiles[name] != goFiles[name] {
			t.Errorf("%s differs:\n go:   %q\n lisp: %q", name, goFiles[name], lispFiles[name])
		}
		delete(lispFiles, name)
	}
	for name := range lispFiles {
		t.Errorf("%s: only the Lisp store wrote it", name)
	}
	seen := validateTree(t, loadSchemas(t), filepath.Join(root, "lisp"))
	if seen["curated-memory.schema.json"] == 0 || seen["curated-usage-line.schema.json"] == 0 {
		t.Errorf("the Lisp store wrote no memory or usage file (%v)", seen)
	}
	oks, errs := 0, 0
	for _, w := range want {
		_, rest, _ := strings.Cut(w, " ")
		switch {
		case strings.HasPrefix(rest, "ok "):
			oks++
		case strings.HasPrefix(rest, "error "):
			errs++
		}
	}
	t.Logf("identical: %d steps (%d writes done, %d refused), %d files", len(want), oks, errs, len(names))
}

// settleCase is one held definition and the usage ledger events it is settled on.
type settleCase struct {
	Kind     string               `json:"kind"`
	Name     string               `json:"name"`
	HeldID   string               `json:"heldId"`
	Time     string               `json:"time"`
	Detector string               `json:"detector"`
	Events   []curated.ModelEvent `json:"events"`
}

func TestLispSettleParity(t *testing.T) {
	sbcl, script := lispParity(t)
	rng := rand.New(rand.NewPCG(11, 13))
	held := time.Date(2026, 11, 1, 10, 0, 0, 123456789, time.UTC)
	defs := []layer.Definition{
		{Kind: layer.KindInstructions, Name: "consolidation", Mode: layer.ModeExtend, Text: "Keep every figure with its unit."},
		{Kind: layer.KindRemedy, Name: rules.RemedyKeepHead, Text: "remedy[1]{detector}:\n  uncited-assistant-specifics"},
		{Kind: layer.KindDetector, Name: "go-plans", Text: rules.CodeDetectorText("func Count() int { return 1 }", "plans")},
		{Kind: layer.KindDetector, Name: "missed-paths", Text: "detector[1]{check,description}:\n  thin-chunk,notes counted"},
	}
	var cases []settleCase
	var want []string
	for i := 0; i < 400; i++ {
		d := defs[i%len(defs)]
		d.Time = held
		id := d.HeldID()
		detector := "other"
		if d.Kind == layer.KindDetector {
			if def, err := rules.ParseDetector(d.Name, d.Text); err == nil && def.IsCode() {
				detector = "code"
			} else if err == nil && def.Retired() {
				detector = "retired"
			}
		}
		var events []curated.ModelEvent
		for k := rng.IntN(5); k > 0; k-- {
			offsets := []time.Duration{-time.Hour, -time.Nanosecond, 0, time.Nanosecond, time.Minute, 3 * time.Hour}
			e := curated.ModelEvent{Time: held.Add(offsets[rng.IntN(len(offsets))]).Format(time.RFC3339Nano), Model: "p/m", Engine: curated.EngineV2}
			if rng.IntN(8) == 0 {
				e.Engine = ""
			}
			switch rng.IntN(3) {
			case 0:
				e.Kind = curated.ModelKindDetectorPass
				e.Cut = fmt.Sprintf("e%d", rng.IntN(9))
				e.Counts = map[string]int{}
				if rng.IntN(2) == 0 {
					e.Counts[rules.DefinedCountKey(d.Name)] = rng.IntN(3)
				}
				if rng.IntN(4) == 0 {
					e.Errors = map[string]string{d.Name: "panic: index out of range"}
				}
			default:
				e.Kind = curated.ModelKindPass
				e.Stage = rules.StageCompaction
				e.Outcome = []string{curated.OutcomeOK, curated.OutcomeEmpty, ""}[rng.IntN(3)]
				e.Tokens = []int{0, 40, 250}[rng.IntN(3)]
				e.Level1 = rng.IntN(2) == 0
				e.Counts = map[string]int{}
				for _, k := range []string{rules.CountLevel1Failed, rules.CountLevel1SpecificsRefused, rules.CountLevel1UnionKept, rules.CountLevel1Refused} {
					if rng.IntN(5) == 0 {
						e.Counts[k] = rng.IntN(3)
					}
				}
			}
			if rng.IntN(4) != 0 {
				e.RanHeld = []string{id}
			} else if rng.IntN(2) == 0 {
				e.RanHeld = []string{d.Key() + "@0000000000000000"}
			}
			events = append(events, e)
		}
		decided, clean, reason := rules.SettleV2(events, d)
		cases = append(cases, settleCase{Kind: d.Kind, Name: d.Name, HeldID: id, Time: held.Format(time.RFC3339Nano), Detector: detector, Events: events})
		state, cleanWord := "held", "unclean"
		if decided {
			state = "decided"
		}
		if clean {
			cleanWord = "clean"
		}
		want = append(want, fmt.Sprintf("%d %s %s %s", i+1, state, cleanWord, quoted(reason)))
	}
	path := filepath.Join(t.TempDir(), "cases.json")
	writeJSON(t, path, cases)
	got := runLisp(t, sbcl, script, "settle", path)
	diffParity(t, want, got)
	decided := 0
	for _, w := range want {
		if strings.Contains(w, " decided ") {
			decided++
		}
	}
	t.Logf("identical: %d cases, %d decided", len(want), decided)
}

// The files the Lisp layer writes validate: its manifest against lisp-layer-generation and its
// held index against the Go layer's layer-held-index.
func TestLispLayerFilesValidate(t *testing.T) {
	sbcl, script := lispParity(t)
	set := loadSchemas(t)
	root := t.TempDir()
	runLisp(t, sbcl, script, "layer", root)
	seen := map[string]int{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		switch {
		case d.Name() == ".lock" || strings.HasSuffix(rel, ".lisp"):
		case strings.HasSuffix(rel, "/manifest.jsonl"):
			for n, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
				seen["manifest"]++
				for _, e := range set.ValidateJSON("lisp-layer-generation.schema.json", []byte(line)) {
					t.Errorf("%s:%d: %v", rel, n+1, e)
				}
			}
		case strings.HasSuffix(rel, "/held/index.json"):
			seen["index"]++
			for _, e := range set.ValidateJSON("layer-held-index.schema.json", data) {
				t.Errorf("%s: %v", rel, e)
			}
		default:
			t.Errorf("%s: no schema covers this stored file", rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen["manifest"] < 5 || seen["index"] == 0 {
		t.Errorf("the Lisp layer wrote too little: %v", seen)
	}
}
