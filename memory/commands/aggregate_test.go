package commands

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
	tf "github.com/drvova/pig-observational-memory/memory/testfix"
)

// /om:status counts the records by kind and by label and sums the sleep records (phase 7).
func TestStatusAggregates(t *testing.T) {
	a := tf.Observation("aaaaaaaaaa01", func(o *ledger.Observation) { o.Kind, o.Content = "fact", "The port is 5432." })
	b := tf.Observation("aaaaaaaaaa02", func(o *ledger.Observation) {
		o.Kind, o.Content, o.Supersedes = "correction", "Actually the port is 5433.", []string{a.ID}
	})
	c := tf.Observation("aaaaaaaaaa03")
	sleep := ledger.SleepData{Version: 1, Trigger: ledger.SleepAtIdle, ThroughEntryID: "e1", Records: 3,
		Duplicates: []ledger.SleepDuplicate{{ID: c.ID, Of: b.ID, By: ledger.DuplicateByHash}},
		Checks:     []ledger.SleepCheck{{ID: b.ID, Kind: ledger.CheckSupersede, Verdict: ledger.VerdictChallenged}}}
	entries := []session.Entry{
		tf.ObservationsRecordedEntry("e1", []ledger.Observation{a, b, c}, "u1"),
		tf.Entry(map[string]any{"type": "custom", "id": "s1", "parentId": nil, "timestamp": tf.DefaultTimestamp, "customType": ledger.OMSleep, "data": tf.ToJSONValue(sleep)}),
	}
	got := strings.Join(recordsLines(entries), "\n")
	for _, want := range []string{
		"Records by kind: correction 1, fact 1, untyped 1",
		"Records by label: believed 1, superseded 1, retracted 0, contested 1, held 0",
		"Sleep: 1 run (latest idle, through e1): 1 duplicates hidden, 0 links, 0 decayed, 0 verified, 0 retired, 1 checks (1 challenged)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("status lacks %q:\n%s", want, got)
		}
	}
	if empty := strings.Join(recordsLines(nil), "\n"); !strings.Contains(empty, "Sleep: no run on this branch") || strings.Contains(empty, "by kind") {
		t.Errorf("empty status:\n%s", empty)
	}
}
