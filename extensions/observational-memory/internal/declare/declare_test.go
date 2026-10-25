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
		"instructions/consolidation": true,
	}
	for _, d := range rules.Detectors {
		want["detector/"+d] = true
	}
	for _, r := range rules.Remedies {
		want["remedy/"+r] = true
	}
	s := store(t)
	for key, text := range layer.Shipped() {
		kind, name, _ := strings.Cut(key, "/")
		if !want[key] {
			t.Fatalf("%s ships but is not expected", key)
		}
		delete(want, key)
		if digest := layer.Digest(text); len(digest) != 16 {
			t.Fatalf("%s: digest %q", key, digest)
		}
		if err := s.Check(layer.Definition{Kind: kind, Name: name, Model: layer.AllModels, Text: text}); err != nil {
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

// Fields a note schema adds are read back: the level-1 input lines show them, notes queries
// select them, and a summary section may name them.
func TestSchemaFieldsAreReadBack(t *testing.T) {
	reflections, observations, _ := memory()
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
	if _, err := declare.ParseSummary("summary[1]{table,query,fields}:\n  notes,notes,id project content"); err != nil {
		t.Fatal(err)
	}
	if _, err := declare.ParseSummary("summary[1]{table,query,fields}:\n  notes,notes,id Content content"); err == nil {
		t.Fatal("a field no schema can add was accepted")
	}
}
