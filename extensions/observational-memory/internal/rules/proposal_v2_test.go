package rules

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
)

// Level 1 may define only what changes memory v2 and v2 evidence can judge: the consolidation
// instructions (extending the seed, never replacing it), a detector, a catalogue of a code
// remedy; a format, a summary, a schema, a tool, another stage's instructions and a catalogue
// of a remedy v2 never runs are refused.
func TestCheckV2Definition(t *testing.T) {
	for _, c := range []struct {
		kind, name string
		ok         bool
	}{
		{layer.KindInstructions, agents.InstructionsConsolidation, true},
		{layer.KindDetector, "missed-paths", true},
		{layer.KindRemedy, RemedyKeepHead, true},
		{layer.KindFormat, "pipes", false},
		{layer.KindFormat, "rows", false},
		{layer.KindInstructions, agents.InstructionsObserver, false},
		{layer.KindRemedy, RemedyRequireKind, false},
		{layer.KindSummary, "default", false},
		{layer.KindSchema, "observation", false},
		{layer.KindTool, "deploys", false},
	} {
		if err := CheckV2Definition(c.kind, c.name); (err == nil) != c.ok {
			t.Errorf("%s/%s: %v, want ok %v", c.kind, c.name, err, c.ok)
		}
	}
	if err := CheckV2Proposal(layer.KindInstructions, agents.InstructionsConsolidation, "mode: extend\nMerge less."); err != nil {
		t.Fatalf("an extension refused: %v", err)
	}
	if err := CheckV2Proposal(layer.KindInstructions, agents.InstructionsConsolidation, "Merge less."); err != nil {
		t.Fatalf("an addition with no mode line refused: %v", err)
	}
	if err := CheckV2Proposal(layer.KindInstructions, agents.InstructionsConsolidation, "mode: replace\nAlways merge every pair."); err == nil || !strings.Contains(err.Error(), "never replace") {
		t.Fatalf("a replacement of the seed accepted: %v", err)
	}
}

// Only a definition memory v2 held is one it settles and waits for: a v1 hold of the same kind
// (a detector held by a v1 pass) is v1's.
func TestSettlesUnderV2OnlyItsOwnHolds(t *testing.T) {
	v2 := layer.Definition{Kind: layer.KindDetector, Name: "missed-paths", Engine: curated.EngineV2}
	if !SettlesUnderV2(v2) {
		t.Fatal("a v2 hold is not settled under v2")
	}
	v1 := v2
	v1.Engine = ""
	if SettlesUnderV2(v1) {
		t.Fatal("a v1 hold is settled under v2")
	}
}

// ProposalDue offers definitions only when the v2 evidence differs from what the last offer was
// shown (from none, before any), and never while a held v2 definition waits for its proof; v1
// passes are never v2 evidence.
func TestProposalDueOnlyWhenEvidenceChanged(t *testing.T) {
	store := newStore(t)
	if _, due, err := ProposalDue(store, "p/m"); err != nil || due {
		t.Fatalf("due with no evidence (%v)", err)
	}
	if err := store.RecordModel(curated.ModelEvent{Model: "p/m", Kind: curated.ModelKindPass, Stage: StageObserver, Tokens: 50, Counts: map[string]int{DetectThinChunk: 3}}); err != nil {
		t.Fatal(err)
	}
	if _, due, _ := ProposalDue(store, "p/m"); due {
		t.Fatal("a v1 pass made a v2 offer due")
	}
	v2Compaction(t, store, 100, nil, map[string]int{CountLevel1UnionKept: 1})
	evidence, due, err := ProposalDue(store, "p/m")
	if err != nil || !due || evidence.Counts[CountLevel1UnionKept] != 1 || !strings.Contains(evidence.Text(), "compactions") {
		t.Fatalf("due %v evidence %+v (%v)", due, evidence, err)
	}
	if _, _, _, err := store.Layer().HoldPass("p/m", nil, layer.Generation{Op: layer.OpPropose, Evidence: evidence.Digest(), Events: evidence.Events}); err != nil {
		t.Fatal(err)
	}
	v2Compaction(t, store, 100, nil, map[string]int{CountLevel1UnionKept: 4})
	if _, due, _ := ProposalDue(store, "p/m"); due {
		t.Fatal("due with the same counts non-zero")
	}
	v2Compaction(t, store, 100, nil, map[string]int{CountLevel1SpecificsRefused: 1})
	if _, due, _ := ProposalDue(store, "p/m"); !due {
		t.Fatal("not due after a new count")
	}
	if _, err := store.Layer().Hold(layer.Definition{Kind: layer.KindInstructions, Name: agents.InstructionsConsolidation, Model: "p/m", Text: "Merge less."}); err != nil {
		t.Fatal(err)
	}
	if _, due, _ := ProposalDue(store, "p/m"); due {
		t.Fatal("due while a held definition waits for its proof")
	}
}

// SettleV2 counts only the non-empty compaction passes that record having run with exactly a
// definition's held text: never the compaction that held it, never an empty one, never another
// session's compaction that ran without it, wherever those land in the shared ledger. It decides
// each kind on its own evidence: the consolidation instructions on a level-1 answer under them
// (reverted on a loss), a detector on detector runs under it.
func TestSettleV2(t *testing.T) {
	store := newStore(t)
	held := func(kind, name, text string) layer.Definition {
		d, err := store.Layer().Hold(layer.Definition{Kind: kind, Name: name, Model: "p/m", Text: text})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	events := func() []curated.ModelEvent {
		e, err := store.ModelEvents("p/m")
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	compaction := func(p Pass) {
		t.Helper()
		if _, err := AfterPass(store, "p/m", p); err != nil {
			t.Fatal(err)
		}
	}
	instructions := held(layer.KindInstructions, agents.InstructionsConsolidation, "Merge less.")
	detector := held(layer.KindDetector, "missed-paths", "detector[1]{check,description}:\n  thin-chunk,notes counted")
	if instructions.Engine != curated.EngineV2 || detector.Engine != curated.EngineV2 {
		t.Fatalf("a hold through a v2 store is not marked v2: %q %q", instructions.Engine, detector.Engine)
	}
	both := []string{instructions.HeldID(), detector.HeldID()}

	// The compaction that held them read its layer before the hold: it ran with neither, and its
	// clean level-1 answer is never their proof.
	if err := RecordDetection(store, "p/m", Findings{Counts: map[string]int{}}, 200); err != nil {
		t.Fatal(err)
	}
	compaction(Pass{Level1: true, Counts: map[string]int{}})
	// Another session's compaction recorded after the hold, whose layer was read before it: its
	// pass names neither, so it is no evidence either, though it comes after the hold.
	if err := RecordDetection(store, "p/m", Findings{Counts: map[string]int{}}, 200); err != nil {
		t.Fatal(err)
	}
	compaction(Pass{Level1: true, Counts: map[string]int{CountLevel1UnionKept: 1}})
	for _, d := range []layer.Definition{instructions, detector} {
		if decided, _, reason := SettleV2(events(), d); decided {
			t.Fatalf("%s/%s settled by a compaction that never ran under it (%s)", d.Kind, d.Name, reason)
		}
	}
	// An empty compaction that ran under them: no evidence.
	compaction(Pass{Empty: true, RanHeld: both})
	for _, d := range []layer.Definition{instructions, detector} {
		if decided, _, _ := SettleV2(events(), d); decided {
			t.Fatalf("%s/%s settled by an empty compaction", d.Kind, d.Name)
		}
	}
	// A compaction that ran under them, whose level 1 kept a fact back.
	if err := RecordDetection(store, "p/m", Findings{Counts: map[string]int{}}, 200); err != nil {
		t.Fatal(err)
	}
	compaction(Pass{Level1: true, Counts: map[string]int{CountLevel1UnionKept: 1}, RanHeld: both})
	if decided, clean, reason := SettleV2(events(), instructions); !decided || clean || !strings.Contains(reason, CountLevel1UnionKept) {
		t.Fatalf("instructions: decided %v clean %v (%s)", decided, clean, reason)
	}
	if decided, clean, reason := SettleV2(events(), detector); !decided || !clean || !strings.Contains(reason, "200 source tokens over 1 later compaction") {
		t.Fatalf("detector: decided %v clean %v (%s)", decided, clean, reason)
	}
	// A pass that ran with another text of the name is no evidence for this one.
	changed := held(layer.KindDetector, "missed-paths", "detector[1]{check,description}:\n  thin-chunk,notes counted again")
	if decided, _, _ := SettleV2(events(), changed); decided {
		t.Fatal("a re-held text was settled on passes that ran with the old one")
	}
}

// V2UnitsSinceProposal counts the non-empty v2 compaction passes after the usage ledger length
// the last level-1 offer recorded (from the start before any), never a v1 observer pass.
func TestV2UnitsSinceProposal(t *testing.T) {
	store := newStore(t)
	if units, offered, err := V2UnitsSinceProposal(store, "p/m"); err != nil || units != 0 || offered {
		t.Fatalf("empty: %d %v (%v)", units, offered, err)
	}
	v2Compaction(t, store, 100, nil, map[string]int{CountLevel1UnionKept: 1})
	v2Compaction(t, store, 100, nil, nil)
	if units, offered, _ := V2UnitsSinceProposal(store, "p/m"); units != 2 || offered {
		t.Fatalf("before an offer: %d %v", units, offered)
	}
	evidence, _, err := ProposalDue(store, "p/m")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := store.Layer().HoldPass("p/m", nil, layer.Generation{Op: layer.OpPropose, Evidence: evidence.Digest(), Events: evidence.Events}); err != nil {
		t.Fatal(err)
	}
	if units, offered, _ := V2UnitsSinceProposal(store, "p/m"); units != 0 || !offered {
		t.Fatalf("at the offer: %d %v", units, offered)
	}
	v2Compaction(t, store, 100, nil, nil)
	if err := store.RecordModel(curated.ModelEvent{Model: "p/m", Kind: curated.ModelKindPass, Stage: StageObserver, Tokens: 50, Counts: map[string]int{DetectThinChunk: 3}}); err != nil {
		t.Fatal(err)
	}
	v2Compaction(t, store, 100, nil, map[string]int{CountLevel1UnionKept: 2})
	if units, offered, _ := V2UnitsSinceProposal(store, "p/m"); units != 2 || !offered {
		t.Fatalf("after the offer: %d %v", units, offered)
	}
}

// A held Go detector is settled on the detector pass events that started its held text, never
// on compaction passes: reverted when any of those runs gave no count (unfinished and crashed
// runs included), written when one counted and none failed, held while none ran it. A held
// query detector is retired: never settled, never blocking an offer.
func TestSettleV2GoDetectors(t *testing.T) {
	store := newStore(t)
	hold := func(name, text string) layer.Definition {
		t.Helper()
		d, err := store.Layer().Hold(layer.Definition{Kind: layer.KindDetector, Name: name, Model: "p/m", Text: text})
		if err != nil {
			t.Fatal(err)
		}
		return d
	}
	events := func() []curated.ModelEvent {
		e, err := store.ModelEvents("p/m")
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	pass := func(ranHeld []string, counts map[string]int, errs map[string]string) {
		t.Helper()
		if err := store.RecordModel(curated.ModelEvent{Model: "p/m", Kind: curated.ModelKindDetectorPass, Engine: curated.EngineV2, Cut: "e9", Tokens: 100, RanHeld: ranHeld, Counts: counts, Errors: errs}); err != nil {
			t.Fatal(err)
		}
	}
	good := hold("plans", CodeDetectorText("func Count() int { return om.Notes().Matched }", "notes"))
	loops := hold("loops", CodeDetectorText("func Count() int { for {} }", "loops"))
	query := hold("old", "detector[1]{query}:\n  \"notes | where kind=plan\"")
	if !SettlesUnderV2(good) || SettlesUnderV2(query) {
		t.Fatal("SettlesUnderV2: a Go detector is settled, a query detector never")
	}
	// A compaction pass naming them is no evidence for a Go detector.
	compaction := Pass{Level1: true, Counts: map[string]int{CountLevel1UnionKept: 1}, RanHeld: []string{good.HeldID(), loops.HeldID(), query.HeldID()}}
	if err := RecordDetection(store, "p/m", Findings{Counts: map[string]int{}}, 200); err != nil {
		t.Fatal(err)
	}
	if _, err := AfterPass(store, "p/m", compaction); err != nil {
		t.Fatal(err)
	}
	for _, d := range []layer.Definition{good, loops, query} {
		if decided, _, reason := SettleV2(events(), d); decided {
			t.Fatalf("%s settled without a detector pass (%s)", d.Name, reason)
		}
	}
	// A pass that never started them (still queued at the kill) names neither.
	pass(nil, map[string]int{}, nil)
	if decided, _, _ := SettleV2(events(), good); decided {
		t.Fatal("settled by a pass that never started it")
	}
	pass([]string{good.HeldID(), loops.HeldID(), query.HeldID()}, map[string]int{DefinedCountKey("plans"): 0}, map[string]string{"loops": "unfinished at the next compaction"})
	if decided, clean, reason := SettleV2(events(), good); !decided || !clean || !strings.Contains(reason, "1 later detector pass") {
		t.Fatalf("good: %v %v (%s)", decided, clean, reason)
	}
	if decided, clean, reason := SettleV2(events(), loops); !decided || clean || !strings.Contains(reason, "unfinished at the next compaction") {
		t.Fatalf("loops: %v %v (%s)", decided, clean, reason)
	}
	if decided, _, _ := SettleV2(events(), query); decided {
		t.Fatal("a held query detector was settled")
	}
	// The held query detector never blocks an offer; a held Go detector does.
	if _, err := store.Layer().ClaimHeld([]layer.Definition{good, loops}, true, "test"); err != nil {
		t.Fatal(err)
	}
	if _, due, err := ProposalDue(store, "p/m"); err != nil || !due {
		t.Fatalf("a retired held query detector blocked the offer: due %v (%v)", due, err)
	}
}

// A model's Go detector counts reach the level-1 offer through the detector pass events of the
// compactions in the evidence window (each belonging to the compaction recorded before it): a
// first non-zero count changes the digest, a refused smoke run's count is not offer evidence,
// and a detector pass of a compaction outside the window counts nothing.
func TestV2EvidenceCarriesDefinedDetectorCounts(t *testing.T) {
	store := newStore(t)
	detectorPass := func(counts map[string]int) {
		t.Helper()
		if err := store.RecordModel(curated.ModelEvent{Model: "p/m", Kind: curated.ModelKindDetectorPass, Engine: curated.EngineV2, Cut: "c", Tokens: 100, Counts: counts}); err != nil {
			t.Fatal(err)
		}
	}
	v2Compaction(t, store, 100, nil, nil)
	detectorPass(map[string]int{DefinedCountKey("old-loops"): 4})
	for range MinEvidence {
		v2Compaction(t, store, 100, nil, nil)
	}
	before, err := V2EvidenceOf(store, "p/m")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := before.Counts[DefinedCountKey("old-loops")]; ok {
		t.Fatalf("a detector pass outside the window counted: %v", before.Counts)
	}
	detectorPass(map[string]int{DefinedCountKey("retry-loops"): 7, DefinedCountKey("quiet"): 0, CountLevel1Refused: 1})
	after, err := V2EvidenceOf(store, "p/m")
	if err != nil {
		t.Fatal(err)
	}
	if after.Counts[DefinedCountKey("retry-loops")] != 7 || !strings.Contains(after.Text(), "defined:retry-loops: 7") {
		t.Fatalf("the detector's count is not offer evidence: %v\n%s", after.Counts, after.Text())
	}
	if _, ok := after.Counts[CountLevel1Refused]; ok {
		t.Fatalf("a refused smoke run is offer evidence: %v", after.Counts)
	}
	if _, ok := after.Counts[DefinedCountKey("quiet")]; ok {
		t.Fatalf("a zero count is offer evidence: %v", after.Counts)
	}
	if before.Digest() == after.Digest() {
		t.Fatal("a first non-zero detector count left the digest unchanged")
	}
	if after.Tokens != before.Tokens {
		t.Fatalf("a detector pass added source tokens: %d, want %d", after.Tokens, before.Tokens)
	}
}
