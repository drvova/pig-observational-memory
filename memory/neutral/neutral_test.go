package neutral

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/hooks"
	"github.com/drvova/pig-observational-memory/memory/hostinfo"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/runtime"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

type fixed []session.Entry

func (f *fixed) Entries() ([]session.Entry, error) { return append([]session.Entry(nil), (*f)...), nil }

func at(ts string) map[string]any { return map[string]any{"timestamp": ts} }

func setup(t *testing.T) (*Host, *fixed, *runtime.Runtime) {
	t.Helper()
	root := t.TempDir()
	old := hostinfo.AgentDir
	hostinfo.AgentDir = func() (string, error) { return root, nil }
	t.Cleanup(func() { hostinfo.AgentDir = old })
	tr := &fixed{
		testfix.RawMessage("u1", "Deploy the api to staging on port 8080 on Friday.", at("2026-05-01T10:00:00.000Z")),
		testfix.MessageEntry("a1", testfix.AssistantText("Will do: staging, port 8080."), at("2026-05-01T10:00:05.000Z")),
	}
	// Enough conversation that the summary budget (bounded by the source it replaces) holds
	// records rather than the minimal brief.
	for i := range 40 {
		id := fmt.Sprint(i)
		*tr = append(*tr,
			testfix.RawMessage("q"+id, fmt.Sprintf("Note %d: the build cache for service-%d lives in /var/cache/svc%d and is pruned every %d hours.", i, i, i, i+2), at("2026-05-01T11:00:00.000Z")),
			testfix.MessageEntry("r"+id, testfix.AssistantText(fmt.Sprintf("Understood, service-%d prunes /var/cache/svc%d every %d hours.", i, i, i+2)), at("2026-05-01T11:00:05.000Z")))
	}
	h := &Host{Session: "s1", Dir: t.TempDir(), Root: root, Transcript: tr, Store: StoreFor(root, "test", "s1")}
	return h, tr, runtime.New()
}

// Stored entries are placed after the entry they name, in written order, with parent ids
// relinked; one naming an entry the transcript lost goes last.
func TestBranchMerge(t *testing.T) {
	tr := []session.Entry{testfix.RawMessage("u1", "a"), testfix.RawMessage("u2", "b")}
	stored := []Stored{
		{After: "u1", Entry: map[string]any{"type": "custom", "id": "x1"}},
		{After: "x1", Entry: map[string]any{"type": "custom", "id": "x2"}},
		{After: "u1", Entry: map[string]any{"type": "custom", "id": "x3"}},
		{After: "gone", Entry: map[string]any{"type": "custom", "id": "x4"}},
		{After: "", Entry: map[string]any{"type": "custom", "id": "x0"}},
	}
	b := Branch(tr, stored)
	var ids []string
	for _, e := range b {
		ids = append(ids, e.ID)
	}
	if got := strings.Join(ids, " "); got != "x0 u1 x1 x2 x3 u2 x4" {
		t.Fatalf("order %s", got)
	}
	if b[2].ParentID != "u1" || b[0].ParentID != "" {
		t.Fatalf("parents %q %q", b[2].ParentID, b[0].ParentID)
	}
}

// A turn end stores level-0 records beside the transcript (never in it); a compaction cuts on
// a marker at the tip, renders a summary from the records with no request, and stores the
// compaction; the next compaction's range starts at that marker.
func TestTurnEndAndCompaction(t *testing.T) {
	counter := testfix.CountRequests(t)
	h, tr, rt := setup(t)
	hooks.OnTurnEnd(rt, h)
	branch, err := h.GetBranch()
	if err != nil || len(branch) <= len(*tr) || len(*tr) != 82 {
		t.Fatalf("branch %d entries, transcript %d, %v", len(branch), len(*tr), err)
	}
	c, err := h.Compaction(context.Background(), rt, "auto")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(c.Summary, "8080") {
		t.Fatalf("summary lacks the record:\n%s", c.Summary)
	}
	if got, _ := h.LatestSummary(); got != c.Summary {
		t.Fatal("the stored compaction is not the latest")
	}
	if counter.Total() != 0 {
		t.Fatalf("requests %v", counter.ByStage())
	}
	*tr = append(*tr, testfix.RawMessage("u2", "Also the database migrates on Monday.", at("2026-05-02T09:00:00.000Z")))
	branch, _ = h.GetBranch()
	start := ledger.CompactionRangeStartIndex(branch)
	if branch[start].CustomType != CutMarker || branch[len(branch)-1].ID != "u2" {
		t.Fatalf("range starts at %+v", branch[start])
	}
	c2, err := h.Compaction(context.Background(), rt, "auto")
	if err != nil || !strings.Contains(c2.Summary, "Monday") || !strings.Contains(c2.Summary, "8080") {
		t.Fatalf("second summary %v:\n%v", err, c2)
	}
	if filepath.Dir(h.Store.Path) == "" {
		t.Fatal("no store path")
	}
}
