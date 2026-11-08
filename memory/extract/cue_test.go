package extract

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// cueCasesPath holds the cue cases (docs/memory-v2.md, "Cues"), which lisp/tests/delivery.lisp
// reads too.
var cueCasesPath = filepath.Join("..", "..", "contracts", "cue.jsonl")

type cueCase struct {
	Name    string           `json:"name"`
	Entries []map[string]any `json:"entries"`
	Call    map[string]any   `json:"call"`
	Cwd     string           `json:"cwd"`
	Cap     int              `json:"cap"`
	Cue     *Cue             `json:"cue"`
}

const cueCwd = "/work/app"

func cueRecord(id, kind, content string, f func(*ledger.Observation)) ledger.Observation {
	o := ledger.Observation{ID: id, Content: content, Kind: ledger.ObservationKind(kind), Timestamp: testfix.DefaultTimestamp, Relevance: "medium", SourceEntryIDs: []string{"u" + id[10:]}, TokenCount: 10, Origin: ledger.OriginCode}
	if f != nil {
		f(&o)
	}
	return o
}

func cueRecorded(id string, records ...ledger.Observation) map[string]any {
	return map[string]any{"type": "custom", "id": id, "parentId": nil, "timestamp": testfix.DefaultTimestamp, "customType": ledger.OMObservationsRecorded,
		"data": map[string]any{"observations": records, "coversUpToId": "u" + id}}
}

func cueGiven(id, record string) map[string]any {
	return map[string]any{"type": "custom", "id": id, "parentId": nil, "timestamp": testfix.DefaultTimestamp, "customType": ledger.OMCue,
		"data": map[string]any{"record": record, "key": "paths:x", "compaction": ""}}
}

func cueCompactionEntry(id string) map[string]any {
	return map[string]any{"type": "compaction", "id": id, "parentId": nil, "timestamp": testfix.DefaultTimestamp, "summary": "s", "firstKeptEntryId": "u9", "tokensBefore": 100}
}

// cueBase is a ledger with a failed make run, a rule on a directory, a rule on a file, a decision
// on a file and a held rule.
func cueBase() []map[string]any {
	failed := cueRecord("0000000000c1", "attempt", "bash \"make ci\" failed: dial tcp 127.0.0.1:5432: connect: connection refused", func(o *ledger.Observation) {
		o.Tool, o.Outcome = "bash", ledger.OutcomeFailed
		o.Keys.Add(ledger.KeyCommands, "make ci")
		o.Keys.Add(ledger.KeyErrors, "dial tcp 127.0.0.1:5432: connect: connection refused")
	})
	worked := cueRecord("0000000000c2", "attempt", "bash \"go vet ./...\" worked", func(o *ledger.Observation) {
		o.Tool, o.Outcome = "bash", ledger.OutcomeWorked
		o.Keys.Add(ledger.KeyCommands, "go vet ./...")
	})
	dir := cueRecord("0000000000c3", "preference", "Don't touch `migrations/`.", func(o *ledger.Observation) {
		o.Keys.Add(ledger.KeyPaths, "migrations/")
	})
	file := cueRecord("0000000000c4", "decision", "We keep the rate limit in `api/limit.go` because the gateway reads it.", func(o *ledger.Observation) {
		o.Keys.Add(ledger.KeyPaths, "api/limit.go")
	})
	return []map[string]any{cueRecorded("r1", failed, worked, dir, file)}
}

// cueContested is a ledger of contests (docs/memory-v2.md, "Cues"): two of the user's rules on
// `make lint` one negates (written in order, or swapped when swap is set), a user's rule against
// a model's decision on one file whose ids run against their authorities, a claim the
// assistant pushed back on, a long pair, a pair whose other side a correction superseded, a
// value's older record a newer one updated, and a retracted rule.
func cueContested(swap bool) []map[string]any {
	lint := func(id, content, other string) ledger.Observation {
		return cueRecord(id, "preference", content, func(o *ledger.Observation) {
			o.Authority = ledger.AuthorityUser
			o.Keys.Add(ledger.KeyCommands, "make lint")
			if other != "" {
				o.Conflicts = []string{other}
			}
		})
	}
	first, second := lint("0000000000d1", "Run `make lint` before each commit.", ""), lint("0000000000d2", "Don't run `make lint` before each commit.", "0000000000d1")
	if swap {
		first, second = lint("0000000000d2", "Don't run `make lint` before each commit.", ""), lint("0000000000d1", "Run `make lint` before each commit.", "0000000000d2")
	}
	rule := cueRecord("0000000000d4", "preference", "Never edit `gen/api.go` by hand.", func(o *ledger.Observation) {
		o.Authority = ledger.AuthorityUser
		o.Keys.Add(ledger.KeyPaths, "gen/api.go")
	})
	decision := cueRecord("0000000000d3", "decision", "We edit `gen/api.go` by hand while the generator is down.", func(o *ledger.Observation) {
		o.Authority = ledger.AuthorityModel
		o.Keys.Add(ledger.KeyPaths, "gen/api.go")
		o.Conflicts = []string{"0000000000d4"}
	})
	claim := cueRecord("0000000000d5", "preference", "Skip the tests in `slow/`.", func(o *ledger.Observation) {
		o.Authority, o.Source, o.Hedge = ledger.AuthorityUser, ledger.SourceUserSaid, ledger.HedgeContested
		o.Keys.Add(ledger.KeyPaths, "slow/")
	})
	pushback := cueRecord("0000000000d6", "pushback", "Skipping them hides the flaky lock test.", func(o *ledger.Observation) {
		o.Authority, o.Source = ledger.AuthorityModel, ledger.SourceAssistantClaim
		o.RespondsTo = []string{"0000000000d5"}
	})
	longA := cueRecord("0000000000d7", "preference", "Keep the vendored tree under `third_party/` exactly as upstream ships it, byte for byte.", func(o *ledger.Observation) {
		o.Authority = ledger.AuthorityUser
		o.Keys.Add(ledger.KeyPaths, "third_party/")
	})
	longB := cueRecord("0000000000d8", "decision", "Do not keep the vendored tree under `third_party/` exactly as upstream ships it; we patch it.", func(o *ledger.Observation) {
		o.Authority = ledger.AuthorityTool
		o.Keys.Add(ledger.KeyPaths, "third_party/")
		o.Conflicts = []string{"0000000000d7"}
	})
	lone := cueRecord("0000000000d9", "preference", "Use `pnpm` in `web/`.", func(o *ledger.Observation) {
		o.Authority = ledger.AuthorityUser
		o.Keys.Add(ledger.KeyPaths, "web/")
	})
	gone := cueRecord("0000000000da", "decision", "We don't use `pnpm` in `web/`.", func(o *ledger.Observation) {
		o.Authority = ledger.AuthorityModel
		o.Conflicts = []string{"0000000000d9"}
	})
	fix := cueRecord("0000000000db", "correction", "We use `npm` in `web/`.", func(o *ledger.Observation) {
		o.Authority = ledger.AuthorityUser
		o.Supersedes = []string{"0000000000da"}
	})
	older := cueRecord("0000000000dc", "decision", "We pin Go 1.22 in `go.mod`.", func(o *ledger.Observation) {
		o.Authority = ledger.AuthorityUser
		o.Keys.Add(ledger.KeyPaths, "go.mod")
	})
	newer := cueRecord("0000000000dd", "decision", "We pin Go 1.23 now.", func(o *ledger.Observation) {
		o.Authority = ledger.AuthorityUser
		o.PossiblyUpdates = []string{"0000000000dc"}
	})
	retracted := cueRecord("0000000000de", "preference", "Run `make deploy` after each merge.", func(o *ledger.Observation) {
		o.Authority, o.Status = ledger.AuthorityUser, ledger.StatusRetracted
		o.Keys.Add(ledger.KeyCommands, "make deploy")
	})
	return []map[string]any{cueRecorded("r2", first, second, rule, decision, claim, pushback, longA, longB, lone, gone, fix, older, newer, retracted)}
}

func cueCases() []cueCase {
	bash := func(command, output string) map[string]any {
		return map[string]any{"tool": "Bash", "args": map[string]any{"command": command}, "output": output}
	}
	edit := func(path string) map[string]any {
		return map[string]any{"tool": "Edit", "args": map[string]any{"file_path": path}, "output": "ok"}
	}
	cases := []cueCase{
		{Name: "the same command that failed", Entries: cueBase(), Call: bash("make ci", "")},
		{Name: "the same error line", Entries: cueBase(), Call: bash("make test", "--- FAIL\ndial tcp 127.0.0.1:5432: connect: connection refused\n")},
		{Name: "a path inside a ruled directory", Entries: cueBase(), Call: edit("migrations/0004_users.sql")},
		{Name: "an absolute path under cwd", Entries: cueBase(), Call: edit("/work/app/api/limit.go")},
		// Scenario 28: look-alikes never fire.
		{Name: "look-alike path", Entries: cueBase(), Call: edit("api/limits.go")},
		{Name: "look-alike directory", Entries: cueBase(), Call: edit("migrations-old/0001.sql")},
		{Name: "look-alike error", Entries: cueBase(), Call: bash("make test", "dial tcp 127.0.0.1:5433: connect: connection refused")},
		{Name: "look-alike command", Entries: cueBase(), Call: bash("make  ci-fast", "")},
		{Name: "a command that worked is no cue", Entries: cueBase(), Call: bash("go vet ./...", "")},
		{Name: "no key", Entries: cueBase(), Call: map[string]any{"tool": "WebSearch", "args": map[string]any{"query": "make ci"}, "output": ""}},
		{Name: "a record once per compaction", Entries: append(cueBase(), cueGiven("q1", "0000000000c1")), Call: bash("make ci", "")},
		{Name: "again after a compaction", Entries: append(cueBase(), cueGiven("q1", "0000000000c1"), cueCompactionEntry("k1")), Call: bash("make ci", "")},
		{Name: "the cap", Entries: append(cueBase(), cueGiven("q1", "x1"), cueGiven("q2", "x2"), cueGiven("q3", "x3")), Call: bash("make ci", "")},
		{Name: "cues off", Entries: cueBase(), Call: bash("make ci", ""), Cap: -1},
		// A cue never settles a contest (docs/memory-v2.md, "Cues"): it names every side, in
		// authority then id order whichever was written last, or abstains.
		{Name: "a contested pair names both sides", Entries: cueContested(false), Call: bash("make lint", "")},
		{Name: "a contested pair written the other way round", Entries: cueContested(true), Call: bash("make lint", "")},
		{Name: "a contested pair, the higher authority first", Entries: cueContested(false), Call: edit("gen/api.go")},
		{Name: "a contested claim names its pushback", Entries: cueContested(false), Call: edit("slow/lock_test.go")},
		{Name: "a contested pair too long to quote whole abstains", Entries: cueContested(false), Call: edit("third_party/zlib/zlib.h")},
		{Name: "a contest whose other side was superseded abstains", Entries: cueContested(false), Call: edit("web/package.json")},
		{Name: "a superseded value is never cued", Entries: cueContested(false), Call: edit("go.mod")},
		{Name: "a retracted rule is never cued", Entries: cueContested(false), Call: bash("make deploy", "")},
		{Name: "a pair once per compaction", Entries: append(cueContested(false), cueGiven("q1", "0000000000d1")), Call: bash("make lint", "")},
	}
	for i := range cases {
		cases[i].Cwd = cueCwd
		if cases[i].Cap == 0 {
			cases[i].Cap = CueCap
		}
	}
	return cases
}

func cueEntries(maps []map[string]any) []ledger.Entry {
	out := make([]ledger.Entry, len(maps))
	for i, m := range maps {
		raw, _ := json.Marshal(m)
		var back map[string]any
		_ = json.Unmarshal(raw, &back)
		out[i] = session.FromMap(back)
	}
	return out
}

// TestCueCases writes or checks contracts/cue.jsonl (go test ./extract -run CueCases -args
// -write): each case's decision, scenario 28's look-alikes abstaining.
func TestCueCases(t *testing.T) {
	var b bytes.Buffer
	for _, c := range cueCases() {
		raw, _ := json.Marshal(c.Entries)
		_ = json.Unmarshal(raw, &c.Entries)
		args, _ := c.Call["args"].(map[string]any)
		output, _ := c.Call["output"].(string)
		tool, _ := c.Call["tool"].(string)
		if cue, ok := CueFor(cueEntries(c.Entries), CueCall{Tool: tool, Args: args, Output: output}, c.Cwd, c.Cap); ok {
			c.Cue = &cue
		}
		line, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if writeConversation() {
		if err := os.WriteFile(cueCasesPath, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(cueCasesPath)
	if err != nil || !bytes.Equal(want, b.Bytes()) {
		t.Fatalf("contracts/cue.jsonl is stale; rerun with -args -write and review the diff (%v)", err)
	}
	fired := map[string]bool{}
	texts := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(b.String()), "\n") {
		var c cueCase
		_ = json.Unmarshal([]byte(line), &c)
		fired[c.Name] = c.Cue != nil
		if c.Cue != nil && (!strings.Contains(c.Cue.Text, "om.Recall(`"+c.Cue.Record+"`)") || !strings.Contains(c.Cue.Text, "unverified")) {
			t.Errorf("%s: cue text %q", c.Name, c.Cue.Text)
		}
		texts[c.Name] = ""
		if c.Cue != nil {
			texts[c.Name] = c.Cue.Text
		}
	}
	// Both sides, each with its label and snippet, the user's first, the same whichever side
	// was written last.
	pair := texts["a contested pair names both sides"]
	if pair == "" || pair != texts["a contested pair written the other way round"] ||
		strings.Index(pair, "om.Recall(`0000000000d1`)") > strings.Index(pair, "om.Recall(`0000000000d2`)") ||
		strings.Count(pair, "(contested from user)") != 2 {
		t.Errorf("pair cue %q, swapped %q", pair, texts["a contested pair written the other way round"])
	}
	if mixed := texts["a contested pair, the higher authority first"]; strings.Index(mixed, "(contested from user)") > strings.Index(mixed, "(contested from model)") || !strings.Contains(mixed, "om.Recall(`0000000000d3`)") {
		t.Errorf("authority order %q", mixed)
	}
	if claim := texts["a contested claim names its pushback"]; !strings.Contains(claim, "om.Recall(`0000000000d6`)") || !strings.Contains(claim, "(contested from user)") {
		t.Errorf("claim cue %q", claim)
	}
	for name, want := range map[string]bool{
		"the same command that failed": true, "the same error line": true, "a path inside a ruled directory": true, "an absolute path under cwd": true,
		"look-alike path": false, "look-alike directory": false, "look-alike error": false, "look-alike command": false,
		"a command that worked is no cue": false, "no key": false, "a record once per compaction": false, "again after a compaction": true,
		"the cap": false, "cues off": false,
		"a contested pair names both sides": true, "a contested pair written the other way round": true, "a contested pair, the higher authority first": true,
		"a contested claim names its pushback": true, "a contested pair too long to quote whole abstains": false,
		"a contest whose other side was superseded abstains": false, "a superseded value is never cued": false, "a retracted rule is never cued": false,
		"a pair once per compaction": false,
	} {
		if fired[name] != want {
			t.Errorf("%s: fired %v, want %v", name, fired[name], want)
		}
	}
}
