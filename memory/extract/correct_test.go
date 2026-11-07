package extract

import (
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/omtool"
	"github.com/drvova/pig-observational-memory/memory/session"
	tf "github.com/drvova/pig-observational-memory/memory/testfix"
)

// om.Correct's write becomes a correction record, never an edit (phase 7): on the user's quoted
// words it supersedes the record with the user's authority; without, it only possibly updates
// it with the model's, so the three-way rule weighs it beside the record.
func TestCorrectionAsSupersession(t *testing.T) {
	target := tf.Observation("aaaaaaaaaa01", func(o *ledger.Observation) {
		o.Content, o.Kind, o.Subject, o.SourceEntryIDs = "The staging port is 5432.", "fact", "staging", []string{"u0"}
	})
	code := func(id string) call { return call{id, omtool.ToolName, map[string]any{"code": "om.Correct(...)"}} }
	codeResult := func(id, callID string, w omtool.Write) session.Entry {
		e := result(id, callID, omtool.ToolName, "Recorded", false)
		e.Message["details"] = tf.ToJSONValue(omtool.Details{Code: "x", Outcome: omtool.OutcomeOK, Writes: []omtool.Write{w}})
		return e
	}
	entries := []session.Entry{
		user("u0", "The staging port is 5432."),
		tf.ObservationsRecordedEntry("r0", []ledger.Observation{target}, "u0"),
		user("u1", "No, the staging port is 5433 now."),
		assistant("a1", code("c1")),
		codeResult("t1", "c1", omtool.Write{Op: omtool.WriteCorrect, Name: target.ID, Description: "The staging port is 5433.", Type: omtool.CorrectQuoted}),
		assistant("a2", code("c2")),
		codeResult("t2", "c2", omtool.Write{Op: omtool.WriteCorrect, Name: target.ID, Description: "The staging port is 6000."}),
		assistant("a3", code("c3")),
		codeResult("t3", "c3", omtool.Write{Op: omtool.WriteCorrect, Name: "bbbbbbbbbbbb", Description: "No such record."}),
	}
	r := run(entries, func(in *Input) { in.Start = 2 })
	quoted := find(t, r, "correction", "The staging port is 5433.")
	if len(quoted.Supersedes) != 1 || quoted.Supersedes[0] != target.ID || quoted.Source != ledger.SourceUserSaid ||
		quoted.Authority != ledger.AuthorityUser || quoted.Origin != ledger.OriginModel || quoted.Subject != "staging" {
		t.Fatalf("quoted correction %+v", quoted)
	}
	unquoted := find(t, r, "correction", "The staging port is 6000.")
	if len(unquoted.Supersedes) != 0 || len(unquoted.PossiblyUpdates) == 0 || unquoted.PossiblyUpdates[0] != target.ID ||
		unquoted.Authority != ledger.AuthorityModel {
		t.Fatalf("unquoted correction %+v", unquoted)
	}
	absent(t, r, "No such record.")
	// The three-way rule: the model's correction of the user's fact stands contested beside it.
	if d := ledger.Supersession(unquoted, target); d != ledger.DecisionContested && d != ledger.DecisionKeepBoth {
		t.Fatalf("decision %q", d)
	}
}
