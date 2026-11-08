// Package memeval is the Tier 0 retention harness (docs/brain-memory.md, phase 0; the baseline
// is docs/brain-memory-baseline.md): deterministic synthetic coding transcripts generated from
// a seeded fact script, with probes planted by class, run through the real engine at level 0
// (ConsolidateOff) across many compactions in a small window, with zero model requests. Each
// probe is classified after each compaction as shown in the summary, reachable through a
// snippet the summary prints (run in a namespace child), or lost; hard invariants are checked
// on every compaction.
package memeval

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

// Class is a probe's class.
type Class string

// The probe classes the report names.
const (
	ClassIdentifier Class = "identifier"
	ClassError      Class = "error-text"
	ClassConstraint Class = "constraint"
	ClassExclusion  Class = "scope-exclusion"
	ClassDecision   Class = "decision"
	ClassIntention  Class = "intention"
	ClassSuperseded Class = "superseded"
)

// Classes is every class, in report order.
var Classes = []Class{ClassIdentifier, ClassError, ClassConstraint, ClassExclusion, ClassDecision, ClassIntention, ClassSuperseded}

// Probe is one planted fact: Needles must all appear on one summary line (or in one snippet's
// output) for it to count; for a superseded value, Needles is the current value and Old the
// value it replaced, which must never be shown as current.
type Probe struct {
	Name    string
	Class   Class
	Turn    int
	Needles []string
	// Old is the superseded value (ClassSuperseded only); OldTurn the turn it was stated at.
	Old     string
	OldTurn int
	// Subject is the words naming what the superseded value is of.
	Subject string
}

// Call is one tool call of an assistant turn and its result.
type Call struct {
	Name    string
	Args    map[string]any
	Result  string
	IsError bool
	// Note is the assistant text before the call.
	Note string
}

// Turn is one exchange: the user's message, the assistant's tool calls and its final reply.
type Turn struct {
	User  string
	Calls []Call
	Reply string
	// Custom are custom messages (an extension's or another harness's text, never the user's)
	// placed after the user's message; the Tier 0 generator writes none.
	Custom []string
	// Zeno are the calls the Zeno arm's agent adds after Calls (zeno.go): every path and command
	// the previous summary showed, read or run again. Their entries are "z"-prefixed, so the
	// script's own entries keep their ids.
	Zeno []Call
}

// Script is a generated session: the turns and the probes planted in them.
type Script struct {
	Seed   int64
	Turns  []Turn
	Probes []Probe
}

// Options shape a script.
type Options struct {
	// Turns is how many turns the script has.
	Turns int
	// Rename draws every planted name (identifiers, paths, commands, constraint names) from a
	// second stream, so a run with Rename differs from one without only in those names.
	Rename bool
	// PlantEvery repeats the probe set every that many turns after the first (0: once).
	PlantEvery int
}

// Base is the first turn's time; each turn is four minutes later.
var Base = time.Date(2026, 5, 4, 9, 0, 0, 0, time.UTC)

// TurnTime is turn i's timestamp.
func TurnTime(i int) time.Time { return Base.Add(time.Duration(i) * 4 * time.Minute) }

var (
	verbs      = []string{"retry", "parse", "load", "flush", "resolve", "decode", "merge", "probe", "scan", "batch"}
	nouns      = []string{"Backoff", "Config", "Ledger", "Shard", "Token", "Header", "Window", "Bucket", "Lease", "Cursor"}
	dirs       = []string{"net", "store", "auth", "billing", "search", "queue", "cache", "api", "sched", "wire"}
	tableNames = []string{"users", "orders", "invoices", "tenants", "sessions", "keys", "events", "carts"}
	columns    = []string{"email", "slug", "handle", "token", "number", "external_id"}
	makeTgts   = []string{"test-unit", "check", "verify", "test-all", "ci-local", "test-fast"}
	banned     = []string{"cargo test", "go test ./...", "npm test", "pytest", "bazel test //...", "tox"}
	fenceRoots = []string{"db", "deploy", "vendor", "web", "tools", "infra"}
	fenceLeafs = []string{"migrations", "legacy", "generated", "schema", "static", "proto"}
	libsA      = []string{"sqlc", "pgx", "zerolog", "chi", "ristretto", "goose"}
	libsB      = []string{"gorm", "database/sql", "logrus", "gin", "bigcache", "atlas"}
	reasons    = []string{"the queries need compile-time checks", "it avoids reflection on the hot path", "its allocations stay flat under load", "it keeps the dependency tree small", "the team already reviews its output"}
	features   = []string{"rate limiting", "request tracing", "a circuit breaker", "pagination", "idempotency keys", "graceful shutdown"}
	envs       = []string{"staging", "canary", "preview", "dev"}
	services   = []string{"database", "billing worker", "search proxy", "metrics gateway", "auth sidecar", "queue broker"}
	words      = strings.Fields("handler request response latency cache shard replica commit branch review lint module package import vendor timeout warning trace span metric gauge counter histogram worker pool buffer channel mutex")
)

// names draws the names a probe set uses from rng.
type names struct {
	ident, identFile, constraintName, table, makeTarget, bannedCmd, fence, libA, libB, reason, feature, featureFile, service string
	attempts, baseMs, burst, oldPort, newPort                                                                                int
}

// drawNames draws one probe set's names; used holds every earlier set's, so no two sets of a
// script share a statement (the engine would rightly keep only the first of two identical ones).
func drawNames(rng *rand.Rand, used map[string]bool) names {
	pick := func(list []string) string { return list[rng.Intn(len(list))] }
	uniq := func(class string, draw func() string) string {
		for {
			if v := draw(); !used[class+v] {
				used[class+v] = true
				return v
			}
		}
	}
	n := names{}
	var w string
	n.ident = uniq("ident", func() string {
		w = pick(nouns)
		return fmt.Sprintf("%sWith%s%d", pick(verbs), w, 10+rng.Intn(90))
	})
	n.identFile = fmt.Sprintf("internal/%s/%s.go", pick(dirs), strings.ToLower(w))
	n.attempts, n.baseMs = 3+rng.Intn(7), 50*(1+rng.Intn(9))
	n.constraintName = uniq("error", func() string {
		n.table = pick(tableNames)
		return fmt.Sprintf("%s_%s_key", n.table, pick(columns))
	})
	uniq("constraint", func() string {
		n.makeTarget, n.bannedCmd = pick(makeTgts), pick(banned)
		return n.makeTarget + "|" + n.bannedCmd
	})
	n.fence = uniq("fence", func() string { return pick(fenceRoots) + "/" + pick(fenceLeafs) + "/" })
	uniq("decision", func() string {
		i := rng.Intn(len(libsA))
		n.libA, n.libB, n.reason = libsA[i], libsB[i], pick(reasons)
		return n.libA + "|" + n.reason
	})
	n.featureFile = uniq("feature", func() string {
		n.feature = pick(features)
		f := strings.Fields(n.feature)
		return fmt.Sprintf("internal/%s/%s.go", pick(dirs), f[len(f)-1])
	})
	n.burst = 10 + rng.Intn(90)
	n.service = uniq("service", func() string { return pick(envs) + " " + pick(services) })
	n.oldPort = 5000 + rng.Intn(1900)
	n.newPort = n.oldPort + 1 + rng.Intn(50)
	return n
}

// Generate is the script for seed: probes planted in the first turns (and every PlantEvery
// turns), the rest coding filler whose specifics compete for the summary budget, with
// distractors that share words with the probes.
func Generate(seed int64, opt Options) Script {
	if opt.Turns <= 0 {
		opt.Turns = 200
	}
	filler := rand.New(rand.NewSource(seed))
	nameSeed := seed*7919 + 17
	if opt.Rename {
		nameSeed = seed*104729 + 31
	}
	nameRng := rand.New(rand.NewSource(nameSeed))
	s := Script{Seed: seed}
	pending := map[int]Turn{}
	used := map[string]bool{}
	plant := func(start, round int) {
		n := drawNames(nameRng, used)
		suffix := ""
		if round > 0 {
			suffix = fmt.Sprintf("#%d", round)
		}
		add := func(turn int, t Turn, p *Probe) {
			pending[turn] = t
			if p != nil {
				p.Turn = turn
				p.Name = string(p.Class) + suffix
				s.Probes = append(s.Probes, *p)
			}
		}
		ident := fmt.Sprintf("%s(maxAttempts=%d, base=%dms)", n.ident, n.attempts, n.baseMs)
		add(start, Turn{
			User:  fmt.Sprintf("Never run `%s` directly here; always use `make %s` instead.", n.bannedCmd, n.makeTarget),
			Reply: "Understood, I will use the make target.",
		}, &Probe{Class: ClassConstraint, Needles: []string{"make " + n.makeTarget}})
		add(start+1, Turn{
			User:  fmt.Sprintf("Don't touch `%s` while you work on this.", n.fence),
			Reply: "I will leave that directory alone.",
		}, &Probe{Class: ClassExclusion, Needles: []string{n.fence}})
		add(start+2, Turn{
			User: fmt.Sprintf("In `%s`, add `%s` and call it from the client.", n.identFile, ident),
			Calls: []Call{
				{Name: "read", Args: map[string]any{"path": n.identFile}, Result: fillerOutput(filler, 6), Note: "Reading the file before editing it."},
				{Name: "edit", Args: map[string]any{"path": n.identFile, "oldText": "func client() {", "newText": "func client() {\n\t" + n.ident + "()"}, Result: "Edited " + n.identFile},
			},
			Reply: fmt.Sprintf("Added `%s` to `%s`.", ident, n.identFile),
		}, &Probe{Class: ClassIdentifier, Needles: []string{ident}})
		errText := fmt.Sprintf("pq: duplicate key value violates unique constraint \"%s\"", n.constraintName)
		add(start+3, Turn{
			User: fmt.Sprintf("Run the %s store tests with `make %s`.", n.table, n.makeTarget),
			Calls: []Call{{Name: "bash", Args: map[string]any{"command": "make " + n.makeTarget + " PKG=./internal/store/" + n.table}, IsError: true,
				Result: fmt.Sprintf("--- FAIL: TestInsert%s (0.02s)\n    %s_test.go:42: %s\nFAIL\nCommand exited with code 2", upperFirst(n.table), n.table, errText), Note: "Running the store tests through the make target."}},
			Reply: fmt.Sprintf("The insert test fails: %s.", errText),
		}, &Probe{Class: ClassError, Needles: []string{errText}})
		add(start+4, Turn{
			User:  fmt.Sprintf("We decided to use %s over %s because %s.", n.libA, n.libB, n.reason),
			Reply: "Noted, I will build on that.",
		}, &Probe{Class: ClassDecision, Needles: []string{n.libA, n.reason}})
		add(start+5, Turn{
			User:  fmt.Sprintf("The %s listens on port %d.", n.service, n.oldPort),
			Reply: "Got it.",
		}, nil)
		add(start+6, Turn{
			User:  fmt.Sprintf("Next we will add %s to `%s` with a burst of %d.", n.feature, n.featureFile, n.burst),
			Reply: "I will start on that after the current fix.",
		}, &Probe{Class: ClassIntention, Needles: []string{n.featureFile}})
		add(start+9, Turn{
			User:  fmt.Sprintf("Actually, the %s port is %d, not %d.", n.service, n.newPort, n.oldPort),
			Reply: "Thanks for the correction.",
		}, &Probe{Class: ClassSuperseded, Needles: []string{fmt.Sprint(n.newPort)}, Old: fmt.Sprint(n.oldPort), OldTurn: start + 5, Subject: n.service})
	}
	plant(0, 0)
	if opt.PlantEvery > 0 {
		for round, start := 1, opt.PlantEvery; start+10 < opt.Turns; round, start = round+1, start+opt.PlantEvery {
			plant(start, round)
		}
	}
	for i := 0; i < opt.Turns; i++ {
		if t, ok := pending[i]; ok {
			s.Turns = append(s.Turns, t)
			continue
		}
		s.Turns = append(s.Turns, fillerTurn(filler, i))
	}
	return s
}

// fillerTurn is an ordinary coding exchange: a request naming a file, a read, an edit and a
// test run, sometimes a distractor statement with a port or a path.
func fillerTurn(rng *rand.Rand, i int) Turn {
	d, w := dirs[rng.Intn(len(dirs))], words[rng.Intn(len(words))]
	file := fmt.Sprintf("internal/%s/%s.go", d, w)
	var user string
	switch rng.Intn(5) {
	case 0:
		user = fmt.Sprintf("Fix the flaky test in `internal/%s/%s_test.go`.", d, w)
	case 1:
		user = fmt.Sprintf("The %s timeout should be %dms in `%s`.", w, 100*(1+rng.Intn(30)), file)
	case 2:
		user = fmt.Sprintf("Rename the %s field in `%s` to %s%d.", w, file, words[rng.Intn(len(words))], rng.Intn(100))
	case 3:
		user = fmt.Sprintf("The %s service runs on port %d in the dev cluster.", w, 7000+rng.Intn(2000))
	default:
		user = fmt.Sprintf("Add a %s metric to `%s` and log it at debug level.", w, file)
	}
	pkg := "./internal/" + d + "/..."
	return Turn{
		User: user,
		Calls: []Call{
			{Name: "read", Args: map[string]any{"path": file}, Result: fillerOutput(rng, 20+rng.Intn(20)), Note: "Reading the file first."},
			{Name: "edit", Args: map[string]any{"path": file, "oldText": w, "newText": w + "V2"}, Result: "Edited " + file},
			{Name: "bash", Args: map[string]any{"command": "go vet " + pkg}, Result: fillerOutput(rng, 10+rng.Intn(20))},
		},
		Reply: fmt.Sprintf("Updated `%s`; vet is clean for %s.", file, pkg),
	}
}

// fillerOutput is lines of tool output.
func fillerOutput(rng *rand.Rand, lines int) string {
	var b strings.Builder
	for i := 0; i < lines; i++ {
		fmt.Fprintf(&b, "%s.go:%d: %s %s %s %s\n", words[rng.Intn(len(words))], 1+rng.Intn(900), words[rng.Intn(len(words))], words[rng.Intn(len(words))], words[rng.Intn(len(words))], words[rng.Intn(len(words))])
	}
	return strings.TrimRight(b.String(), "\n")
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Filler is n filler turns from seed: ordinary coding exchanges, no probes. Scenario tests put
// their own turns before them.
func Filler(seed int64, n int) []Turn {
	rng := rand.New(rand.NewSource(seed))
	out := make([]Turn, n)
	for i := range out {
		out[i] = fillerTurn(rng, i)
	}
	return out
}

// say is a turn with only the user's message and a short reply.
func say(user string) Turn { return Turn{User: user, Reply: "Understood."} }
