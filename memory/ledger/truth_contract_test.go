package ledger_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
	tf "github.com/drvova/pig-observational-memory/memory/testfix"
)

var writeTruth = flag.Bool("write", false, "rewrite contracts/truth.jsonl")

// truthPath holds the truth-maintenance cases (contracts/truth.md), which lisp/tests/truth.lisp
// reads too.
var truthPath = filepath.Join("..", "..", "contracts", "truth.jsonl")

// TruthCase is one case: a branch's entries and what the fold works out from them, or record pairs
// and their supersession decisions.
type TruthCase struct {
	Name    string           `json:"name"`
	Entries []map[string]any `json:"entries,omitempty"`
	// Observations and Active are the fold's records and its active ones, by id; Reflections its
	// reflections; Labels every record's label; Out and Forgotten truth maintenance's result;
	// Projection the active records of the full projection.
	Observations []string          `json:"observations,omitempty"`
	Active       []string          `json:"active,omitempty"`
	Reflections  []string          `json:"reflections,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Out          map[string]string `json:"out,omitempty"`
	Forgotten    map[string]string `json:"forgotten,omitempty"`
	Projection   []string          `json:"projection,omitempty"`
	// Pairs are [later, earlier] records with Decisions their Supersession decisions.
	Pairs     [][2]ledger.Observation `json:"pairs,omitempty"`
	Decisions []string                `json:"decisions,omitempty"`
	// Decayed are the active records decay lowers, by clock ("compactions:1", "turns:2": a
	// clock and its horizon; ledger.Decayed), for the cases that list Clocks.
	Clocks  []string            `json:"clocks,omitempty"`
	Decayed map[string][]string `json:"decayed,omitempty"`
	// Plans are records with Intents their types (IntentOf).
	Plans   []ledger.Observation `json:"plans,omitempty"`
	Intents []string             `json:"intents,omitempty"`
}

func obs(id, content string, f func(*ledger.Observation)) ledger.Observation {
	o := ledger.Observation{ID: id, Content: content, Timestamp: tf.DefaultTimestamp, Relevance: "medium", SourceEntryIDs: []string{"u" + id[11:]}, TokenCount: 10, Origin: ledger.OriginCode}
	if f != nil {
		f(&o)
	}
	return o
}

func recorded(id string, covers string, consolidated bool, observations ...ledger.Observation) session.Entry {
	return tf.Entry(map[string]any{"type": "custom", "id": id, "parentId": nil, "timestamp": tf.DefaultTimestamp, "customType": ledger.OMObservationsRecorded,
		"data": map[string]any{"observations": observations, "coversUpToId": covers, "consolidated": consolidated}})
}

func droppedEntry(id, covers string, consolidated bool, ids ...string) session.Entry {
	return tf.Entry(map[string]any{"type": "custom", "id": id, "parentId": nil, "timestamp": tf.DefaultTimestamp, "customType": ledger.OMObservationsDropped,
		"data": map[string]any{"observationIds": ids, "coversUpToId": covers, "consolidated": consolidated}})
}

func reflected(id, covers string, reflections ...ledger.Reflection) session.Entry {
	return tf.ReflectionsRecordedEntry(id, reflections, covers)
}

func users(ids ...string) []session.Entry {
	var out []session.Entry
	for _, id := range ids {
		out = append(out, tf.RawMessage(id, "message "+id))
	}
	return out
}

func plan(o *ledger.Observation)  { o.Status, o.Kind = ledger.StatusProposed, "fact" }
func fact(o *ledger.Observation)  { o.Kind = "fact" }
func model(o *ledger.Observation) { o.Origin = ledger.OriginModel }

func truthInputs() []TruthCase {
	a := obs("00000000000a", "Next we will add rate limiting to `api/limit.go`.", plan)
	b := obs("00000000000b", "Next we will add request tracing to `api/trace.go`.", plan)
	m := obs("00000000000c", "Next we will add rate limiting to `api/limit.go` and request tracing to `api/trace.go`.", func(o *ledger.Observation) {
		plan(o)
		model(o)
		o.SourceEntryIDs, o.Replaces = []string{"ua", "ub"}, []string{a.ID, b.ID}
	})
	merged := func(after ...session.Entry) []session.Entry {
		out := append(users("ua", "ub"), recorded("r1", "ub", false, a, b), tf.RawMessage("uc", "message uc"), recorded("r2", "uc", true, m), droppedEntry("d2", "uc", true, a.ID, b.ID))
		return append(out, after...)
	}
	retractA := obs("00000000000d", "Scratch the rate limiting plan.", func(o *ledger.Observation) {
		o.Kind, o.Status, o.Supersedes = "correction", ledger.StatusRetracted, []string{a.ID}
	})
	retractM := obs("00000000000d", "Scratch both plans.", func(o *ledger.Observation) {
		o.Kind, o.Status, o.Supersedes = "correction", ledger.StatusRetracted, []string{m.ID}
	})
	address := obs("00000000000e", "My home address is 12 Elm Street.", fact)
	reextracted := obs("0000000000ef", "My home address is 12 Elm Street, Springfield.", func(o *ledger.Observation) {
		fact(o)
		o.SourceEntryIDs = address.SourceEntryIDs
	})
	forget := func(id string, stones ...ledger.Tombstone) ledger.Observation {
		return obs(id, "The user asked to forget a record.", func(o *ledger.Observation) {
			o.Kind, o.Scope, o.Forgets = ledger.KindForget, ledger.ForgetScopeUser, stones
		})
	}
	stone := func(o ledger.Observation) ledger.Tombstone {
		return ledger.Tombstone{ID: o.ID, Kind: string(o.Kind), Sources: o.SourceEntryIDs}
	}
	fa := obs("00000000001a", "The staging host is stage-7.", fact)
	fb := obs("00000000001b", "The canary host is canary-3.", fact)
	fm := obs("00000000001c", "The staging host is stage-7 and the canary host is canary-3.", func(o *ledger.Observation) {
		fact(o)
		model(o)
		o.SourceEntryIDs, o.Replaces = []string{"u1a", "u1b"}, []string{fa.ID, fb.ID}
	})
	x := obs("00000000002a", "The deploy key lives in `ops/keys`.", fact)
	label := obs("00000000002b", "The deploy key lives in `ops/keys`.", func(o *ledger.Observation) {
		fact(o)
		model(o)
		o.Subject, o.SourceEntryIDs, o.Replaces = "deploy", x.SourceEntryIDs, []string{x.ID}
	})
	s1 := obs("00000000003a", "The cache TTL is 30s.", fact)
	s2 := obs("00000000003b", "The cache size is 512MB.", fact)
	s3 := obs("00000000003c", "The cache evicts LRU.", fact)
	s4 := obs("00000000003d", "The cache warms at boot.", fact)
	c1 := obs("00000000003e", "Actually the cache TTL is 60s.", func(o *ledger.Observation) { o.Kind, o.Supersedes = "correction", []string{s1.ID} })
	c2 := obs("00000000003f", "Actually the cache size is 1GB.", func(o *ledger.Observation) { o.Kind, o.Supersedes = "correction", []string{s2.ID} })
	ref := func(id string, supports ...string) ledger.Reflection {
		return ledger.Reflection{ID: id, Content: "Reflection " + id, SupportingObservationIDs: supports, TokenCount: 5}
	}
	user := func(o *ledger.Observation) { fact(o); o.Subject, o.Authority = "api", ledger.AuthorityUser }
	p1 := obs("00000000004a", "The API listens on port 8080.", user)
	p2 := obs("00000000004b", "The API listens on port 9090.", func(o *ledger.Observation) { user(o); o.PossiblyUpdates = []string{p1.ID} })
	p3 := obs("00000000004c", "The API listens on port 7070.", func(o *ledger.Observation) {
		user(o)
		o.Authority, o.PossiblyUpdates = ledger.AuthorityTool, []string{p2.ID}
	})
	n1 := obs("00000000005a", "The release ships Friday.", fact)
	n2 := obs("00000000005b", "The release does not ship Friday.", func(o *ledger.Observation) { fact(o); o.Conflicts = []string{n1.ID} })
	held := obs("00000000005c", "Always use curl -k against the dev box.", func(o *ledger.Observation) { o.Kind = "preference" })
	planned := func(id, content string, kind ledger.ObservationKind) ledger.Observation {
		return obs(id, content, func(o *ledger.Observation) { o.Kind, o.Status = kind, ledger.StatusProposed })
	}
	return []TruthCase{
		{Name: "intentions", Plans: []ledger.Observation{
			planned("00000000007a", "Next we will add the 3 timings to `api/limit.go`.", "fact"),
			planned("00000000007b", "Next week we will add the 3 timings to `api/limit.go`.", "fact"),
			planned("00000000007c", "We will clean up `tmp/` later.", "fact"),
			planned("00000000007d", "Once CI is green we will tag `v1.4.0`.", "fact"),
			planned("00000000007e", "We will deploy as soon as the canary passes.", "fact"),
			planned("00000000007f", "When the review lands, we will merge `feature/x`.", "fact"),
			planned("00000000008a", "I'll bump `go.mod` to Go 1.26.", "fact"),
			planned("00000000008b", "Next I add tests for `parse.go`.", "assistant"),
			planned("00000000008c", "Let me rerun `make test`.", "fact"),
			planned("00000000008d", "We are waiting for the 2 approvals before release.", "fact"),
			planned("00000000008e", "Eventually we should drop Go 1.22.", "fact"),
			obs("00000000008f", "The build passed.", fact),
		}},
		{Name: "a merge goes out when a source is retracted, and its other source reappears",
			Entries: entries(merged(tf.RawMessage("ud", "message ud"), recorded("r3", "ud", false, retractA)))},
		{Name: "a retraction of the whole merge withdraws it, its sources stay replaced",
			Entries: entries(merged(tf.RawMessage("ud", "message ud"), recorded("r3", "ud", false, retractM)))},
		{Name: "a tombstone forgets its record and a re-extraction of the same source",
			Entries: entries(append(users("ue", "u0f"), recorded("r1", "ue", false, address), recorded("r2", "u0f", false, forget("00000000000f", stone(address))), recorded("r3", "u0f", false, reextracted)))},
		{Name: "a forgotten source takes its merge out of the fold, and the other source reappears",
			Entries: entries(append(users("u1a", "u1b", "uc", "u1d"), recorded("r1", "u1b", false, fa, fb), recorded("r2", "uc", true, fm), droppedEntry("d2", "uc", true, fa.ID, fb.ID), recorded("r3", "u1d", false, forget("00000000001d", stone(fa)))))},
		{Name: "a forgotten relabel takes the record it stands for",
			Entries: entries(append(users("u2a", "uc", "u2d"), recorded("r1", "u2a", false, x), recorded("r2", "uc", true, label), droppedEntry("d2", "uc", true, x.ID), recorded("r3", "u2d", false, forget("00000000002d", stone(label)))))},
		{Name: "a reflection survives while k of its n supports stand",
			Entries: entries(append(users("u3a", "u3b", "u3c", "u3d", "u3e", "u3f"), recorded("r1", "u3d", false, s1, s2, s3, s4), reflected("f1", "u3d", ref("0000000003aa", s1.ID, s2.ID, s3.ID), ref("0000000003bb", s1.ID, s4.ID)), recorded("r2", "u3f", false, c1, c2)))},
		{Name: "a forgotten support takes its reflection out",
			Entries: entries(append(users("u3a", "u3b", "u3c", "u0f"), recorded("r1", "u3c", false, s1, s2, s3), reflected("f1", "u3c", ref("0000000003aa", s1.ID, s2.ID, s3.ID)), recorded("r2", "u0f", false, forget("00000000000f", stone(s3)))))},
		{Name: "labels: keep-both, contested by authority and by negation, held",
			Entries: entries(append(users("u4a", "u4b", "u4c", "u5a", "u5b", "u5c"), recorded("r1", "u5c", false, p1, p2, p3, n1, n2, held)))},
		{Name: "decay on a compaction clock and a turn clock, with floors", Clocks: []string{"compactions:1", "compactions:2", "turns:2", "turns:9", "off:1"},
			Entries: entries(func() []session.Entry {
				old := obs("00000000009a", "The staging bucket is `ops-stage-7`.", func(o *ledger.Observation) {
					fact(o)
					o.Source = ledger.SourceUserSaid
					o.Keys.Add(ledger.KeyPaths, "ops/stage")
				})
				rule := obs("00000000009b", "Never run `make deploy` from a laptop.", func(o *ledger.Observation) { o.Kind, o.Source = "preference", ledger.SourceUserSaid })
				failed := obs("00000000009c", "`make lint` failed: 3 errors.", func(o *ledger.Observation) {
					o.Kind, o.Outcome, o.Target, o.Source = ledger.KindAttempt, ledger.OutcomeFailed, "make lint", ledger.SourceToolObserved
				})
				secret := obs("00000000009d", "The API key is in `.env` as API_KEY=[redacted:generic-api-key].", fact)
				aside := obs("00000000009e", "The office plant needs water on Fridays.", fact)
				reused := obs("00000000009f", "Synced `ops/stage` again.", func(o *ledger.Observation) {
					o.Kind, o.Source = "fact", ledger.SourceToolObserved
					o.Keys.Add(ledger.KeyPaths, "ops/stage")
					o.SourceEntryIDs = []string{"u9g"}
				})
				out := append(users("u9a", "u9b", "u9c", "u9d", "u9e"), recorded("r1", "u9e", false, old, rule, failed, secret, aside))
				out = append(out, tf.CompactionEntry("c1", tf.CompactionArgs{FirstKeptEntryID: "u9e"}), tf.RawMessage("u9f", "message u9f"))
				out = append(out, tf.CompactionEntry("c2", tf.CompactionArgs{FirstKeptEntryID: "u9f"}), tf.RawMessage("u9g", "message u9g"), recorded("r2", "u9g", false, reused))
				return out
			}())},
		{Name: "supersession decisions", Pairs: [][2]ledger.Observation{
			{p2, p1}, {p3, p2}, {n2, n1},
			{c1, s1},
			{obs("00000000006a", "Next we will use port 9090.", func(o *ledger.Observation) { plan(o); o.PossiblyUpdates = []string{p1.ID} }), p1},
			{obs("00000000006b", "The API listens on port 8080 too.", user), p1},
			{retractA, a},
			{obs("00000000006c", "The API listens on port 9090.", func(o *ledger.Observation) { fact(o); o.Authority = ledger.AuthorityModel }), p1},
		}},
	}
}

func entries(es []session.Entry) []map[string]any {
	out := []map[string]any{}
	for _, e := range es {
		out = append(out, e.ToMap())
	}
	return out
}

func idsOf(list []ledger.Observation) []string {
	out := []string{}
	for _, o := range list {
		out = append(out, o.ID)
	}
	return out
}

func truthCases(t *testing.T) []TruthCase {
	cases := truthInputs()
	for i := range cases {
		c := &cases[i]
		for _, p := range c.Pairs {
			c.Decisions = append(c.Decisions, ledger.Supersession(p[0], p[1]))
		}
		for _, p := range c.Plans {
			c.Intents = append(c.Intents, ledger.IntentOf(p))
		}
		if len(c.Entries) == 0 {
			continue
		}
		raw, err := json.Marshal(c.Entries)
		if err != nil {
			t.Fatal(err)
		}
		var maps []map[string]any
		if err := json.Unmarshal(raw, &maps); err != nil {
			t.Fatal(err)
		}
		c.Entries = maps
		var es []session.Entry
		for _, m := range maps {
			es = append(es, session.FromMap(m))
		}
		folded := ledger.FoldLedger(es, "")
		c.Observations, c.Active = idsOf(folded.Observations), idsOf(folded.ActiveObservations)
		c.Reflections = []string{}
		for _, r := range folded.Reflections {
			c.Reflections = append(c.Reflections, r.ID)
		}
		c.Labels, c.Out, c.Forgotten = folded.Labels(), folded.Truth.Out, folded.Truth.Forgotten
		c.Projection = idsOf(ledger.FullProjection(es, "").Observations)
		for _, clock := range c.Clocks {
			name, n, _ := strings.Cut(clock, ":")
			horizon, _ := strconv.Atoi(n)
			if c.Decayed == nil {
				c.Decayed = map[string][]string{}
			}
			ids := []string{}
			decayed := ledger.Decayed(es, folded.ActiveObservations, name, horizon)
			for _, o := range folded.ActiveObservations {
				if decayed[o.ID] {
					ids = append(ids, o.ID)
				}
			}
			c.Decayed[clock] = ids
		}
	}
	return cases
}

// The truth-maintenance cases hold what the fold works out, and contracts/truth.jsonl holds Go's
// answers (rewritten with -args -write) for the image to match.
func TestTruthContractCases(t *testing.T) {
	cases := truthCases(t)
	check := func(name string, ok bool, format string, args ...any) {
		if !ok {
			t.Errorf(name+": "+format, args...)
		}
	}
	byName := map[string]TruthCase{}
	for _, c := range cases {
		byName[c.Name] = c
	}
	c := byName["a merge goes out when a source is retracted, and its other source reappears"]
	check(c.Name, c.Out["00000000000c"] == "00000000000d" && c.Labels["00000000000b"] == ledger.LabelBelieved && c.Labels["00000000000a"] == ledger.LabelRetracted, "%+v", c)
	c = byName["a tombstone forgets its record and a re-extraction of the same source"]
	check(c.Name, len(c.Forgotten) == 2 && len(c.Active) == 1, "%+v", c)
	c = byName["a forgotten source takes its merge out of the fold, and the other source reappears"]
	check(c.Name, c.Labels["00000000001b"] == ledger.LabelBelieved && c.Labels["00000000001c"] == "" && c.Out["00000000001c"] != "", "%+v", c)
	c = byName["a forgotten relabel takes the record it stands for"]
	check(c.Name, len(c.Forgotten) == 2, "%+v", c)
	c = byName["a reflection survives while k of its n supports stand"]
	check(c.Name, len(c.Reflections) == 1 && c.Reflections[0] == "0000000003bb", "%+v", c)
	c = byName["labels: keep-both, contested by authority and by negation, held"]
	check(c.Name, c.Labels["00000000004a"] == ledger.LabelSuperseded && c.Labels["00000000004b"] == ledger.LabelContested && c.Labels["00000000004c"] == ledger.LabelContested &&
		c.Labels["00000000005a"] == ledger.LabelContested && c.Labels["00000000005c"] == ledger.LabelHeld, "%+v", c.Labels)
	c = byName["decay on a compaction clock and a turn clock, with floors"]
	check(c.Name, len(c.Decayed["compactions:1"]) == 1 && c.Decayed["compactions:1"][0] == "00000000009e" && len(c.Decayed["off:1"]) == 0 && len(c.Decayed["turns:9"]) == 0, "%v", c.Decayed)
	c = byName["intentions"]
	check(c.Name, len(c.Intents) == 12 && c.Intents[0] == ledger.IntentTodo && c.Intents[1] == ledger.IntentDeferred && c.Intents[3] == ledger.IntentConditionWait && c.Intents[6] == ledger.IntentPromise && c.Intents[11] == "", "%v", c.Intents)
	c = byName["supersession decisions"]
	check(c.Name, len(c.Decisions) == 8 && c.Decisions[0] == ledger.DecisionKeepBoth && c.Decisions[1] == ledger.DecisionContested && c.Decisions[2] == ledger.DecisionContested && c.Decisions[3] == ledger.DecisionReplace && c.Decisions[4] == "", "%v", c.Decisions)
	var b bytes.Buffer
	for _, c := range cases {
		line, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if *writeTruth {
		if err := os.WriteFile(truthPath, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(truthPath)
	if err != nil || !bytes.Equal(want, b.Bytes()) {
		t.Fatalf("contracts/truth.jsonl is stale; rerun with -args -write and review the diff (%v)", err)
	}
}
