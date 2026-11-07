package sleep_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/sleep"
	tf "github.com/drvova/pig-observational-memory/memory/testfix"
)

var writeCases = flag.Bool("write", false, "rewrite contracts/sleep.jsonl")

// casesPath holds the sleep pass's cases, which lisp/tests/sleep.lisp reads too.
var casesPath = filepath.Join("..", "..", "contracts", "sleep.jsonl")

// Case is one line of contracts/sleep.jsonl: a branch and the pass's input, then the sleep
// record's data as JSON text (byte for byte), the records it hides, and every record's label once
// the record is on the branch.
type Case struct {
	Name         string                `json:"name"`
	Entries      []map[string]any      `json:"entries"`
	Trigger      string                `json:"trigger"`
	Decay        string                `json:"decay,omitempty"`
	DecayHorizon int                   `json:"decayHorizon,omitempty"`
	Repo         *host.Repo            `json:"repo,omitempty"`
	Structure    *host.StructureAnswer `json:"structure,omitempty"`
	JSON         string                `json:"json"`
	Hidden       []string              `json:"hidden,omitempty"`
	Labels       map[string]string     `json:"labels,omitempty"`
	Due          int                   `json:"due"`
}

func obs(id, content string, f func(*ledger.Observation)) ledger.Observation {
	o := ledger.Observation{ID: id, Content: content, Timestamp: tf.DefaultTimestamp, Relevance: "medium", SourceEntryIDs: []string{"u" + id[9:]}, TokenCount: 10, Origin: ledger.OriginCode, Kind: "fact"}
	if f != nil {
		f(&o)
	}
	return o
}

func recorded(id, covers string, consolidated bool, observations ...ledger.Observation) session.Entry {
	return tf.Entry(map[string]any{"type": "custom", "id": id, "parentId": nil, "timestamp": tf.DefaultTimestamp, "customType": ledger.OMObservationsRecorded,
		"data": map[string]any{"observations": observations, "coversUpToId": covers, "consolidated": consolidated}})
}

func user(id string) session.Entry { return tf.RawMessage(id, "message "+id) }

func compaction(id, firstKept string) session.Entry {
	return tf.CompactionEntry(id, tf.CompactionArgs{Summary: "summary " + id, FirstKeptEntryID: firstKept, Details: map[string]any{"type": ledger.OMFolded, "version": 3, "fullFold": true, "observations": []any{}, "reflections": []any{}, "engine": "v2"}}, nil)
}

func sleepEntry(id string, data ledger.SleepData) session.Entry {
	return tf.Entry(map[string]any{"type": "custom", "id": id, "parentId": nil, "timestamp": tf.DefaultTimestamp, "customType": ledger.OMSleep, "data": tf.ToJSONValue(data)})
}

func attempt(o *ledger.Observation) {
	o.Kind, o.Outcome, o.Tool = ledger.KindAttempt, ledger.OutcomeWorked, "bash"
}

func keys(kind string, values ...string) func(*ledger.Observation) {
	return func(o *ledger.Observation) {
		for _, v := range values {
			o.Keys.Add(kind, v)
		}
	}
}

func both(fs ...func(*ledger.Observation)) func(*ledger.Observation) {
	return func(o *ledger.Observation) {
		for _, f := range fs {
			f(o)
		}
	}
}

type input struct {
	name      string
	entries   []session.Entry
	trigger   string
	decay     string
	horizon   int
	repo      *host.Repo
	structure *host.StructureAnswer
}

func inputs() []input {
	// Duplicates: the same attempt three times (the later two hidden), one named by a correction
	// (kept), and two evidence records whose error lines normalize alike.
	a1 := obs("000000000a01", "Ran `go vet ./...` [bash worked]", attempt)
	a2 := obs("000000000a02", "Ran `go vet ./...` [bash worked]", attempt)
	a3 := obs("000000000a03", "Ran `go vet ./...` [bash worked]", attempt)
	f1 := obs("000000000a04", "The staging port is 5432.", nil)
	f2 := obs("000000000a05", "The staging port is 5432.", nil)
	fix := obs("000000000a06", "Actually the staging port is 5433.", func(o *ledger.Observation) { o.Kind, o.Supersedes = "correction", []string{f2.ID} })
	e1 := obs("000000000a07", "main.go:12: undefined: retryLease\nFAIL\tpkg/lease", func(o *ledger.Observation) { o.Kind, o.Tool = ledger.KindEvidence, "bash" })
	e2 := obs("000000000a08", "main.go:40:  undefined:   retryLease\nFAIL\tpkg/lease", func(o *ledger.Observation) { o.Kind, o.Tool = ledger.KindEvidence, "bash" })
	dupBranch := []session.Entry{user("u0a01"), recorded("r1", "u0a01", false, a1, f1), user("u0a02"), recorded("r2", "u0a02", false, a2, f2, e1),
		user("u0a03"), recorded("r3", "u0a03", false, a3, fix, e2)}

	// Links: three records on one path, one on another, a key on more than MaxLinkGroup records.
	var many []ledger.Observation
	for i := range 9 {
		many = append(many, obs("000000000c"+string(rune('a'+i))+"0", "Touched `pkg/wide.go` "+string(rune('a'+i)), keys(ledger.KeyPaths, "pkg/wide.go")))
	}
	l1 := obs("000000000b01", "Edited `api/limit.go` to add the limiter.", keys(ledger.KeyPaths, "api/limit.go"))
	l2 := obs("000000000b02", "Ran `go test ./api` [bash failed]", both(attempt, func(o *ledger.Observation) { o.Outcome = ledger.OutcomeFailed }, keys(ledger.KeyPaths, "api/limit.go"), keys(ledger.KeyCommands, "go test ./api")))
	l3 := obs("000000000b03", "Ran `go test ./api` [bash worked]", both(attempt, keys(ledger.KeyCommands, "go test ./api")))
	linkBranch := append([]session.Entry{user("u0b01"), recorded("r1", "u0b01", false, l1, l2, l3)}, recorded("r2", "u0b01", false, many...))

	// Incremental: the same link and duplicate already in a sleep record are not repeated.
	prior := ledger.SleepData{Version: ledger.SleepVersion, Trigger: ledger.SleepAtIdle, ThroughEntryID: "r3", Records: 6,
		Duplicates: []ledger.SleepDuplicate{{ID: a2.ID, Of: a1.ID, By: ledger.DuplicateByHash}}}
	incremental := append(append([]session.Entry{}, dupBranch...), sleepEntry("s1", prior), user("u0a09"))

	// Decay on the compaction clock, horizon 1: the attempts decay, the user's preference is
	// protected.
	pref := obs("000000000d01", "Always run `make ci-local` before pushing.", func(o *ledger.Observation) {
		o.Kind, o.Source, o.Authority, o.FromUser = "preference", ledger.SourceUserSaid, ledger.AuthorityUser, true
	})
	d1 := obs("000000000d02", "Ran `ls` [bash worked]", attempt)
	decayBranch := []session.Entry{user("u0d01"), recorded("r1", "u0d01", false, pref, d1), user("u0d02"), compaction("c1", "u0d02"), user("u0d03"), compaction("c2", "u0d03"), user("u0d04")}

	// Verification: a decision on a path that holds, one on a path gone at HEAD, one whose path's
	// blob changed.
	frame := func(path, head, body string) func(*ledger.Observation) {
		return func(o *ledger.Observation) {
			o.Kind, o.Source = ledger.KindDecision, ledger.SourceUserSaid
			o.Frame = ledger.Frame{RepoID: "repo-1", Head: head, Path: path, BodyHash: body}
		}
	}
	v1 := obs("000000000e01", "We decided to keep the retry loop in `pkg/retry.go` because callers rely on it.", frame("pkg/retry.go", "aaaaaaa1", "blob-1"))
	v2 := obs("000000000e02", "We decided to keep the cache in `pkg/old.go` because it is warm.", frame("pkg/old.go", "aaaaaaa1", "blob-2"))
	v3 := obs("000000000e03", "We decided to log in `pkg/log.go` because ops reads it.", frame("pkg/log.go", "aaaaaaa1", "blob-3"))
	verifyBranch := []session.Entry{user("u0e01"), recorded("r1", "u0e01", false, v1, v2, v3)}
	repo := &host.Repo{ID: "repo-1", Head: "bbbbbbb2"}
	answer := &host.StructureAnswer{RepoID: "repo-1", Head: "bbbbbbb2", Paths: []host.PathAnswer{
		{Path: "pkg/retry.go", Known: true, Exists: true, Blob: "blob-1"},
		{Path: "pkg/old.go", Known: true, Exists: false},
		{Path: "pkg/log.go", Known: true, Exists: true, Blob: "blob-9"},
	}}

	// Checks: a level-1 record replacing x, restated by a later code record (challenged); one
	// replacing y with only unrelated later evidence (holds); a lesson whose cited record's
	// command later fails (challenged).
	x := obs("000000000f01", "The deploy target is eu-west-1.", nil)
	y := obs("000000000f02", "The build cache lives in `/tmp/cache`.", nil)
	cmd := obs("000000000f03", "Ran `make deploy` [bash worked]", both(attempt, keys(ledger.KeyCommands, "make deploy")))
	mx := obs("000000000f04", "The deploy target is us-east-2.", func(o *ledger.Observation) {
		o.Origin, o.Replaces, o.SourceEntryIDs = ledger.OriginModel, []string{x.ID}, x.SourceEntryIDs
	})
	my := obs("000000000f05", "The build cache lives in `/var/cache/build`.", func(o *ledger.Observation) {
		o.Origin, o.Replaces, o.SourceEntryIDs = ledger.OriginModel, []string{y.ID}, y.SourceEntryIDs
	})
	restate := obs("000000000f06", "the deploy  target is EU-WEST-1.", nil)
	failed := obs("000000000f07", "Ran `make deploy` [bash failed]", both(attempt, func(o *ledger.Observation) { o.Outcome = ledger.OutcomeFailed }, keys(ledger.KeyCommands, "make deploy")))
	lesson := ledger.Reflection{ID: "000000000f08", Content: "Deploys go through `make deploy`.", SupportingObservationIDs: []string{cmd.ID}, TokenCount: 6}
	checkBranch := []session.Entry{user("u0f01"), recorded("r1", "u0f01", false, x, y, cmd), user("u0f02"),
		recorded("r2", "u0f02", true, mx, my), tf.ReflectionsRecordedEntry("rf", []ledger.Reflection{lesson}, "u0f02"),
		tf.Entry(map[string]any{"type": "custom", "id": "d2", "parentId": nil, "timestamp": tf.DefaultTimestamp, "customType": ledger.OMObservationsDropped,
			"data": map[string]any{"observationIds": []string{x.ID, y.ID}, "coversUpToId": "u0f02", "consolidated": true}}),
		user("u0f03"), recorded("r3", "u0f03", false, restate, failed)}

	return []input{
		{name: "duplicates by hash and by error lines", entries: dupBranch, trigger: ledger.SleepAtCompaction},
		{name: "links of shared keys", entries: linkBranch, trigger: ledger.SleepManual},
		{name: "incremental after an earlier sleep record", entries: incremental, trigger: ledger.SleepAtIdle},
		{name: "decay noted when on", entries: decayBranch, trigger: ledger.SleepAtCompaction, decay: "compactions", horizon: 1},
		{name: "decay off notes nothing", entries: decayBranch, trigger: ledger.SleepAtCompaction},
		{name: "verification and retirement against HEAD", entries: verifyBranch, trigger: ledger.SleepManual, repo: repo, structure: answer},
		{name: "checks of level-1 supersedes and lessons", entries: checkBranch, trigger: ledger.SleepAtIdle},
		{name: "an empty ledger", entries: []session.Entry{user("u0z01")}, trigger: ledger.SleepManual},
	}
}

func run(in input) Case {
	si := sleep.Input{Entries: in.entries, Trigger: in.trigger, Decay: in.decay, DecayHorizon: in.horizon, Structure: in.structure}
	c := Case{Name: in.name, Trigger: in.trigger, Decay: in.decay, DecayHorizon: in.horizon, Repo: in.repo, Structure: in.structure,
		Due: ledger.CompactionsSinceSleep(in.entries)}
	if in.repo != nil {
		si.Repo = *in.repo
	}
	for _, e := range in.entries {
		c.Entries = append(c.Entries, e.ToMap())
	}
	data, _ := sleep.Pass(si)
	encoded, err := json.Marshal(data)
	if err != nil {
		panic(err)
	}
	c.JSON = string(encoded)
	after := append(append([]session.Entry{}, in.entries...), sleepEntry("s-new", data))
	for _, o := range ledger.FoldLedger(after, "").ActiveObservations {
		if _, hidden := ledger.SleepStateOf(after).Duplicates[o.ID]; hidden {
			c.Hidden = append(c.Hidden, o.ID)
		}
	}
	c.Labels = ledger.FoldLedger(after, "").Labels()
	return c
}

func encodeCases(cases []Case) []byte {
	var b bytes.Buffer
	for _, c := range cases {
		line, err := json.Marshal(c)
		if err != nil {
			panic(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// TestSleepContract checks contracts/sleep.jsonl is the pass's output (go test ./sleep -run
// SleepContract -args -write rewrites it).
func TestSleepContract(t *testing.T) {
	var cases []Case
	for _, in := range inputs() {
		cases = append(cases, run(in))
	}
	want := encodeCases(cases)
	if *writeCases {
		if err := os.WriteFile(casesPath, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(casesPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("contracts/sleep.jsonl is stale: go test ./sleep -run SleepContract -args -write")
	}
}
