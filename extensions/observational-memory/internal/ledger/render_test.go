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

// renderLines renders in the lines format with no other options.
func renderLines(reflections []ledger.Reflection, observations []ledger.Observation, maxTokens int) ledger.RenderedSummary {
	return ledger.RenderSummaryWithOptions(reflections, observations, maxTokens, ledger.RenderOptions{Format: ledger.FormatLines})
}

// linesSummary is renderLines' text.
func linesSummary(reflections []ledger.Reflection, observations []ledger.Observation, maxTokens int) string {
	return renderLines(reflections, observations, maxTokens).Text
}

func TestRenderSummaryBasics(t *testing.T) {
	if linesSummary(nil, nil, 0) != "" {
		t.Fatal("empty memory should render empty")
	}
	ref := tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"}, func(r *ledger.Reflection) { r.Content = "User prefers source-backed memory." })
	summary := linesSummary([]ledger.Reflection{ref}, nil, 0)
	if !strings.Contains(summary, "These are condensed memories from earlier in this session.") || !strings.Contains(summary, "use the memory tool") {
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
	summary = linesSummary(nil, []ledger.Observation{obs}, 0)
	if !strings.Contains(summary, "## Observations\n[aaaaaaaaaaaa] 2026-05-02 10:30 [high] User confirmed recall should use exact source entry ids.") {
		t.Fatal("observation line wrong")
	}
	obs2 := tf.Observation("aaaaaaaaaaaa", withSources("entry-user", "entry-tool"))
	summary = linesSummary([]ledger.Reflection{tf.Reflection("eeeeeeeeeeee", []string{"aaaaaaaaaaaa"})}, []ledger.Observation{obs2}, 0)
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
	rendered := renderLines(reflections, observations, 0)
	if len(rendered.Reflections) != 3 || len(rendered.Observations) != 4 || rendered.OmittedObservations != 0 || strings.Contains(rendered.Text, "omitted") {
		t.Fatal("unbudgeted render omitted records")
	}
	rendered = renderLines(reflections, observations, 500)
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
	rendered = renderLines(verbose, observations, 500)
	if len(rendered.Observations) < 2 || rendered.Observations[len(rendered.Observations)-1].ID != "a4aaaaaaaaaa" || len(rendered.Reflections) < 1 || rendered.Reflections[len(rendered.Reflections)-1].ID != "e6eeeeeeeeee" || rendered.OmittedReflections == 0 || !strings.Contains(rendered.Text, "older reflection") {
		t.Fatal("observations did not keep half the budget")
	}
	rendered = renderLines(reflections, observations[:1], 500)
	if len(rendered.Observations) != 1 || len(rendered.Reflections) != 3 {
		t.Fatal("reflections did not reclaim unused budget")
	}
	if linesSummary(reflections, observations, 0) != linesSummary(reflections, observations, -5) {
		t.Fatal("non-positive budget should be ignored")
	}
}

func TestRenderSummaryOversizedNewest(t *testing.T) {
	older := tf.Observation("a1aaaaaaaaaa", withContent("Short older fact."))
	newest := tf.Observation("a2aaaaaaaaaa", withContent(strings.Repeat("x", 4000)))
	rendered := renderLines(nil, []ledger.Observation{older, newest}, 500)
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
	rendered := renderLines(nil, observations, 390)
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
	rendered := ledger.RenderSummaryWithOptions(folded.Reflections, nil, 0, ledger.RenderOptions{ReflectionDates: dates, Format: ledger.FormatLines})
	if !strings.Contains(rendered.Text, "["+padID("e1", 'e')+"] (2023-05-20 to 2023-06-02) ") || !strings.Contains(rendered.Text, ledger.DatedReflectionsNote) || !strings.Contains(rendered.Text, "["+padID("e3", 'e')+"] Reflection") {
		t.Fatalf("dated render wrong:\n%s", rendered.Text)
	}
	if plain := renderLines(folded.Reflections, nil, 0); strings.Contains(plain.Text, ledger.DatedReflectionsNote) {
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
	rendered := renderLines(nil, observations, 390)
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

func TestClippedOverflowListsWhatTheBudgetCannotHoldInFull(t *testing.T) {
	observations := []ledger.Observation{}
	for i := 0; i < 12; i++ {
		observations = append(observations, tf.Observation(padID(fmt.Sprintf("c%d", i), 'c'), func(o *ledger.Observation) {
			o.Content = fmt.Sprintf("User talked through topic %d at length ", i) + strings.Repeat("with many more words ", 8)
		}))
	}
	plain := renderLines(nil, observations, 600)
	if plain.OmittedObservations == 0 {
		t.Fatal("the budget should force omissions in this fixture")
	}
	clipped := ledger.RenderSummaryWithOptions(nil, observations, 600, ledger.RenderOptions{ClippedOverflow: true, Format: ledger.FormatLines})
	if clipped.ClippedObservations == 0 || clipped.OmittedObservations >= plain.OmittedObservations {
		t.Fatalf("clipped %d, omitted %d (plain omitted %d): overflow should be listed clipped, not omitted", clipped.ClippedObservations, clipped.OmittedObservations, plain.OmittedObservations)
	}
	if !strings.Contains(clipped.Text, ledger.ClippedObservationsHeading) || !strings.Contains(clipped.Text, "…") {
		t.Fatalf("clipped lines missing from:\n%s", clipped.Text)
	}
	if got := (len(clipped.Text) + 3) / 4; got > 600 {
		t.Fatalf("summary is ~%d tokens, over the 600 budget", got)
	}
	ample := ledger.RenderSummaryWithOptions(nil, observations, 100_000, ledger.RenderOptions{ClippedOverflow: true, Format: ledger.FormatLines})
	if ample.ClippedObservations != 0 || len(ample.Observations) != len(observations) {
		t.Fatalf("with room for every line in full nothing should be clipped (clipped %d, full %d)", ample.ClippedObservations, len(ample.Observations))
	}
}

func TestObservationToObserverLine(t *testing.T) {
	o := ledger.Observation{ID: "aaaaaaaaaaaa", Timestamp: "2026-05-02 10:30", Relevance: "high", Content: "User finished the game in 25 hours."}
	if got := ledger.ObservationToObserverLine(o); got != ledger.ObservationToSummaryLine(o) {
		t.Fatalf("unstructured %q", got)
	}
	o.Kind, o.Subject, o.Scope, o.Supersedes = "correction", "gaming hours", "normal difficulty", []string{"bbbbbbbbbbbb", "cccccccccccc"}
	want := "[aaaaaaaaaaaa] 2026-05-02 10:30 [high] User finished the game in 25 hours. {kind: correction; subject: gaming hours; scope: normal difficulty; supersedes: bbbbbbbbbbbb cccccccccccc}"
	if got := ledger.ObservationToObserverLine(o); got != want {
		t.Fatalf("structured\n got %q\nwant %q", got, want)
	}
	o.Kind, o.Scope, o.Supersedes = "", "", nil
	if got := ledger.ObservationToObserverLine(o); !strings.HasSuffix(got, " {subject: gaming hours}") {
		t.Fatalf("subject only %q", got)
	}
}
