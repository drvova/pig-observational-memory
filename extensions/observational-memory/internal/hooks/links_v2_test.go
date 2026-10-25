package hooks

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/consolidate"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// Under memory v2 a turn end records a plan as proposed and a later "scrap that" as a retraction
// superseding it, by code; the compaction's summary shows the retraction with its recall pointer
// and drops the plan it withdrew. Neither makes a request.
func TestV2StatusesAndRetractionsWithoutRequests(t *testing.T) {
	counter := testfix.CountRequests(t)
	entries := append(codingTurn("x", "Fix the loader for config version 3."),
		userMessage("u2", "We will migrate the orders table to Postgres next week."),
		assistantMessage("a2", "Understood."),
		userMessage("u3", "Ok, scrap that."),
	)
	rt, h := v2Setup(t, entries)
	OnTurnEnd(rt, h)

	var plan, retraction ledger.Observation
	for _, a := range appendedOfType(h, ledger.OMObservationsRecorded) {
		data, _ := ledger.ObservationsRecordedDataFrom(a.Data)
		for _, o := range data.Observations {
			switch {
			case strings.Contains(o.Content, "Postgres"):
				plan = o
			case strings.Contains(o.Content, "scrap that"):
				retraction = o
			}
		}
	}
	if plan.Status != ledger.StatusProposed || retraction.Status != ledger.StatusRetracted || len(retraction.Supersedes) != 1 || retraction.Supersedes[0] != plan.ID {
		t.Fatalf("plan %+v\nretraction %+v", plan, retraction)
	}
	if counter.Total() != 0 {
		t.Fatalf("turn end requests %v", counter.ByStage())
	}

	h.SetBranch(append(h.Entries, userMessage("u4", "Now the docs.")))
	c := v2CompactionRun(t, rt, h, "u4")
	if !strings.Contains(c.Summary, "Ok, scrap that. (was "+plan.ID+")") || strings.Contains(c.Summary, "orders table") {
		t.Fatalf("summary:\n%s", c.Summary)
	}
	for _, o := range c.Details.Observations {
		if o.ID == plan.ID {
			t.Fatalf("the withdrawn plan stays active: %+v", o)
		}
	}
	if counter.Total() != 0 || h.ModelsFake.Streams.Calls() != 0 {
		t.Fatalf("requests %v", counter.ByStage())
	}
}

// Level 1 settles a possible update it shows both records of, inside its one request: a
// confirmed link becomes a supersede, so the summary shows the later value with its recall
// pointer and drops the earlier one; the report counts it.
func TestV2Level1ConfirmsPossibleUpdate(t *testing.T) {
	const oldID, laterID = "cccccccccc01", "cccccccccc02"
	old := testfix.Observation(oldID, func(o *ledger.Observation) {
		o.Content, o.SourceEntryIDs, o.Origin, o.Kind, o.Subject = "The staging deploy uses port 8080", []string{"u0"}, ledger.OriginCode, "fact", "deploy"
	})
	later := testfix.Observation(laterID, func(o *ledger.Observation) {
		o.Content, o.SourceEntryIDs, o.Origin, o.Kind, o.Subject = "The staging deploy uses port 9090", []string{"u1"}, ledger.OriginCode, "fact", "deploy"
		o.PossiblyUpdates = []string{oldID}
	})
	entries := []ledger.Entry{
		userMessage("u0", "Staging deploys on port 8080"+strings.Repeat(", running the usual checks", 40)),
		testfix.ObservationsRecordedEntry("om-0", []ledger.Observation{old}, "u0"),
		userMessage("u1", "Staging now uses port 9090"+strings.Repeat(", after the usual checks", 40)),
		assistantMessage("a1", "Noted."),
		testfix.ObservationsRecordedEntry("om-1", []ledger.Observation{later}, "a1"),
		userMessage("u2", "Next please"),
	}
	counter := testfix.CountRequests(t)
	rt, h := v2Setup(t, entries)
	cfg := rt.Config()
	cfg.ConsolidateOff = false
	rt.SetConfig(cfg)
	h.ModelsFake.Streams.Replies = []map[string]any{testfix.AssistantToolCall("call-1", consolidate.ToolName, map[string]any{
		"links": []any{map[string]any{"id": laterID, "updates": oldID, "verdict": consolidate.VerdictConfirm}},
	})}

	outcome := runV2Hook(rt, h, v2Event(entries, "u2", 20_000, "threshold"))
	if outcome.Compaction == nil {
		t.Fatalf("outcome %+v", outcome)
	}
	c := outcome.Compaction
	if counter.Total() != 1 || counter.Stage(consolidate.Stage) != 1 {
		t.Fatalf("requests %v", counter.ByStage())
	}
	report := c.Details.Consolidation
	if report == nil || !report.Applied || report.Confirmed != 1 || report.Rejected != 0 || report.Records != 0 {
		t.Fatalf("report %+v", report)
	}
	if !strings.Contains(c.Summary, "port 9090") || !strings.Contains(c.Summary, "(was "+oldID+")") || strings.Contains(c.Summary, "port 8080") {
		t.Fatalf("summary:\n%s", c.Summary)
	}
	for _, o := range c.Details.Observations {
		if o.ID == oldID || o.ID == laterID {
			t.Fatalf("a settled record stayed active: %+v", o)
		}
	}
}
