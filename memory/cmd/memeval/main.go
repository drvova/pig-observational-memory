// Command memeval runs the Tier 0 retention harness (memory/memeval, docs/brain-memory-baseline.md)
// and prints the retention curves and the invariants as markdown tables. It makes no model
// request: level 0 only, over generated transcripts.
//
//	go run ./cmd/memeval -seeds 20
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/drvova/pig-observational-memory/memory/memeval"
	"github.com/drvova/pig-observational-memory/memory/serialize"
)

func main() {
	seeds := flag.Int("seeds", 5, "seeds 1..n to run")
	turns := flag.Int("turns", 0, "turns per transcript (0: the default)")
	compactions := flag.Int("compactions", 0, "compactions per transcript at most (0: the default)")
	window := flag.Int("window", 0, "context window in tokens (0: the default small window)")
	summaryMax := flag.Int("summary-max", 0, "summary cap in tokens, as compactionSummaryMaxTokens (0: none)")
	noSnippets := flag.Bool("no-snippets", false, "do not run the printed snippets (every unshown probe counts as lost)")
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
}
