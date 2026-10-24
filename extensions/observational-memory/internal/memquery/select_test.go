package memquery

import (
	"fmt"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
)

// Select returns tables with the record ids of their rows, reads records given directly, is
// not cut by the default limit, honours an explicit one, and refuses writes, recall and
// sources the caller does not allow.
func TestSelectReadsGivenRecords(t *testing.T) {
	observations := []ledger.Observation{}
	for i := 0; i < DefaultLimit+5; i++ {
		subject := "baking"
		if i%2 == 1 {
			subject = "travel"
		}
		observations = append(observations, ledger.Observation{ID: fmt.Sprintf("%012x", i+1), Timestamp: "2026-05-02 10:30", Relevance: "low", Subject: subject, Content: fmt.Sprintf("note %d", i)})
	}
	env := Env{Records: &Records{Observations: observations}}
	sel, err := Select(env, "notes | group by subject", SourceNotes)
	if err != nil {
		t.Fatal(err)
	}
	if len(sel.Tables) != 2 || sel.Tables[0].Name != "notes.baking" || sel.Count != len(observations) || len(sel.IDs[0]) != len(sel.Tables[0].Rows) || sel.IDs[0][0] != observations[0].ID {
		t.Fatalf("selection %+v", sel)
	}
	if sel, _ := Select(env, "notes | limit 3"); sel.Count != 3 {
		t.Fatalf("limit: %d", sel.Count)
	}
	if sel, _ := Select(env, "notes | where subject=travel | count"); sel.Count != 12 || sel.IDs[0][0] != "" {
		t.Fatalf("count: %+v", sel)
	}
	for _, q := range []string{"recall aaaaaaaaaaaa", "save type=user name=x description=y", "history | limit 1"} {
		if _, err := Select(env, q, SourceNotes, SourceReflections); err == nil || !strings.Contains(err.Error(), "notes") {
			t.Fatalf("%s: err %v", q, err)
		}
	}
}
