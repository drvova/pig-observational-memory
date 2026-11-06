package memeval

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/drvova/pig-observational-memory/memory/debuglog"
)

// Counts are the compaction counts the curves report.
var Counts = []int{1, 2, 4, 5, 8, 10, 16, 20}

// Config is one Tier 0 measurement.
type Config struct {
	Seeds   []int64
	Turns   int
	Window  Window
	Options Options
	// MaxCompactions is the last compaction simulated.
	MaxCompactions int
}

// DefaultConfig measures seeds 1..seeds over 600 turns (up to 30 compactions) in SmallWindow,
// the probe set planted every 50 turns.
func DefaultConfig(seeds int) Config {
	c := Config{Turns: 600, Window: SmallWindow, MaxCompactions: 30, Options: Options{PlantEvery: 50}}
	for s := 1; s <= seeds; s++ {
		c.Seeds = append(c.Seeds, int64(s))
	}
	return c
}

// Measurement is every seed's run and result.
type Measurement struct {
	Config  Config
	Runs    []Run
	Results []Result
	// Requests is how many model requests the runs made (Tier 0 makes none).
	Requests int
}

// Measure simulates and evaluates every seed, counting the model requests made meanwhile
// (process-wide, as testfix.CountRequests does).
func Measure(ctx context.Context, cfg Config, lister Lister) (Measurement, error) {
	m := Measurement{Config: cfg}
	var mu sync.Mutex
	unsubscribe := debuglog.Subscribe(func(event string, data map[string]any) {
		if debuglog.IsRequest(data) {
			mu.Lock()
			m.Requests++
			mu.Unlock()
		}
	})
	defer unsubscribe()
	for _, seed := range cfg.Seeds {
		opt := cfg.Options
		opt.Turns = cfg.Turns
		run, err := Simulate(ctx, Generate(seed, opt), cfg.Window, cfg.MaxCompactions)
		if err != nil {
			return m, err
		}
		m.Runs = append(m.Runs, run)
		m.Results = append(m.Results, Evaluate(run, run.Script.Probes, lister))
	}
	return m, nil
}

// Tally is the outcomes of one class at one compaction count.
type Tally struct {
	Shown, Reachable, Lost, FalseCurrent int
}

// Total is how many probes were scored.
func (t Tally) Total() int { return t.Shown + t.Reachable + t.Lost }

// Curves tallies the results by class and compaction count (pending probes left out).
func Curves(results []Result) map[Class]map[int]Tally {
	out := map[Class]map[int]Tally{}
	for _, r := range results {
		for _, c := range r.Cells {
			if out[c.Class] == nil {
				out[c.Class] = map[int]Tally{}
			}
			t := out[c.Class][c.N]
			switch c.Outcome {
			case Shown:
				t.Shown++
			case Reachable:
				t.Reachable++
			case Lost:
				t.Lost++
			}
			if c.FalseCurrent {
				t.FalseCurrent++
			}
			out[c.Class][c.N] = t
		}
	}
	return out
}

// InvariantTally is how many compactions kept and broke each invariant, with the first break.
type InvariantTally struct {
	Kept, Broken int
	First        string
}

// InvariantTallies tallies the checks.
func InvariantTallies(results []Result) map[string]InvariantTally {
	out := map[string]InvariantTally{}
	for _, r := range results {
		for _, c := range r.Checks {
			t := out[c.Name]
			if c.OK {
				t.Kept++
			} else {
				t.Broken++
				if t.First == "" {
					t.First = fmt.Sprintf("seed %d, compaction %d: %s", r.Seed, c.N, c.Detail)
				}
			}
			out[c.Name] = t
		}
	}
	return out
}

// Table is the curves as a markdown table: one row per class, one column per count, each
// cell shown/reachable/lost in percent of the probes scored.
func Table(results []Result, counts []int) string {
	curves := Curves(results)
	var b strings.Builder
	b.WriteString("| class |")
	for _, n := range counts {
		fmt.Fprintf(&b, " %d |", n)
	}
	b.WriteString("\n|---|")
	for range counts {
		b.WriteString("---|")
	}
	b.WriteString("\n")
	for _, class := range Classes {
		fmt.Fprintf(&b, "| %s |", class)
		for _, n := range counts {
			t := curves[class][n]
			if t.Total() == 0 {
				b.WriteString(" n/a |")
				continue
			}
			fmt.Fprintf(&b, " %s / %s / %s", pct(t.Shown, t.Total()), pct(t.Reachable, t.Total()), pct(t.Lost, t.Total()))
			if class == ClassSuperseded {
				fmt.Fprintf(&b, " (old as current %d)", t.FalseCurrent)
			}
			b.WriteString(" |")
		}
		b.WriteString("\n")
	}
	b.WriteString("| probes per class |")
	for _, n := range counts {
		fmt.Fprintf(&b, " %d |", curves[ClassIdentifier][n].Total())
	}
	b.WriteString("\n")
	return b.String()
}

// InvariantTable is the invariants as a markdown table.
func InvariantTable(results []Result) string {
	tallies := InvariantTallies(results)
	var b strings.Builder
	b.WriteString("| invariant | compactions kept | broken | first break |\n|---|---|---|---|\n")
	for _, name := range Invariants {
		t := tallies[name]
		first := t.First
		if first == "" {
			first = "none"
		}
		fmt.Fprintf(&b, "| %s | %d | %d | %s |\n", name, t.Kept, t.Broken, strings.ReplaceAll(first, "|", "\\|"))
	}
	return b.String()
}

func pct(n, total int) string { return fmt.Sprintf("%d%%", (100*n+total/2)/total) }
