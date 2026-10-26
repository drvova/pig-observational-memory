package hooks

import (
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// retiredToolBranch is thinBranch with the retained turn's long result made by bakes, a tool the
// agent defined with the removed query language before the code tool.
func retiredToolBranch() []ledger.Entry {
	entries := thinBranch()
	for _, e := range entries {
		switch e.ID {
		case "pa1":
			for _, block := range e.Message["content"].([]any) {
				if b, _ := block.(map[string]any); b["type"] == "toolCall" {
					b["name"], b["arguments"] = "bakes", map[string]any{"input": "sourdough"}
				}
			}
		case "pt1":
			e.Message["toolName"] = "bakes"
		}
	}
	return entries
}

// An old defined-tool call on the branch stays memory plumbing after the defined tools are
// retired (docs/code-tool.md, step 8): with the stored definition's name in the skip list
// (Manager.NoteRetired at session start), thinning leaves its result as it was, while the same
// branch with no skip list thins it; nothing registers a tool.
func TestRetiredDefinedToolCallsAreNotThinned(t *testing.T) {
	counter := testfix.CountRequests(t)
	control, hc := v2Setup(t, retiredToolBranch())
	if n := OnContext(control, hc, contextMessages(t, retiredToolBranch())); n != 1 {
		t.Fatalf("without the skip list thinned %d", n)
	}

	rt, h := v2Setup(t, retiredToolBranch())
	h.Home = t.TempDir()
	store := curated.New(curated.DefaultDir(h.Home))
	if _, err := store.Define(curated.Definition{Name: "bakes", Query: `notes | search "sourdough"`, Description: "Bakes."}, "s0"); err != nil {
		t.Fatal(err)
	}
	m := harness.NewManager(func(host.ToolHost) (curated.Call, bool) { return curated.Call{Store: store, Source: "s0"}, true })
	m.NoteRetired(h)
	rt.SetSkipTools(m.SkipNames)
	messages := contextMessages(t, retiredToolBranch())
	if n := OnContext(rt, h, messages); n != 0 {
		t.Fatalf("an old defined-tool call was thinned (%d)", n)
	}
	if text := resultTextOf(messages, "pc1"); text == "" || text[:len("--- FAIL")] != "--- FAIL" {
		t.Fatalf("the old defined-tool result changed: %.80q", text)
	}
	if len(h.Registered) != 0 || len(h.SetActiveCalls) != 0 {
		t.Fatalf("registered %d, set-active %v", len(h.Registered), h.SetActiveCalls)
	}
	if counter.Total() != 0 {
		t.Fatalf("requests %v", counter.ByStage())
	}
}
