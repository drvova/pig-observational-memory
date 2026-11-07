package hooks

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/consolidate"
	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/harness"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// citeReply cites the curated memory name with ids.
func citeReply(name string, ids ...string) map[string]any {
	list := make([]any, len(ids))
	for i, id := range ids {
		list[i] = id
	}
	return testfix.AssistantToolCall("call-1", consolidate.ToolName, map[string]any{
		"curated": []any{map[string]any{"name": name, "supportingIds": list}},
	})
}

// Level 1 sees the curated memories and may cite one with records of the user's own words: the
// citation is recorded, but a citation alone never promotes a staged memory (decision 7: a user
// quote or two independent evidence roots), and the compaction still makes exactly one request.
// A citation resting on the assistant's words alone is refused. A memory another session staged
// is visible only there (curated.SeenBy), so it is not listed, and a citation of it is refused.
func TestV2Level1CitationNeverPromotesStagedMemory(t *testing.T) {
	for _, c := range []struct {
		name     string
		ids      []string
		stagedBy string
		cited    bool
	}{
		{"user support", []string{l1PortID}, "", true},
		{"assistant support only", []string{l1MakefileID}, "", false},
		{"staged by another session", []string{l1PortID}, "earlier-session", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			counter := testfix.CountRequests(t)
			rt, h, entries := level1Setup(t, citeReply("deploy-port", c.ids...))
			h.Home = t.TempDir()
			store, ok := harness.StoreOf(h, rt.Config())
			if !ok {
				t.Fatal("no store")
			}
			stagedBy := c.stagedBy
			if stagedBy == "" {
				stagedBy = h.SessionID()
			}
			if _, err := store.Save(curated.Memory{Name: "deploy-port", Type: curated.TypeProject, Description: "Staging deploys use port 8080.", Staged: true}, stagedBy); err != nil {
				t.Fatal(err)
			}

			out := level1CompactionOf(t, runV2Hook(rt, h, v2Event(entries, "u2", 20_000, "threshold")))

			if counter.Total() != 1 || h.ModelsFake.Streams.Calls() != 1 {
				t.Fatalf("requests %v", counter.ByStage())
			}
			listed := strings.Contains(fmt.Sprint(h.ModelsFake.Streams.Requests[0]["messages"]), "- deploy-port [project] (staged): Staging deploys use port 8080.")
			if listed != (c.stagedBy == "") {
				t.Fatalf("listed %v: a staged memory is listed only in the session that staged it:\n%v", listed, h.ModelsFake.Streams.Requests[0]["messages"])
			}
			report := out.Details.Consolidation
			m, err := store.Get("deploy-port")
			if err != nil {
				t.Fatal(err)
			}
			if c.cited {
				if !report.Applied || report.Cited != 1 || report.Promoted != 0 || !m.Staged {
					t.Fatalf("a citation alone promoted it: report %+v, memory %+v", report, m)
				}
				if events, err := store.UsageEventsByName(); err != nil || !slices.ContainsFunc(events["deploy-port"], func(e curated.UsageEvent) bool { return e.Event == curated.EventCited }) {
					t.Fatalf("the citation is not recorded: %+v, %v", events["deploy-port"], err)
				}
				if text, _ := curated.IndexFor(store, harness.ProjectRoot(h.Cwd()), 0); strings.Contains(text, "deploy-port") {
					t.Fatalf("a staged memory a citation alone bears is in the next index:\n%s", text)
				}
				return
			}
			if report.Applied || report.Cited != 0 || !m.Staged || report.Refused != 1 {
				t.Fatalf("an assistant-only citation: report %+v, memory %+v", report, m)
			}
		})
	}
}
