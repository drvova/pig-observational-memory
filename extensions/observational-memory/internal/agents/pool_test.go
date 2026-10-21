package agents_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

func relevance(r string) func(*ledger.Observation) {
	return func(o *ledger.Observation) { o.Relevance = ledger.Relevance(r) }
}

func content(c string) func(*ledger.Observation) {
	return func(o *ledger.Observation) { o.Content = c }
}

func tokenCount(n int) func(*ledger.Observation) {
	return func(o *ledger.Observation) { o.TokenCount = n }
}

func TestPoolBelowTarget(t *testing.T) {
	observations := []ledger.Observation{tf.Observation("aaaaaaaaaaaa", relevance("low"))}
	if got := tokens.ObservationLineTokenCount(tokens.ObservationLine{ID: observations[0].ID, Timestamp: observations[0].Timestamp, Relevance: "low", Content: observations[0].Content}); got != 18 {
		t.Fatalf("line tokens %d", got)
	}
	metrics := agents.ObservationPoolMetrics(observations, 100)
	if metrics.ObservationTokens != 18 || metrics.TargetTokens != 100 || metrics.TokensOverTarget != 0 || metrics.Fullness != 0.18 || metrics.ActiveObservationCount != 1 || metrics.DroppableCount != 1 || metrics.OverTarget || metrics.Ready {
		t.Fatalf("metrics %+v", metrics)
	}
}

func TestPoolAtTarget(t *testing.T) {
	observations := []ledger.Observation{
		tf.Observation("aaaaaaaaaaaa", relevance("low"), content(strings.Repeat("x", 154))),
		tf.Observation("bbbbbbbbbbbb", relevance("medium"), content(strings.Repeat("x", 151))),
	}
	metrics := agents.ObservationPoolMetrics(observations, 100)
	if metrics.ObservationTokens != 100 || metrics.Fullness != 1 || metrics.TokensOverTarget != 0 || metrics.MaxDropsAllowed != 0 || metrics.OverTarget || metrics.Ready {
		t.Fatalf("metrics %+v", metrics)
	}
}

func TestPoolAboveTarget(t *testing.T) {
	observations := []ledger.Observation{
		tf.Observation("aaaaaaaaaaaa", relevance("low"), content(strings.Repeat("x", 154))),
		tf.Observation("bbbbbbbbbbbb", relevance("medium"), content(strings.Repeat("x", 151))),
		tf.Observation("cccccccccccc", relevance("critical"), content(strings.Repeat("x", 149))),
	}
	metrics := agents.ObservationPoolMetrics(observations, 100)
	if metrics.ObservationTokens != 150 || metrics.TokensOverTarget != 50 || metrics.ActiveObservationCount != 3 || metrics.DroppableCount != 3 || metrics.MaxDropsAllowed != 1 || !metrics.OverTarget || !metrics.Ready {
		t.Fatalf("metrics %+v", metrics)
	}
}

func TestPoolClampsDrops(t *testing.T) {
	observations := []ledger.Observation{tf.Observation("aaaaaaaaaaaa", relevance("low"), content("x")), tf.Observation("bbbbbbbbbbbb", relevance("critical"), content("x"))}
	metrics := agents.ObservationPoolMetrics(observations, 0)
	if metrics.TokensOverTarget != 25 || metrics.MaxDropsAllowed != 2 || !metrics.Ready {
		t.Fatalf("metrics %+v", metrics)
	}
}

func TestPoolUsesFoldedActive(t *testing.T) {
	entries := []ledger.Entry{
		tf.TextCustomMessage("raw-1", "aaaaaaaa"),
		tf.ObservationsRecordedEntry("om-obs", []ledger.Observation{tf.Observation("aaaaaaaaaaaa", relevance("low")), tf.Observation("bbbbbbbbbbbb", relevance("low"))}, "raw-1"),
		tf.ObservationsDroppedEntry("om-drop", []string{"aaaaaaaaaaaa"}, "raw-1"),
	}
	folded := ledger.FoldLedger(entries, "")
	metrics := agents.ObservationPoolMetrics(folded.ActiveObservations, 100)
	if !reflect.DeepEqual(tf.IDs(folded.ActiveObservations), []string{"bbbbbbbbbbbb"}) || metrics.ObservationTokens != 18 || metrics.OverTarget || metrics.Ready {
		t.Fatalf("metrics %+v", metrics)
	}
}

func TestCoverageTiers(t *testing.T) {
	if agents.CoverageTierForCount(0) != "none" || agents.CoverageTierForCount(1) != "partial" || agents.CoverageTierForCount(2) != "strong" || agents.CoverageTierForCount(10) != "strong" {
		t.Fatal("tier mapping wrong")
	}
	none, partial, strong := tf.Observation("aaaaaaaaaaaa"), tf.Observation("bbbbbbbbbbbb"), tf.Observation("cccccccccccc")
	coverage := agents.ReflectionCoverageMap([]ledger.Observation{none, partial, strong}, []ledger.Reflection{
		tf.Reflection("rrrrrrrrrrr1", []string{"bbbbbbbbbbbb", "cccccccccccc"}),
		tf.Reflection("rrrrrrrrrrr2", []string{"cccccccccccc", "cccccccccccc"}),
	})
	if coverage["aaaaaaaaaaaa"] != "none" || coverage["bbbbbbbbbbbb"] != "partial" || coverage["cccccccccccc"] != "strong" {
		t.Fatalf("coverage %v", coverage)
	}
}

func TestCoverageSummary(t *testing.T) {
	observations := []ledger.Observation{
		tf.Observation("aaaaaaaaaaaa", relevance("low"), tokenCount(3)),
		tf.Observation("bbbbbbbbbbbb", relevance("critical"), tokenCount(5)),
		tf.Observation("cccccccccccc", relevance("critical"), tokenCount(7)),
	}
	coverage := agents.ReflectionCoverageMap(observations, []ledger.Reflection{tf.Reflection("rrrrrrrrrrr1", []string{"bbbbbbbbbbbb", "cccccccccccc"}), tf.Reflection("rrrrrrrrrrr2", []string{"cccccccccccc"})})
	summary := agents.SummarizeCoverageByRelevance(observations, coverage)
	if *summary["low"]["none"] != (agents.CoverageBucket{Count: 1, Tokens: 3}) || *summary["critical"]["partial"] != (agents.CoverageBucket{Count: 1, Tokens: 5}) || *summary["critical"]["strong"] != (agents.CoverageBucket{Count: 1, Tokens: 7}) {
		t.Fatalf("summary %+v", summary)
	}
}

func TestCoverageTransitions(t *testing.T) {
	observations := []ledger.Observation{
		tf.Observation("aaaaaaaaaaaa", relevance("high"), tokenCount(3)),
		tf.Observation("bbbbbbbbbbbb", relevance("critical"), tokenCount(5)),
		tf.Observation("cccccccccccc", relevance("critical"), tokenCount(7)),
	}
	before := agents.ReflectionCoverageMap(observations, []ledger.Reflection{tf.Reflection("rrrrrrrrrrr1", []string{"bbbbbbbbbbbb"})})
	after := agents.ReflectionCoverageMap(observations, []ledger.Reflection{
		tf.Reflection("rrrrrrrrrrr1", []string{"bbbbbbbbbbbb"}),
		tf.Reflection("rrrrrrrrrrr2", []string{"aaaaaaaaaaaa", "bbbbbbbbbbbb", "cccccccccccc"}),
		tf.Reflection("rrrrrrrrrrr3", []string{"cccccccccccc"}),
	})
	summary := agents.SummarizeCoverageTransitionsByRelevance(observations, before, after)
	if len(summary["low"]) != 0 || len(summary["medium"]) != 0 || *summary["high"]["none->partial"] != (agents.CoverageBucket{Count: 1, Tokens: 3}) || *summary["critical"]["partial->strong"] != (agents.CoverageBucket{Count: 1, Tokens: 5}) || *summary["critical"]["none->strong"] != (agents.CoverageBucket{Count: 1, Tokens: 7}) {
		t.Fatalf("transitions %+v", summary)
	}
}

func TestDropperLine(t *testing.T) {
	line := agents.ObservationToDropperLine(tf.Observation("aaaaaaaaaaaa", relevance("critical"), content("Important fact")), "strong")
	for _, want := range []string{"[aaaaaaaaaaaa]", "[critical]", "[coverage: strong]", "Important fact"} {
		if !strings.Contains(line, want) {
			t.Fatalf("line missing %q", want)
		}
	}
	if strings.Contains(line, "drop-priority") || strings.Contains(line, "drop-resistance") {
		t.Fatal("line leaked drop metadata")
	}
}

func TestBoundedMaxTokens(t *testing.T) {
	if agents.AgentLoopMaxTokens != 32_000 || agents.BoundedMaxTokens(host.Model{}, agents.AgentLoopMaxTokens) != 32_000 {
		t.Fatal("default budget wrong")
	}
	if agents.BoundedMaxTokens(tf.Model("p", "m", map[string]any{"maxTokens": 64_000}), 8_192) != 8_192 {
		t.Fatal("requested budget should win when smaller")
	}
	if agents.BoundedMaxTokens(tf.Model("p", "m", map[string]any{"maxTokens": 8_192}), agents.AgentLoopMaxTokens) != 8_192 || agents.BoundedMaxTokens(tf.Model("p", "m", map[string]any{"maxTokens": 8_192}), 64_000) != 8_192 {
		t.Fatal("model limit should clamp")
	}
	if agents.BoundedMaxTokens(tf.Model("p", "m", map[string]any{"maxTokens": 0}), 8_192) != 8_192 || agents.BoundedMaxTokens(tf.Model("p", "m", map[string]any{"maxTokens": -1}), 8_192) != 8_192 {
		t.Fatal("non-positive model limits should be ignored")
	}
}
