package declare_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/declare"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/memquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

const model = "p/m"

func store(t *testing.T) layer.Store { return layer.Store{Dir: t.TempDir()} }

func load(t *testing.T, s layer.Store) declare.Active {
	t.Helper()
	h, reports, err := s.Load(model)
	if err != nil || len(reports) != 0 {
		t.Fatalf("load: %v %+v", err, reports)
	}
	return declare.Of(h)
}

// Every built-in ships as a seed with a digest, and every seed passes the checks a definition
// of its kind must pass: the kernel's and its interpreter's.
func TestSeedsShipWithDigestsAndPassTheirInterpreters(t *testing.T) {
	want := map[string]bool{
		"format/rows": true, "format/jsonl": true, "schema/observation": true, "summary/default": true,
		"instructions/observer": true, "instructions/reflector": true, "instructions/dropper": true,
	}
	for _, d := range rules.Detectors {
		want["detector/"+d] = true
	}
	for _, r := range rules.Remedies {
		want["remedy/"+r] = true
	}
	s := store(t)
	for _, seed := range declare.Seeds() {
		key := seed.Kind + "/" + seed.Name
		delete(want, key)
		if seed.Digest != layer.Digest(seed.Text) || len(seed.Digest) != 16 {
			t.Fatalf("%s: digest %q", key, seed.Digest)
		}
		if err := s.Check(layer.Definition{Kind: seed.Kind, Name: seed.Name, Model: layer.AllModels, Text: seed.Text}); err != nil {
			t.Fatalf("seed %s fails its own checks: %v", key, err)
		}
	}
	if len(want) != 0 {
		t.Fatalf("not shipped: %v", want)
	}
}

// The format seeds stand for the kernel's native codecs; a model's own declaration of a name
// is interpreted by the generic codec, and a new name joins its codecs.
func TestFormatsResolveThroughTheLayer(t *testing.T) {
	seeds := declare.SeedsOnly(model)
	if c, ok := seeds.Codec(declare.FormatRows); !ok || c != tables.Rows {
		t.Fatalf("rows seed is not the native codec: %v", c)
	}
	if c, ok := seeds.Codec(declare.FormatJSONL); !ok || c != tables.JSONLines {
		t.Fatalf("jsonl seed is not the native codec: %v", c)
	}
	if got := strings.Join(seeds.CodecNames(), ","); got != "rows,jsonl" {
		t.Fatalf("seed codecs %s", got)
	}
	s := store(t)
	semi := tables.Format{Name: "semi", Header: tables.HeaderCounted, Delimiter: ';', Quote: '\'', Escape: '^', Escapes: "nr", Indent: 2}
	if _, err := s.Put(layer.Definition{Kind: layer.KindFormat, Name: "semi", Model: model, Text: semi.Declaration()}); err != nil {
		t.Fatal(err)
	}
	semi.Name = "rows"
	if _, err := s.Put(layer.Definition{Kind: layer.KindFormat, Name: "rows", Model: model, Text: semi.Declaration()}); err != nil {
		t.Fatal(err)
	}
	active := load(t, s)
	c, _ := active.Codec("rows")
	if f, ok := tables.DeclaredFormat(c); !ok || f.Delimiter != ';' {
		t.Fatalf("the model's rows declaration does not stand: %v", c)
	}
	if got := strings.Join(active.CodecNames(), ","); got != "rows,jsonl,semi" {
		t.Fatalf("codecs %s", got)
	}
	// Another model still has the native codec.
	if c, _ := declare.SeedsOnly("q/other").Codec("rows"); c != tables.Rows {
		t.Fatal("a model's declaration reached another model")
	}
}

// A format, schema or summary its interpreter cannot read is refused when it is made.
func TestDeclarationsAreRefused(t *testing.T) {
	s := store(t)
	for name, d := range map[string]layer.Definition{
		"format escapes": {Kind: layer.KindFormat, Name: "bad", Text: tables.Format{Name: "bad", Header: tables.HeaderCounted, Delimiter: ';', Quote: '\'', Escape: '^', Escapes: "t", Indent: 2}.Declaration()},
		"format text":    {Kind: layer.KindFormat, Name: "bad", Text: "use semicolons please"},
		"schema type":    {Kind: layer.KindSchema, Name: declare.SchemaNotes, Text: "schema[1]{field,type}:\n  subject,number"},
		"schema kernel":  {Kind: layer.KindSchema, Name: declare.SchemaNotes, Text: "schema[1]{field,type}:\n  relevance,text"},
		"summary source": {Kind: layer.KindSummary, Name: declare.SummaryDefault, Text: "history | limit 5"},
		"summary write":  {Kind: layer.KindSummary, Name: declare.SummaryDefault, Text: "forget name=x"},
		"summary hides":  {Kind: layer.KindSummary, Name: declare.SummaryDefault, Text: "summary[1]{table,query}:\n  omitted,notes"},
		"summary id":     {Kind: layer.KindSummary, Name: declare.SummaryDefault, Text: "summary[1]{table,query,fields}:\n  facts,notes,content id"},
		"summary field":  {Kind: layer.KindSummary, Name: declare.SummaryDefault, Text: "summary[1]{table,query,fields}:\n  facts,notes,id colour"},
		"summary count":  {Kind: layer.KindSummary, Name: declare.SummaryDefault, Text: "summary[1]{table,query,fields}:\n  facts,notes | count,id"},
		"summary syntax": {Kind: layer.KindSummary, Name: declare.SummaryDefault, Text: "notes | where"},
		"summary twice":  {Kind: layer.KindSummary, Name: declare.SummaryDefault, Text: "summary[2]{table,query}:\n  a,notes\n  a,reflections"},
	} {
		d.Model = model
		if _, err := s.Put(d); err == nil {
			t.Fatalf("%s: accepted", name)
		} else if name == "schema kernel" {
			if _, ok := kernel.AsRefusal(err); !ok {
				t.Fatalf("%s: not a kernel refusal: %v", name, err)
			}
		} else if !strings.Contains(err.Error(), "refused") {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

func memory() ([]ledger.Reflection, []ledger.Observation, map[string]string) {
	observations := []ledger.Observation{}
	kinds := []ledger.ObservationKind{"fact", "event", "plan"}
	for i := 0; i < 12; i++ {
		observations = append(observations, ledger.Observation{
			ID: fmt.Sprintf("%012x", i+1), Timestamp: fmt.Sprintf("2026-05-%02d 10:00", i+1), Relevance: "medium",
			Kind: kinds[i%3], Subject: []string{"baking", "travel"}[i%2], Content: fmt.Sprintf("User note %d with a detail that matters.", i),
		})
	}
	reflections := []ledger.Reflection{{ID: "eeeeeeeeeeee", Content: "User bakes every weekend.", SupportingObservationIDs: []string{"000000000001"}}}
	return reflections, observations, map[string]string{"eeeeeeeeeeee": "2026-05-01"}
}

// While the summary seed stands the native tables layout renders it; a model's summary query
// selects what the summary shows, and the kernel still discloses everything else.
func TestSummaryResolvesThroughTheLayer(t *testing.T) {
	reflections, observations, dates := memory()
	options := ledger.RenderOptions{ReflectionDates: dates}
	native := ledger.RenderSummaryWithOptions(reflections, observations, 0, options)
	if got := declare.SeedsOnly(model).RenderSummary(reflections, observations, 0, options); got.Text != native.Text {
		t.Fatalf("the seed summary is not the native layout:\n%s", got.Text)
	}
	s := store(t)
	text := "summary[2]{table,query,fields}:\n  facts,\"notes | where kind=fact\",id when content\n  plans,\"notes | where kind=plan | group by subject\","
	if _, err := s.Put(layer.Definition{Kind: layer.KindSummary, Name: declare.SummaryDefault, Model: model, Text: text}); err != nil {
		t.Fatal(err)
	}
	active := load(t, s)
	got := active.RenderSummary(reflections, observations, 0, options)
	for _, want := range []string{"summary query", "facts[4]{id,when,content}", "plans.baking[", "plans.travel[", "omitted["} {
		if !strings.Contains(got.Text, want) {
			t.Fatalf("custom summary lacks %q:\n%s", want, got.Text)
		}
	}
	if len(got.Observations) != 8 || got.OmittedObservations != 4 || got.OmittedReflections != 1 {
		t.Fatalf("shown %d omitted %d/%d", len(got.Observations), got.OmittedObservations, got.OmittedReflections)
	}
	// Every event note (which the query never selects) is disclosed.
	for _, o := range observations {
		if o.Kind == "event" && strings.Contains(got.Text, o.Content) {
			t.Fatalf("an unselected note is shown: %s", o.Content)
		}
	}
	// The lines layout is the native one whatever the layer holds.
	lines := options
	lines.Format = ledger.FormatLines
	if got := active.RenderSummary(reflections, observations, 0, lines); got.Text != ledger.RenderSummaryWithOptions(reflections, observations, 0, lines).Text {
		t.Fatal("the lines layout went through the summary query")
	}
}

// A schema definition is the model's observer schema; without one the seed stands.
func TestSchemaResolvesThroughTheLayer(t *testing.T) {
	if got := strings.Join(declare.SeedsOnly(model).Schema().Names(), ","); got != "kind,subject,scope,supersedes" {
		t.Fatalf("seed schema %s", got)
	}
	s := store(t)
	if _, err := s.Put(layer.Definition{Kind: layer.KindSchema, Name: declare.SchemaNotes, Model: model, Text: "schema[2]{field,type,values}:\n  kind,enum,fact\n  mood,enum,happy sad"}); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(load(t, s).Schema().Names(), ","); got != "kind,mood" {
		t.Fatalf("schema %s", got)
	}
}

// A format declared under a shipped codec's name is described by its own spec, never by the
// native rows wording, so the summary's instructions state the syntax its tables use.
func TestRedeclaredRowsIsDescribedByItsOwnSpec(t *testing.T) {
	s := store(t)
	semi := tables.Format{Name: "rows", Header: tables.HeaderCounted, Delimiter: ';', Quote: '\'', Escape: '\\', Escapes: "nrt", Indent: 4}
	if _, err := s.Put(layer.Definition{Kind: layer.KindFormat, Name: "rows", Model: model, Text: semi.Declaration()}); err != nil {
		t.Fatal(err)
	}
	c, ok := load(t, s).Codec("rows")
	if !ok || c == tables.Rows {
		t.Fatalf("redeclared rows is the native codec")
	}
	if got := ledger.TablesUsageInstructionsFor(c); got == ledger.TablesUsageInstructions {
		t.Fatal("a redeclared rows is described with the native wording")
	}
	if got := ledger.CustomSummaryInstructionsFor(c); strings.Contains(got, "values in field order; an empty value means none.") {
		t.Fatalf("custom summary instructions use the native wording:\n%s", got)
	}
	if ledger.TablesUsageInstructionsFor(tables.Rows) != ledger.TablesUsageInstructions || ledger.TablesUsageInstructionsFor(nil) != ledger.TablesUsageInstructions {
		t.Fatal("the native rows codec lost its wording")
	}
}

// A summary section that shows ids without content discloses nothing: it is refused, and the
// kernel counts no such row as showing its record.
func TestSummaryRowsWithoutContentShowNothing(t *testing.T) {
	if _, err := declare.ParseSummary("summary[1]{table,query,fields}:\n  notes,notes,id"); err == nil || !strings.Contains(err.Error(), "content") {
		t.Fatalf("a summary without content was accepted: %v", err)
	}
	reflections, observations, dates := memory()
	ids := make([]string, len(observations))
	rows := make([][]string, len(observations))
	for i, o := range observations {
		ids[i], rows[i] = o.ID, []string{o.ID}
	}
	sections := []ledger.SummarySection{{Table: tables.Table{Name: "notes", Fields: []string{"id"}, Rows: rows}, IDs: ids}}
	got := ledger.RenderSectionsSummary(sections, reflections, observations, 0, ledger.RenderOptions{ReflectionDates: dates})
	if len(got.Observations) != 0 || got.OmittedObservations != len(observations) || !strings.Contains(got.Text, "omitted[") {
		t.Fatalf("id-only rows counted as shown: shown %d omitted %d\n%s", len(got.Observations), got.OmittedObservations, got.Text)
	}
}

// A model's summary that selects no record of a non-empty memory gives way to the seed layout,
// and the report names the definition.
func TestSummaryThatSelectsNothingFallsBackToTheSeed(t *testing.T) {
	reflections, observations, dates := memory()
	options := ledger.RenderOptions{ReflectionDates: dates}
	s := store(t)
	if _, err := s.Put(layer.Definition{Kind: layer.KindSummary, Name: declare.SummaryDefault, Model: model, Text: "notes | where kind = correction"}); err != nil {
		t.Fatal(err)
	}
	got, report := load(t, s).RenderSummaryReport(reflections, observations, 0, options)
	if got.Text != ledger.RenderSummaryWithOptions(reflections, observations, 0, options).Text {
		t.Fatalf("a summary that selects nothing was kept:\n%s", got.Text)
	}
	if !strings.Contains(report, "summary/default") || !strings.Contains(report, model) || !strings.Contains(report, "selects no record") {
		t.Fatalf("report %q", report)
	}
	// One that selects something stands, with no report.
	if _, err := s.Put(layer.Definition{Kind: layer.KindSummary, Name: declare.SummaryDefault, Model: model, Text: "notes | where kind = fact"}); err != nil {
		t.Fatal(err)
	}
	if got, report := load(t, s).RenderSummaryReport(reflections, observations, 0, options); report != "" || len(got.Observations) != 4 {
		t.Fatalf("report %q, shown %d", report, len(got.Observations))
	}
}

// A section that sorts its own rows keeps its first rows under budget pressure.
func TestSortedSummaryKeepsItsFirstRows(t *testing.T) {
	reflections, observations, dates := memory()
	options := ledger.RenderOptions{ReflectionDates: dates}
	summary, err := declare.ParseSummary("notes | sort when desc")
	if err != nil {
		t.Fatal(err)
	}
	full := summary.Render(reflections, observations, 0, options)
	got := summary.Render(reflections, observations, tokens.EstimateStringTokens(full.Text)*2/3, options)
	if len(got.Observations) == 0 || len(got.Observations) == len(observations) {
		t.Fatalf("budget kept %d of %d", len(got.Observations), len(observations))
	}
	newest := observations[len(observations)-1].ID
	oldest := observations[0].ID
	shown := map[string]bool{}
	for _, o := range got.Observations {
		shown[o.ID] = true
	}
	if !shown[newest] || shown[oldest] {
		t.Fatalf("a sorted section kept the wrong end: newest %v oldest %v", shown[newest], shown[oldest])
	}
}

// Formats under new names join the codec rotation in the order they were first written, so a
// later name never moves one the model already had.
func TestNewFormatsJoinInTheOrderFirstWritten(t *testing.T) {
	at := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	s := store(t)
	s.Now = func() time.Time { at = at.Add(time.Minute); return at }
	for _, name := range []string{"zeta", "alpha"} {
		f := tables.Format{Name: name, Header: tables.HeaderCounted, Delimiter: ';', Quote: '\'', Escape: '^', Escapes: "nr", Indent: 2}
		if _, err := s.Put(layer.Definition{Kind: layer.KindFormat, Name: name, Model: model, Text: f.Declaration()}); err != nil {
			t.Fatal(err)
		}
	}
	if got := strings.Join(load(t, s).CodecNames(), ","); got != "rows,jsonl,zeta,alpha" {
		t.Fatalf("codecs %s", got)
	}
}

// Fields a note schema adds are read back: the observer's prior lines show them, notes queries
// select them, and a summary section may show them.
func TestSchemaFieldsAreReadBack(t *testing.T) {
	reflections, observations, dates := memory()
	for i := range observations {
		if i%2 == 0 {
			observations[i].Fields = map[string]string{"project": "bakery"}
		}
	}
	if line := ledger.ObservationToObserverLine(observations[0]); !strings.Contains(line, "project: bakery") {
		t.Fatalf("observer line %q", line)
	}
	sel, err := memquery.Select(memquery.Env{Records: &memquery.Records{Observations: observations, Reflections: reflections}}, "notes | where project = bakery | count", memquery.SourceNotes)
	if err != nil || sel.Count != 6 {
		t.Fatalf("select project: %+v %v", sel.Count, err)
	}
	summary, err := declare.ParseSummary("summary[1]{table,query,fields}:\n  notes,notes,id project content")
	if err != nil {
		t.Fatal(err)
	}
	got := summary.Render(reflections, observations, 0, ledger.RenderOptions{ReflectionDates: dates})
	if !strings.Contains(got.Text, "notes[12]{id,project,content}") || !strings.Contains(got.Text, "bakery") {
		t.Fatalf("summary with a schema field:\n%s", got.Text)
	}
	if _, err := declare.ParseSummary("summary[1]{table,query,fields}:\n  notes,notes,id Content content"); err == nil {
		t.Fatal("a field no schema can add was accepted")
	}
}
