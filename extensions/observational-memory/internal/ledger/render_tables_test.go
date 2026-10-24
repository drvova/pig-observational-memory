package ledger_test

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

var updateGolden = flag.Bool("update", false, "rewrite the tables-format golden files in testdata")

// checkGolden compares got with testdata/name; with -update it rewrites the
// file instead. The lines goldens were written by the renderer before the
// tables format existed and are never updated.
func checkGolden(t *testing.T, name, got string, updatable bool) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *updateGolden && updatable {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("%s differs\n--- got\n%s\n--- want\n%s", name, got, want)
	}
}

func linesGoldenFixture() ([]ledger.Reflection, []ledger.Observation, map[string]string) {
	at := func(ts, content string) func(*ledger.Observation) {
		return func(o *ledger.Observation) { o.Timestamp = ts; o.Content = content }
	}
	observations := []ledger.Observation{
		tf.Observation(padID("a1", 'a'), at("2023-05-10 09:00", "User chatted about general topics "+strings.Repeat("and more ", 12))),
		tf.Observation(padID("a2", 'a'), at("2023-05-12 10:00", "User bought boots for $500 at Nordstrom.")),
		tf.Observation(padID("a3", 'a'), at("2023-05-14 11:00", "User chatted about the weather "+strings.Repeat("at length ", 10))),
		tf.Observation(padID("a4", 'a'), at("2023-05-20 12:00", "User started a sourdough starter yesterday.")),
	}
	reflections := []ledger.Reflection{
		tf.Reflection(padID("e1", 'e'), []string{padID("a2", 'a')}, func(r *ledger.Reflection) { r.Content = "User shops for footwear." }),
		tf.Reflection(padID("e2", 'e'), []string{padID("a4", 'a')}, func(r *ledger.Reflection) { r.Content = "User bakes " + strings.Repeat("bread ", 30) }),
	}
	dates := map[string]string{padID("e1", 'e'): "2023-05-12", padID("e2", 'e'): "2023-05-14 to 2023-05-20"}
	return reflections, observations, dates
}

// The lines format renders byte for byte what it rendered before the tables
// format was added (the goldens come from that renderer).
func TestLinesFormatUnchanged(t *testing.T) {
	reflections, observations, dates := linesGoldenFixture()
	for _, budget := range []int{0, 400} {
		got := ledger.RenderSummaryWithOptions(reflections, observations, budget, ledger.RenderOptions{ReflectionDates: dates, Format: ledger.FormatLines})
		checkGolden(t, fmt.Sprintf("lines_%d.golden", budget), got.Text, false)
	}
}

// mixedLedger has legacy records without a subject, two subjects, a
// supersession, relative dates, reflections and enough chatter that a tight
// budget omits some of it.
func mixedLedger() ([]ledger.Reflection, []ledger.Observation, map[string]string) {
	obs := func(id, ts, content string, modify ...func(*ledger.Observation)) ledger.Observation {
		return tf.Observation(padID(id, 'a'), append([]func(*ledger.Observation){func(o *ledger.Observation) { o.Timestamp = ts; o.Content = content }}, modify...)...)
	}
	subject := func(kind ledger.ObservationKind, subject, scope string, supersedes ...string) func(*ledger.Observation) {
		return func(o *ledger.Observation) {
			o.Kind, o.Subject, o.Scope, o.Supersedes = kind, subject, scope, supersedes
		}
	}
	observations := []ledger.Observation{
		obs("a1", "2023-05-08 09:00", "User chatted about general topics "+strings.Repeat("and more ", 12)),
		obs("a2", "2023-05-10 09:30", "User started a sourdough starter yesterday."),
		obs("b1", "2023-05-12 10:00", "User bakes bread twice a week.", subject("fact", "baking", "")),
		obs("g1", "2023-05-15 20:00", "User finished the game in 30 hours.", subject("event", "gaming hours", "hard difficulty")),
		obs("a3", "2023-05-16 11:00", "User chatted about the weather "+strings.Repeat("at length ", 10)),
		obs("b2", "2023-05-20 08:15", "User now bakes bread three times a week, since last Tuesday.", subject("correction", "baking", "", padID("b1", 'a'))),
		obs("a4", "2023-05-21 12:00", "User asked for a reading list, \"short ones\".", func(o *ledger.Observation) { o.Relevance = "high" }),
	}
	reflections := []ledger.Reflection{
		tf.Reflection(padID("e1", 'e'), []string{padID("b1", 'a'), padID("b2", 'a')}, func(r *ledger.Reflection) { r.Content = "User bakes at home: bread, every week." }),
		tf.Reflection(padID("e2", 'e'), []string{"unknownaaaaa"}, func(r *ledger.Reflection) { r.Content = "User prefers short answers." }),
	}
	dates := map[string]string{padID("e1", 'e'): "2023-05-12 to 2023-05-20"}
	return reflections, observations, dates
}

func TestTablesFormatGolden(t *testing.T) {
	reflections, observations, dates := mixedLedger()
	full := ledger.RenderSummaryWithOptions(reflections, observations, 0, ledger.RenderOptions{ReflectionDates: dates})
	if full.OmittedObservations != 0 || full.OmittedReflections != 0 || strings.Contains(full.Text, "omitted[") {
		t.Fatalf("an unbudgeted render omitted records:\n%s", full.Text)
	}
	checkGolden(t, "tables_full.golden", full.Text, true)
	tight := ledger.RenderSummaryWithOptions(reflections, observations, 540, ledger.RenderOptions{ReflectionDates: dates})
	if tight.OmittedObservations == 0 || !strings.Contains(tight.Text, "\nomitted[") {
		t.Fatalf("the budget should omit records here:\n%s", tight.Text)
	}
	checkGolden(t, "tables_tight.golden", tight.Text, true)
	// Both goldens decode with the codec they were written in.
	for _, text := range []string{full.Text, tight.Text} {
		if _, err := tables.Rows.Decode(strings.TrimPrefix(text, ledger.TablesUsageInstructions+"\n\n")); err != nil {
			t.Fatalf("tables do not decode: %v", err)
		}
	}
}

func TestTablesFormatLayout(t *testing.T) {
	reflections, observations, dates := mixedLedger()
	text := ledger.RenderSummaryWithOptions(reflections, observations, 0, ledger.RenderOptions{ReflectionDates: dates}).Text
	decoded, err := tables.Rows.Decode(strings.TrimPrefix(text, ledger.TablesUsageInstructions+"\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, table := range decoded {
		names = append(names, table.Name)
	}
	// gaming hours' newest record is older than baking's, so it comes first.
	if got := strings.Join(names, " "); got != "reflections notes.gaming_hours notes.baking notes" {
		t.Fatalf("tables %q", got)
	}
	baking := decoded[2]
	if id, _ := baking.Cell(0, "id"); id != padID("b1", 'a') {
		t.Fatalf("baking is not chronological: %v", baking.Rows)
	}
	if by, _ := baking.Cell(0, "superseded_by"); by != padID("b2", 'a') {
		t.Fatalf("superseded record lost its replacement: %v", baking.Rows)
	}
	if content, _ := baking.Cell(1, "content"); !strings.Contains(content, "last Tuesday [=2023-05-16]") {
		t.Fatalf("relative date not resolved against the record's date: %q", content)
	}
	if when, _ := baking.Cell(1, "when"); when != "2023-05-20" {
		t.Fatalf("when %q", when)
	}
	if _, ok := baking.Cell(1, "scope"); ok {
		t.Fatal("an empty scope should be absent")
	}
	if scope, _ := decoded[1].Cell(0, "scope"); scope != "hard difficulty" {
		t.Fatalf("scope %q", scope)
	}
	notes := decoded[3]
	if strings.Join(notes.Fields, ",") != "id,when,relevance,content" || len(notes.Rows) != 4 {
		t.Fatalf("legacy notes %v %v", notes.Fields, notes.Rows)
	}
	if content, _ := notes.Cell(1, "content"); content != "User started a sourdough starter yesterday [=2023-05-09]." {
		t.Fatalf("legacy relative date %q", content)
	}
	if d, _ := decoded[0].Cell(0, "dates"); d != "2023-05-12..2023-05-20" {
		t.Fatalf("reflection dates %q", d)
	}
	if _, ok := decoded[0].Cell(1, "dates"); ok {
		t.Fatal("an undated reflection should have no dates")
	}
}

// A legacy record replaced by a subject record gets a superseded_by column in
// the notes table.
func TestTablesFormatLegacySupersession(t *testing.T) {
	old := tf.Observation(padID("a1", 'a'), withContent("User owns two bikes."))
	replacement := tf.Observation(padID("b1", 'a'), func(o *ledger.Observation) {
		o.Content, o.Subject, o.Supersedes = "User owns three bikes.", "bikes", []string{old.ID}
	})
	text := ledger.RenderSummaryWithBudget(nil, []ledger.Observation{old, replacement}, 0).Text
	if !strings.Contains(text, "notes[1]{id,when,relevance,content,superseded_by}:\n  "+old.ID+",2026-05-02,medium,User owns two bikes.,"+replacement.ID) {
		t.Fatalf("legacy supersession:\n%s", text)
	}
}

// The rendered summary's estimate stays within the budget, the omitted table
// included, and every record not kept is counted in an omitted span.
func TestTablesFormatBudgetAdherence(t *testing.T) {
	reflections, observations, dates := mixedLedger()
	for i := 0; i < 60; i++ {
		subject := ""
		if i%3 == 0 {
			subject = fmt.Sprintf("topic %d", i%7)
		}
		observations = append(observations, tf.Observation(fmt.Sprintf("%012x", i+1), func(o *ledger.Observation) {
			o.Timestamp = fmt.Sprintf("2023-06-%02d 10:00", 1+i%28)
			o.Content = fmt.Sprintf("User mentioned item %d, two days ago, ", i) + strings.Repeat("with detail ", i%9)
			o.Subject = subject
		}))
	}
	for i := 0; i < 20; i++ {
		reflections = append(reflections, tf.Reflection(fmt.Sprintf("%011xe", i+1), nil, func(r *ledger.Reflection) {
			r.Content = fmt.Sprintf("Reflection %d ", i) + strings.Repeat("r", 10*i)
		}))
	}
	for budget := 450; budget <= 6000; budget += 61 {
		rendered := ledger.RenderSummaryWithOptions(reflections, observations, budget, ledger.RenderOptions{ReflectionDates: dates})
		if got := (len(rendered.Text) + 3) / 4; got > budget {
			t.Fatalf("budget %d: summary is ~%d tokens", budget, got)
		}
		omitted := rendered.OmittedObservations + rendered.OmittedReflections
		if omitted == 0 {
			continue
		}
		decoded, err := tables.Rows.Decode(strings.TrimPrefix(rendered.Text, ledger.TablesUsageInstructions+"\n\n"))
		if err != nil {
			t.Fatalf("budget %d: %v", budget, err)
		}
		last := decoded[len(decoded)-1]
		if last.Name != "omitted" {
			t.Fatalf("budget %d: no omitted table", budget)
		}
		counted := 0
		for r := range last.Rows {
			n := 0
			count, _ := last.Cell(r, "count")
			fmt.Sscan(count, &n)
			counted += n
		}
		if counted != omitted {
			t.Fatalf("budget %d: omitted spans count %d, %d omitted", budget, counted, omitted)
		}
	}
}

func TestSubjectTableName(t *testing.T) {
	for subject, want := range map[string]string{
		"baking":          "notes.baking",
		"Gaming  Hours":   "notes.gaming_hours",
		"online courses!": "notes.online_courses",
		"c++ / go":        "notes.c_go",
		"!!":              "",
		"":                "",
	} {
		if got := ledger.SubjectTableName(subject); got != want {
			t.Errorf("SubjectTableName(%q) = %q, want %q", subject, got, want)
		}
	}
}

// A budget the whole summary fits keeps every record, even though the
// per-record costs selection uses are deliberately pessimistic.
func TestTablesFormatKeepsEverythingWhenFullRenderFits(t *testing.T) {
	observations := []ledger.Observation{}
	for i := 0; i < 40; i++ {
		observations = append(observations, tf.Observation(padID(fmt.Sprintf("c%02d", i), 'c'), withContent(fmt.Sprintf("User likes thing %d", i))))
	}
	full := ledger.RenderSummaryWithOptions(nil, observations, 0, ledger.RenderOptions{})
	estimate := (len(full.Text) + 3) / 4
	fit := ledger.RenderSummaryWithOptions(nil, observations, estimate, ledger.RenderOptions{})
	if fit.Text != full.Text || fit.OmittedObservations != 0 {
		t.Fatalf("budget %d (the full render's estimate) omitted %d records", estimate, fit.OmittedObservations)
	}
	short := ledger.RenderSummaryWithOptions(nil, observations, estimate-1, ledger.RenderOptions{})
	if short.OmittedObservations == 0 || !strings.Contains(short.Text, "\nomitted[") || (len(short.Text)+3)/4 > estimate-1 {
		t.Fatalf("budget %d should omit records and fit:\n%s", estimate-1, short.Text)
	}
}
