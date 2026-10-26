package harness_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

const model = "p/m"

var t0 = time.Date(2026, 5, 2, 10, 0, 0, 0, time.UTC)

// events builds ledger events a minute apart, in order.
func events(es ...curated.ModelEvent) []curated.ModelEvent {
	for i := range es {
		es[i].Model = model
		es[i].Time = t0.Add(time.Duration(i) * time.Minute).Format(time.RFC3339)
	}
	return es
}

// units is es each as one compaction's evidence (counted under v2, a compaction after each):
// one unit per event, in order.
func units(es ...curated.ModelEvent) []curated.ModelEvent {
	out := make([]curated.ModelEvent, 0, 2*len(es))
	for _, e := range es {
		out = append(out, v2(e), v2Compaction)
	}
	return events(out...)
}

func repeat(n int, e curated.ModelEvent) []curated.ModelEvent {
	out := make([]curated.ModelEvent, n)
	for i := range out {
		out[i] = e
	}
	return out
}

func concat(groups ...[]curated.ModelEvent) []curated.ModelEvent {
	out := []curated.ModelEvent{}
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

func decode(codec, outcome string) curated.ModelEvent {
	return curated.ModelEvent{Kind: curated.ModelKindDecode, Codec: codec, Outcome: outcome}
}

// memory is a code call of the memory tool (ModelKindCode); syntax marks it as the query
// language's calls were marked, which the code tool never grades.
func memory(outcome, target string, syntax bool) curated.ModelEvent {
	return curated.ModelEvent{Kind: curated.ModelKindCode, Outcome: outcome, Target: target, Syntax: syntax}
}

// queryCall is a call of the removed query language (ModelKindMemory): history, never graded.
func queryCall(outcome, target string, syntax bool) curated.ModelEvent {
	return curated.ModelEvent{Kind: curated.ModelKindMemory, Outcome: outcome, Target: target, Syntax: syntax}
}

func defined(tool, outcome string) curated.ModelEvent {
	return curated.ModelEvent{Kind: curated.ModelKindDefined, Tool: tool, Outcome: outcome}
}

func TestDefaultProfile(t *testing.T) {
	p := harness.Compute(model, nil, nil)
	if p.Codec != tables.Codecs()[0].Name() || p.Layout != ledger.FormatTables || p.SummaryHalvings != 0 || len(p.DisabledTools) != 0 || len(p.Switches) != 0 {
		t.Fatalf("default profile %+v", p)
	}
	if p.SummaryScale() != 1 || p.ApplySummary(50_000) != 50_000 || p.CodecValue().Spec() != tables.Rows.Spec() {
		t.Fatalf("default profile does not use the whole room and the short spec")
	}
}

// Codec: decode errors in the current codec meeting the minimum-evidence rule against parsed
// decodes switch to the next codec; the last codec is kept.
func TestCodecSwitch(t *testing.T) {
	rows, jsonl := tables.Rows.Name(), tables.JSONLines.Name()
	cases := []struct {
		name string
		evs  []curated.ModelEvent
		want string
	}{
		{"one short of the minimum", repeat(harness.MinEvidence-1, decode(rows, curated.OutcomeError)), rows},
		{"a tie is no majority", concat(repeat(3, decode(rows, curated.OutcomeOK)), repeat(3, decode(rows, curated.OutcomeError))), rows},
		{"repeated errors", concat(repeat(2, decode(rows, curated.OutcomeOK)), repeat(3, decode(rows, curated.OutcomeError))), jsonl},
		{"errors in another codec do not count", repeat(5, decode(jsonl, curated.OutcomeError)), rows},
		{"the last codec is kept", concat(repeat(3, decode(rows, curated.OutcomeError)), repeat(5, decode(jsonl, curated.OutcomeError))), jsonl},
	}
	for _, c := range cases {
		p := harness.Compute(model, units(c.evs...), nil)
		if p.Codec != c.want {
			t.Errorf("%s: codec %s, want %s", c.name, p.Codec, c.want)
		}
		if c.want == jsonl {
			sw := p.Switches[0]
			if sw.Setting != harness.SettingCodec || sw.To != jsonl || !strings.Contains(sw.Reason, "3 rows decode errors") || sw.At.IsZero() {
				t.Errorf("%s: switch %+v", c.name, sw)
			}
			if p.CodecValue() != tables.JSONLines {
				t.Errorf("%s: CodecValue %s", c.name, p.CodecValue().Name())
			}
		}
	}
}

// Layout: rows that parse with the wrong columns switch tables to lines, once.
func TestLayoutSwitch(t *testing.T) {
	rows := tables.Rows.Name()
	p := harness.Compute(model, units(concat(repeat(1, decode(rows, curated.OutcomeOK)), repeat(3, decode(rows, curated.OutcomeColumns)))...), nil)
	if p.Layout != ledger.FormatLines || p.SummaryFormat(config.Defaults) != ledger.FormatLines {
		t.Fatalf("layout %s", p.Layout)
	}
	if p.Codec != rows {
		t.Fatalf("wrong columns switched the codec: %+v", p)
	}
	p = harness.Compute(model, units(concat(repeat(3, decode(rows, curated.OutcomeColumns)), repeat(3, decode(rows, curated.OutcomeOK)))...), nil)
	if p.Layout != ledger.FormatLines {
		t.Fatal("the layout switched back")
	}
	p = harness.Compute(model, units(concat(repeat(3, decode(rows, curated.OutcomeOK)), repeat(3, decode(rows, curated.OutcomeColumns)))...), nil)
	if p.Layout != ledger.FormatTables {
		t.Fatal("a tie switched the layout")
	}
	// The configured lines layout wins over the profile's tables.
	cfg := config.Defaults
	cfg.MemoryFormat = config.MemoryFormatLines
	if harness.Default(model).SummaryFormat(cfg) != ledger.FormatLines {
		t.Fatal("the configured layout was overridden")
	}
}

// The long-spec instructions setting is retired with the query language (docs/code-tool.md):
// no error of a code call, no decode error and no query-language syntax error switches
// anything, since no switch may change the memory tool by model.
func TestNoSwitchChangesTheTool(t *testing.T) {
	p := harness.Compute(model, units(concat(
		repeat(4, memory(curated.OutcomeError, "", true)),
		repeat(4, queryCall(curated.OutcomeError, "", true)),
		repeat(1, decode(tables.Rows.Name(), curated.OutcomeOK)),
	)...), nil)
	if len(p.Switches) != 0 {
		t.Fatalf("profile %+v", p)
	}
	// Query-language calls stay history: their shown hits never halve the summary.
	shown := queryCall(curated.OutcomeShown, curated.TargetSummary, false)
	if p := harness.Compute(model, units(repeat(6, shown)...), nil); p.SummaryHalvings != 0 {
		t.Fatalf("query-language hits halved the summary: %+v", p.Switches)
	}
}

// Summary: hits on records the summary showed halve it; hits on omitted records double it back
// toward the room; index hits do not count.
func TestSummarySwitch(t *testing.T) {
	shown := memory(curated.OutcomeShown, curated.TargetSummary, false)
	omitted := memory(curated.OutcomeOmitted, curated.TargetSummary, false)
	index := memory(curated.OutcomeShown, curated.TargetIndex, false)

	p := harness.Compute(model, units(repeat(5, index)...), nil)
	if p.SummaryHalvings != 0 {
		t.Fatal("index hits shrank the summary")
	}
	p = harness.Compute(model, units(concat(repeat(1, omitted), repeat(3, shown))...), nil)
	if p.SummaryHalvings != 1 || p.SummaryScale() != 0.5 {
		t.Fatalf("halvings %d", p.SummaryHalvings)
	}
	if sw := p.Switches[0]; sw.Setting != harness.SettingSummary || sw.To != "1/2 of the room" {
		t.Fatalf("switch %+v", sw)
	}
	// Each switch resets the counts: three more shown halve again, then omitted grow back.
	p = harness.Compute(model, units(concat(repeat(3, shown), repeat(3, shown), repeat(3, omitted))...), nil)
	if p.SummaryHalvings != 1 || len(p.Switches) != 3 || p.Switches[2].To != "1/2 of the room" {
		t.Fatalf("halvings %d switches %v", p.SummaryHalvings, p.Switches)
	}
	// Omitted hits never grow past the whole room.
	p = harness.Compute(model, units(repeat(9, omitted)...), nil)
	if p.SummaryHalvings != 0 || len(p.Switches) != 0 {
		t.Fatalf("grew past the room: %+v", p)
	}
	// Halving stops where the floor governs every window.
	p = harness.Compute(model, units(repeat(3*(harness.MaxSummaryHalvings+4), shown)...), nil)
	if p.SummaryHalvings != harness.MaxSummaryHalvings {
		t.Fatalf("halvings %d, want the cap %d", p.SummaryHalvings, harness.MaxSummaryHalvings)
	}
}

// Tools: a defined tool that errors more than it succeeds for this model is deactivated for
// it; calls of an earlier definition of the name do not count.
func TestToolSwitch(t *testing.T) {
	p := harness.Compute(model, units(concat(repeat(2, defined("bake-count", curated.OutcomeError)), repeat(1, defined("bake-count", curated.OutcomeOK)), repeat(1, defined("bake-count", curated.OutcomeError)))...), nil)
	if !p.ToolDisabled("bake-count") || p.Switches[0].To != "bake-count off" {
		t.Fatalf("profile %+v", p)
	}
	p = harness.Compute(model, units(concat(repeat(3, defined("bake-count", curated.OutcomeError)), repeat(3, defined("other", curated.OutcomeOK)))...), nil)
	if p.ToolDisabled("other") {
		t.Fatal("a tool that returns results was deactivated")
	}
	p = harness.Compute(model, units(concat(repeat(3, defined("bake-count", curated.OutcomeEmpty)), repeat(3, defined("bake-count", curated.OutcomeError)))...), nil)
	if p.ToolDisabled("bake-count") {
		t.Fatal("empty results count as errors")
	}
	// Redefined after the errors: they belong to the earlier definition.
	redefined := []curated.Definition{{Name: "bake-count", Created: t0.Add(10 * time.Minute)}}
	p = harness.Compute(model, units(repeat(3, defined("bake-count", curated.OutcomeError))...), redefined)
	if p.ToolDisabled("bake-count") {
		t.Fatal("calls before the definition counted")
	}
}

// No profile can grow the summary past the room or lose a codec.
func TestBadProfileStaysInsideTheRoom(t *testing.T) {
	for _, halvings := range []int{-3, 0, 1, 5, 1000} {
		p := harness.Profile{Codec: "nope", SummaryHalvings: halvings}
		for _, room := range []int{1, 500, 8_000, 9_000, 200_000} {
			got := p.ApplySummary(room)
			if got > room || got < 1 {
				t.Errorf("halvings %d: ApplySummary(%d) = %d", halvings, room, got)
			}
			if room <= config.CompactionSummaryFallbackMaxTokens && got != room {
				t.Errorf("halvings %d: room %d under the floor shrank to %d", halvings, room, got)
			}
		}
		if p.CodecValue() != tables.Codecs()[0] {
			t.Errorf("unknown codec gave %s", p.CodecValue().Name())
		}
	}
	if got := (harness.Profile{SummaryHalvings: 2}).ApplySummary(100_000); got != 25_000 {
		t.Fatalf("a quarter of the room = %d", got)
	}
	if got := (harness.Profile{SummaryHalvings: 4}).ApplySummary(100_000); got != config.CompactionSummaryFallbackMaxTokens {
		t.Fatalf("the floor governs: %d", got)
	}
}

func TestSwitchString(t *testing.T) {
	s := harness.Switch{Setting: harness.SettingCodec, To: "jsonl", Reason: "why", At: t0}
	if got := s.String(); got != fmt.Sprintf("2026-05-02 10:00 codec → jsonl: why") {
		t.Fatalf("String() = %q", got)
	}
}

// v2 marks an event as counted under memory v2.
func v2(e curated.ModelEvent) curated.ModelEvent {
	e.Engine = curated.EngineV2
	return e
}

// v2Compaction is a memory v2 compaction pass: the end of one evidence unit.
var v2Compaction = curated.ModelEvent{Kind: curated.ModelKindPass, Stage: "compaction", Engine: curated.EngineV2, Tokens: 100}

// Under memory v2 one evidence unit is one compaction: the events counted under v2 between two
// compactions give each setting at most one unit, so many errors in one turn never switch a
// setting, the same errors over MinEvidence compactions do, and events after the latest
// compaction wait for the next.
func TestV2ProfileCountsCompactions(t *testing.T) {
	shown := v2(memory(curated.OutcomeShown, curated.TargetSummary, false))

	p := harness.Compute(model, events(concat(repeat(9, shown), repeat(1, v2Compaction))...), nil)
	if p.SummaryHalvings != 0 {
		t.Fatalf("one compaction halved the summary: %+v", p.Switches)
	}
	p = harness.Compute(model, events(repeat(9, shown)...), nil)
	if p.SummaryHalvings != 0 {
		t.Fatal("events with no compaction after them counted")
	}
	// The summary halves on MinEvidence compactions whose queries hit shown records.
	shrink := concat(repeat(4, shown), repeat(1, v2Compaction))
	if p = harness.Compute(model, events(concat(shrink, shrink)...), nil); p.SummaryHalvings != 0 {
		t.Fatal("two compactions halved the summary")
	}
	if p = harness.Compute(model, events(concat(shrink, shrink, shrink)...), nil); p.SummaryHalvings != 1 {
		t.Fatalf("halvings %d", p.SummaryHalvings)
	}
	// A defined tool is turned off on MinEvidence compactions where it erred more than it worked.
	erring := concat(repeat(3, v2(defined("finder", curated.OutcomeError))), repeat(1, v2Compaction))
	if p = harness.Compute(model, events(concat(erring, erring, erring)...), nil); !p.ToolDisabled("finder") {
		t.Fatalf("profile %+v", p)
	}
	// Memory v1's events (no engine) never count.
	v1Shown := memory(curated.OutcomeShown, curated.TargetSummary, false)
	if p = harness.Compute(model, events(concat(repeat(3, v1Shown), repeat(1, v2Compaction), repeat(3, v1Shown), repeat(1, v2Compaction), repeat(3, v1Shown), repeat(1, v2Compaction))...), nil); p.SummaryHalvings != 0 {
		t.Fatal("v1's events counted")
	}
}

// The profile reads memory v2's events only: v1's decode errors and syntax errors leave it at
// the default, and v1's partial tallies never carry into it.
func TestProfileEnginesNeverMix(t *testing.T) {
	rows := tables.Rows.Name()
	v1Errors := concat(repeat(3, decode(rows, curated.OutcomeColumns)), repeat(3, memory(curated.OutcomeError, "", true)), repeat(3, memory(curated.OutcomeShown, curated.TargetSummary, false)))
	if p := harness.Compute(model, events(v1Errors...), nil); p.Codec != rows || p.SummaryHalvings != 0 || len(p.Switches) != 0 {
		t.Fatalf("v1 evidence shaped the v2 profile: %+v", p)
	}
	shrink := concat(repeat(4, v2(memory(curated.OutcomeShown, curated.TargetSummary, false))), repeat(1, v2Compaction))
	// Two v1 shown hits, then one v2 compaction unit: no halving (it needs MinEvidence units of
	// its own).
	mixed := concat(repeat(2, memory(curated.OutcomeShown, curated.TargetSummary, false)), shrink)
	if p := harness.Compute(model, events(mixed...), nil); p.SummaryHalvings != 0 {
		t.Fatalf("v1 tallies carried into v2: %+v", p)
	}
}
