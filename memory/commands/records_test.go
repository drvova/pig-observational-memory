package commands_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/commands"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/omtool"
	"github.com/drvova/pig-observational-memory/memory/session"
	tf "github.com/drvova/pig-observational-memory/memory/testfix"
)

func recordsBranch() []session.Entry {
	a := tf.Observation("aaaaaaaaaa01", func(o *ledger.Observation) {
		o.Content, o.Kind, o.Source, o.Authority = "The staging port is 5432.", "fact", ledger.SourceUserSaid, ledger.AuthorityUser
		o.Writer = ledger.Writer{Harness: "pig", Session: "s1", Engine: ledger.EngineGo}
		o.Frame = ledger.Frame{RepoID: "repo-1", Head: "0123456789abcdef", Path: "db/config.go"}
	})
	b := tf.Observation("aaaaaaaaaa02", func(o *ledger.Observation) {
		o.Content, o.Kind, o.Supersedes = "Actually the staging port is 5433.", "correction", []string{"aaaaaaaaaa01"}
	})
	dup := tf.Observation("aaaaaaaaaa03", func(o *ledger.Observation) { o.Content = "The staging port is 5433." })
	var many []ledger.Observation
	for i := range 23 {
		many = append(many, tf.Observation(fmt.Sprintf("bbbbbbbbbb%02d", i)))
	}
	sleep := ledger.SleepData{Version: 1, Trigger: ledger.SleepManual, ThroughEntryID: "e2", Records: 3,
		Duplicates: []ledger.SleepDuplicate{{ID: dup.ID, Of: b.ID, By: ledger.DuplicateByHash}},
		Verified:   []ledger.SleepVerification{{ID: a.ID, Head: "fedcba9876543210", Verdict: ledger.VerdictHolds}}}
	details := omtool.Details{Code: "x", Outcome: omtool.OutcomeOK, Hits: []string{b.ID}, Searches: []string{"staging port"}}
	return []session.Entry{
		tf.RawMessage("u1", "The staging port is 5432."),
		tf.ObservationsRecordedEntry("e1", append([]ledger.Observation{a, b, dup}, many...), "u1"),
		tf.CompactionEntry("c1", tf.CompactionArgs{FirstKeptEntryID: "u1", Details: map[string]any{"type": ledger.OMFolded, "version": 3, "fullFold": true, "observations": []any{}, "reflections": []any{}, "engine": "v2", "shown": []any{b.ID}}}),
		tf.MessageEntry("t1", map[string]any{"role": "toolResult", "toolCallId": "k1", "toolName": omtool.ToolName, "content": []any{map[string]any{"type": "text", "text": "ok"}}, "details": tf.ToJSONValue(details)}),
		tf.Entry(map[string]any{"type": "custom", "id": "s1", "parentId": nil, "timestamp": tf.DefaultTimestamp, "customType": ledger.OMSleep, "data": tf.ToJSONValue(sleep)}),
	}
}

// /om:view records pages the fold's records, bounded, and /om:view record shows one record's
// provenance, lifecycle, sleep records and where it was used.
func TestRecordViews(t *testing.T) {
	entries := recordsBranch()
	page := commands.RecordsPage(entries, "")
	lines := strings.Split(page, "\n")
	if len(lines) != commands.RecordsPageSize+2 || !strings.HasPrefix(lines[0], "── Records (page 1 of 2, 26 recorded) ──") ||
		!strings.Contains(page, "aaaaaaaaaa01 fact superseded user-said/user: The staging port is 5432.") ||
		!strings.Contains(lines[len(lines)-1], "/om:view records 2") {
		t.Fatalf("page 1:\n%s", page)
	}
	if last := commands.RecordsPage(entries, "2"); strings.Count(last, "\n") != 7 {
		t.Fatalf("page 2:\n%s", last)
	}
	if out := commands.RecordsPage(entries, "x"); out != commands.RecordsUsage {
		t.Fatalf("bad page %q", out)
	}
	a := commands.RecordView(entries, "aaaaaaaaaa01")
	for _, want := range []string{"Provenance: source user-said, authority user, hedge unknown; writer harness pig, session s1, engine go; frame repo repo-1 at 0123456", "path db/config.go",
		"Lifecycle: superseded", "Sleep: verified at fedcba9: holds", "shown in 0 summaries", "Scope: this session"} {
		if !strings.Contains(a, want) {
			t.Errorf("record a lacks %q:\n%s", want, a)
		}
	}
	b := commands.RecordView(entries, "aaaaaaaaaa02")
	for _, want := range []string{"Lifecycle: believed; supersedes aaaaaaaaaa01", "shown in 1 summaries (c1)", "returned by 1 memory tool calls, recalled by 0; searches: staging port"} {
		if !strings.Contains(b, want) {
			t.Errorf("record b lacks %q:\n%s", want, b)
		}
	}
	if d := commands.RecordView(entries, "aaaaaaaaaa03"); !strings.Contains(d, "Sleep: duplicate of aaaaaaaaaa02 (by hash), never shown") {
		t.Errorf("duplicate:\n%s", d)
	}
	if n := commands.RecordView(entries, "cccccccccccc"); n != "No record cccccccccccc." {
		t.Errorf("unknown %q", n)
	}
	if out, usage := commands.ViewOutputArgs(entries, "record"); !usage || out != commands.RecordsUsage {
		t.Errorf("record without an id %q", out)
	}
}
