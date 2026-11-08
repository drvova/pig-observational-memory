// Command memeval runs the Tier 0 retention harness (memory/memeval, docs/brain-memory-baseline.md)
// and prints the retention curves and the invariants as markdown tables. It makes no model
// request: level 0 only, over generated transcripts.
//
//	go run ./cmd/memeval -seeds 20
//	go run ./cmd/memeval -suite forgetting -seeds 5
//	go run ./cmd/memeval -suite sharing -seeds 5
//	go run ./cmd/memeval -suite constraintrot
//	go run ./cmd/memeval -suite arcbench -arcbench-cases <dir>/data/rule-loss/cases.jsonl
//	go run ./cmd/memeval -suite retrieval -seeds 20
//	go run ./cmd/memeval -suite settle -seeds 20
//
// The forgetting (Tier 0b) and sharing (Tier 0c) suites print a table of checks each, pass, fail
// or not built; their only requests are scripted level-1 replies a fake host serves.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/drvova/pig-observational-memory/memory/memeval"
	"github.com/drvova/pig-observational-memory/memory/rules"
	"github.com/drvova/pig-observational-memory/memory/serialize"
	"github.com/drvova/pig-observational-memory/memory/session"
)

func main() {
	seeds := flag.Int("seeds", 5, "seeds 1..n to run")
	turns := flag.Int("turns", 0, "turns per transcript (0: the default)")
	compactions := flag.Int("compactions", 0, "compactions per transcript at most (0: the default)")
	window := flag.Int("window", 0, "context window in tokens (0: the default small window)")
	summaryMax := flag.Int("summary-max", 0, "summary cap in tokens, as compactionSummaryMaxTokens (0: none)")
	noSnippets := flag.Bool("no-snippets", false, "do not run the printed snippets (every unshown probe counts as lost)")
	suite := flag.String("suite", "tier0", "what to run: tier0 (retention), forgetting (Tier 0b), sharing (Tier 0c), settle (the no-op definition replay), all, cues (the cue and false-cue rates), constraintrot, arcbench or retrieval")
	arcCases := flag.String("arcbench-cases", "", "ARCBench's data/rule-loss/cases.jsonl (data only; its code is never run)")
	sleepArm := flag.String("sleep", "on", "the sleep pass's compaction fallback: on (sleepAfterCompactions 4, the default) or off")
	sumsum := flag.Bool("sumsum", false, "also print the summary-of-summary arm's erosion curve over the same runs")
	dreamArm := flag.String("dreams", "off", "the dream section (phase 8): off (the default) or on; with on every compaction is checked for a false promotion")
	dreamBench := flag.String("dreambench-out", "", "write the DreamBench-SWE bar's items (each compaction's summary with dreams off and on, with the probes' questions) to this JSONL file; ready, not run: it needs models")
	abOut := flag.String("ab-out", "", "write the Tier 1/2 A/B items (tables and natural-language renderings of the same summaries, with their questions and reference answers) to this JSONL file; ready, not run: it needs a model")
	flag.Parse()
	serialize.Location = time.UTC

	cfg := memeval.DefaultConfig(*seeds)
	if *turns > 0 {
		cfg.Turns = *turns
	}
	if *compactions > 0 {
		cfg.MaxCompactions = *compactions
	}
	if *window > 0 {
		cfg.Window.Context = *window
	}
	cfg.Window.SummaryMaxTokens = *summaryMax
	switch *sleepArm {
	case "on":
	case "off":
		cfg.Window.SleepOff = true
	default:
		fmt.Fprintln(os.Stderr, "memeval: -sleep is on or off")
		os.Exit(2)
	}
	switch *dreamArm {
	case "off":
	case "on":
		cfg.Window.Dreams = true
	default:
		fmt.Fprintln(os.Stderr, "memeval: -dreams is on or off")
		os.Exit(2)
	}
	// The dream figures, printed last whatever ran, with dreams on.
	defer func() {
		if cfg.Window.Dreams {
			sections, lines, promotions := memeval.DreamFigures()
			fmt.Printf("\nDreams (-dreams on): %d dream sections, %d dream lines, %d false promotions (a dream line or the header in a fact class, a record or the curated store), 0 requests\n", sections, lines, promotions)
		}
	}()
	var lister memeval.Lister
	child := &memeval.ChildLister{}
	defer child.Close()
	if !*noSnippets {
		lister = child
	}
	start := time.Now()
	var output func([]session.Entry, string) (string, error)
	if !*noSnippets {
		output = child.Printed
	}
	suites := func(runs []memeval.Run) {
		sc := memeval.DefaultSuiteConfig(*seeds)
		sc.Lister, sc.Output, sc.Runs = lister, output, runs
		sc.Window.SleepOff = cfg.Window.SleepOff
		sc.Window.Dreams = cfg.Window.Dreams
		if *window > 0 {
			sc.Window.Context = *window
		}
		var run []func(context.Context, memeval.SuiteConfig) (memeval.Suite, error)
		if *suite == "forgetting" || *suite == "all" {
			run = append(run, memeval.Forgetting)
		}
		if *suite == "sharing" || *suite == "all" {
			run = append(run, memeval.Sharing)
		}
		bad := false
		for _, fn := range run {
			t := time.Now()
			s, err := fn(context.Background(), sc)
			if err != nil {
				fmt.Fprintln(os.Stderr, "memeval:", err)
				os.Exit(1)
			}
			fmt.Printf("%s: %d seeds, %d model requests, all %d of them scripted level-1 replies the fake host served (no model called), %s\n\n",
				s.Name, s.Seeds, s.Requests, s.Scripted, time.Since(t).Round(time.Millisecond))
			fmt.Print(s.Table())
			for _, extra := range s.Extra {
				fmt.Println()
				fmt.Print(extra)
			}
			fmt.Println()
			bad = bad || s.Requests != s.Scripted
		}
		if bad {
			os.Exit(1)
		}
	}
	// settle prints the no-op definition replay (decision 24, scenario 30).
	settle := func() {
		t := time.Now()
		r, noisy, effect, err := memeval.NoOpSettleRate(context.Background(), cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, "memeval:", err)
			os.Exit(1)
		}
		fmt.Printf("No-op definition replay: %d seeds, %d compactions, held before each and judged at MinEvidence (%d) compactions under it, 0 model requests, %s\n\n",
			r.Seeds, r.Compactions, rules.MinEvidence, time.Since(t).Round(time.Millisecond))
		fmt.Println("The recorded ledgers:")
		fmt.Println()
		fmt.Print(r.Table())
		fmt.Printf("\nThe same ledgers with each compaction's failures drawn from 0 to %d (the noise arm):\n\n", memeval.NoiseMax)
		fmt.Print(noisy.Table())
		fmt.Printf("\nThe noise arm's ledgers, the definition removing %d%% of each compaction's failures under it, rounded up (the true-effect arm: what it does not settle is a false negative):\n\n", memeval.TrueEffectShare)
		fmt.Print(effect.Table())
		fmt.Println()
	}
	switch *suite {
	case "tier0", "all":
	case "constraintrot":
		w := cfg.Window
		results, err := memeval.ConstraintRot(context.Background(), w, 1, []int{1, 2, 4}, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "memeval:", err)
			os.Exit(1)
		}
		capped := w
		capped.SummaryMaxTokens = 1200
		cappedResults, err := memeval.ConstraintRot(context.Background(), capped, 1, []int{1, 2, 4}, nil)
		if err != nil {
			fmt.Fprintln(os.Stderr, "memeval:", err)
			os.Exit(1)
		}
		fmt.Printf("ConstraintRot rebuild: %d policies, level 0, 0 model requests (violations need an agent model: ready, not run)\n\n", len(memeval.ConstraintRotPolicies))
		fmt.Print(memeval.RuleTable(results))
		fmt.Println()
		fmt.Println("With the summary capped at 1,200 tokens:")
		fmt.Println()
		fmt.Print(memeval.RuleTable(cappedResults))
		return
	case "arcbench":
		cases, err := readARCBench(*arcCases)
		if err != nil {
			fmt.Fprintln(os.Stderr, "memeval:", err)
			os.Exit(2)
		}
		results, err := memeval.ARCBench(context.Background(), cases, []int{0, 750, 300})
		if err != nil {
			fmt.Fprintln(os.Stderr, "memeval:", err)
			os.Exit(1)
		}
		fmt.Printf("ARCBench rule-loss cases: %d, level 0, 0 model requests (violation@3 needs the agent model: ready, not run)\n\n", len(cases))
		fmt.Print(memeval.ARCBenchTable(results))
		return
	case "forgetting", "sharing":
		suites(nil)
		return
	case "settle":
		settle()
		return
	case "cues":
		t := memeval.CueRates(cfg)
		fmt.Printf("Cues: %d seeds, %d turns each, level 0 after every turn, a compaction every %d turns, 0 model requests, %s\n\n", t.Seeds, cfg.Turns, memeval.CueCompactionEvery, time.Since(start).Round(time.Millisecond))
		fmt.Print(t.Table())
		return
	case "retrieval":
		var runs []memeval.Run
		for _, seed := range cfg.Seeds {
			opt := cfg.Options
			opt.Turns = cfg.Turns
			run, err := memeval.Simulate(context.Background(), memeval.Generate(seed, opt), cfg.Window, cfg.MaxCompactions)
			if err != nil {
				fmt.Fprintln(os.Stderr, "memeval:", err)
				os.Exit(1)
			}
			runs = append(runs, run)
		}
		tallies, err := memeval.RetrievalCheck(context.Background(), runs)
		if err != nil {
			fmt.Fprintln(os.Stderr, "memeval:", err)
			os.Exit(1)
		}
		fmt.Printf("Retrieval check: %d seeds, om.Notes(om.Search(<key>)) over each session's last branch, in process, 0 model requests, %s\n\n", len(cfg.Seeds), time.Since(start).Round(time.Millisecond))
		fmt.Print(memeval.RetrievalTable(tallies))
		return
	default:
		fmt.Fprintln(os.Stderr, "memeval: -suite is tier0, forgetting, sharing, settle, all, cues, constraintrot, arcbench or retrieval")
		os.Exit(2)
	}
	m, err := memeval.Measure(context.Background(), cfg, lister)
	if err != nil {
		fmt.Fprintln(os.Stderr, "memeval:", err)
		os.Exit(1)
	}
	snippets, failed, compacted := 0, 0, 0
	for i, r := range m.Results {
		snippets += r.Snippets
		failed += len(r.SnippetErrors)
		compacted += len(m.Runs[i].Snapshots)
	}
	fmt.Printf("Tier 0: %d seeds, %d compactions, %d snippets run (%d failed), %d model requests, %s\n\n",
		len(cfg.Seeds), compacted, snippets, failed, m.Requests, time.Since(start).Round(time.Millisecond))
	fmt.Println("Shown / reachable / lost, by compactions since the probe left the context:")
	fmt.Println()
	fmt.Print(memeval.Table(m.Results, memeval.Counts))
	fmt.Println()
	fmt.Print(memeval.InvariantTable(m.Results))
	sleeps, hidden, contradictions := memeval.SleepFigures(m.Runs)
	fmt.Printf("\nSleep (-sleep %s): %d sleep records, %d duplicates hidden, %d contradictions (the generator plants none), 0 requests\n", *sleepArm, sleeps, hidden, contradictions)
	promises := memeval.PromiseFigures(m.Runs, 1)
	fmt.Println()
	fmt.Println("Promise syndrome: every compaction checked as written, then with one row showing a protected record dropped at random:")
	fmt.Println()
	fmt.Print(promises.Table())
	if *sumsum {
		var eroded []memeval.Result
		for i, run := range m.Runs {
			eroded = append(eroded, memeval.SummaryOfSummary(run, m.Runs[i].Script.Probes))
		}
		fmt.Println()
		fmt.Println("Summary-of-summary arm (the erosion curve; shown / reachable / lost, nothing reachable):")
		fmt.Println()
		fmt.Print(memeval.Table(eroded, memeval.Counts))
	}
	if *dreamBench != "" {
		var off, on []memeval.Run
		for _, seed := range cfg.Seeds {
			opt := cfg.Options
			opt.Turns = cfg.Turns
			w := cfg.Window
			for _, dreams := range []bool{false, true} {
				w.Dreams = dreams
				run, err := memeval.Simulate(context.Background(), memeval.Generate(seed, opt), w, cfg.MaxCompactions)
				if err != nil {
					fmt.Fprintln(os.Stderr, "memeval:", err)
					os.Exit(1)
				}
				if dreams {
					on = append(on, run)
				} else {
					off = append(off, run)
				}
			}
		}
		n, err := memeval.WriteDreamBenchItems(*dreamBench, off, on)
		if err != nil {
			fmt.Fprintln(os.Stderr, "memeval:", err)
			os.Exit(1)
		}
		fmt.Printf("\nDreamBench-SWE bar: %d items written to %s (ready, not run: answering and judging need models)\n", n, *dreamBench)
	}
	if *abOut != "" {
		n, err := memeval.WriteABItems(*abOut, m.Runs)
		if err != nil {
			fmt.Fprintln(os.Stderr, "memeval:", err)
			os.Exit(1)
		}
		fmt.Printf("\nTier 1/2 A/B: %d items written to %s (ready, not run: answering and judging need models)\n", n, *abOut)
	}
	broken := false
	for _, t := range memeval.InvariantTallies(m.Results) {
		broken = broken || t.Broken > 0
	}
	if broken || failed > 0 || m.Requests > 0 || promises.FalseAlarms > 0 || promises.Detected < promises.Injected {
		os.Exit(1)
	}
	if *suite == "all" {
		fmt.Println()
		suites(m.Runs)
		settle()
	}
}

// readARCBench reads ARCBench's cases file as data: one JSON object per line.
func readARCBench(path string) ([]memeval.ARCBenchCase, error) {
	if path == "" {
		return nil, fmt.Errorf("-suite arcbench needs -arcbench-cases")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []memeval.ARCBenchCase
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for scanner.Scan() {
		var c memeval.ARCBenchCase
		if err := json.Unmarshal(scanner.Bytes(), &c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, scanner.Err()
}
