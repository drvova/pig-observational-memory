package memeval

import (
	"context"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
)

// The promise syndrome locates every protected row dropped from a summary and raises nothing on
// the summaries as written, at the default window and under a summary cap; every compaction
// after the first rechecks the one before it in its details (cmd/memeval prints the 20-seed
// figures).
func TestPromiseFaultInjection(t *testing.T) {
	var runs []Run
	for _, capped := range []int{0, 1200} {
		w := SmallWindow
		w.SummaryMaxTokens = capped
		run, err := Simulate(context.Background(), Generate(7, Options{Turns: 220, PlantEvery: 50}), w, 8)
		if err != nil {
			t.Fatal(err)
		}
		runs = append(runs, run)
	}
	got := PromiseFigures(runs, 1)
	if got.Checked < 6 || got.Unread > 0 || got.FalseAlarms > 0 || got.Injected == 0 || got.Detected != got.Injected || got.StoredFindings > 0 || got.Stored < got.Checked-2 {
		t.Fatalf("%+v\n%s", got, got.Table())
	}
	if got.Promised <= got.Shown || got.Shown == 0 {
		t.Fatalf("promised %d, shown %d: the runs should list some protected records", got.Promised, got.Shown)
	}
	// A summary that keeps its text but whose ledger promises more than it was built from: the
	// digest moves.
	snap := runs[0].Snapshots[len(runs[0].Snapshots)-1]
	branch := append([]ledger.Entry{}, snap.Branch...)
	at := len(branch) - 1
	e := branch[at]
	details := e.Details.(map[string]any)
	promise := map[string]any{}
	for k, v := range details["promise"].(map[string]any) {
		promise[k] = v
	}
	promise["digest"] = "000000000000"
	moved := map[string]any{}
	for k, v := range details {
		moved[k] = v
	}
	moved["promise"] = promise
	e.Details = moved
	branch[at] = e
	if c, ok := ledger.CheckPromise(branch, at); !ok || !c.Mismatch {
		t.Fatalf("a moved digest: %+v", c)
	}
}
