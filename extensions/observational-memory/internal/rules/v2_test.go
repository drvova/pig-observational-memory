package rules

import (
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
)

func newV2Store(t *testing.T) curated.Store {
	t.Helper()
	store := newStore(t)
	store.V2 = true
	return store
}

// v2Compaction records one memory v2 compaction of p/m after a detector run counting counts over
// tokens source tokens.
func v2Compaction(t *testing.T, store curated.Store, tokens int, counts map[string]int, level1 map[string]int) []Transition {
	t.Helper()
	if err := RecordDetection(store, "p/m", Findings{Counts: counts}, tokens); err != nil {
		t.Fatal(err)
	}
	ts, err := AfterPass(store, "p/m", Pass{Stage: StageCompaction, Engine: curated.EngineV2, Counts: level1})
	if err != nil {
		t.Fatal(err)
	}
	return ts
}

// DetectV2 counts the v2 detectors over one extraction: the assistant entries whose added
// specifics no record of the extraction cites, and the memory queries asking for dropped
// records; it counts no v1-only check.
func TestDetectV2CountsOnlyV2Detectors(t *testing.T) {
	branch := []ledger.Entry{
		user("u1", "Which laptops should I look at, maybe yesterday's list?"),
		assistant("a1", "Look at the ThinkPad X1 and the Dell XPS 13, both under 1500 dollars."),
	}
	f := DetectV2(ObserverPass{Branch: branch, Covered: branch, Notes: []ledger.Observation{note("aaaaaaaaaaaa", "User asked about laptops yesterday.", cites("u1"))}})
	if !reflect.DeepEqual(f.Uncited, []string{"a1"}) || f.Counts[DetectUncitedAssistantSpecifics] != 1 {
		t.Fatalf("findings %+v", f)
	}
	for _, d := range []string{DetectUnresolvedRelativeDate, DetectThinChunk, DetectCrossScopeSupersede} {
		if _, counted := f.Counts[d]; counted {
			t.Fatalf("%s counted under v2: %+v", d, f.Counts)
		}
	}
}

// One evidence unit is one compaction: its pass sums the detector runs since the previous
// compaction (counts and tokens), adds level 1's code checks and the memory syntax errors
// counted under v2 since then, and ignores what v1 counted.
func TestV2CompactionSumsDetectorRunsSinceTheLastOne(t *testing.T) {
	store := newV2Store(t)
	_ = RecordDetection(store, "p/m", Findings{Counts: map[string]int{DetectUncitedAssistantSpecifics: 1}}, 40)
	_ = RecordDetection(store, "p/m", Findings{Counts: map[string]int{DetectUncitedAssistantSpecifics: 2, DetectDroppedThenQueried: 1}, DroppedKinds: map[string]int{"fact": 1}}, 60)
	_ = store.RecordModel(curated.ModelEvent{Model: "p/m", Kind: curated.ModelKindMemory, Syntax: true, Outcome: curated.OutcomeError, Engine: curated.EngineV2})
	_ = store.RecordModel(curated.ModelEvent{Model: "p/m", Kind: curated.ModelKindMemory, Syntax: true, Outcome: curated.OutcomeError}) // v1's
	if _, err := AfterPass(store, "p/m", Pass{Stage: StageCompaction, Engine: curated.EngineV2, Counts: map[string]int{CountLevel1Refused: 2, CountLevel1UnionKept: 0}}); err != nil {
		t.Fatal(err)
	}
	events, _ := store.ModelEvents("p/m")
	p := events[len(events)-1]
	want := map[string]int{DetectUncitedAssistantSpecifics: 3, DetectDroppedThenQueried: 1, DetectDecodeFailure: 1, CountLevel1Refused: 2}
	if p.Kind != curated.ModelKindPass || p.Stage != StageCompaction || p.Engine != curated.EngineV2 || p.Tokens != 100 || !reflect.DeepEqual(p.Counts, want) || p.Kinds["fact"] != 1 {
		t.Fatalf("pass %+v", p)
	}
	if !isCompactionPass(p) || isObserverPass(p) || isGradedPass(p) {
		t.Fatal("a v2 compaction must be v2 evidence only")
	}
	// The next compaction starts a new unit: the runs before it are not counted again.
	v2Compaction(t, store, 10, nil, nil)
	events, _ = store.ModelEvents("p/m")
	if p := events[len(events)-1]; p.Tokens != 10 || len(p.Counts) != 0 {
		t.Fatalf("second pass %+v", p)
	}
	// An empty compaction is no evidence, and the runs before it count for none.
	_ = RecordDetection(store, "p/m", Findings{Counts: map[string]int{DetectUncitedAssistantSpecifics: 5}}, 30)
	if _, err := AfterPass(store, "p/m", Pass{Stage: StageCompaction, Engine: curated.EngineV2, Empty: true}); err != nil {
		t.Fatal(err)
	}
	events, _ = store.ModelEvents("p/m")
	if p := events[len(events)-1]; p.Outcome != curated.OutcomeEmpty || p.Tokens != 0 || isCompactionPass(p) {
		t.Fatalf("empty pass %+v", p)
	}
}

// Under v2 the harness writes keep-head for a rising uncited count, never require-kind (a
// re-ask is a request), and only after MinEvidence compactions; the rule is v2's, so v1's For
// never applies it and v2's does, and v1's rules never apply under v2.
func TestV2HarnessWritesKeepHeadOnCompactions(t *testing.T) {
	store := newV2Store(t)
	uncited := map[string]int{DetectUncitedAssistantSpecifics: 1}
	if _, err := Add(store, Rule{Detector: DetectThinChunk, Remedy: RemedySplitChunk, Models: []string{"p/m"}, State: StateEstablished}); err != nil {
		t.Fatal(err)
	}
	var transitions []Transition
	for i := 0; i < MinEvidence; i++ {
		if ts := v2Compaction(t, store, 100, uncited, nil); i < MinEvidence-1 && len(ts) > 0 {
			t.Fatalf("compaction %d wrote %+v", i, ts)
		} else {
			transitions = append(transitions, ts...)
		}
	}
	if len(transitions) != 1 {
		t.Fatalf("transitions %+v", transitions)
	}
	r := transitions[0].Rule
	if r.Remedy != RemedyKeepHead || r.Engine != curated.EngineV2 || r.State != StateProbation || r.BaselineEvents != 3 || r.BaselineTokens != 300 {
		t.Fatalf("rule %+v", r)
	}
	v2 := For(store, "p/m")
	if !v2.Has(RemedyKeepHead) || v2.Has(RemedySplitChunk) || v2.Has(RemedyRequireKind) {
		t.Fatalf("v2 rules %+v", v2.Rules)
	}
	v1Store := store
	v1Store.V2 = false
	if v1 := For(v1Store, "p/m"); v1.Has(RemedyKeepHead) || !v1.Has(RemedySplitChunk) {
		t.Fatalf("v1 rules %+v", v1.Rules)
	}
	// The stored v2 rule reads back whole, engine included.
	all, _ := List(store)
	found := false
	for _, stored := range all {
		if stored.ID == r.ID {
			found = reflect.DeepEqual(stored, r)
		}
	}
	if !found {
		t.Fatalf("stored rules %+v, written %+v", all, r)
	}
	// The status names compactions and the v2 counts.
	if lines := strings.Join(StatusLines(store, []string{"p/m"}), "\n"); !strings.Contains(lines, "Rules: 1 active") || !strings.Contains(lines, "over its last 3 compactions") || !strings.Contains(lines, "uncited-assistant-specifics 3") {
		t.Fatalf("status %s", lines)
	}
}

// v1's evaluation never reads a v2 compaction: three v2 passes with rising counts write no v1
// rule, and a v1 rule on probation is not judged by them.
func TestV1EvaluateIgnoresV2Passes(t *testing.T) {
	v2pass := curated.ModelEvent{Kind: curated.ModelKindPass, Stage: StageCompaction, Engine: curated.EngineV2, Tokens: 100, Counts: map[string]int{DetectThinChunk: 1, DetectUncitedAssistantSpecifics: 1}, Revisions: Revisions()}
	events := []curated.ModelEvent{v2pass, v2pass, v2pass}
	if changed, _ := Evaluate("p/m", events, nil, t0); len(changed) != 0 {
		t.Fatalf("v1 wrote %+v from v2 passes", changed)
	}
	probation := Rule{ID: "r1", Detector: DetectThinChunk, Remedy: RemedySplitChunk, Models: []string{"p/m"}, State: StateProbation, BaselineEvents: 3, BaselineTokens: 300}
	if changed, _ := Evaluate("p/m", events, []Rule{probation}, t0); len(changed) != 0 {
		t.Fatalf("v2 passes judged a v1 rule: %+v", changed)
	}
	// And a v1 rule file reads and writes as it always did: no engine column.
	if text := encode(probation); strings.Contains(text, "engine") {
		t.Fatalf("v1 rule text %s", text)
	}
	if (Rule{Detector: DetectThinChunk, Remedy: RemedySplitChunk}).Signature() != strings.Join([]string{DetectThinChunk, RemedySplitChunk, "", "", ""}, "\x1f") {
		t.Fatal("a v1 rule's signature changed")
	}
}

// A model's proposal under v2 must answer a v2 detector with a code-only remedy; it is held as a
// v2 rule.
func TestV2ProposalsTakeOnlyCodeRemedies(t *testing.T) {
	store := newV2Store(t)
	for _, p := range []Proposal{
		{Detector: DetectUncitedAssistantSpecifics, Remedy: RemedyRequireKind},
		{Detector: DetectThinChunk, Remedy: RemedySplitChunk},
		{Detector: DetectCrossScopeSupersede, Remedy: RemedyPromptLine, Text: "Never supersede across scopes."},
	} {
		if _, err := Propose(store, "p/m", nil, p); err == nil || !strings.Contains(err.Error(), "memoryEngine v2") {
			t.Fatalf("%+v: %v", p, err)
		}
	}
	r, err := Propose(store, "p/m", nil, Proposal{Detector: DetectUncitedAssistantSpecifics, Remedy: RemedyKeepHead})
	if err != nil || r.Engine != curated.EngineV2 {
		t.Fatalf("rule %+v err %v", r, err)
	}
	if !For(store, "p/m").Has(RemedyKeepHead) {
		t.Fatal("the held v2 proposal does not apply under v2")
	}
}
