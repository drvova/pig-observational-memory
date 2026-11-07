package memeval

import (
	"context"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/core"
	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/engine"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// coreSession runs turns, then filler, as session id in cwd on the store under home until one
// compaction (on host h when given, continuing its branch).
func coreSession(t *testing.T, home, cwd, id string, h *testfix.FakeHost, turns ...Turn) *testfix.FakeHost {
	t.Helper()
	if h == nil {
		h = testfix.NewFakeHost()
	}
	script := Script{Seed: 7, Turns: append(append([]Turn{}, turns...), Filler(7, 200)...)}
	run, err := SimulateWith(context.Background(), script, SmallWindow, 1, Sim{Home: home, Cwd: cwd, Session: id, Host: h})
	if err != nil || len(run.Snapshots) != 1 {
		t.Fatalf("%d compactions (%v)", len(run.Snapshots), err)
	}
	return h
}

// startContext is the start context session id in cwd is given (engine.Index): the core's block
// first, then the curated index.
func startContext(t *testing.T, home, cwd, id, model string) string {
	t.Helper()
	h := testfix.NewFakeHost()
	h.Home, h.ID, h.WorkDir = home, id, cwd
	h.Model = testfix.Model("eval", model, map[string]any{"contextWindow": 200_000, "maxTokens": 8_000})
	var pin curated.Pin
	index, on := engine.Index(h, &pin)
	if !on {
		t.Fatal("curated memory is off")
	}
	return index
}

// Decision 16 and phase 5's tombstones across sessions: the user's constraint and an open
// intention of session A show first in session B's start context, before the curated index; a
// one-time permission never becomes a standing rule (scenario 29); a forget session A's user
// made later is a tombstone session B holds.
func TestCoreDeliveryAcrossSessions(t *testing.T) {
	home := t.TempDir()
	cwd := "/work/app"
	rule := "Never deploy from `hotfix/` branches."
	plan := "Next we will add the 3 retry timings to `api/limit.go`."
	permission := "You can push to `release` this once."
	// Session A states them, compacts, and later its user forgets the rule; session B starts
	// between the two (Between, just before the forget's turn).
	forgetAt := 20
	turns := append([]Turn{say(rule), say(plan), say(permission)}, Filler(7, forgetAt-3)...)
	turns = append(append(turns, say("Forget what I said about hotfix branches.")), Filler(8, 200)...)
	h := testfix.NewFakeHost()
	b := ""
	run, err := SimulateWith(context.Background(), Script{Seed: 7, Turns: turns}, SmallWindow, 2, Sim{Home: home, Cwd: cwd, Session: "a", Host: h,
		Between: func(i int, _ []session.Entry) {
			if i == forgetAt-1 {
				b = startContext(t, home, cwd, "b", "other")
			}
		}})
	if err != nil || len(run.Snapshots) != 2 || run.Snapshots[0].Turn >= forgetAt || run.Snapshots[1].Turn <= forgetAt {
		t.Fatalf("compactions %d at %d, %d (%v)", len(run.Snapshots), run.Snapshots[0].Turn, run.Snapshots[len(run.Snapshots)-1].Turn, err)
	}
	if !strings.HasPrefix(b, "## Shared memory") || !strings.Contains(b, rule) || !strings.Contains(b, plan) {
		t.Fatalf("session B's start context lacks A's constraint or intention:\n%s", b)
	}
	if strings.Index(b, rule) > strings.Index(b, "## Curated memory") {
		t.Fatalf("the core's block is not first:\n%s", b)
	}
	if strings.Contains(b, "`release` this once") {
		t.Fatalf("a one-time permission became a standing rule:\n%s", b)
	}
	// Another project sees neither.
	if other := startContext(t, home, "/work/other", "c", "session"); strings.Contains(other, rule) || strings.Contains(other, plan) {
		t.Fatalf("another project's start context holds A's items:\n%s", other)
	}
	// The same model family carries no hint; another family is told who wrote it.
	if !strings.Contains(b, "written by a session model") {
		t.Fatalf("no per-model hint:\n%s", b)
	}
	if same := startContext(t, home, cwd, "d", "session"); strings.Contains(same, "written by") {
		t.Fatalf("a hint for the writer's own family:\n%s", same)
	}
	// Session A's user forgot the rule after: the tombstone holds in every later session.
	if later := startContext(t, home, cwd, "e", "other"); strings.Contains(later, "hotfix") {
		t.Fatalf("a forgotten rule is still shown:\n%s", later)
	}
	st, err := curated.New(curated.DefaultDir(home)).Core().Read()
	if err != nil || len(st.Tombstoned) == 0 {
		t.Fatalf("no tombstone in the core (%v)", err)
	}
}

// Scenario 29: a one-time permission is kept with its occasion, never as a standing rule: no core
// item and no rule in the next session.
func TestScenario29OneTimePermissionNotStanding(t *testing.T) {
	home := t.TempDir()
	for _, p := range []string{
		"You can push to `main` this once.",
		"Just this once, skip the `make lint` step.",
		"For this task you may edit `vendor/` directly.",
	} {
		coreSession(t, home, "/work/app", "a", nil, say(p))
	}
	st, err := curated.New(curated.DefaultDir(home)).Core().Read()
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range st.Items {
		if it.Kind == core.KindRule {
			t.Fatalf("a one-time permission is a standing core rule: %+v", it)
		}
	}
	if b := startContext(t, home, "/work/app", "b", "session"); strings.Contains(b, "this once") || strings.Contains(b, "For this task") {
		t.Fatalf("the next session is given it as a rule:\n%s", b)
	}
	// Offered to the core directly, it is refused.
	_, verdicts, _ := curated.New(curated.DefaultDir(home)).Core().PromoteAll([]core.Candidate{{Kind: core.KindRule, Text: "You can push to `main` this once.", Scope: core.ScopeUser, Via: core.ViaQuote, OneTime: true}})
	if verdicts[0] != core.VerdictOneTime {
		t.Fatal(verdicts)
	}
}
