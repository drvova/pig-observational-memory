package memeval

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
)

// The summary-of-summary arm erodes: it loses probes the engine keeps, and the A/B items carry both arms of every compaction with no request.
func TestSummaryOfSummaryAndABItems(t *testing.T) {
	script := Generate(2, Options{Turns: 400, PlantEvery: 50})
	run, err := Simulate(context.Background(), script, SmallWindow, 10)
	if err != nil {
		t.Fatal(err)
	}
	engine := Evaluate(run, script.Probes, nil)
	eroded := SummaryOfSummary(run, script.Probes)
	if len(eroded.Cells) != len(engine.Cells) {
		t.Fatalf("%d cells, want %d", len(eroded.Cells), len(engine.Cells))
	}
	count := func(r Result, o Outcome) int {
		n := 0
		for _, c := range r.Cells {
			if c.Outcome == o {
				n++
			}
		}
		return n
	}
	if count(eroded, Shown) == 0 || count(eroded, Lost) <= count(engine, Lost) {
		t.Fatalf("summary-of-summary shows %d and loses %d cells, the engine loses %d", count(eroded, Shown), count(eroded, Lost), count(engine, Lost))
	}
	nl := NaturalLanguage(run.Snapshots[0].Summary)
	if !strings.HasPrefix(nl, ledger.UnverifiedNotice) || !strings.Contains(nl, "In ") {
		t.Fatalf("natural rendering %q", nl)
	}
	path := filepath.Join(t.TempDir(), "ab.jsonl")
	n, err := WriteABItems(path, []Run{run})
	if err != nil || n != 2*len(run.Snapshots) {
		t.Fatalf("%d items, %v", n, err)
	}
	if data, _ := os.ReadFile(path); !strings.Contains(string(data), `"arm":"natural"`) {
		t.Fatal("no natural arm")
	}
}
