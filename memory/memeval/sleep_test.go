package memeval

import (
	"context"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/config"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// sleepCompactions are the compactions (1-based) whose hook appended a sleep record.
func sleepCompactions(run Run) []int {
	var out []int
	prev := 0
	for _, s := range run.Snapshots {
		n, _ := ledger.Sleeps(s.Branch)
		if len(n) > prev {
			out = append(out, s.N)
		}
		prev = len(n)
	}
	return out
}

// TestSleepFallbackInCompaction checks the sleep pass's compaction fallback (decision 13): it runs
// in the compaction after K compactions with no sleep record, makes no request, hides only exact
// duplicates and normalized error lines, and is skipped when the ledger is over its bound or K is 0.
func TestSleepFallbackInCompaction(t *testing.T) {
	counter := testfix.CountRequests(t)
	script := Generate(3, Options{Turns: 400})
	run, err := Simulate(context.Background(), script, SmallWindow, 11)
	if err != nil {
		t.Fatal(err)
	}
	k := config.DefaultSleepAfterCompactions
	got := sleepCompactions(run)
	want := []int{k + 1, 2*k + 1}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("sleep ran at compactions %v, want %v (K = %d)", got, want, k)
	}
	last := run.Snapshots[len(run.Snapshots)-1]
	st := ledger.SleepStateOf(last.Branch)
	folded := ledger.FoldLedger(last.Branch, "")
	for id, d := range st.Duplicates {
		o, of := folded.ObservationsByID[id], folded.ObservationsByID[d.Of]
		if d.By == ledger.DuplicateByHash && (o.Content != of.Content || o.Kind != of.Kind) {
			t.Errorf("hash duplicate %s of %s differs: %q vs %q", id, d.Of, o.Content, of.Content)
		}
		for _, shown := range last.Result.Details.Shown {
			if shown == id {
				t.Errorf("duplicate %s is shown", id)
			}
		}
	}
	if n := counter.Total(); n != 0 {
		t.Fatalf("sleep made %d requests, want 0", n)
	}

	w := SmallWindow
	w.SleepOff = true
	off, err := Simulate(context.Background(), script, w, 11)
	if err != nil {
		t.Fatal(err)
	}
	if got := sleepCompactions(off); len(got) != 0 {
		t.Fatalf("sleepAfterCompactions 0 ran sleep at %v", got)
	}
	w = SmallWindow
	w.SleepMaxRecords = 1
	bounded, err := Simulate(context.Background(), script, w, 6)
	if err != nil {
		t.Fatal(err)
	}
	if got := sleepCompactions(bounded); len(got) != 0 {
		t.Fatalf("a ledger over sleepMaxRecords ran sleep at %v", got)
	}
	if n := counter.Total(); n != 0 {
		t.Fatalf("%d requests, want 0", n)
	}
}
