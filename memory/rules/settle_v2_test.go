package rules

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/agents"
	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/ledger"
)

// settleLedger builds compaction passes for SettleV2 by hand, one minute apart from base.
type settleLedger struct {
	base   time.Time
	events []curated.ModelEvent
	root   int
}

func (l *settleLedger) pass(failures, harm int, ranHeld []string, mutate ...func(*curated.ModelEvent)) {
	l.root++
	h := harm
	e := curated.ModelEvent{Time: l.base.Add(time.Duration(len(l.events)+1) * time.Minute).Format(time.RFC3339Nano), Model: "p/m", Kind: curated.ModelKindPass,
		Stage: StageCompaction, Engine: curated.EngineV2, Tokens: 100, Level1: true, RanHeld: ranHeld, Harm: &h, Turns: 4,
		Roots: []string{fmt.Sprintf("%016x", l.root)}, Repo: "r1", Family: ".go",
		Counts: map[string]int{DetectUncitedAssistantSpecifics: failures, CountLevel1Refused: failures}}
	for _, m := range mutate {
		m(&e)
	}
	l.events = append(l.events, e)
}

func (l *settleLedger) controls(failures ...int) {
	for _, f := range failures {
		l.pass(f, 0, nil)
	}
}

func heldAt(kind, name, text string, at time.Time) layer.Definition {
	d := layer.Definition{Kind: kind, Name: name, Model: "p/m", Text: text, Time: at, Engine: curated.EngineV2, State: layer.StateHeld}
	if kind == layer.KindInstructions {
		d.Mode = layer.ModeExtend
	}
	return d
}

var keepHead = "catalogue[1]{detector,rank}:\n  uncited-assistant-specifics,0"

// Decision 24's null arm: a held definition settles only when its failures under it beat both
// control windows by more than twice their A-versus-A spread, after its cost; otherwise it is rejected.
func TestSettleV2NullArm(t *testing.T) {
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	l := &settleLedger{base: base}
	l.controls(3, 2, 3, 2, 3, 2)
	d := heldAt(layer.KindRemedy, RemedyKeepHead, keepHead, base.Add(6*time.Minute+30*time.Second))
	id := []string{d.HeldID()}
	if st := SettleV2(l.events, d); st.Decided {
		t.Fatalf("decided with no unit under it: %+v", st)
	}
	l.pass(0, 0, id)
	l.pass(0, 0, id)
	if st := SettleV2(l.events, d); st.Decided {
		t.Fatalf("decided before MinEvidence units: %+v", st)
	}
	l.pass(1, 0, id)
	st := SettleV2(l.events, d)
	if !st.Decided || !st.Clean || !strings.Contains(st.Reason, "failures 1 under it over 3 compactions against 7 and 8 without it (spread 1)") {
		t.Fatalf("a real effect: %+v", st)
	}
	if st.Scope == nil || fmt.Sprint(st.Scope.Repos, st.Scope.Families) != "[r1] [.go]" {
		t.Fatalf("scope: %+v", st.Scope)
	}
	// The same effect, inside the spread of two noisier control windows: no net effect.
	l = &settleLedger{base: base}
	l.controls(1, 1, 1, 4, 4, 4)
	for range 3 {
		l.pass(1, 0, id)
	}
	if st := SettleV2(l.events, d); !st.Decided || st.Clean || !strings.HasPrefix(st.Reason, "no net effect after cost") {
		t.Fatalf("within the spread: %+v", st)
	}
}

// The effect after cost must exceed twice the spread, and be at least one failure when the
// spread is 0: an effect of exactly twice the spread is rejected, one failure more settles, and
// with no spread one failure settles only when nothing is charged against it.
func TestSettleV2TwiceTheSpread(t *testing.T) {
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	d := heldAt(layer.KindRemedy, RemedyKeepHead, keepHead, base.Add(6*time.Minute+30*time.Second))
	id := []string{d.HeldID()}
	for _, c := range []struct {
		controls, treated []int
		turns             int
		clean             bool
		figures           string
	}{
		{[]int{2, 2, 2, 2, 2, 3}, []int{2, 1, 1}, 4, false, "failures 4 under it over 3 compactions against 7 and 6 without it (spread 1), cost 0.000"},
		{[]int{2, 2, 2, 2, 2, 3}, []int{1, 1, 1}, 4, true, "failures 3 under it over 3 compactions against 7 and 6 without it (spread 1), cost 0.000"},
		{[]int{2, 2, 2, 2, 2, 2}, []int{2, 2, 1}, 4, true, "failures 5 under it over 3 compactions against 6 and 6 without it (spread 0), cost 0.000"},
		{[]int{2, 2, 2, 2, 2, 2}, []int{2, 2, 1}, 5, false, "failures 5 under it over 3 compactions against 6 and 6 without it (spread 0), cost 0.300"},
	} {
		l := &settleLedger{base: base}
		l.controls(c.controls...)
		for _, f := range c.treated {
			l.pass(f, 0, id, func(e *curated.ModelEvent) { e.Turns = c.turns })
		}
		st := SettleV2(l.events, d)
		if !st.Decided || st.Clean != c.clean || !strings.HasSuffix(st.Reason, c.figures) {
			t.Errorf("controls %v, under it %v (turns %d): %+v", c.controls, c.treated, c.turns, st)
		}
	}
}

// A no-op definition (its compactions fail as the ones without it) is rejected, and an
// instructions definition's text is charged against it: a small effect that would settle a free
// definition does not settle a long text.
func TestSettleV2NoOpAndCost(t *testing.T) {
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	l := &settleLedger{base: base}
	l.controls(2, 2, 2, 2, 2, 2)
	noop := heldAt(layer.KindRemedy, RemedyKeepHead, keepHead, base.Add(6*time.Minute+30*time.Second))
	for range 3 {
		l.pass(2, 0, []string{noop.HeldID()})
	}
	if st := SettleV2(l.events, noop); !st.Decided || st.Clean {
		t.Fatalf("a no-op settled: %+v", st)
	}
	short := heldAt(layer.KindInstructions, agents.InstructionsConsolidation, "Keep every figure.", base.Add(6*time.Minute+30*time.Second))
	long := heldAt(layer.KindInstructions, agents.InstructionsConsolidation, strings.Repeat("Keep every figure with its unit. ", 160), base.Add(6*time.Minute+30*time.Second))
	for _, d := range []layer.Definition{short, long} {
		l := &settleLedger{base: base}
		l.controls(2, 2, 2, 2, 2, 2)
		l.pass(1, 0, []string{d.HeldID()})
		l.pass(1, 0, []string{d.HeldID()})
		l.pass(2, 0, []string{d.HeldID()})
		st := SettleV2(l.events, d)
		if d.Text == short.Text && (!st.Decided || !st.Clean) || d.Text == long.Text && (!st.Decided || st.Clean || !strings.Contains(st.Reason, "cost 3.")) {
			t.Fatalf("%d tokens: %+v", costTokens(d), st)
		}
	}
	// Turns beyond both control windows are charged too.
	l = &settleLedger{base: base}
	l.controls(2, 2, 2, 2, 2, 2)
	for range 3 {
		l.pass(1, 0, []string{noop.HeldID()}, func(e *curated.ModelEvent) { e.Turns = 20 })
	}
	if st := SettleV2(l.events, noop); st.Clean || !strings.Contains(st.Reason, "cost 4.800") {
		t.Fatalf("turns: %+v", st)
	}
}

// Harm under a definition rejects it whatever its effect; fewer than 2·MinEvidence controls is
// no null arm; a pass without the harm signal, an empty pass, a pass before the hold and an
// instructions pass whose level 1 did not answer are no evidence.
func TestSettleV2HarmAndEvidence(t *testing.T) {
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	d := heldAt(layer.KindRemedy, RemedyKeepHead, keepHead, base.Add(6*time.Minute+30*time.Second))
	id := []string{d.HeldID()}
	l := &settleLedger{base: base}
	l.controls(3, 3, 3, 3, 3, 3)
	l.pass(0, 2, id)
	l.pass(0, 0, id)
	l.pass(0, 0, id)
	if st := SettleV2(l.events, d); !st.Decided || st.Clean || !strings.HasPrefix(st.Reason, "harm: 2") {
		t.Fatalf("harm: %+v", st)
	}
	l = &settleLedger{base: base}
	l.controls(3, 3, 3, 3)
	early := heldAt(layer.KindRemedy, RemedyKeepHead, keepHead, base.Add(4*time.Minute+30*time.Second))
	for range 3 {
		l.pass(0, 0, []string{early.HeldID()})
	}
	if st := SettleV2(l.events, early); !st.Decided || st.Clean || !strings.HasPrefix(st.Reason, "no null arm: 4") {
		t.Fatalf("no null arm: %+v", st)
	}
	l = &settleLedger{base: base}
	l.controls(3, 3, 3, 3, 3, 3)
	l.pass(0, 0, id, func(e *curated.ModelEvent) { e.Harm = nil })
	l.pass(0, 0, id, func(e *curated.ModelEvent) { e.Outcome = curated.OutcomeEmpty })
	l.pass(0, 0, id, func(e *curated.ModelEvent) { e.Time = base.Format(time.RFC3339Nano) })
	l.pass(0, 0, id, func(e *curated.ModelEvent) { e.Engine = "" })
	if st := SettleV2(l.events, d); st.Decided {
		t.Fatalf("settled on no evidence: %+v", st)
	}
	instructions := heldAt(layer.KindInstructions, agents.InstructionsConsolidation, "Merge less.", base.Add(6*time.Minute+30*time.Second))
	for range 3 {
		l.pass(0, 0, []string{instructions.HeldID()}, func(e *curated.ModelEvent) { e.Level1 = false })
	}
	if st := SettleV2(l.events, instructions); st.Decided {
		t.Fatalf("instructions settled without a level-1 answer: %+v", st)
	}
}

// Independent recurrence: compactions over the same evidence roots count once, so repeated text
// never settles a definition, and 2·MinEvidence such compactions reject it.
func TestSettleV2IndependentRoots(t *testing.T) {
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	d := heldAt(layer.KindRemedy, RemedyKeepHead, keepHead, base.Add(6*time.Minute+30*time.Second))
	l := &settleLedger{base: base}
	l.controls(3, 3, 3, 3, 3, 3)
	same := func(e *curated.ModelEvent) { e.Roots = []string{"00000000000000aa"} }
	for k := 0; k < 2*MinEvidence; k++ {
		if st := SettleV2(l.events, d); st.Decided {
			t.Fatalf("decided after %d repeated compactions: %+v", k, st)
		}
		l.pass(0, 0, []string{d.HeldID()}, same)
	}
	if st := SettleV2(l.events, d); !st.Decided || st.Clean || !strings.Contains(st.Reason, "from 1 independent evidence root") {
		t.Fatalf("repeated roots: %+v", st)
	}
	// A control whose roots an earlier unit had is no control either.
	l = &settleLedger{base: base}
	l.controls(3, 3, 3)
	for range 3 {
		l.pass(3, 0, nil, func(e *curated.ModelEvent) { e.Roots = []string{"0000000000000001"} })
	}
	for range 3 {
		l.pass(0, 0, []string{d.HeldID()})
	}
	if st := SettleV2(l.events, d); !strings.HasPrefix(st.Reason, "no null arm: 3") {
		t.Fatalf("dependent controls counted: %+v", st)
	}
}

// UnitOf: roots of the records citing the cut that are not tool output, the commonest repo and
// path extension, and the same root for the same text in another session.
func TestUnitOf(t *testing.T) {
	obs := func(id, content, source, repo string, paths []string, src ...string) ledger.Observation {
		return ledger.Observation{ID: id, Content: content, Kind: "fact", Source: source, SourceEntryIDs: src, Frame: ledger.Frame{RepoID: repo}, Keys: ledger.Keys{Paths: paths}}
	}
	records := []ledger.Observation{
		obs("a", "Use sqlc.", ledger.SourceUserSaid, "r1", []string{"db/q.SQL"}, "e1"),
		obs("b", "go vet failed", ledger.SourceToolObserved, "r1", []string{"main.go", "x/y.go"}, "e2"),
		obs("c", "outside", ledger.SourceUserSaid, "r2", []string{"a.md"}, "e9"),
		obs("d", "Use sqlc.", ledger.SourceAssistantClaim, "", nil, "e3"),
	}
	u := UnitOf(records, map[string]bool{"e1": true, "e2": true, "e3": true})
	if len(u.Roots) != 1 || u.Roots[0] != RootOf(records[0]) || u.Repo != "r1" || u.Family != ".go" {
		t.Fatalf("unit: %+v", u)
	}
	other := records[0]
	other.ID, other.SourceEntryIDs = "z", []string{"q7"}
	if RootOf(other) != RootOf(records[0]) {
		t.Fatal("the same text in another session is another root")
	}
	if pathExt("a.b/c") != "" || pathExt("x/Y.Lisp") != ".lisp" || pathExt("README") != "" {
		t.Fatal("pathExt")
	}
}

// AfterPass records the settlement fields: roots, harm, turns, scope and the written
// definitions the compaction ran with; an empty pass records none.
func TestAfterPassRecordsSettlementFields(t *testing.T) {
	store := newStore(t)
	if err := RecordDetection(store, "p/m", Findings{Counts: map[string]int{DetectDroppedThenQueried: 2}}, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := AfterPass(store, "p/m", Pass{Level1: true, Counts: map[string]int{CountLevel1UnionKept: 1}, Turns: 3, RanWritten: []string{"remedy/keep-head@0123456789abcdef"},
		Unit: Unit{Roots: []string{"00000000000000aa"}, Repo: "r1", Family: ".go"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := AfterPass(store, "p/m", Pass{Empty: true, Turns: 3}); err != nil {
		t.Fatal(err)
	}
	events, err := store.ModelEvents("p/m")
	if err != nil {
		t.Fatal(err)
	}
	e := events[1]
	if e.Harm == nil || *e.Harm != 3 || e.Turns != 3 || e.Repo != "r1" || e.Family != ".go" || len(e.Roots) != 1 || len(e.RanWritten) != 1 {
		t.Fatalf("pass: %+v", e)
	}
	if events[2].Harm != nil || events[2].Turns != 0 {
		t.Fatalf("empty pass: %+v", events[2])
	}
}
