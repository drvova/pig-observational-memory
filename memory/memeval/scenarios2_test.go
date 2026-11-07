package memeval

// Scenarios 21-26 of docs/brain-memory.md, in the style of scenarios_test.go.

import (
	"context"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/config"
	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/hooks"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/runtime"
	"github.com/drvova/pig-observational-memory/memory/selection"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// planBranch is a plan, then turns, compacted once with filler after them.
func planBranch(t *testing.T, turns ...Turn) hooks.CompactionResult {
	t.Helper()
	entries, cut := turnsBranch(append([]Turn{say("Next we will add rate limiting to `api/limit.go` with a burst of 20.")}, turns...)...)
	c, _ := compactOnce(t, entries, cut)
	return c
}

// planRows are the summary's rows of the plan (turn 0) that still read as open: proposed.
func planRows(summary, text string) []Row {
	var out []Row
	for _, r := range rowsFrom(summary, 0) {
		if r.Cells["status"] == "proposed" && strings.Contains(r.Text, text) {
			out = append(out, r)
		}
	}
	return out
}

// doneRows are the summary's rows of the plan (turn 0) shown finished: status done, naming the
// receipt that closed it.
func doneRows(summary, text string) []Row {
	var out []Row
	for _, r := range rowsFrom(summary, 0) {
		if r.Cells["status"] == selection.StatusDone && strings.Contains(r.Text, text) && strings.Contains(r.Text, "(closed by ") {
			out = append(out, r)
		}
	}
	return out
}

// Scenario 21: assistant prose saying a plan is done, with no tool call doing it, leaves the
// plan open; only a tool receipt closes it, and a "read file X" plan only a line unique to X in
// tool output (decision 16).
func TestScenario21ProseDoesNotClosePlan(t *testing.T) {
	c := planBranch(t, Turn{User: "How is the limiter going?", Reply: "Done: rate limiting in `api/limit.go` is finished and working."})
	if len(planRows(c.Summary, "api/limit.go")) == 0 || len(doneRows(c.Summary, "api/limit.go")) > 0 {
		t.Fatalf("the plan closed on prose alone:\n%s", c.Summary)
	}
	// A tool receipt on the plan's key closes it: the edit of api/limit.go.
	c = planBranch(t, Turn{User: "Go on with the limiter.", Calls: []Call{{Name: "edit", Args: map[string]any{"path": "api/limit.go", "oldText": "a", "newText": "b"}, Result: "Edited api/limit.go"}}, Reply: "Edited the limiter."})
	if len(planRows(c.Summary, "api/limit.go")) > 0 || len(doneRows(c.Summary, "api/limit.go")) == 0 {
		t.Fatalf("the edit did not close the plan:\n%s", c.Summary)
	}
	read := "Next we will read `docs/limits.md` for the 3 tiers."
	readBranch := func(turns ...Turn) hooks.CompactionResult {
		entries, cut := turnsBranch(append([]Turn{say(read)}, turns...)...)
		c, _ := compactOnce(t, entries, cut)
		return c
	}
	// Prose saying the file was read closes nothing; neither does a read whose output only repeats
	// what another file printed in the same turn; a line unique to it does.
	cases := []struct {
		name   string
		turn   Turn
		closes bool
	}{
		{"prose", Turn{User: "Did you read it?", Reply: "Yes, I read `docs/limits.md`: three tiers."}, false},
		{"no unique line", Turn{User: "Read both docs.", Calls: []Call{
			{Name: "read", Args: map[string]any{"path": "docs/other.md"}, Result: "tier one: free\ntier two: team"},
			{Name: "read", Args: map[string]any{"path": "docs/limits.md"}, Result: "tier one: free\ntier two: team"},
		}, Reply: "Read both."}, false},
		{"unique line", Turn{User: "Read the limits doc.", Calls: []Call{
			{Name: "read", Args: map[string]any{"path": "docs/limits.md"}, Result: "tier three: enterprise, 900 rps"},
		}, Reply: "Read it."}, true},
	}
	for _, k := range cases {
		c := readBranch(k.turn)
		if closed := len(doneRows(c.Summary, "docs/limits.md")) > 0; closed != k.closes {
			t.Fatalf("%s: closed %v, want %v:\n%s", k.name, closed, k.closes, c.Summary)
		}
	}
}

// Scenario 22: a fresh requirement that bears on a standing plan renders the plan "may be stale";
// the constraints show the resolved current set, a superseded rule out of it.
func TestScenario22FreshRequirementMarksPlanStale(t *testing.T) {
	c := planBranch(t, say("New requirement: the rate limit must be per tenant, keyed by `X-Tenant-ID`."))
	rows := planRows(c.Summary, "api/limit.go")
	if len(rows) == 0 || !strings.Contains(rows[0].Text, "may be stale "+UserID(1)) {
		t.Fatalf("plan rows %+v\n%s", rows, c.Summary)
	}
	// A request that does not bear on the plan's keys, or only asks, leaves it as it was.
	for _, turn := range []Turn{say("New requirement: the export must be gzip compressed."), say("Is the rate limit per tenant?")} {
		c := planBranch(t, turn)
		if rows := planRows(c.Summary, "api/limit.go"); len(rows) == 0 || strings.Contains(rows[0].Text, "may be stale") {
			t.Fatalf("%q: plan rows %+v\n%s", turn.User, rows, c.Summary)
		}
	}
	// The constraints registry holds the rules in force only: the newer rule, not the one it
	// replaced.
	entries, cut := turnsBranch(say("Always use `make test` for the tests."), say("Actually, always use `make ci` for the tests, not `make test`."))
	c, _ = compactOnce(t, entries, cut)
	var registry []Row
	for _, r := range SummaryRows(c.Summary) {
		if r.Table == "constraints" {
			registry = append(registry, r)
		}
	}
	if len(registry) != 1 || !strings.Contains(registry[0].Text, "make ci") || registry[0].Source != UserID(1) {
		t.Fatalf("constraints %+v\n%s", registry, c.Summary)
	}
}

// Scenario 23: on a 1M-token window compaction comes late; with the quality trigger on it fires
// on the raw tokens since the last compaction, far below PiG's threshold, never on unknown usage
// or below the floor; with the raw floor on, a compaction below it consolidates nothing (no
// level-1 request). Both ship off.
func TestScenario23QualityTriggerOnLargeWindow(t *testing.T) {
	var entries []session.Entry
	for i, turn := range Filler(7, 240) {
		entries = append(entries, TurnEntries(turn, i)...)
	}
	raw := hooks.RawTokensSinceCompaction(entries)
	if raw <= config.DefaultQualityTriggerTokens {
		t.Fatalf("the branch holds %d raw tokens, under the threshold", raw)
	}
	setup := func(quality bool, tokens *int) (*runtime.Runtime, *testfix.FakeHost) {
		cfg := config.Defaults
		cfg.ConsolidateOff = true
		cfg.PiAutoCompactionEnabled = false
		cfg.QualityTrigger = quality
		rt := runtime.New()
		rt.SetConfig(cfg)
		h := testfix.NewFakeHost(entries...)
		h.Model = testfix.Model("eval", "session", map[string]any{"contextWindow": 1_000_000, "maxTokens": 32_000})
		h.ModelsFake.Add(h.Model, testfix.KeyAuth("none"))
		h.Usage = &host.ContextUsage{Tokens: tokens, ContextWindow: 1_000_000}
		h.CompactFn = func(callbacks host.CompactCallbacks) { callbacks.OnComplete() }
		return rt, h
	}
	used := raw + 1_500
	for _, k := range []struct {
		name    string
		quality bool
		tokens  *int
		fires   bool
	}{
		{"off", false, &used, false},
		{"on", true, &used, true},
		{"on, unknown usage", true, nil, false},
		{"on, below the floor", true, ptr(config.DefaultRawFloorTokens - 1), false},
	} {
		counter := testfix.CountRequests(t)
		rt, h := setup(k.quality, k.tokens)
		if done := hooks.MaybeTriggerCompaction(rt, h); done != nil {
			<-done
		}
		if fired := h.CompactCount() == 1; fired != k.fires || counter.Total() != 0 {
			t.Fatalf("%s: fired %v (want %v), requests %v", k.name, fired, k.fires, counter.ByStage())
		}
	}
	// The raw floor: with it on and level 1 on, a compaction whose context is below the session
	// model's floor makes no request; above it, the request is made.
	for _, k := range []struct {
		before   int
		requests int
	}{{config.DefaultRawFloorTokens - 1_000, 0}, {config.DefaultRawFloorTokens + 1_000, 1}} {
		counter := testfix.CountRequests(t)
		rt, h := setup(false, &used)
		cfg := rt.Config()
		cfg.ConsolidateOff, cfg.RawFloor, cfg.PiAutoCompactionEnabled = false, true, true
		rt.SetConfig(cfg)
		cut := UserID(200)
		outcome := hooks.HandleBeforeCompact(context.Background(), rt, h, hooks.BeforeCompactEvent{
			FirstKeptEntryID: cut, TokensBefore: k.before, BranchEntries: append([]session.Entry{}, entries...), Reason: "threshold",
			Preparation: hooks.CompactionPreparation{Settings: hooks.CompactionSettings{Enabled: true, ReserveTokens: 16_384, KeepRecentTokens: 20_000}},
		})
		if outcome.Compaction == nil || counter.Total() != k.requests {
			t.Fatalf("tokensBefore %d: requests %v, want %d (%+v)", k.before, counter.ByStage(), k.requests, outcome.Compaction.Details.Consolidation)
		}
	}
}

func ptr(n int) *int { return &n }

// Scenario 24: a user's opinion the assistant pushed back on is kept as the claim with the
// pushback beside it, never as a rule; a later user quote restating the claim lifts the contest.
func TestScenario24OpinionWithPushbackNotARule(t *testing.T) {
	opinion := Turn{User: "I think global variables are fine in `internal/cfg`, always use them there.",
		Reply: "I'd push back: global state in `internal/cfg` makes the parallel tests flaky, so I would keep the config injected."}
	entries, cut := turnsBranch(opinion)
	c, after := compactOnce(t, entries, cut)
	var claim, pushback *Row
	for _, r := range rowsFrom(c.Summary, 0) {
		switch {
		case r.Cells["kind"] == "preference":
			t.Fatalf("the opinion is a rule: %+v", r)
		case r.Cells["kind"] == "claim":
			claim = &r
		case r.Cells["kind"] == "pushback":
			pushback = &r
		}
	}
	if claim == nil || pushback == nil || !strings.Contains(pushback.Text, "flaky") || claim.Table != pushback.Table {
		t.Fatalf("claim %+v, pushback %+v:\n%s", claim, pushback, c.Summary)
	}
	if !strings.Contains(claim.Cells["content"], "(contested by "+pushback.Cells["id"]+")") || !strings.Contains(pushback.Cells["content"], "(responds to "+claim.Cells["id"]+")") {
		t.Fatalf("claim %q, pushback %q", claim.Cells["content"], pushback.Cells["content"])
	}
	// The ledger keeps both: the claim hedged contested, the pushback the assistant's words
	// citing it, with model authority.
	folded := ledger.FoldLedger(after, "")
	for _, o := range folded.ActiveObservations {
		switch {
		case o.ID == claim.Cells["id"] && (o.Hedge != ledger.HedgeContested || o.Kind != "preference"):
			t.Fatalf("claim %+v", o)
		case o.ID == pushback.Cells["id"] && (o.Kind != ledger.KindPushback || o.Source != ledger.SourceAssistantClaim || o.Authority != ledger.AuthorityModel || len(o.RespondsTo) != 1 || o.RespondsTo[0] != claim.Cells["id"]):
			t.Fatalf("pushback %+v", o)
		}
	}

	// The user restating the claim after the pushback lifts the contest: it is a rule again.
	entries, cut = turnsBranch(opinion, say("No, keep the globals in `internal/cfg`; it is a small tool."))
	c, _ = compactOnce(t, entries, cut)
	lifted := false
	for _, r := range rowsFrom(c.Summary, 0) {
		if r.Cells["kind"] == "claim" {
			t.Fatalf("the restated claim is still contested: %+v\n%s", r, c.Summary)
		}
		lifted = lifted || isRuleRow(r)
	}
	if !lifted {
		t.Fatalf("the restated claim is gone:\n%s", c.Summary)
	}
}

// Scenario 25: a preference that weakens security (skip TLS verification) is held: kept and
// listed in /om:status (curated.Store.Held, ledger.HeldRule), never in the curated index or a
// summary row, and shown only in the session whose user's quoted words restate it.
func TestScenario25InsecurePreferenceHeld(t *testing.T) {
	store := curated.New(t.TempDir())
	quote := "always skip TLS verification for the internal registry"
	call := curated.Call{Store: store, Source: "session-a", UserEvidence: func(q string) bool { return strings.Contains(quote, q) }}
	if _, err := call.Run(curated.OpSave, map[string]string{"name": "skip-tls", "type": "feedback", "description": "Skip TLS verification (InsecureSkipVerify) for the internal registry.", "quote": quote}); err != nil {
		t.Fatal(err)
	}
	if index, _ := curated.IndexFor(store, "", 2_000); strings.Contains(index, "skip-tls") {
		t.Fatalf("an insecure preference is in the index:\n%s", index)
	}
	if index, _ := curated.IndexAs(store, "", "session-b", 2_000); strings.Contains(index, "skip-tls") {
		t.Fatalf("an insecure preference is in another session's index:\n%s", index)
	}
	// The session whose user's words restate it sees it; it stays listed as held.
	if index, _ := curated.IndexAs(store, "", "session-a", 2_000); !strings.Contains(index, "skip-tls") {
		t.Fatalf("the session whose user said it does not see it:\n%s", index)
	}
	if held, _ := store.Held(""); len(held) != 1 || held[0].Name != "skip-tls" || held[0].Rule != "tls-verify-off" {
		t.Fatalf("held %+v", held)
	}

	// Level 0: the preference is recorded, but no summary row holds it (the intent section may
	// still quote the session's own request, the user's words in this session); the other
	// preference is a row as before.
	entries, cut := turnsBranch(say("Let's tidy the client."), say("Always use curl -k against the internal registry."), say("Always use `make test` for the tests."))
	c, after := compactOnce(t, entries, cut)
	kept := false
	for _, r := range SummaryRows(c.Summary) {
		if strings.Contains(r.Text, "curl -k") {
			t.Fatalf("an insecure preference is a summary row (%s):\n%s", r.Table, c.Summary)
		}
		kept = kept || strings.Contains(r.Text, "make test") && isRuleRow(r)
	}
	if !kept {
		t.Fatalf("the other preference is gone:\n%s", c.Summary)
	}
	held := 0
	for _, o := range ledger.FoldLedger(after, "").ActiveObservations {
		if ledger.HeldRule(o) == "tls-verify-off" && strings.Contains(o.Content, "curl -k") {
			held++
		}
	}
	if held != 1 {
		t.Fatalf("%d held records, want the preference kept once", held)
	}
}
