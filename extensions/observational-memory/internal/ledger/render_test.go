package ledger_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/testfix"
)

func TestRenderSummaryBasics(t *testing.T) {
	if ledger.RenderSummary(nil, nil, 0) != "" {
		t.Fatal("empty memory should render empty")
	}
	ref := tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"}, func(r *ledger.Reflection) { r.Content = "User prefers source-backed memory." })
	summary := ledger.RenderSummary([]ledger.Reflection{ref}, nil, 0)
	if !strings.Contains(summary, "These are condensed memories from earlier in this session.") || !strings.Contains(summary, "use the recall tool") {
		t.Fatal("usage instructions missing")
	}
	if !strings.Contains(summary, "## Reflections\n[eeeeeeeeeeee] User prefers source-backed memory.") {
		t.Fatal("reflection line wrong")
	}
	obs := tf.Observation("aaaaaaaaaaaa", func(o *ledger.Observation) {
		o.Content = "User confirmed recall should use exact source entry ids."
		o.Timestamp = "2026-05-02 10:30"
		o.Relevance = "high"
	})
	summary = ledger.RenderSummary(nil, []ledger.Observation{obs}, 0)
	if !strings.Contains(summary, "## Observations\n[aaaaaaaaaaaa] 2026-05-02 10:30 [high] User confirmed recall should use exact source entry ids.") {
		t.Fatal("observation line wrong")
	}
	obs2 := tf.Observation("aaaaaaaaaaaa", withSources("entry-user", "entry-tool"))
	summary = ledger.RenderSummary([]ledger.Reflection{tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})}, []ledger.Observation{obs2}, 0)
	for _, forbidden := range []string{"sourceEntryIds", "supportingObservationIds", "entry-user", "entry-tool", "legacy", "[object Object]"} {
		if strings.Contains(summary, forbidden) {
			t.Fatalf("summary leaked %q", forbidden)
		}
	}
}

func budgetFixtures() ([]ledger.Reflection, []ledger.Observation) {
	reflections := []ledger.Reflection{}
	for i, id := range []string{"e1", "e2", "e3"} {
		n := string(rune('1' + i))
		reflections = append(reflections, tf.Reflection(padID(id, 'e'), []string{"aaaaaaaaaaaa"}, func(r *ledger.Reflection) { r.Content = "Reflection " + n + " " + strings.Repeat("r", 120) }))
	}
	observations := []ledger.Observation{}
	for i, id := range []string{"a1", "a2", "a3", "a4"} {
		n := string(rune('1' + i))
		observations = append(observations, tf.Observation(padID(id, 'a'), withContent("Observation "+n+" "+strings.Repeat("o", 120))))
	}
	return reflections, observations
}

func TestRenderSummaryBudget(t *testing.T) {
	reflections, observations := budgetFixtures()
	rendered := ledger.RenderSummaryWithBudget(reflections, observations, 0)
	if len(rendered.Reflections) != 3 || len(rendered.Observations) != 4 || rendered.OmittedObservations != 0 || strings.Contains(rendered.Text, "omitted") {
		t.Fatal("unbudgeted render omitted records")
	}
	rendered = ledger.RenderSummaryWithBudget(reflections, observations, 500)
	if len(rendered.Reflections) != 3 || !reflect.DeepEqual(tf.IDs(rendered.Observations), []string{"a3aaaaaaaaaa", "a4aaaaaaaaaa"}) || rendered.OmittedReflections != 0 || rendered.OmittedObservations != 2 {
		t.Fatalf("budgeted render %v omitted %d/%d", tf.IDs(rendered.Observations), rendered.OmittedReflections, rendered.OmittedObservations)
	}
	if !strings.Contains(rendered.Text, "(2 older observations omitted to fit the summary budget") || !strings.Contains(rendered.Text, "[a4aaaaaaaaaa]") || strings.Contains(rendered.Text, "[a1aaaaaaaaaa]") {
		t.Fatal("omission notice wrong")
	}
	verbose := []ledger.Reflection{}
	for i, id := range []string{"e1", "e2", "e3", "e4", "e5", "e6"} {
		n := string(rune('1' + i))
		verbose = append(verbose, tf.Reflection(padID(id, 'e'), []string{"aaaaaaaaaaaa"}, func(r *ledger.Reflection) { r.Content = "Reflection " + n + " " + strings.Repeat("r", 400) }))
	}
	rendered = ledger.RenderSummaryWithBudget(verbose, observations, 500)
	if len(rendered.Observations) < 2 || rendered.Observations[len(rendered.Observations)-1].ID != "a4aaaaaaaaaa" || len(rendered.Reflections) < 1 || rendered.Reflections[len(rendered.Reflections)-1].ID != "e6eeeeeeeeee" || rendered.OmittedReflections == 0 || !strings.Contains(rendered.Text, "older reflection") {
		t.Fatal("observations did not keep half the budget")
	}
	rendered = ledger.RenderSummaryWithBudget(reflections, observations[:1], 500)
	if len(rendered.Observations) != 1 || len(rendered.Reflections) != 3 {
		t.Fatal("reflections did not reclaim unused budget")
	}
	if ledger.RenderSummary(reflections, observations, 0) != ledger.RenderSummary(reflections, observations, -5) {
		t.Fatal("non-positive budget should be ignored")
	}
}

func TestRenderSummaryOversizedNewest(t *testing.T) {
	older := tf.Observation("a1aaaaaaaaaa", withContent("Short older fact."))
	newest := tf.Observation("a2aaaaaaaaaa", withContent(strings.Repeat("x", 4000)))
	rendered := ledger.RenderSummaryWithBudget(nil, []ledger.Observation{older, newest}, 500)
	if !reflect.DeepEqual(tf.IDs(rendered.Observations), []string{"a1aaaaaaaaaa"}) || !strings.Contains(rendered.Text, "[a1aaaaaaaaaa] ") || !strings.Contains(rendered.Text, "Short older fact.") {
		t.Fatal("shorter older observation lost behind an oversized newest one")
	}
}
