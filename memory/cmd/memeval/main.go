// Command memeval runs the Tier 0 retention harness (memory/memeval, docs/brain-memory-baseline.md)
// and prints the retention curves and the invariants as markdown tables. It makes no model
// request: level 0 only, over generated transcripts.
//
//	go run ./cmd/memeval -seeds 20
//	go run ./cmd/memeval -suite forgetting -seeds 5
//	go run ./cmd/memeval -suite sharing -seeds 5
//
// The forgetting (Tier 0b) and sharing (Tier 0c) suites print a table of checks each, pass, fail
// or not built; their only requests are scripted level-1 replies a fake host serves.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/drvova/pig-observational-memory/memory/memeval"
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
	suite := flag.String("suite", "tier0", "what to run: tier0 (retention), forgetting (Tier 0b), sharing (Tier 0c) or all")
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
	switch *suite {
	case "tier0", "all":
	case "forgetting", "sharing":
		suites(nil)
		return
	default:
		fmt.Fprintln(os.Stderr, "memeval: -suite is tier0, forgetting, sharing or all")
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
	broken := false
	for _, t := range memeval.InvariantTallies(m.Results) {
		broken = broken || t.Broken > 0
	}
	if broken || failed > 0 || m.Requests > 0 {
		os.Exit(1)
	}
	if *suite == "all" {
		fmt.Println()
		suites(m.Runs)
	}
}
