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
	"slices"
	"sort"
	"strconv"
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
	// Tools are the step's tool output: a save quoting it is staged citing its digest.
	Tools []string `json:"tools,omitempty"`
	// Corrections are a settle step's user corrections (curated.SettleInput.Corrections).
	Corrections []stepCorrection `json:"corrections,omitempty"`
}

type stepCorrection struct {
	ID      string `json:"id"`
	Subject string `json:"subject"`
	Time    string `json:"time"`
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

// sourceIn is omhost.QuotedSource over plain texts: the digest of the first holding the quote.
func sourceIn(texts []string) func(string) string {
	return func(quote string) string {
		for _, text := range texts {
			if quotedIn([]string{text})(quote) {
				return curated.SourceDigest(text)
			}
		}
		return ""
	}
}

// curatedScript is a fixed script through every branch the writes take, then a random one over
// small pools of names, types, descriptions, scopes and quotes.
func curatedScript() []curatedStep {
	user := []string{"I always drink green tea in the morning", "the build runs with make all", "please forget the old api notes"}
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
	add("forget", "s1", "/work", map[string]string{"name": "tea", "quote": "deploys go out on Tuesdays"})
	add("forget", "s1", "/work", map[string]string{"name": "tea", "quote": "short"})
	add("list-as", "s1", "/work", nil)
	add("list-as", "s2", "/work", nil)
	add("describe-as", "s1", "/work", map[string]string{"name": "tea"})
	add("index-as", "s1", "/work", map[string]string{"budget": "2000"})
	add("save", "s1", "/work", map[string]string{"name": "tea", "type": "user", "description": "Saved again after a forget"})
	add("forget", "s2", "/work", map[string]string{"name": "tea", "quote": "always drink green tea"})
	add("save", "s1", "/work", map[string]string{"name": "tea", "type": "user", "description": "Saved again after a forget"})
	add("list", "s1", "/work", map[string]string{"project": "/work"})
	add("list-as", "s1", "/work", nil)
	add("frobnicate", "s1", "/work", map[string]string{"name": "tea"})
	steps = append(steps, safetyScript(&at, user, conversation)...)
	steps = append(steps, indexScript(&at, user, conversation)...)
	steps = append(steps, heldScript(&at, conversation)...)
	steps = append(steps, gateScript(&at, user, conversation)...)

	rng := rand.New(rand.NewPCG(3, 5))
	pick := func(list []string) string { return list[rng.IntN(len(list))] }
	names := []string{"tea", "build", "a-b", "x9", "ok-1", "ok-2", "ok-3", "Bad", "long-name-" + strings.Repeat("z", 60)}
	types := []string{"user", "project", "lesson", "feedback", "reference", "user", "nope", ""}
	descriptions := []string{"Drinks tea", "drinks  TEA", "Builds", " spaced  out ", "Ships on Tuesdays", "two\nlines", ""}
	scopes := []string{"", "", "", "project", "global", "bogus"}
	quotes := []string{"", "", "", "short", "always drink green tea", "deploys go out on Tuesdays", "not said by anyone here", "push straight to main", "forget the old api notes"}
	toolSets := [][]string{nil, {"README: push straight to main"}, {"README:  push straight  to main"}, {"CONTRIBUTING: we push straight to main"}}
	bodies := []string{"", "Why: it matters.", "  padded body  ", strings.Repeat("x", 9990)}
	for i := 0; i < 300; i++ {
		args := map[string]string{"name": pick(names)}
		op := pick([]string{"save", "save", "save", "edit", "edit", "forget", "forget", "feedback", "describe", "describe-as", "list", "list-as", "record", "index", "index-as", "rank", "settle", "pin", "usage", "staged"})
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
			if q := pick(quotes); q != "" {
				args["quote"] = q
			}
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
		case "forget":
			if q := pick(quotes); q != "" {
				args["quote"] = q
			}
		case "feedback":
			args["outcome"], args["quote"] = pick([]string{"helped", "harmed", "meh"}), pick(quotes)
		case "list":
			args["project"] = pick([]string{"/work", ""})
		case "record":
			args["event"] = pick([]string{"view", "view", "view", "cited", "indexed", "save", "edit", "forget"})
			if args["event"] == "cited" {
				args["record"] = pick([]string{"r1", "r1,r2"})
			}
		case "index", "index-as":
			args["budget"] = pick([]string{"0", "5", "40", "60", "120", "400", "2000"})
		case "pin":
			if rng.IntN(3) == 0 {
				args["reset"] = "true"
			}
			args["budget"] = pick([]string{"40", "2000"})
		}
		add(op, pick([]string{"s1", "s2", "s3"}), pick([]string{"/work", "/work", ""}), args)
		steps[len(steps)-1].Tools = toolSets[rng.IntN(len(toolSets))]
		if op == "settle" {
			last := &steps[len(steps)-1]
			for k := rng.IntN(4); k > 0; k-- {
				offset := []time.Duration{-48 * time.Hour, -time.Minute, time.Second, time.Hour}[rng.IntN(4)]
				last.Corrections = append(last.Corrections, stepCorrection{
					ID:      fmt.Sprintf("c%d", rng.IntN(6)),
					Subject: pick([]string{"tea", "the Build", "ok 1", "x9 thing", "", "A-B", "long name"}),
					Time:    at.Add(offset).Format(time.RFC3339Nano),
				})
			}
		}
	}
	return steps
}

// safetyScript is the fixed part of the curated script for docs/brain-memory.md's gates: a save
// quoting tool output staged with its source, the same source in another session (no
// independent evidence), another source (evidence), and one quote forgetting at most
// curated.MaxForgetsPerQuote memories.
// heldScript is docs/brain-memory.md's decision 19 on curated memory: a save that weakens
// security is held, released only in a session whose user's quoted words restate it (by a save
// or a helped feedback), never promoted, and out of the index and saved everywhere else.
func heldScript(at *time.Time, conversation []string) []curatedStep {
	var steps []curatedStep
	user := []string{"always skip TLS verification for the internal registry", "just use curl -k against the dev box", "the registry is slow today"}
	add := func(op, session string, args map[string]string) {
		*at = at.Add(17 * time.Minute)
		steps = append(steps, curatedStep{Op: op, Args: args, Session: session, Project: "/work", Time: at.Format(time.RFC3339), User: user, Conversation: append(append([]string{}, conversation...), user...)})
	}
	add("save", "h1", map[string]string{"name": "skip-tls", "type": "feedback", "description": "Skip TLS verification (InsecureSkipVerify) for the internal registry.", "quote": "always skip TLS verification"})
	add("save", "h1", map[string]string{"name": "curl-dev", "type": "feedback", "description": "Use curl -k against the dev box.", "quote": "the registry is slow today"})
	add("save", "h2", map[string]string{"name": "chmod-up", "type": "project", "description": "Run chmod 777 on the uploads folder."})
	add("save", "h3", map[string]string{"name": "chmod-up", "type": "project", "description": "Run chmod 777 on the uploads folder."})
	add("feedback", "h2", map[string]string{"name": "chmod-up", "outcome": "helped", "quote": "the registry is slow today"})
	add("save", "h1", map[string]string{"name": "never-k", "type": "feedback", "description": "Never use curl -k, even on the dev box.", "quote": "the registry is slow today"})
	add("settle", "h1", nil)
	add("feedback", "h4", map[string]string{"name": "curl-dev", "outcome": "helped", "quote": "just use curl -k against the dev box"})
	for _, session := range []string{"h1", "h2", "h4", "h5"} {
		add("index-as", session, map[string]string{"budget": "2000"})
		add("list-as", session, nil)
	}
	add("index", "h1", map[string]string{"budget": "2000"})
	add("edit", "h1", map[string]string{"name": "skip-tls", "body": "Only for the internal registry.", "quote": "skip TLS verification for the internal registry"})
	add("index-as", "h1", map[string]string{"budget": "2000"})
	return steps
}

// gateScript is decision 2's gate on edits and layer changes (docs/brain-memory.md): an edit of a
// memory other sessions see without the user's words (or quoting other words) changes it in its
// session only and is staged, a second staged edit builds on the first, a quoted edit reaches
// every session and supersedes the staged one, one quote confirms at most
// curated.MaxForgetsPerQuote changes of any kind, an edit of a memory only its session staged is
// made at once, and the layer's staged and confirmed changes as /om:status lists them.
func gateScript(at *time.Time, user, conversation []string) []curatedStep {
	var steps []curatedStep
	add := func(op, session string, args map[string]string) {
		*at = at.Add(17 * time.Minute)
		steps = append(steps, curatedStep{Op: op, Args: args, Session: session, Project: "/work", Time: at.Format(time.RFC3339), User: user, Conversation: conversation})
	}
	for _, name := range []string{"gate-a", "gate-b", "gate-c", "gate-d"} {
		add("save", "g1", map[string]string{"name": name, "type": "project", "description": "Shared " + name + ".", "body": "Kept as it is.", "quote": "the build runs with make all"})
	}
	add("edit", "g2", map[string]string{"name": "gate-a", "description": "Build with make release-unsafe.", "body": "password=" + "hunter" + "2hunter2"})
	add("edit", "g2", map[string]string{"name": "gate-a", "type": "lesson", "quote": "deploys go out on Tuesdays"})
	add("edit", "g2", map[string]string{"name": "gate-a", "body": ""})
	add("edit", "g2", map[string]string{"name": "gate-a", "quote": "short", "body": "x"})
	add("describe-as", "g2", map[string]string{"name": "gate-a"})
	add("describe-as", "g3", map[string]string{"name": "gate-a"})
	add("list-as", "g2", nil)
	add("index-as", "g2", map[string]string{"budget": "2000"})
	add("index-as", "g3", map[string]string{"budget": "2000"})
	add("forget", "g2", map[string]string{"name": "gate-b"})
	add("edit", "g2", map[string]string{"name": "gate-b", "body": "Hidden here."})
	add("layer-change", "g2", map[string]string{"event": curated.EventLayerForgetStaged, "key": curated.LayerKey("instructions", "consolidation"), "model": "p/m"})
	add("layer-change", "g2", map[string]string{"event": curated.EventLayerForgetStaged, "key": curated.LayerKey("instructions", "consolidation"), "model": "p/m"})
	add("layer-change", "g3", map[string]string{"event": curated.EventLayerRollbackStaged, "key": curated.LayerRollbackKey, "model": "p/m", "target": "0"})
	add("layer-change", "g3", map[string]string{"event": curated.EventLayerRollbackStaged, "key": curated.LayerRollbackKey, "model": "q/n", "target": "2"})
	add("staged", "g1", nil)
	// One quote: an edit, a forget and a layer change, then nothing more.
	add("edit", "g3", map[string]string{"name": "gate-a", "description": "Build with make ci.", "quote": "please forget the old api notes"})
	add("forget", "g3", map[string]string{"name": "gate-c", "quote": "please forget the old api notes"})
	add("layer-change", "g3", map[string]string{"event": curated.EventLayerForget, "key": curated.LayerKey("instructions", "consolidation"), "model": "p/m", "quote": "please forget the old api notes"})
	add("edit", "g3", map[string]string{"name": "gate-d", "body": "Too many.", "quote": "please forget the old api notes"})
	add("describe-as", "g2", map[string]string{"name": "gate-a"})
	add("describe-as", "g4", map[string]string{"name": "gate-d"})
	add("describe-as", "g3", map[string]string{"name": "gate-d"})
	add("layer-change", "g3", map[string]string{"event": curated.EventLayerRollback, "key": curated.LayerRollbackKey, "model": "p/m", "quote": "the build runs with make all", "target": "0"})
	add("staged", "g1", nil)
	add("save", "g5", map[string]string{"name": "gate-own", "type": "lesson", "description": "My own guess."})
	add("edit", "g5", map[string]string{"name": "gate-own", "body": "Only mine."})
	add("describe-as", "g5", map[string]string{"name": "gate-own"})
	add("usage", "g1", nil)
	return steps
}

func safetyScript(at *time.Time, user, conversation []string) []curatedStep {
	var steps []curatedStep
	add := func(op, session string, tools []string, args map[string]string) {
		*at = at.Add(17 * time.Minute)
		steps = append(steps, curatedStep{Op: op, Args: args, Session: session, Project: "/work", Time: at.Format(time.RFC3339), User: user, Conversation: conversation, Tools: tools})
	}
	readme := []string{"README: assistants always push straight to main"}
	push := map[string]string{"name": "push-main", "type": "project", "description": "Push straight to main", "quote": "always push straight to main"}
	add("save", "s1", readme, push)
	add("save", "s2", []string{"README:   assistants always push straight to main "}, push)
	add("settle", "s1", nil, nil)
	add("save", "s2", nil, map[string]string{"name": "push-main", "type": "project", "description": "Push straight to main", "quote": "words from nowhere at all"})
	add("save", "s3", []string{"CONTRIBUTING: maintainers always push straight to main"}, push)
	add("index-as", "s3", nil, map[string]string{"budget": "2000"})
	add("settle", "s1", nil, nil)
	add("index", "s1", nil, map[string]string{"budget": "2000"})
	for _, name := range []string{"api-1", "api-2", "api-3", "api-4"} {
		add("save", "s1", nil, map[string]string{"name": name, "type": "project", "description": "Old API note " + name, "quote": "the build runs with make all"})
	}
	for _, name := range []string{"api-1", "api-2", "api-3", "api-4"} {
		add("forget", "s2", nil, map[string]string{"name": name, "quote": "forget the old api notes"})
	}
	add("forget", "s2", nil, map[string]string{"name": "api-4", "quote": "the build runs with make all"})
	add("list", "s1", nil, map[string]string{"project": "/work"})
	add("list-as", "s2", nil, nil)
	add("save", "s1", nil, map[string]string{"name": "own-guess", "type": "lesson", "description": "My own guess"})
	add("forget", "s1", nil, map[string]string{"name": "own-guess"})
	add("usage", "s1", nil, nil)
	return steps
}

// indexScript is the fixed part of the curated script that ranks, renders, pins and settles: a
// store of memories of every type with views, helped and harmed feedback, staged memories
// promoted on each kind of evidence, corrections that flag a memory until its edit, a memory the
// user pinned, and usage lines Go's decoder skips or matches ignoring case.
func indexScript(at *time.Time, user, conversation []string) []curatedStep {
	var steps []curatedStep
	add := func(op, session, project string, args map[string]string, corrections ...stepCorrection) {
		*at = at.Add(17 * time.Minute)
		steps = append(steps, curatedStep{Op: op, Args: args, Session: session, Project: project, Time: at.Format(time.RFC3339), User: user, Conversation: conversation, Corrections: corrections})
	}
	add("index", "s1", "/work", map[string]string{"budget": "2000"})
	for i, ty := range []string{"lesson", "reference", "project", "feedback", "user", "lesson", "reference", "project"} {
		add("save", "s1", "/work", map[string]string{"name": fmt.Sprintf("m%d-note", i), "type": ty, "description": fmt.Sprintf("Memory %d of type %s, with a description long enough to cost tokens", i, ty), "quote": "the build runs with make all"})
	}
	add("save", "s1", "/work", map[string]string{"name": "rafaga-free", "type": "lesson", "description": "Ráfaga 😀 notes count as two UTF-16 units each", "quote": "the build runs with make all"})
	add("index", "s1", "/work", map[string]string{"budget": "2000"})
	add("rank", "s1", "/work", nil)
	for _, name := range []string{"m0-note", "m0-note", "m7-note", "m2-note"} {
		add("record", "s1", "/work", map[string]string{"name": name, "event": "view"})
	}
	add("feedback", "s1", "/work", map[string]string{"name": "m1-note", "outcome": "helped", "quote": "deploys go out on Tuesdays"})
	add("feedback", "s1", "/work", map[string]string{"name": "m6-note", "outcome": "harmed", "quote": "deploys go out on Tuesdays"})
	add("junk", "s1", "/work", nil)
	add("usage", "s1", "/work", nil)
	add("rank", "s1", "/work", nil)
	for _, budget := range []string{"0", "1", "20", "60", "90", "150", "300"} {
		add("index", "s1", "/work", map[string]string{"budget": budget})
	}
	// Staged memories: promoted on a user's quote, on a citation, on a second session; one
	// whose evidence came before an edit is not.
	add("save", "s1", "/work", map[string]string{"name": "staged-quote", "type": "user", "description": "Staged then confirmed"})
	add("save", "s1", "/work", map[string]string{"name": "staged-cite", "type": "project", "description": "Staged then cited"})
	add("save", "s1", "/work", map[string]string{"name": "staged-twice", "type": "reference", "description": "Staged in two sessions"})
	add("save", "s2", "/work", map[string]string{"name": "staged-twice", "type": "reference", "description": "staged in TWO sessions"})
	add("save", "s1", "/work", map[string]string{"name": "staged-edited", "type": "lesson", "description": "Cited before an edit"})
	add("index", "s1", "/work", map[string]string{"budget": "2000"})
	add("feedback", "s1", "/work", map[string]string{"name": "staged-quote", "outcome": "helped", "quote": "always drink green tea"})
	add("record", "s1", "/work", map[string]string{"name": "staged-cite", "event": "cited", "record": "r1,r2"})
	add("record", "s1", "/work", map[string]string{"name": "staged-edited", "event": "cited", "record": "r3"})
	add("edit", "s1", "/work", map[string]string{"name": "staged-edited", "body": "Edited after the citation."})
	add("pin", "s1", "/work", map[string]string{"budget": "2000"})
	add("settle", "s1", "/work", nil,
		stepCorrection{ID: "c1", Subject: "m3 note", Time: at.Add(time.Minute).Format(time.RFC3339Nano)},
		stepCorrection{ID: "c2", Subject: "note", Time: at.Add(-72 * time.Hour).Format(time.RFC3339Nano)},
		stepCorrection{ID: "c3", Subject: "M4", Time: at.Add(time.Minute + 500*time.Microsecond).Format(time.RFC3339Nano)})
	add("pin", "s1", "/work", map[string]string{"budget": "2000"})
	add("pin", "s1", "/work", map[string]string{"budget": "2000", "reset": "true"})
	add("settle", "s1", "/work", nil, stepCorrection{ID: "c1", Subject: "m3 note", Time: at.Add(time.Minute).Format(time.RFC3339Nano)})
	add("usage", "s1", "/work", nil)
	add("rank", "s1", "/work", nil)
	add("user-pin", "s1", "/work", map[string]string{"name": "m4-note"})
	add("rank", "s1", "/work", nil)
	add("edit", "s1", "/work", map[string]string{"name": "m3-note", "body": "Fixed after the correction."})
	add("rank", "s1", "/work", nil)
	add("index", "s1", "/work", map[string]string{"budget": "60"})
	add("index", "s1", "", map[string]string{"budget": "2000"})
	return steps
}

// goCurated runs the script on the Go store at dir, one line per step as tool-parity.lisp
// prints it.
func goCurated(t *testing.T, dir string, steps []curatedStep) []string {
	t.Helper()
	var lines []string
	var pin curated.Pin
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
		call.SourceOf = sourceIn(s.Tools)
		switch s.Op {
		case "record":
			if s.Args["event"] == "cited" {
				err = store.RecordCited(s.Args["name"], s.Args["record"])
			} else {
				err = store.Record(s.Args["name"], s.Args["event"])
			}
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, fmt.Sprintf("%d recorded", n+1))
		case "junk":
			f, err := os.OpenFile(filepath.Join(dir, "usage.jsonl"), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.WriteString(usageJunk)
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			lines = append(lines, fmt.Sprintf("%d junk", n+1))
		case "user-pin":
			path := filepath.Join(dir, s.Args["name"]+".md")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(strings.Replace(string(data), "\n---\n", "\npinned: true\n---\n", 1)), 0o600); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, fmt.Sprintf("%d pinned", n+1))
		case "usage":
			usage, err := store.Usage()
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for name := range usage {
				names = append(names, name)
			}
			sort.Strings(names)
			parts := []string{}
			for _, name := range names {
				e := usage[name]
				last := ""
				if !e.Last.IsZero() {
					last = e.Last.UTC().Format(time.RFC3339)
				}
				parts = append(parts, fmt.Sprintf("%s:%d/%d/%d/%d/%d/%s", name, e.Views, e.Helped, e.Harmed, e.Edits, e.Corrected, last))
			}
			lines = append(lines, fmt.Sprintf("%d usage %s", n+1, strings.Join(parts, " ")))
		case "rank":
			ms, err := store.List(s.Project)
			if err != nil {
				t.Fatal(err)
			}
			usage := store.RankUsage()
			names := []string{}
			for _, m := range curated.Rank(ms, usage) {
				name := m.Name
				if curated.Flagged(m, usage[m.Name]) {
					name += "!"
				}
				names = append(names, name)
			}
			lines = append(lines, fmt.Sprintf("%d rank %s", n+1, strings.Join(names, ",")))
		case "index":
			budget, _ := strconv.Atoi(s.Args["budget"])
			text, shown := curated.IndexFor(store, s.Project, budget)
			lines = append(lines, fmt.Sprintf("%d index %s %s", n+1, strings.Join(shown, ","), quoted(text)))
		case "index-as":
			budget, _ := strconv.Atoi(s.Args["budget"])
			text, shown := curated.IndexAs(store, s.Project, s.Session, budget)
			lines = append(lines, fmt.Sprintf("%d index %s %s", n+1, strings.Join(shown, ","), quoted(text)))
		case "list-as":
			ms, err := store.ListAs(s.Project, s.Session)
			if err != nil {
				t.Fatal(err)
			}
			names := []string{}
			for _, m := range ms {
				names = append(names, m.Name)
			}
			lines = append(lines, fmt.Sprintf("%d list %s", n+1, strings.Join(names, ",")))
		case "describe-as":
			m, err := store.GetAs(s.Args["name"], s.Project, s.Session)
			if err != nil {
				lines = append(lines, fmt.Sprintf("%d error %s", n+1, quoted(err.Error())))
				continue
			}
			lines = append(lines, fmt.Sprintf("%d describe %s", n+1, quoted(curated.Describe(m))))
		case "pin":
			if s.Args["reset"] == "true" {
				pin.Reset()
			}
			budget, _ := strconv.Atoi(s.Args["budget"])
			text, shown, rendered := pin.Get(func() (string, []string) { return curated.IndexFor(store, s.Project, budget) })
			lines = append(lines, fmt.Sprintf("%d pin %t %s %s %s", n+1, rendered, strings.Join(shown, ","), strings.Join(pin.Shown(), ","), quoted(text)))
		case "settle":
			in := curated.SettleInput{Project: s.Project}
			for _, c := range s.Corrections {
				when, err := time.Parse(time.RFC3339Nano, c.Time)
				if err != nil {
					t.Fatal(err)
				}
				in.Corrections = append(in.Corrections, curated.Correction{ID: c.ID, Subject: c.Subject, Time: when})
			}
			settled, err := store.Settle(in)
			if err != nil {
				t.Fatal(err)
			}
			promoted := []string{}
			for _, p := range settled.Promoted {
				promoted = append(promoted, p.Name+"="+p.Evidence)
			}
			lines = append(lines, fmt.Sprintf("%d settle %s | %s", n+1, quoted(strings.Join(promoted, ";")), strings.Join(settled.Flagged, ",")))
		case "layer-change":
			target := -1
			if s.Args["target"] != "" {
				target, _ = strconv.Atoi(s.Args["target"])
			}
			if err := store.RecordLayerChange(s.Args["event"], s.Args["key"], s.Args["model"], s.Session, s.Args["quote"], target); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, fmt.Sprintf("%d recorded", n+1))
		case "staged":
			staged, err := store.StagedChanges(s.Project)
			if err != nil {
				t.Fatal(err)
			}
			parts := []string{}
			for _, c := range staged {
				parts = append(parts, c.String())
			}
			lines = append(lines, fmt.Sprintf("%d staged %s", n+1, quoted(strings.Join(parts, "; "))))
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

// usageJunk are usage lines Go's decoder skips (a member of the wrong type, no name, not JSON)
// or matches ignoring case, and a line still being written, which the next append completes
// into one the decoder skips.
const usageJunk = `{"name":"m0-note","event":"view","time":5}
not json
{"name":"","event":"view"}
{"NAME":"m5-note","Event":"view","time":"2026-11-01T09:00:00Z"}
{"name":"m5-note","event":"view","user":"yes"}
{"name":"m5-note","event":"helped","user":true,"quote":null}
[1,2]
null
{"name":"m5-note","event":"view"`

// dropUsageJunk removes the lines the script's junk step wrote (usageJunk, its unfinished last
// line completed by the next append) from the usage ledger at path, so what the store itself
// wrote is checked against the schema.
func dropUsageJunk(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	junk := strings.Split(usageJunk, "\n")
	partial := junk[len(junk)-1]
	kept := []string{}
	for _, line := range strings.SplitAfter(string(data), "\n") {
		if line == "" || slices.Contains(junk[:len(junk)-1], strings.TrimSuffix(line, "\n")) || strings.HasPrefix(line, partial) {
			continue
		}
		kept = append(kept, line)
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "")), 0o600); err != nil {
		t.Fatal(err)
	}
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
	dropUsageJunk(t, filepath.Join(lispDir, "usage.jsonl"))
	seen := validateTree(t, loadSchemas(t), filepath.Join(root, "lisp"))
	if seen["curated-memory.schema.json"] == 0 || seen["curated-usage-line.schema.json"] == 0 {
		t.Errorf("the Lisp store wrote no memory or usage file (%v)", seen)
	}
	oks, errs, also, more, promoted, flagged := 0, 0, 0, 0, 0, 0
	for _, w := range want {
		_, rest, _ := strings.Cut(w, " ")
		switch {
		case strings.HasPrefix(rest, "ok "):
			oks++
		case strings.HasPrefix(rest, "error "):
			errs++
		case strings.HasPrefix(rest, "index "):
			if strings.Contains(rest, `\nalso: `) {
				also++
			}
			if strings.Contains(rest, " more memor") {
				more++
			}
		case strings.HasPrefix(rest, "settle "):
			if !strings.HasPrefix(rest, `settle "" |`) {
				promoted++
			}
			if !strings.HasSuffix(rest, "| ") {
				flagged++
			}
		}
	}
	t.Logf("identical: %d steps (%d writes done, %d refused; %d indexes with an also line, %d with more left out; %d settles promoting, %d flagging), %d files",
		len(want), oks, errs, also, more, promoted, flagged, len(names))
	if also == 0 || more == 0 || promoted == 0 || flagged == 0 {
		t.Error("the script no longer reaches every branch of the index and the settle")
	}
	// docs/brain-memory.md's gates: staged and confirmed forgets and edits, the per-quote bound,
	// the layer's staged changes, stagings from the same source and from distinct ones, and a
	// promotion on independent sessions.
	joined := strings.Join(want, "\n")
	for _, reach := range []string{
		"ok \"Hid memory tea in this session and staged its forget: no user's words ask for it",
		"staged its forget: the quote is not a user's words",
		"the most one quote confirms",
		"ok \"Changed memory gate-a [project] in this session only and staged the edit: no user's words ask for it",
		"staged the edit: the quote is not a user's words",
		"ok \"Updated memory gate-a [project]: Build with make ci.\"",
		"ok \"Changed memory gate-d [project] in this session only and staged the edit: the quote already confirmed 3 changes",
		"edit gate-a (g2): Build with make release-unsafe.; forget gate-b (g2);",
		"; layer forget p/m instructions/consolidation (g2); layer rollback p/m to generation 0 (g3); layer rollback q/n to generation 2 (g3)\"",
		"ok \"Updated memory gate-own [lesson]: My own guess.\"",
		"ok \"Forgot memory api-4.\"",
		"ok \"Forgot memory own-guess.\"",
		"but not from other source content it quotes",
		"from other source content, which is the evidence",
		"push-main=" + curated.EvidenceSessions,
		"It weakens security (tls-verify-off), so it is held: the user's words restate it",
		"It weakens security (world-writable), so it is held: listed in /om:status",
		"Recorded that memory curl-dev helped. It weakens security",
	} {
		if !strings.Contains(joined, reach) {
			t.Errorf("the script no longer reaches %q", reach)
		}
	}
	// The quoted layer forget and rollback take theirs out of the staged changes.
	confirmed := false
	for _, l := range want {
		confirmed = confirmed || strings.Contains(l, " staged \"") && strings.Contains(l, "forget gate-b (g2); edit gate-d (g3): Shared gate-d.;") &&
			!strings.Contains(l, "layer forget p/m") && !strings.Contains(l, "layer rollback p/m") && strings.HasSuffix(l, "; layer rollback q/n to generation 2 (g3)\"")
	}
	if !confirmed {
		t.Error("the script no longer confirms a staged layer change")
	}
}

// settleCase is one held definition and the usage ledger events it is settled on.
type settleCase struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	HeldID   string `json:"heldId"`
	Time     string `json:"time"`
	Detector string `json:"detector"`
	// Text is the definition's text as declared, whose tokens an instructions definition is
	// charged (rules.SettleV2's cost).
	Text   string               `json:"text"`
	Events []curated.ModelEvent `json:"events"`
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
		// A long case is a null arm: controls before the hold, then compactions under it, so the
		// settlement rules reach their decisions; a short one is random.
		n, long := rng.IntN(5), i%3 == 0
		if long {
			n = 8 + rng.IntN(10)
		}
		for j := 0; j < n; j++ {
			offsets := []time.Duration{-time.Hour, -time.Nanosecond, 0, time.Nanosecond, time.Minute, 3 * time.Hour}
			at := held.Add(offsets[rng.IntN(len(offsets))])
			if long {
				at = held.Add(time.Duration(j-n/2) * time.Minute)
			}
			e := curated.ModelEvent{Time: at.Format(time.RFC3339Nano), Model: "p/m", Engine: curated.EngineV2}
			if rng.IntN(8) == 0 {
				e.Engine = ""
			}
			switch kind := rng.IntN(3); {
			case kind == 0 && !long:
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
				for _, k := range []string{rules.CountLevel1Failed, rules.CountLevel1SpecificsRefused, rules.CountLevel1UnionKept, rules.CountLevel1Refused,
					rules.DetectUncitedAssistantSpecifics, rules.DetectDecodeFailure, rules.DetectDroppedThenQueried} {
					if rng.IntN(5) == 0 {
						e.Counts[k] = rng.IntN(3)
					}
				}
				// The settlement fields (rules.SettleV2's null arm): most passes carry them.
				if rng.IntN(6) != 0 {
					harm := rules.HarmOf(e.Counts)
					e.Harm = &harm
				}
				for k := rng.IntN(3); k > 0; k-- {
					e.Roots = append(e.Roots, fmt.Sprintf("%016x", rng.IntN(30)))
				}
				sort.Strings(e.Roots)
				e.Roots = slices.Compact(e.Roots)
				e.Turns = rng.IntN(12)
				e.Repo = []string{"", "r1", "r2"}[rng.IntN(3)]
				e.Family = []string{"", ".go", ".md"}[rng.IntN(3)]
				if rng.IntN(6) == 0 {
					e.RanWritten = []string{d.Key() + "@00000000000000ff"}
				}
			}
			switch {
			case long && j < n/2:
				if rng.IntN(8) == 0 {
					e.RanHeld = []string{d.Key() + "@0000000000000000"}
				}
			case rng.IntN(4) != 0:
				e.RanHeld = []string{id}
			case rng.IntN(2) == 0:
				e.RanHeld = []string{d.Key() + "@0000000000000000"}
			}
			events = append(events, e)
		}
		cases = append(cases, settleCase{Kind: d.Kind, Name: d.Name, HeldID: id, Time: held.Format(time.RFC3339Nano), Detector: detector, Text: d.Declared(), Events: events})
		want = append(want, fmt.Sprintf("%d %s", i+1, settleWords(rules.SettleV2(events, d))))
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
