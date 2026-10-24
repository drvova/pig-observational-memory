package ledger_test

import (
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
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
	if !strings.Contains(rendered.Text, "(2 observations omitted to fit the summary budget") || !strings.Contains(rendered.Text, "[a4aaaaaaaaaa]") || strings.Contains(rendered.Text, "[a1aaaaaaaaaa]") {
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

// The omission notice uses plain digits, as the TypeScript version renders it.
func TestOmissionNoticeUsesPlainDigits(t *testing.T) {
	observations := make([]ledger.Observation, 0, 1200)
	for i := 0; i < 1200; i++ {
		observations = append(observations, tf.Observation(fmt.Sprintf("%012x", i+1)))
	}
	rendered := ledger.RenderSummaryWithBudget(nil, observations, 390)
	match := regexp.MustCompile(`\(([0-9,]+) observations omitted`).FindStringSubmatch(rendered.Text)
	if match == nil || len(match[1]) < 4 || strings.Contains(match[1], ",") {
		t.Fatalf("notice count %v in %q", match, rendered.Text)
	}
}

func TestReflectionDateRangesAndDatedRender(t *testing.T) {
	at := func(ts string) func(*ledger.Observation) { return func(o *ledger.Observation) { o.Timestamp = ts } }
	o1 := tf.Observation(padID("a1", 'a'), at("2023-05-20 10:00"))
	o2 := tf.Observation(padID("a2", 'a'), at("2023-06-02 18:30"))
	o3 := tf.Observation(padID("a3", 'a'), at("2023-06-02 09:15"))
	folded := ledger.FoldedLedger{
		ObservationsByID: map[string]ledger.Observation{o1.ID: o1, o2.ID: o2, o3.ID: o3},
		Reflections: []ledger.Reflection{
			tf.Reflection(padID("e1", 'e'), []string{o1.ID, o2.ID}),
			tf.Reflection(padID("e2", 'e'), []string{o2.ID, o3.ID}),
			tf.Reflection(padID("e3", 'e'), []string{"unknownaaaaa"}),
		},
	}
	dates := ledger.ReflectionDateRanges(folded)
	want := map[string]string{padID("e1", 'e'): "2023-05-20 to 2023-06-02", padID("e2", 'e'): "2023-06-02"}
	if !reflect.DeepEqual(dates, want) {
		t.Fatalf("dates = %v, want %v", dates, want)
	}
	rendered := ledger.RenderSummaryWithOptions(folded.Reflections, nil, 0, ledger.RenderOptions{ReflectionDates: dates})
	if !strings.Contains(rendered.Text, "["+padID("e1", 'e')+"] (2023-05-20 to 2023-06-02) ") || !strings.Contains(rendered.Text, ledger.DatedReflectionsNote) || !strings.Contains(rendered.Text, "["+padID("e3", 'e')+"] Reflection") {
		t.Fatalf("dated render wrong:\n%s", rendered.Text)
	}
	if plain := ledger.RenderSummaryWithBudget(folded.Reflections, nil, 0); strings.Contains(plain.Text, ledger.DatedReflectionsNote) {
		t.Fatal("an undated render must not carry the dated-reflections note")
	}
}

func TestSummaryKeepsOlderSpecificsOverNewerChatter(t *testing.T) {
	content := func(text string) func(*ledger.Observation) { return func(o *ledger.Observation) { o.Content = text } }
	observations := []ledger.Observation{
		tf.Observation(padID("a1", 'a'), content("User bought boots for $500 at Nordstrom.")),
	}
	for i, id := range []string{"a2", "a3", "a4", "a5", "a6"} {
		observations = append(observations, tf.Observation(padID(id, 'a'), content("User chatted about general topics "+strings.Repeat("and more ", 3+i))))
	}
	rendered := ledger.RenderSummaryWithBudget(nil, observations, 390)
	ids := tf.IDs(rendered.Observations)
	if len(ids) == 0 || ids[0] != padID("a1", 'a') {
		t.Fatalf("kept %v: the oldest observation, the only one with a price and a name, should outlast newer chatter", ids)
	}
	if ids[len(ids)-1] != padID("a6", 'a') {
		t.Fatalf("kept %v: the recency share must still keep the newest observation", ids)
	}
	if rendered.OmittedObservations == 0 {
		t.Fatal("the budget should force omissions in this fixture")
	}
}
