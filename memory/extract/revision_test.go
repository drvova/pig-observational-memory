package extract

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
)

// jsonValueOf is v as decoded JSON, as an entry read back from a session holds it.
func jsonValueOf(v any) any {
	raw, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(raw, &out)
	return out
}

// A file's successive edits become a chain of small revisions: each names the one before it,
// counts the lines added and removed, keeps the first changed line redacted, and a failed edit
// is none. A later extraction continues the chain from the om.revisions.recorded entries on the
// branch.
func TestRevisionChain(t *testing.T) {
	ok := func(id, callID, name string) session.Entry { return result(id, callID, name, "ok", false) }
	key := "AKIA" + strings.Repeat("Q7", 4) + "ZX3V" + "LMN5"
	first := []session.Entry{
		user("u1", "Add the limiter"),
		assistant("a1",
			call{"c1", "Write", map[string]any{"file_path": "/work/app/api/limit.go", "content": "package api\n\nconst Rate = 10\n"}},
			call{"c2", "Edit", map[string]any{"file_path": "/work/app/api/limit.go", "old_string": "const Rate = 10", "new_string": "const Rate = 20\nconst Burst = 5 // AWS_ACCESS_KEY_ID=" + key}},
			call{"c3", "Edit", map[string]any{"file_path": "/work/app/api/limit.go", "old_string": "x", "new_string": "y"}},
		),
		ok("t1", "c1", "Write"), ok("t2", "c2", "Edit"),
		result("t3", "c3", "Edit", "<tool_use_error>String to replace not found in file.</tool_use_error>", true),
	}
	r := run(first)
	if len(r.Revisions) != 2 {
		t.Fatalf("revisions %+v", r.Revisions)
	}
	w, e := r.Revisions[0], r.Revisions[1]
	if w.Prev != "" || e.Prev != w.ID || w.Path != "/work/app/api/limit.go" || w.Added != 3 || e.Added != 2 || e.Removed != 1 {
		t.Fatalf("chain %+v %+v", w, e)
	}
	if e.Summary != "const Rate = 20" || strings.Contains(e.Summary+w.Summary, key) {
		t.Fatalf("summaries %q %q", w.Summary, e.Summary)
	}
	if len(e.ID) != 12 || e.ID == w.ID {
		t.Fatalf("id %q", e.ID)
	}
	// The next turn's edit continues the chain from the stored entry.
	stored := append(first, session.Entry{Type: "custom", ID: "om-r", CustomType: ledger.OMRevisionsRecorded, Data: jsonValueOf(ledger.RevisionsRecordedData{Revisions: r.Revisions})})
	next := append(stored,
		user("u2", "Raise the burst"),
		assistant("a2", call{"c4", "Edit", map[string]any{"file_path": "api/limit.go", "old_string": "Burst = 5", "new_string": "Burst = 8"}}),
		ok("t4", "c4", "Edit"),
	)
	r2 := Extract(Input{Entries: next, Start: len(stored), End: len(next), Cwd: cwd})
	if len(r2.Revisions) != 1 || r2.Revisions[0].Prev != e.ID {
		t.Fatalf("the next turn's revision %+v, want prev %s", r2.Revisions, e.ID)
	}
	if chain := ledger.FileRevisions(append(next, session.Entry{Type: "custom", ID: "om-r2", CustomType: ledger.OMRevisionsRecorded, Data: jsonValueOf(ledger.RevisionsRecordedData{Revisions: r2.Revisions})}), "/work/app/api/limit.go"); len(chain) != 3 {
		t.Fatalf("the file's chain %d long", len(chain))
	}
}
