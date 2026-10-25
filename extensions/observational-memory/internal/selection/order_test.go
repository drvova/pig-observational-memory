package selection

import (
	"fmt"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
)

// The classes take records in order, newest first within a class: user corrections and
// preferences (not the superseded correction), open and failed attempts (not the resolved
// one), records with specifics no chosen line carries, reflections, then everything else.
func TestChooseClassOrder(t *testing.T) {
	id := func(n int) string { return fmt.Sprintf("%012x", 0xc00000000000+n) }
	entries := []ledger.Entry{}
	observations := []ledger.Observation{}
	add := func(o ledger.Observation) {
		n := len(observations)
		source := fmt.Sprintf("e%02d", n)
		entries = append(entries, ledger.Entry{Type: "message", ID: source, Message: map[string]any{"role": "user", "content": "x"}})
		o.ID, o.SourceEntryIDs, o.Timestamp, o.Relevance = id(n), []string{source}, "2026-05-01 10:00", "medium"
		observations = append(observations, o)
	}
	add(ledger.Observation{Kind: "correction", Content: "actually the budget stays small"})                                                                                    // 0
	add(ledger.Observation{Kind: "preference", Content: "never indent with tabs"})                                                                                             // 1
	add(ledger.Observation{Kind: ledger.KindAttempt, Outcome: ledger.OutcomeFailed, Tool: "bash", Target: "make", Content: `bash "make" failed`})                              // 2
	add(ledger.Observation{Kind: "correction", Content: "actually it runs nightly"})                                                                                           // 3
	add(ledger.Observation{Kind: "fact", Content: "it runs hourly", Supersedes: []string{id(3)}})                                                                              // 4
	add(ledger.Observation{Kind: ledger.KindAttempt, Outcome: ledger.OutcomeFailed, Tool: "bash", Target: "lint", Content: `bash "lint" failed`})                              // 5
	add(ledger.Observation{Kind: ledger.KindAttempt, Outcome: ledger.OutcomeWorked, Tool: "bash", Target: "lint", Content: `bash "lint" worked`, Supersedes: []string{id(5)}}) // 6
	add(ledger.Observation{Kind: "fact", Content: "the flight to Porto leaves at nine"})                                                                                       // 7
	add(ledger.Observation{Kind: "fact", Content: "the train to Porto is late"})                                                                                               // 8
	add(ledger.Observation{Kind: "preference", Content: "always run the linter", Origin: ledger.OriginModel})                                                                  // 9
	add(ledger.Observation{Kind: "fact", Content: "plain line"})                                                                                                               // 10
	add(ledger.Observation{Kind: ledger.KindAttempt, Outcome: ledger.OutcomeOpen, Tool: "bash", Target: "deploy", Content: `bash "deploy" open`})                              // 11
	reflection := ledger.Reflection{ID: "dddddddddddd", Content: "the user is careful", SupportingObservationIDs: []string{id(10)}}
	folded := ledger.FoldedLedger{Observations: observations, ActiveObservations: observations, ObservationsByID: map[string]ledger.Observation{}}
	for _, o := range observations {
		folded.ObservationsByID[o.ID] = o
	}
	s := newState(Input{Observations: observations, Reflections: []ledger.Reflection{reflection}, Folded: folded, Branch: entries, Budget: 1 << 20})
	p := s.choose(1 << 20)
	got := []string{}
	for _, it := range p.order {
		if p.on[it] {
			got = append(got, it.id())
		}
	}
	// 1 and 0 (newest first); 11 and 2; 8 (Porto, missing so far) but not 7 (the line 8
	// chose carries Porto); then the reflection, dropping 10; then the rest newest first: 9,
	// 7, 6 (dropping 5), 4 (dropping 3).
	want := []string{id(1), id(0), id(11), id(2), id(8), "dddddddddddd", id(9), id(7), id(6), id(4)}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order\n got %v\nwant %v", got, want)
	}
	for _, gone := range []string{id(3), id(5), id(10)} {
		if !p.dropped[gone] {
			t.Errorf("%s not dropped", gone)
		}
	}
}

// A level-1 merge of user corrections (origin model, FromUser) keeps the user class; a model
// record of the same kind without FromUser does not.
func TestChooseKeepsMergedUserCorrections(t *testing.T) {
	id := func(n int) string { return fmt.Sprintf("%012x", 0xd00000000000+n) }
	entries := []ledger.Entry{}
	observations := []ledger.Observation{}
	add := func(o ledger.Observation) {
		n := len(observations)
		source := fmt.Sprintf("e%02d", n)
		entries = append(entries, ledger.Entry{Type: "message", ID: source, Message: map[string]any{"role": "user", "content": "x"}})
		o.ID, o.SourceEntryIDs, o.Timestamp, o.Relevance = id(n), []string{source}, "2026-05-01 10:00", "medium"
		observations = append(observations, o)
	}
	add(ledger.Observation{Kind: "correction", Content: "the port is 8443, never 8080; tabs are banned", Origin: ledger.OriginModel, FromUser: true}) // 0
	add(ledger.Observation{Kind: "correction", Content: "the queue holds 64 jobs", Origin: ledger.OriginModel})                                       // 1
	add(ledger.Observation{Kind: "fact", Content: "the flight to Porto leaves at nine"})                                                              // 2
	folded := ledger.FoldedLedger{Observations: observations, ActiveObservations: observations, ObservationsByID: map[string]ledger.Observation{}}
	for _, o := range observations {
		folded.ObservationsByID[o.ID] = o
	}
	s := newState(Input{Observations: observations, Folded: folded, Branch: entries, Budget: 1 << 20})
	p := s.choose(1 << 20)
	got := []string{}
	for _, it := range p.order {
		got = append(got, it.id())
	}
	// 0 in class 2; then 2 and 1 by their specifics, newest first.
	want := []string{id(0), id(2), id(1)}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order\n got %v\nwant %v", got, want)
	}
}

// orderOf is the ids choose takes under an unbounded budget, in the order it takes them.
func orderOf(t *testing.T, observations []ledger.Observation, entries []ledger.Entry) ([]string, *selected) {
	t.Helper()
	folded := ledger.FoldedLedger{Observations: observations, ActiveObservations: observations, ObservationsByID: map[string]ledger.Observation{}}
	for _, o := range observations {
		folded.ObservationsByID[o.ID] = o
	}
	s := newState(Input{Observations: observations, Folded: folded, Branch: entries, Budget: 1 << 20})
	p := s.choose(1 << 20)
	got := []string{}
	for _, it := range p.order {
		if p.on[it] {
			got = append(got, it.id())
		}
	}
	return got, p
}

// Taking a record of a possibly_updates or conflicts pair tries the other next, in the same
// class, whichever side was taken; a retraction and what it withdrew are never taken in class 2
// or 4, so the retraction comes with the rest and drops the plan.
func TestChooseLinkedPairsAndRetractions(t *testing.T) {
	id := func(n int) string { return fmt.Sprintf("%012x", 0xe00000000000+n) }
	entries := []ledger.Entry{}
	observations := []ledger.Observation{}
	add := func(o ledger.Observation) {
		n := len(observations)
		source := fmt.Sprintf("e%02d", n)
		entries = append(entries, ledger.Entry{Type: "message", ID: source, Message: map[string]any{"role": "user", "content": "x"}})
		o.ID, o.SourceEntryIDs, o.Timestamp, o.Relevance = id(n), []string{source}, "2026-05-01 10:00", "medium"
		if o.Kind == "" {
			o.Kind = "fact"
		}
		observations = append(observations, o)
	}
	add(ledger.Observation{Content: "plain line about alpha"})                                                                      // 0
	add(ledger.Observation{Content: "plain line about bravo"})                                                                      // 1
	add(ledger.Observation{Content: "plain line about charlie"})                                                                    // 2
	add(ledger.Observation{Content: "the pool size is 12", PossiblyUpdates: []string{id(0)}})                                       // 3
	add(ledger.Observation{Content: "plain line about delta"})                                                                      // 4
	add(ledger.Observation{Kind: "preference", Content: "never run the cache on Redis", Conflicts: []string{id(4)}})                // 5
	add(ledger.Observation{Content: "we will ship build 42 on Friday", Status: ledger.StatusProposed})                              // 6
	add(ledger.Observation{Kind: "correction", Content: "scrap that", Status: ledger.StatusRetracted, Supersedes: []string{id(6)}}) // 7
	got, p := orderOf(t, observations, entries)
	// Class 2: 5 (the retraction 7 is not), then its partner 4. Class 4: 3 by its 12, then its
	// partner 0; 6 is withdrawn, so not by its 42. Class 6, newest first: 7 (dropping 6), 2, 1.
	want := []string{id(5), id(4), id(3), id(0), id(7), id(2), id(1)}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("order\n got %v\nwant %v", got, want)
	}
	if !p.dropped[id(6)] {
		t.Fatal("the retracted plan is not dropped")
	}
}
