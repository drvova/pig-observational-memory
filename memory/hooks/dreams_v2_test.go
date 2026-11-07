package hooks

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/consolidate"
	"github.com/drvova/pig-observational-memory/memory/dreams"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// Scenario 20 with dreams on: level 1's one request also offers hypotheses; one that
// over-generalizes clean inputs into a rule ("the user always deploys on Fridays") is kept only in
// its om.hypotheses.recorded entry and the summary's dream section, never as a record, a fact
// class line, a reflection or a constraint; the request count stays one.
func TestV2DreamHypothesisNeverAFact(t *testing.T) {
	counter := testfix.CountRequests(t)
	hypothesis := "Does the user always deploy on Fridays?"
	rt, h, entries := level1Setup(t, testfix.AssistantToolCall("call-1", consolidate.ToolName, map[string]any{
		"merges":     []any{map[string]any{"ids": []any{l1PortID, l1MakefileID}, "content": "Staging deploys run from the Makefile on port 8080", "subject": "deploy"}},
		"hypotheses": []any{map[string]any{"content": hypothesis, "supportingIds": []any{l1PortID}}},
	}))
	cfg := rt.Config()
	cfg.Dreams = true
	rt.SetConfig(cfg)

	c := level1CompactionOf(t, runV2Hook(rt, h, v2Event(entries, "u2", 20_000, "threshold")))
	if counter.Total() != 1 {
		t.Fatalf("%d requests, want the one level-1 request", counter.Total())
	}
	request := h.ModelsFake.Streams.Requests[0]
	raw, _ := json.Marshal(request)
	if !strings.Contains(string(raw), `"hypotheses"`) {
		t.Fatal("the dreams setting did not offer hypotheses")
	}
	head, section, ok := strings.Cut(c.Summary, dreams.Header)
	if !ok || !strings.Contains(section, "- level-1 hypothesis: "+hypothesis) || strings.Contains(head, "Fridays") {
		t.Fatalf("summary:\n%s", c.Summary)
	}
	for _, o := range c.Details.Observations {
		if strings.Contains(o.Content, "Fridays") {
			t.Fatalf("a hypothesis became a record: %+v", o)
		}
	}
	for _, r := range c.Details.Reflections {
		if strings.Contains(r.Content, "Fridays") {
			t.Fatalf("a hypothesis became a reflection: %+v", r)
		}
	}
	if !strings.Contains(strings.Join(h.AppendedTypes(), ","), "om.hypotheses.recorded") {
		t.Fatalf("appended %v", h.AppendedTypes())
	}

	// Dreams off: the same reply's hypothesis is dropped and no section is written.
	counter2 := testfix.CountRequests(t)
	rt2, h2, entries2 := level1Setup(t, testfix.AssistantToolCall("call-1", consolidate.ToolName, map[string]any{
		"merges":     []any{map[string]any{"ids": []any{l1PortID, l1MakefileID}, "content": "Staging deploys run from the Makefile on port 8080", "subject": "deploy"}},
		"hypotheses": []any{map[string]any{"content": hypothesis, "supportingIds": []any{l1PortID}}},
	}))
	c2 := level1CompactionOf(t, runV2Hook(rt2, h2, v2Event(entries2, "u2", 20_000, "threshold")))
	if strings.Contains(c2.Summary, dreams.Header) || strings.Contains(c2.Summary, "Fridays") || strings.Contains(strings.Join(h2.AppendedTypes(), ","), "om.hypotheses.recorded") {
		t.Fatalf("dreams off:\n%s", c2.Summary)
	}
	raw2, _ := json.Marshal(h2.ModelsFake.Streams.Requests[0])
	if strings.Contains(string(raw2), `"hypotheses"`) || counter2.Total() != 1 {
		t.Fatal("dreams off offered hypotheses")
	}
}
