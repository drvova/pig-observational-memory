package harness_test

import (
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/selection"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

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
	return tf.CompactionEntry("c1", tf.CompactionArgs{FirstKeptEntryID: "u1", Details: details, Summary: selection.TablesInstructionsFor(codec) + "\n\n" + body + "\n\n---\n\nKeep bbbbbbbbbbb1 in mind? no."})
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

// A summary encoded in a format the model declared under the name rows is decoded in that
// format, as the compaction recorded it, so its omitted span starts still count as omitted.
func TestVisibilityDecodesTheRecordedCodec(t *testing.T) {
	f := tables.Format{Name: "rows", Header: tables.HeaderCounted, Delimiter: ';', Quote: '\'', Escape: '\\', Escapes: "nrt", Indent: 4}
	codec, err := tables.Declared(f)
	if err != nil {
		t.Fatal(err)
	}
	entry := compactionIn(t, codec, "aaaaaaaaaaa1", "aaaaaaaaaaa2", "aaaaaaaaaaa3")
	details := entry.Details.(map[string]any)
	details["summaryCodec"], details["summaryFormat"] = "rows", f.Declaration()
	v := harness.VisibilityOf([]ledger.Entry{tf.RawMessage("u1", "hi"), entry}, nil)
	if outcome, _ := v.Classify([]string{"aaaaaaaaaaa2"}); outcome != curated.OutcomeOmitted {
		t.Fatalf("declared rows omitted span start: %s", outcome)
	}
	if outcome, _ := v.Classify([]string{"aaaaaaaaaaa1"}); outcome != curated.OutcomeShown {
		t.Fatalf("declared rows shown: %s", outcome)
	}
}

// A memory v2 compaction records what its summary shows (ledger.MemoryDetails.Shown): a record
// is shown exactly when it is listed there, even when its id is printed only as another line's
// pointer, and a shown record whose line carries no record id (the files line) is still shown.
func TestVisibilityReadsV2Shown(t *testing.T) {
	details := tf.MemoryDetails(tf.MemoryDetailsArgs{FullFold: true, Observations: []ledger.Observation{tf.Observation("aaaaaaaaaaa1"), tf.Observation("aaaaaaaaaaa2"), tf.Observation("aaaaaaaaaaa3")}})
	details["engine"], details["shown"] = "v2", []any{"aaaaaaaaaaa1", "aaaaaaaaaaa3"}
	summary := ledger.TablesSummaryIntro + "\n\n- [aaaaaaaaaaa1] fact (conflicts aaaaaaaaaaa2)\n\n---\n\nfiles: main.go (e7)"
	v := harness.VisibilityOf([]ledger.Entry{tf.RawMessage("u1", "hi"), tf.CompactionEntry("c1", tf.CompactionArgs{FirstKeptEntryID: "u1", Details: details, Summary: summary})}, nil)
	for id, want := range map[string]string{"aaaaaaaaaaa1": curated.OutcomeShown, "aaaaaaaaaaa2": curated.OutcomeOmitted, "aaaaaaaaaaa3": curated.OutcomeShown} {
		if outcome, _ := v.Classify([]string{id}); outcome != want {
			t.Errorf("%s: %s, want %s", id, outcome, want)
		}
	}
}
