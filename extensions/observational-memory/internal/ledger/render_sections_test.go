package ledger_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

func sectionOf(name string, observations []ledger.Observation) ledger.SummarySection {
	t := tables.Table{Name: name, Fields: []string{"id", "content"}}
	ids := []string{}
	for _, o := range observations {
		t.Rows = append(t.Rows, []string{o.ID, o.Content})
		ids = append(ids, o.ID)
	}
	return ledger.SummarySection{Table: t, IDs: ids}
}

// The kernel lays a summary query's sections out under the budget and always discloses every
// record the summary does not show, whether the query left it out or it did not fit.
func TestSectionsSummaryDisclosesEveryOmittedRecord(t *testing.T) {
	observations := []ledger.Observation{}
	for i := 0; i < 30; i++ {
		observations = append(observations, ledger.Observation{ID: fmt.Sprintf("%012x", i+1), Timestamp: fmt.Sprintf("2026-05-%02d 10:00", i+1), Relevance: "low", Content: fmt.Sprintf("Note number %d about something worth keeping for a while.", i)})
	}
	reflections := []ledger.Reflection{{ID: "eeeeeeeeeeee", Content: "A durable fact."}}
	// The query selects the even notes only.
	even := []ledger.Observation{}
	for i, o := range observations {
		if i%2 == 0 {
			even = append(even, o)
		}
	}
	full := ledger.RenderSectionsSummary([]ledger.SummarySection{sectionOf("picked", even)}, reflections, observations, 0, ledger.RenderOptions{})
	if !strings.HasPrefix(full.Text, ledger.TablesSummaryIntro) || !strings.Contains(full.Text, "picked[15]") || full.OmittedObservations != 15 || full.OmittedReflections != 1 {
		t.Fatalf("full render: %d %d\n%s", full.OmittedObservations, full.OmittedReflections, full.Text)
	}
	if !strings.Contains(full.Text, "omitted[") || !strings.Contains(full.Text, "eeeeeeeeeeee") {
		t.Fatalf("the records the query left out are not disclosed:\n%s", full.Text)
	}
	if _, ok := ledger.TablesSummaryBody(full.Text); !ok {
		t.Fatal("the summary body cannot be found")
	}
	// Under a budget the newest rows are kept, the estimate stays within the budget, and every
	// left-out record is still disclosed.
	budget := (len(full.Text)+3)/4 - 120
	cut := ledger.RenderSectionsSummary([]ledger.SummarySection{sectionOf("picked", even)}, reflections, observations, budget, ledger.RenderOptions{})
	if got := (len(cut.Text) + 3) / 4; got > budget {
		t.Fatalf("estimate %d over budget %d", got, budget)
	}
	if len(cut.Observations) == 0 || len(cut.Observations) >= 15 || cut.Observations[len(cut.Observations)-1].ID != even[len(even)-1].ID {
		t.Fatalf("kept %d, last %+v", len(cut.Observations), cut.Observations)
	}
	if cut.OmittedObservations != 30-len(cut.Observations) || !strings.Contains(cut.Text, "omitted[") {
		t.Fatalf("omitted %d\n%s", cut.OmittedObservations, cut.Text)
	}
}
