package hooks

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/consolidate"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/selection"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// The level-1 fixture's two code records, both from the cut range.
var (
	l1PortID     = "aaaaaaaaaaa1"
	l1MakefileID = "aaaaaaaaaaa2"
)

// level1Branch is a cut range (u1, a1) whose level-0 records are already extracted, then the
// kept turn u2:
//
//	0 u1  1 a1  2 om(records, covers a1)  3 u2
func level1Branch() []ledger.Entry {
	port := testfix.Observation(l1PortID, func(o *ledger.Observation) {
		o.Content, o.SourceEntryIDs, o.Origin, o.Kind, o.Subject = "The deploy uses port 8080 on staging", []string{"u1"}, ledger.OriginCode, "fact", "deploy"
	})
	makefile := testfix.Observation(l1MakefileID, func(o *ledger.Observation) {
		o.Content, o.SourceEntryIDs, o.Origin, o.Kind, o.Subject = "Staging deploys run from the Makefile", []string{"a1"}, ledger.OriginCode, "fact", "deploy"
	})
	return []ledger.Entry{
		// Long enough that the summary budget (never more than the range removes) holds records.
		userMessage("u1", "Deploy the api to staging on port 8080"+strings.Repeat(", running the usual checks", 40)),
		assistantMessage("a1", "Deployed with the Makefile target"+strings.Repeat(", after the usual checks", 40)),
		testfix.ObservationsRecordedEntry("om-1", []ledger.Observation{port, makefile}, "a1"),
		userMessage("u2", "Next please"),
	}
}

// level1Setup is v2Setup with level 1 on, its replies scripted.
func level1Setup(t *testing.T, replies ...map[string]any) (*runtime.Runtime, *testfix.FakeHost, []ledger.Entry) {
	t.Helper()
	entries := level1Branch()
	rt, h := v2Setup(t, entries)
	cfg := rt.Config()
	cfg.ConsolidateOff = false
	rt.SetConfig(cfg)
	h.ModelsFake.Streams.Replies = replies
	return rt, h, entries
}

// mergeReply merges the two code records into content.
func mergeReply(content string) map[string]any {
	return testfix.AssistantToolCall("call-1", consolidate.ToolName, map[string]any{
		"merges": []any{map[string]any{"ids": []any{l1PortID, l1MakefileID}, "content": content, "subject": "deploy"}},
	})
}

// level1CompactionOf asserts a compaction on PiG's cut and returns it.
func level1CompactionOf(t *testing.T, outcome BeforeCompactOutcome) CompactionResult {
	t.Helper()
	if outcome.Cancel || outcome.Compaction == nil {
		t.Fatalf("expected a compaction, got %+v", outcome)
	}
	if outcome.Compaction.FirstKeptEntryID != "u2" || outcome.Compaction.Details.Consolidation == nil {
		t.Fatalf("compaction %+v", outcome.Compaction)
	}
	return *outcome.Compaction
}

// With consolidate on, a v2 compaction makes exactly one request, on the session model, and
// records what validation accepts: the merge replaces both records, its sources set by code.
func TestV2Level1MakesExactlyOneRequest(t *testing.T) {
	counter := testfix.CountRequests(t)
	rt, h, entries := level1Setup(t, mergeReply("Staging deploys run from the Makefile on port 8080"))

	c := level1CompactionOf(t, runV2Hook(rt, h, v2Event(entries, "u2", 20_000, "threshold")))

	if counter.Total() != 1 || counter.Stage(consolidate.Stage) != 1 || h.ModelsFake.Streams.Calls() != 1 {
		t.Fatalf("requests %v", counter.ByStage())
	}
	if model := h.ModelsFake.Streams.Models[0]; host.ModelID(model) != "session" {
		t.Fatalf("request on %v, want the session model", model)
	}
	report := c.Details.Consolidation
	if !report.Requested || !report.Applied || report.Level() != 1 || report.Merges != 1 || report.Dropped != 2 {
		t.Fatalf("report %+v", report)
	}
	if c.Usage == nil || c.ToMap()["usage"] == nil {
		t.Fatal("the compaction does not carry the request's usage")
	}
	if !strings.Contains(c.Summary, "Staging deploys run from the Makefile on port 8080") || strings.Contains(c.Summary, l1PortID) {
		t.Fatalf("summary:\n%s", c.Summary)
	}
	var merged *ledger.Observation
	for _, o := range c.Details.Observations {
		if o.Origin == ledger.OriginModel {
			merged = &o
		}
		if o.ID == l1PortID || o.ID == l1MakefileID {
			t.Fatalf("a replaced record stayed active: %+v", o)
		}
	}
	if merged == nil || strings.Join(merged.SourceEntryIDs, ",") != "u1,a1" {
		t.Fatalf("merged %+v", merged)
	}
	if types := strings.Join(h.AppendedTypes(), ","); !strings.Contains(types, ledger.OMObservationsRecorded) || !strings.Contains(types, ledger.OMObservationsDropped) {
		t.Fatalf("appended %s", types)
	}
	// The request carries the cut range, the candidates and the kernel's rules.
	request := h.ModelsFake.Streams.Requests[0]
	system, _ := request["systemPrompt"].(string)
	if !strings.Contains(system, "consolidate_memory") || request["maxTokens"] != 32_000 {
		t.Fatalf("request %v", request)
	}
}

// No request with consolidate off, in passive mode, for the extension's own compaction with
// auto-compaction off, on overflow, or when the input and output cap do not fit the window.
func TestV2Level1MakesNoRequest(t *testing.T) {
	for _, c := range []struct {
		name   string
		setup  func(rt *runtime.Runtime, h *testfix.FakeHost)
		reason string
		want   string
	}{
		{"consolidate off", func(rt *runtime.Runtime, h *testfix.FakeHost) {
			cfg := rt.Config()
			cfg.ConsolidateOff = true
			rt.SetConfig(cfg)
		}, "threshold", level1Off},
		{"passive", func(rt *runtime.Runtime, h *testfix.FakeHost) {
			cfg := rt.Config()
			cfg.Passive = true
			rt.SetConfig(cfg)
		}, "threshold", level1Passive},
		{"own auto-off compaction", func(rt *runtime.Runtime, h *testfix.FakeHost) {
			if !rt.TryStartCompact() {
				t.Fatal("slot taken")
			}
		}, "manual", level1OwnTrigger},
		{"overflow", func(rt *runtime.Runtime, h *testfix.FakeHost) {}, "overflow", level1Overflow},
		{"no room", func(rt *runtime.Runtime, h *testfix.FakeHost) {
			// 36,000 - 32,000 output - 4,096 margin leaves nothing for the input.
			h.Model = testfix.Model("anthropic", "session", map[string]any{"contextWindow": 36_000, "maxTokens": 32_000})
			h.ModelsFake.Add(h.Model, testfix.KeyAuth("key"))
		}, "threshold", level1NoRoom},
		{"no auth", func(rt *runtime.Runtime, h *testfix.FakeHost) {
			delete(h.ModelsFake.Auth, "anthropic")
		}, "threshold", "the session model is unavailable"},
	} {
		t.Run(c.name, func(t *testing.T) {
			counter := testfix.CountRequests(t)
			rt, h, entries := level1Setup(t, mergeReply("Staging deploys run from the Makefile on port 8080"))
			c.setup(rt, h)
			compaction := level1CompactionOf(t, runV2Hook(rt, h, v2Event(entries, "u2", 20_000, c.reason)))
			if counter.Total() != 0 || h.ModelsFake.Streams.Calls() != 0 {
				t.Fatalf("requests %v", counter.ByStage())
			}
			report := compaction.Details.Consolidation
			if report.Requested || report.Applied || !strings.HasPrefix(report.Reason, c.want) || compaction.Usage != nil {
				t.Fatalf("report %+v usage %v", report, compaction.Usage)
			}
			// Level 0 stands: both records are still active and rendered.
			if ids := strings.Join(observationIDs(compaction.Details.Observations), ","); !strings.Contains(ids, l1PortID) || !strings.Contains(ids, l1MakefileID) {
				t.Fatalf("records %s", ids)
			}
		})
	}
}

// A request context cancelled before the hook starts makes no request and no compaction.
func TestV2Level1CancelledBeforeStart(t *testing.T) {
	counter := testfix.CountRequests(t)
	rt, h, entries := level1Setup(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outcome := HandleBeforeCompactRequest(ctx, rt, h, v2Event(entries, "u2", 20_000, "manual"))
	if outcome.Compaction != nil || outcome.Cancel || counter.Total() != 0 {
		t.Fatalf("outcome %+v, requests %v", outcome, counter.ByStage())
	}
}

// A request cancelled while it runs counts as made (aborted) and the hook returns nothing.
func TestV2Level1AbortedDuringRequest(t *testing.T) {
	counter := testfix.CountRequests(t)
	rt, h, entries := level1Setup(t)
	h.ModelsFake.Streams.Block = true
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan BeforeCompactOutcome)
	go func() {
		done <- HandleBeforeCompactRequest(ctx, rt, h, v2Event(entries, "u2", 20_000, "manual"))
	}()
	deadline := time.Now().Add(5 * time.Second)
	for h.ModelsFake.Streams.Calls() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	outcome := <-done
	if outcome.Compaction != nil || counter.Total() != 1 {
		t.Fatalf("outcome %+v, requests %v", outcome, counter.ByStage())
	}
}

// Every failure leaves level 0 standing, retries nothing, and still returns a compaction.
func TestV2Level1FailureStillCompacts(t *testing.T) {
	for _, c := range []struct {
		name  string
		reply map[string]any
		want  string
	}{
		{"no tool call", testfix.AssistantText("I would merge them."), consolidate.FailureNoCall},
		{"stream error", testfix.AssistantError("error", "overloaded"), "consolidate stream ended"},
		{"length", func() map[string]any {
			m := mergeReply("Staging deploys run from the Makefile on port 8080")
			m["stopReason"] = "length"
			return m
		}(), "the response reached the output cap"},
		{"empty output", testfix.AssistantToolCall("call-1", consolidate.ToolName, map[string]any{}), consolidate.FailureNothing},
		{"foreign ids only", testfix.AssistantToolCall("call-1", consolidate.ToolName, map[string]any{"records": []any{map[string]any{"id": "ffffffffffff", "subject": "x"}}}), consolidate.FailureNoneValid},
	} {
		t.Run(c.name, func(t *testing.T) {
			counter := testfix.CountRequests(t)
			rt, h, entries := level1Setup(t, c.reply, mergeReply("never asked for"))
			compaction := level1CompactionOf(t, runV2Hook(rt, h, v2Event(entries, "u2", 20_000, "threshold")))
			if counter.Total() != 1 {
				t.Fatalf("requests %v (retried?)", counter.ByStage())
			}
			report := compaction.Details.Consolidation
			if !report.Requested || report.Applied || !strings.HasPrefix(report.Reason, c.want) {
				t.Fatalf("report %+v", report)
			}
			for _, a := range h.Appended {
				if a.CustomType == ledger.OMObservationsDropped || a.CustomType == ledger.OMReflectionsRecorded {
					t.Fatalf("a failure appended %+v", a)
				}
			}
			if ids := strings.Join(observationIDs(compaction.Details.Observations), ","); ids != l1PortID+","+l1MakefileID {
				t.Fatalf("records %s", ids)
			}
		})
	}
}

// A weak model's merge that drops a specific is refused, and the union keeps the facts: both
// records stand and the summary still carries the port.
func TestV2Level1UnionKeepsFacts(t *testing.T) {
	counter := testfix.CountRequests(t)
	reply := testfix.AssistantToolCall("call-1", consolidate.ToolName, map[string]any{
		"merges":  []any{map[string]any{"ids": []any{l1PortID, l1MakefileID}, "content": "Staging deploys run from the Makefile"}},
		"records": []any{map[string]any{"id": l1MakefileID, "relevance": "high"}},
	})
	rt, h, entries := level1Setup(t, reply)
	compaction := level1CompactionOf(t, runV2Hook(rt, h, v2Event(entries, "u2", 20_000, "threshold")))
	report := compaction.Details.Consolidation
	if counter.Total() != 1 || !report.Applied || report.Refused != 1 || report.Merges != 0 || report.Records != 1 {
		t.Fatalf("report %+v", report)
	}
	if !strings.Contains(compaction.Summary, "port 8080") {
		t.Fatalf("the port was lost:\n%s", compaction.Summary)
	}
	ids := observationIDs(compaction.Details.Observations)
	if strings.Join(ids, ",") == "" || !strings.Contains(strings.Join(ids, ","), l1PortID) || strings.Contains(strings.Join(ids, ","), l1MakefileID) {
		t.Fatalf("records %v", ids)
	}
}

// Level 1 runs on the session model only: other models registered with auth (memory v1's
// `model` and `fallbackModel` would have named them) are never asked, and a failed request is
// not retried on any of them.
func TestV2Level1RunsOnTheSessionModelOnly(t *testing.T) {
	counter := testfix.CountRequests(t)
	rt, h, entries := level1Setup(t, testfix.AssistantError("error", "overloaded"), mergeReply("Staging deploys run from the Makefile on port 8080"))
	configured := testfix.Model("openai", "memory", map[string]any{"contextWindow": 200_000, "maxTokens": 32_000})
	fallback := testfix.Model("openai", "fallback", map[string]any{"contextWindow": 200_000, "maxTokens": 32_000})
	h.ModelsFake.Add(configured, testfix.KeyAuth("key"))
	h.ModelsFake.Add(fallback, testfix.KeyAuth("key"))

	compaction := level1CompactionOf(t, runV2Hook(rt, h, v2Event(entries, "u2", 20_000, "threshold")))

	if counter.Total() != 1 || h.ModelsFake.Streams.Calls() != 1 {
		t.Fatalf("requests %v (retried on another model?)", counter.ByStage())
	}
	if model := h.ModelsFake.Streams.Models[0]; host.ModelID(model) != "session" {
		t.Fatalf("request on %v, want the session model", model)
	}
	if report := compaction.Details.Consolidation; !report.Requested || report.Applied {
		t.Fatalf("report %+v", report)
	}
}

// A panic after the request (here while rendering) is recovered into the minimal brief, which
// still carries the request's usage and report: it was made and paid for.
func TestV2Level1PanicAfterRequestKeepsUsage(t *testing.T) {
	old := renderV2Records
	renderV2Records = func(selection.Input) selection.Result { panic("renderer bug") }
	t.Cleanup(func() { renderV2Records = old })
	counter := testfix.CountRequests(t)
	rt, h, entries := level1Setup(t, mergeReply("Staging deploys run from the Makefile on port 8080"))

	compaction := level1CompactionOf(t, runV2Hook(rt, h, v2Event(entries, "u2", 20_000, "threshold")))

	if counter.Total() != 1 || compaction.Usage == nil {
		t.Fatalf("requests %v usage %v", counter.ByStage(), compaction.Usage)
	}
	if report := compaction.Details.Consolidation; !report.Requested {
		t.Fatalf("report %+v", report)
	}

	// A panic before any request reports level 1 as not run, with no usage.
	renderV2Records = func(selection.Input) selection.Result { panic("renderer bug") }
	rt, h, entries = level1Setup(t)
	cfg := rt.Config()
	cfg.ConsolidateOff = true
	rt.SetConfig(cfg)
	compaction = level1CompactionOf(t, runV2Hook(rt, h, v2Event(entries, "u2", 20_000, "threshold")))
	if report := compaction.Details.Consolidation; report.Requested || report.Reason != level1Off || compaction.Usage != nil {
		t.Fatalf("report %+v usage %v", report, compaction.Usage)
	}
}

// Custom instructions reach the level-1 input; a summary level 1 shaped does not repeat them.
// When level 1 fails, level 0's summary carries them as before.
func TestV2Level1CustomInstructionsGoToLevel1Only(t *testing.T) {
	const instructions = "Focus on the deploy."
	rt, h, entries := level1Setup(t, mergeReply("Staging deploys run from the Makefile on port 8080"))
	event := v2Event(entries, "u2", 20_000, "manual")
	event.CustomInstructions = instructions
	applied := level1CompactionOf(t, runV2Hook(rt, h, event))
	if !applied.Details.Consolidation.Applied || strings.Contains(applied.Summary, instructions) {
		t.Fatalf("applied summary:\n%s", applied.Summary)
	}
	if messages, _ := h.ModelsFake.Streams.Requests[0]["messages"].([]any); !strings.Contains(fmt.Sprint(messages), instructions) {
		t.Fatalf("the level-1 input lacks the instructions: %v", messages)
	}

	rt, h, entries = level1Setup(t, testfix.AssistantText("no tool"))
	event = v2Event(entries, "u2", 20_000, "manual")
	event.CustomInstructions = instructions
	failed := level1CompactionOf(t, runV2Hook(rt, h, event))
	if failed.Details.Consolidation.Applied || !strings.HasSuffix(failed.Summary, "\n\n---\n\n"+instructions) {
		t.Fatalf("level-0 summary:\n%s", failed.Summary)
	}
}
