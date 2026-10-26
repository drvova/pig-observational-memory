package harness_test

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omtool"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

const (
	obsBake = "b1aaaaaaaaaa"
	obsShop = "c1aaaaaaaaaa"
)

// session is a fake host with a store, two notes, the host's tools and another extension's.
func session(t *testing.T, extra ...ledger.Entry) (*tf.FakeHost, curated.Store, *harness.Manager) {
	t.Helper()
	entries := append([]ledger.Entry{
		tf.RawMessage("u1", "I bake sourdough every Sunday."),
		tf.ObservationsRecordedEntry("om1", []ledger.Observation{
			tf.Observation(obsBake, func(o *ledger.Observation) {
				o.Content, o.Subject, o.SourceEntryIDs = "User bakes sourdough.", "baking", []string{"u1"}
			}),
			tf.Observation(obsShop, func(o *ledger.Observation) { o.Content, o.SourceEntryIDs = "User bought boots.", []string{"u1"} }),
		}, "u1"),
	}, extra...)
	h := tf.NewFakeHost(entries...)
	h.Home = t.TempDir()
	h.Model = tf.Model("p", "m")
	h.Tools = []host.ToolInfo{{Name: "read", Source: "builtin"}, {Name: omtool.ToolName, Source: "observational-memory"}, {Name: "lookup", Source: "other-extension"}}
	h.Active = []string{"read", omtool.ToolName, "lookup"}
	store := curated.New(curated.DefaultDir(h.Home))
	m := harness.NewManager(func(host.ToolHost) (curated.Call, bool) {
		return curated.Call{Store: store, Source: "session-1"}, true
	})
	t.Cleanup(m.Namespace().Reset)
	return h, store, m
}

// code runs src as one memory tool call.
func code(t *testing.T, h *tf.FakeHost, m *harness.Manager, src string) harness.CodeResult {
	t.Helper()
	return m.RunMemory(h, map[string]any{"code": src})
}

// codeOK runs src and fails the test when the call did not end ok.
func codeOK(t *testing.T, h *tf.FakeHost, m *harness.Manager, src string) harness.CodeResult {
	t.Helper()
	r := code(t, h, m, src)
	if r.IsError {
		t.Fatalf("%s: %s", src, r.Text)
	}
	return r
}

func hasMessage(h *tf.FakeHost, part string) bool {
	for _, m := range h.Messages() {
		if strings.Contains(m, part) {
			return true
		}
	}
	return false
}

// Every code call is counted as a ModelKindCode event (engine v2): a compile error, a runtime
// error and a crash as errors, a write as ok, reads that all returned nothing as empty, hits
// classified against the summary and the index; a cancelled call is not counted.
func TestRunMemoryRecordsCodeOutcomes(t *testing.T) {
	// A later user message, so the earlier turn's text is history, not the current turn.
	h, store, m := session(t, compaction(t, obsBake, obsShop), tf.RawMessage("u9", "and now?"))
	m.IndexShown = func() []string { return nil }
	calls := []struct{ code, outcome, target string }{
		{`om.History(om.Search("sourdough"))`, curated.OutcomeOK, ""},
		{`om.Notes(om.Search("nothing-like-this"))`, curated.OutcomeEmpty, ""},
		{`om.Notes(`, curated.OutcomeError, ""},
		{`notes | search "x"`, curated.OutcomeError, ""},
		{`var m map[string]int; m["x"] = 1`, curated.OutcomeError, ""},
		{`om.Notes(om.Subject("baking"))`, curated.OutcomeShown, curated.TargetSummary},
		{"om.Recall(`" + obsShop + "`)", curated.OutcomeOmitted, curated.TargetSummary},
		{"om.Recall(`" + obsBake + "`)", curated.OutcomeOK, ""},
		{`om.Save("prefers-tabs", "user", "Tabs.")`, curated.OutcomeOK, ""},
		{``, curated.OutcomeOK, ""},
	}
	for _, c := range calls {
		code(t, h, m, c.code)
	}
	events, _ := store.ModelEvents("p/m")
	if len(events) != len(calls) {
		t.Fatalf("events %+v", events)
	}
	for i, c := range calls {
		e := events[i]
		if e.Kind != curated.ModelKindCode || e.Outcome != c.outcome || e.Target != c.target || e.Engine != curated.EngineV2 || e.Syntax || e.Legacy != (i == 3) {
			t.Errorf("%s: event %+v", c.code, e)
		}
	}
	// /om:status counts them, errors and legacy queries apart, from the ledger alone.
	if lines := strings.Join(harness.ModelStatusLines(store, configOn(), "p/m"), "\n"); !strings.Contains(lines, "Code calls: 10 (3 errors, 1 queries of the removed language)") {
		t.Fatalf("status:\n%s", lines)
	}
	// Code events are read by the profile, and errors move no switch.
	for i := 0; i < 2*harness.MinEvidence; i++ {
		code(t, h, m, `om.Notes(`)
		compacted(store, "p/m")
	}
	if p := profileFor(h, configOn(), h.Model); len(p.Switches) != 0 || p.Events == 0 {
		t.Fatalf("errors switched the profile: %+v", p)
	}
	// Without a store nothing is recorded, and the call still runs.
	off := harness.NewManager(func(host.ToolHost) (curated.Call, bool) { return curated.Call{}, false })
	before, _ := store.ModelEvents("p/m")
	if r := off.RunMemory(h, map[string]any{"code": "om.Notes().Len()"}); r.IsError || r.Text != "2" {
		t.Fatalf("no store: %s", r.Text)
	}
	if after, _ := store.ModelEvents("p/m"); len(after) != len(before) {
		t.Fatal("recorded without a store")
	}
}

// om.Recall fetches the evidence behind a record the summary showed, as the tool's guidelines
// ask: recalls of shown records never shrink the summary, while reads listing them do.
func TestRecallOfShownRecordsKeepsTheSummary(t *testing.T) {
	h, store, m := session(t, compaction(t, obsBake, obsShop))
	m.IndexShown = func() []string { return nil }
	for i := 0; i < 2*harness.MinEvidence; i++ {
		codeOK(t, h, m, "om.Recall(`"+obsBake+"`)")
		compacted(store, "p/m")
	}
	if p := profileFor(h, configOn(), h.Model); p.SummaryHalvings != 0 {
		t.Fatalf("recalls halved the summary: %+v", p.Switches)
	}
	for i := 0; i < harness.MinEvidence; i++ {
		codeOK(t, h, m, `om.Notes(om.Subject("baking"))`)
		compacted(store, "p/m")
	}
	if p := profileFor(h, configOn(), h.Model); p.SummaryHalvings != 1 {
		t.Fatalf("reads of shown records did not halve it: %+v", p.Switches)
	}
}

// The defined tools are retired (docs/code-tool.md, step 8): a session start registers none,
// changes no active list and notifies nothing; their names, except one another tool holds, stay
// memory plumbing; om.Define stores nothing; om.Retired and /om:status list every stored
// definition and every legacy file the layer refused, and a call of a retired name is refused.
func TestDefinedToolsAreRetired(t *testing.T) {
	h, store, m := session(t)
	for _, name := range []string{"bakes", "lookup", "gone"} {
		if _, err := store.Define(curated.Definition{Name: name, Query: "notes", Description: "x"}, "s0"); err != nil {
			t.Fatal(err)
		}
	}
	_ = store.RecordFeedback(curated.DefinitionUsageKey("gone"), curated.EventHarmed, "quote long enough")
	dir := filepath.Join(store.Dir, "definitions")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	text := tables.Rows.Encode(tables.Table{Name: "definition", Fields: []string{"name", "query", "description", "created", "sources"}, Rows: [][]string{{"coverage", "notes", "Old.", "2026-05-02T10:00:00Z", "s0"}}})
	if err := os.WriteFile(filepath.Join(dir, "coverage.rows"), []byte(text+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	active := append([]string(nil), h.Active...)
	m.NoteRetired(h)
	m.NoteRetired(h)
	if len(h.Registered) != 0 || len(h.SetActiveCalls) != 0 || !reflect.DeepEqual(h.Active, active) || len(h.Messages()) != 0 {
		t.Fatalf("registered %d, set-active %v, active %v, notified %v", len(h.Registered), h.SetActiveCalls, h.Active, h.Messages())
	}
	// lookup is another extension's tool: its calls stay conversation.
	if got, want := m.SkipNames(), []string{omtool.ToolName, "bakes", "coverage", "gone"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("skip names %v, want %v", got, want)
	}
	r := codeOK(t, h, m, `fmt.Print(om.Define("other"))`)
	if !strings.Contains(r.Text, "defined tools are retired") {
		t.Fatalf("define %s", r.Text)
	}
	if _, err := store.GetDefinition("other"); !errors.Is(err, curated.ErrNotFound) {
		t.Fatal("om.Define stored a definition")
	}
	r = codeOK(t, h, m, `l := om.Retired(om.Limit(200))
for _, q := range l.Rows { fmt.Println(q.Name, q.Origin) }`)
	for _, want := range []string{"bakes layer", "gone layer", "lookup layer", "coverage legacy"} {
		if !strings.Contains(r.Text, want) {
			t.Fatalf("om.Retired lacks %q:\n%s", want, r.Text)
		}
	}
	status := statusText(h, config.Defaults)
	for _, want := range []string{"Retired defined queries (4;", "bakes: om.Notes()", "coverage (not migrated into the layer: refused"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status lacks %q:\n%s", want, status)
		}
	}
	if strings.Contains(status, "Defined tools") {
		t.Fatalf("status still lists defined tools:\n%s", status)
	}
	// Curated memory off: no store, so no retired name.
	off := harness.NewManager(func(host.ToolHost) (curated.Call, bool) { return curated.Call{}, false })
	off.NoteRetired(h)
	if got := off.SkipNames(); !reflect.DeepEqual(got, []string{omtool.ToolName}) {
		t.Fatalf("skip names with curated memory off %v", got)
	}
}

// The calls the retired defined tools recorded (ModelKindDefined) are history: however many of
// them errored, for any tool's name, the memory tool's included, no profile switches.
func TestDefinedToolEventsAreNotGraded(t *testing.T) {
	h, store, _ := session(t)
	for i := 0; i < 10; i++ {
		for _, tool := range []string{omtool.ToolName, "lookup", "bakes"} {
			_ = store.RecordModel(curated.ModelEvent{Model: "p/m", Kind: curated.ModelKindDefined, Tool: tool, Outcome: curated.OutcomeError, Engine: curated.EngineV2})
		}
		compacted(store, "p/m")
	}
	if p := profileFor(h, configOn(), h.Model); len(p.Switches) != 0 || p.Events == 0 {
		t.Fatalf("defined-tool events switched the profile: %+v", p)
	}
}

// om.Recall lists the reflections citing a note.
func TestRunMemoryCitedBy(t *testing.T) {
	h, _, m := session(t, tf.ReflectionsRecordedEntry("rf", []ledger.Reflection{tf.Reflection("ffffffffff01", []string{obsBake}, func(r *ledger.Reflection) {
		r.Content = "User bakes sourdough."
	})}, "u1"))
	r := codeOK(t, h, m, "r, err := om.Recall(`"+obsBake+"`)\nfmt.Print(r, err, r.CitedBy)")
	if !strings.Contains(r.Text, "[ffffffffff01] User bakes sourdough.") || !strings.Contains(r.Text, "[ffffffffff01]") || r.Details.Recalled[0] != obsBake {
		t.Fatalf("recall: %s %+v", r.Text, r.Details)
	}
}

// compacted records one compaction of model: the end of an evidence unit, after which the
// profile applies the events counted since the previous one.
func compacted(store curated.Store, model string) {
	_ = store.RecordModel(curated.ModelEvent{Model: model, Kind: curated.ModelKindPass, Stage: "compaction", Engine: curated.EngineV2, Tokens: 100})
}
