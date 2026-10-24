package harness_test

import (
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

func usage(names ...string) []curated.UsageEvent {
	out := []curated.UsageEvent{}
	for _, n := range names {
		out = append(out, curated.UsageEvent{Name: "query:x", Event: n})
	}
	return out
}

const (
	ok    = curated.EventCallOK
	empty = curated.EventCallEmpty
	fail  = curated.EventCallError
	save  = curated.EventSave
)

func TestProbationTransitions(t *testing.T) {
	cases := []struct {
		name   string
		events []string
		want   string
	}{
		{"new", []string{save}, harness.StateProbation},
		{"one result", []string{save, ok}, harness.StateProbation},
		{"empty results do not establish", []string{save, empty, empty, empty}, harness.StateProbation},
		{"two results establish", []string{save, ok, empty, ok}, harness.StateEstablished},
		{"errors before the minimum", []string{save, fail, fail}, harness.StateProbation},
		{"repeated errors retire", []string{save, fail, ok, fail, fail}, harness.StateRetired},
		{"retired before established", []string{save, fail, fail, fail, ok, ok}, harness.StateRetired},
		{"harmed retires", []string{save, ok, ok, curated.EventHarmed}, harness.StateRetired},
		{"established counts start over", []string{save, fail, ok, ok, fail, ok}, harness.StateEstablished},
		{"established then failing", []string{save, ok, ok, fail, fail, fail}, harness.StateRetired},
		{"a redefinition starts over", []string{save, fail, fail, fail, save, ok}, harness.StateProbation},
	}
	for _, c := range cases {
		s := harness.Probation(usage(c.events...))
		if s.State != c.want || s.Reason == "" {
			t.Errorf("%s: %+v, want %s", c.name, s, c.want)
		}
	}
	s := harness.Probation(usage(save, ok, empty, fail))
	if s.Calls != 3 || s.Results != 1 || s.Empty != 1 || s.Errors != 1 {
		t.Fatalf("counts %+v", s)
	}
}

// summaryEntries is a branch whose latest compaction rendered note a1 and omitted a2 and a3
// (a2 is the omitted span's first id).
func summaryEntries(t *testing.T) []ledger.Entry {
	t.Helper()
	return []ledger.Entry{tf.RawMessage("u1", "hi"), compaction(t, "aaaaaaaaaaa1", "aaaaaaaaaaa2", "aaaaaaaaaaa3")}
}

// compaction is a compaction entry whose summary rendered the note shown and omitted the
// others (the first of them is the omitted span's first id), with reflection bbbbbbbbbbb1
// named only in appended instructions.
func compaction(t *testing.T, shown string, omitted ...string) ledger.Entry {
	t.Helper()
	return compactionIn(t, tables.Rows, shown, omitted...)
}

// compactionIn is compaction with the summary encoded in codec.
func compactionIn(t *testing.T, codec tables.Codec, shown string, omitted ...string) ledger.Entry {
	t.Helper()
	body := codec.Encode(
		tables.Table{Name: "notes", Fields: []string{"id", "when", "relevance", "content"}, Rows: [][]string{{shown, "2026-05-02", "high", "Shown."}}},
		tables.Table{Name: "omitted", Fields: []string{"from", "to", "count", "first_id"}, Rows: [][]string{{"2026-05-01", "2026-05-01", "2", omitted[0]}}},
	)
	observations := []ledger.Observation{tf.Observation(shown)}
	for _, id := range omitted {
		observations = append(observations, tf.Observation(id))
	}
	details := tf.MemoryDetails(tf.MemoryDetailsArgs{FullFold: true, Observations: observations, Reflections: []ledger.Reflection{tf.Reflection("bbbbbbbbbbb1", nil)}})
	return tf.CompactionEntry("c1", tf.CompactionArgs{FirstKeptEntryID: "u1", Details: details, Summary: ledger.TablesUsageInstructionsFor(codec) + "\n\n" + body + "\n\n---\n\nKeep bbbbbbbbbbb1 in mind? no."})
}

func TestVisibilityClassify(t *testing.T) {
	v := harness.VisibilityOf(summaryEntries(t), []string{"prefers-tabs"})
	cases := []struct {
		hits           []string
		outcome, where string
	}{
		{nil, curated.OutcomeOK, ""},
		{[]string{"aaaaaaaaaaa1"}, curated.OutcomeShown, curated.TargetSummary},
		{[]string{"aaaaaaaaaaa2"}, curated.OutcomeOmitted, curated.TargetSummary},
		{[]string{"aaaaaaaaaaa1", "aaaaaaaaaaa3"}, curated.OutcomeOmitted, curated.TargetSummary},
		{[]string{"prefers-tabs"}, curated.OutcomeShown, curated.TargetIndex},
		{[]string{"aaaaaaaaaaa1", "prefers-tabs"}, curated.OutcomeShown, curated.TargetSummary},
		{[]string{"aaaaaaaaaaa1", "newer0000000"}, curated.OutcomeOK, ""},
		// The reflection's id is only in the appended instructions, after the memory tables:
		// it still counts as shown, since the text names it.
		{[]string{"bbbbbbbbbbb1"}, curated.OutcomeShown, curated.TargetSummary},
	}
	for _, c := range cases {
		outcome, where := v.Classify(c.hits)
		if outcome != c.outcome || where != c.where {
			t.Errorf("Classify(%v) = %s/%s, want %s/%s", c.hits, outcome, where, c.outcome, c.where)
		}
	}
	// A summary in the jsonl codec lists its omitted spans the same way.
	jsonl := harness.VisibilityOf([]ledger.Entry{tf.RawMessage("u1", "hi"), compactionIn(t, tables.JSONLines, "aaaaaaaaaaa1", "aaaaaaaaaaa2", "aaaaaaaaaaa3")}, nil)
	if outcome, _ := jsonl.Classify([]string{"aaaaaaaaaaa2"}); outcome != curated.OutcomeOmitted {
		t.Fatalf("jsonl omitted span start: %s", outcome)
	}
	if outcome, _ := jsonl.Classify([]string{"aaaaaaaaaaa1"}); outcome != curated.OutcomeShown {
		t.Fatalf("jsonl shown: %s", outcome)
	}
	// Without a compaction nothing is in a summary.
	if outcome, _ := harness.VisibilityOf([]ledger.Entry{tf.RawMessage("u1", "hi")}, nil).Classify([]string{"aaaaaaaaaaa1"}); outcome != curated.OutcomeOK {
		t.Fatalf("no compaction: %s", outcome)
	}
}

func configOn() config.Config { return config.Defaults }
