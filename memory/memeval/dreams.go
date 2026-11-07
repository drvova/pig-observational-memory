package memeval

import (
	"bufio"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/dreams"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
)

// Dreams (docs/brain-memory.md, decision 5 and phase 8's gate): with the dreams setting on, every
// compaction a simulation runs is checked for a false promotion: a dream line, or the section's
// header, anywhere but the dream section itself: in the summary's tables before it, in a record
// or any other om.* entry on the branch (level 1's quarantined om.hypotheses.recorded entry
// excepted), or in the curated store (memories, the core log, the layer, the index). The counts
// are process-wide, like scriptedServed, so Tier 0 and every suite report them.
var (
	dreamSections   atomic.Int64
	dreamLines      atomic.Int64
	dreamPromotions atomic.Int64
)

// DreamFigures are the dream sections, their lines and the false promotions found so far.
func DreamFigures() (sections, lines, promotions int64) {
	return dreamSections.Load(), dreamLines.Load(), dreamPromotions.Load()
}

// checkDreams counts one compaction's dream section and its false promotions.
func checkDreams(summary string, branch []session.Entry, home string) {
	head, section, ok := strings.Cut(summary, dreams.Header)
	if !ok {
		return
	}
	dreamSections.Add(1)
	var lines []string
	for _, l := range strings.Split(section, "\n") {
		if strings.HasPrefix(l, "- ") {
			lines = append(lines, l)
		}
	}
	dreamLines.Add(int64(len(lines)))
	needles := append([]string{dreams.Header}, lines...)
	holds := func(text string) bool {
		for _, n := range needles {
			raw, _ := json.Marshal(n)
			if strings.Contains(text, n) || strings.Contains(text, strings.Trim(string(raw), `"`)) {
				return true
			}
		}
		return false
	}
	if holds(head) {
		dreamPromotions.Add(1)
	}
	for _, e := range branch {
		if e.Type != "custom" || !strings.HasPrefix(e.CustomType, "om.") || e.CustomType == ledger.OMHypothesesRecorded {
			continue
		}
		raw, _ := json.Marshal(e.Data)
		if holds(string(raw)) {
			dreamPromotions.Add(1)
		}
	}
	if home == "" {
		return
	}
	_ = filepath.WalkDir(curated.DefaultDir(home), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if data, err := os.ReadFile(path); err == nil && holds(string(data)) {
			dreamPromotions.Add(1)
		}
		return nil
	})
}

// WriteDreamBenchItems writes the DreamBench-SWE bar's items (phase 8's gate): for every
// compaction of the paired runs (the same seeds with dreams off and on), the summary of each arm
// and the questions of the probes it removed, as the Tier 1/2 A/B writes them. Answering them with
// models and a fixed judge from another family (decision 21), with the controls and ship rule,
// is ready, not run: it needs models. It makes no request.
func WriteDreamBenchItems(path string, off, on []Run) (int, error) {
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	n := 0
	for i := range off {
		if i >= len(on) {
			break
		}
		for _, arm := range []struct {
			name string
			run  Run
		}{{"dreams-off", off[i]}, {"dreams-on", on[i]}} {
			for _, snap := range arm.run.Snapshots {
				var qs []ABQuestion
				for _, p := range arm.run.Script.Probes {
					if p.Turn < snap.Turn {
						qs = append(qs, ABQuestion{Probe: p.Name, Class: p.Class, Reference: strings.Join(p.Needles, " | "),
							Question: "From the memory above, what was recorded about " + p.Needles[0] + "? Quote every exact value, and say which lines are only hypotheses."})
					}
				}
				line, err := json.Marshal(ABItem{Seed: arm.run.Script.Seed, Compaction: snap.N, Arm: arm.name, Summary: snap.Summary, Questions: qs})
				if err != nil {
					return n, err
				}
				w.Write(line)
				w.WriteByte('\n')
				n++
			}
		}
	}
	return n, w.Flush()
}
