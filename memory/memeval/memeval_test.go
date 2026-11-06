package memeval

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/serialize"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

func TestMain(m *testing.M) {
	serialize.Location = time.UTC
	code := m.Run()
	child.Close()
	os.Exit(code)
}

// testConfig is the Tier 0 measurement the tests run: DefaultConfig over 2 seeds by default,
// fast enough for CI, or MEMEVAL_SEEDS=n seeds. Under the race detector the default is one seed
// over 430 turns (21 compactions).
func testConfig(t *testing.T) Config {
	if v := os.Getenv("MEMEVAL_SEEDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 {
			t.Fatalf("MEMEVAL_SEEDS=%q", v)
		}
		return DefaultConfig(n)
	}
	if raceEnabled {
		cfg := DefaultConfig(1)
		cfg.Turns, cfg.MaxCompactions = 430, 21
		return cfg
	}
	return DefaultConfig(2)
}

// The test binary's one namespace child, cached across the controls.
var (
	child  = &ChildLister{}
	lister = &CachingLister{Lister: child}
)

var (
	measured    Measurement
	measureErr  error
	measureOnce sync.Once
)

// measurement is the Tier 0 measurement the tests share (testConfig), counting requests while
// it runs.
func measurement(t *testing.T) Measurement {
	t.Helper()
	measureOnce.Do(func() {
		counter := testfix.CountRequests(t)
		measured, measureErr = Measure(context.Background(), testConfig(t), lister)
		if measureErr == nil && counter.Total() != 0 {
			measureErr = fmt.Errorf("model requests made: %v", counter.ByStage())
		}
	})
	if measureErr != nil {
		t.Fatal(measureErr)
	}
	return measured
}

// noLoss are the classes the current engine never loses (shown or reachable through a printed
// snippet after every compaction count measured): the baseline later phases must not regress
// (docs/brain-memory-baseline.md). The other classes are measured, not gated.
var noLoss = []Class{ClassIdentifier, ClassConstraint, ClassIntention, ClassSuperseded}

// TestTier0Retention is the Tier 0 run (docs/brain-memory.md, phase 0): every seed through 30
// compactions at level 0, zero model requests, every invariant on every compaction, no
// printed snippet that fails to run, and the no-loss classes never lost; the curves are logged.
func TestTier0Retention(t *testing.T) {
	m := measurement(t)
	if m.Requests != 0 {
		t.Fatalf("%d requests", m.Requests)
	}
	for _, r := range m.Results {
		if len(r.SnippetErrors) > 0 {
			t.Fatalf("seed %d: printed snippets failed: %v", r.Seed, r.SnippetErrors)
		}
		if r.Snippets == 0 {
			t.Fatalf("seed %d ran no snippet", r.Seed)
		}
	}
	for _, run := range m.Runs {
		if len(run.Snapshots) < 20 {
			t.Fatalf("seed %d: only %d compactions", run.Script.Seed, len(run.Snapshots))
		}
	}
	for name, tally := range InvariantTallies(m.Results) {
		if tally.Broken > 0 {
			t.Errorf("invariant %q broken on %d compactions; first: %s", name, tally.Broken, tally.First)
		}
	}
	curves := Curves(m.Results)
	for _, class := range noLoss {
		for n, tally := range curves[class] {
			if tally.Lost > 0 {
				t.Errorf("%s lost after %d compactions: %+v", class, n, tally)
			}
		}
	}
	for n, tally := range curves[ClassSuperseded] {
		if tally.FalseCurrent > 0 {
			t.Errorf("superseded value shown as current after %d compactions: %+v", n, tally)
		}
	}
	t.Logf("shown / reachable / lost by compactions since the probe left the context\n%s\n%s", Table(m.Results, Counts), InvariantTable(m.Results))
}

// Determinism control: the same seed run again gives the same script, the same summaries byte
// for byte and the same result.
func TestTier0Deterministic(t *testing.T) {
	m := measurement(t)
	first := m.Runs[0]
	cfg := m.Config
	opt := cfg.Options
	opt.Turns = cfg.Turns
	again, err := Simulate(context.Background(), Generate(first.Script.Seed, opt), cfg.Window, 8)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(first.Script, again.Script) {
		t.Fatal("scripts differ")
	}
	if len(again.Snapshots) != 8 {
		t.Fatalf("%d compactions", len(again.Snapshots))
	}
	for i := range again.Snapshots {
		if first.Snapshots[i].Summary != again.Snapshots[i].Summary {
			t.Fatalf("compaction %d: summaries differ", i+1)
		}
	}
	prefix := first
	prefix.Snapshots = first.Snapshots[:8]
	if !reflect.DeepEqual(Evaluate(prefix, prefix.Script.Probes, lister), Evaluate(again, again.Script.Probes, lister)) {
		t.Fatal("results differ")
	}
}

// Shuffled-gold control: scoring a run against another seed's probes (same turns, other names
// and values) finds almost nothing, so the scorer matches the planted bytes, not the shape of
// the turns.
func TestTier0ShuffledGold(t *testing.T) {
	m := measurement(t)
	other := Generate(101, Options{Turns: m.Config.Turns, PlantEvery: m.Config.Options.PlantEvery})
	real, shuffled := hits(m.Results[0]), hits(Evaluate(m.Runs[0], other.Probes, lister))
	t.Logf("hits: real %d of %d, shuffled gold %d of %d", real.hit, real.total, shuffled.hit, shuffled.total)
	if real.total != shuffled.total || shuffled.hit*20 > shuffled.total || shuffled.hit*4 > real.hit {
		t.Fatalf("shuffled gold scores %d of %d (real %d of %d)", shuffled.hit, shuffled.total, real.hit, real.total)
	}
}

type hitCount struct{ hit, total int }

func hits(r Result) hitCount {
	var h hitCount
	for _, c := range r.Cells {
		h.total++
		if c.Outcome != Lost {
			h.hit++
		}
	}
	return h
}

// Renamed-ids control: the same scripts with every planted name drawn again lose exactly the
// same probes, so the curves measure the classes, not particular names. Which kept probes are
// shown rather than reachable may shift, since names of other lengths change what fits the
// budget; the shifts are logged. Under the race detector it compares 150 turns of one seed.
func TestTier0RenamedIDs(t *testing.T) {
	m := measurement(t)
	cfg := m.Config
	if raceEnabled && os.Getenv("MEMEVAL_SEEDS") == "" {
		cfg.Turns, cfg.MaxCompactions = 150, 7
		var err error
		if m, err = Measure(context.Background(), cfg, lister); err != nil {
			t.Fatal(err)
		}
	}
	cfg.Options.Rename = true
	renamed, err := Measure(context.Background(), cfg, lister)
	if err != nil {
		t.Fatal(err)
	}
	// A probe is compared after each compaction count both runs scored it at: names of other
	// lengths change the summaries' sizes, so a run can reach its last compaction a turn earlier
	// and score a late probe once fewer (logged).
	cells := func(results []Result) map[string]Cell {
		out := map[string]Cell{}
		for i, r := range results {
			for _, c := range r.Cells {
				out[fmt.Sprintf("%d/%s/%d", i, c.Probe, c.N)] = c
			}
		}
		return out
	}
	x, y := cells(m.Results), cells(renamed.Results)
	compared, only := 0, 0
	for key, c := range x {
		d, ok := y[key]
		if !ok {
			only++
			continue
		}
		compared++
		if (c.Outcome == Lost) != (d.Outcome == Lost) || c.FalseCurrent != d.FalseCurrent {
			t.Errorf("%s: %+v, renamed %+v", key, c, d)
		}
	}
	only += len(y) - compared
	if compared == 0 || only > len(x)/20 {
		t.Fatalf("compared %d cells, %d scored in one run only", compared, only)
	}
	a, b := Curves(m.Results), Curves(renamed.Results)
	for _, class := range Classes {
		for _, n := range Counts {
			if x, y := a[class][n], b[class][n]; x != y {
				t.Logf("%s after %d: %+v, renamed %+v", class, n, x, y)
			}
		}
	}
	t.Logf("compared %d cells, %d scored in one run only", compared, only)
	if m.Runs[0].Script.Probes[0].Needles[0] == renamed.Runs[0].Script.Probes[0].Needles[0] {
		t.Fatal("rename changed nothing")
	}
}
