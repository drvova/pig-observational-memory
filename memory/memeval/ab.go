package memeval

import (
	"bufio"
	"encoding/json"
	"os"
	"sort"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/ledger"
)

// The Tier 1/2 A/B harness (docs/brain-memory.md, phase 7 gate; decision 18): the same records
// shown two ways, the engine's tables summary and a natural-language rendering of its rows, each
// with the questions a probe asks and their reference answers. Writing the items makes no
// request; answering them with models of several families and judging them with a fixed judge
// from another family (decision 21) is ready, not run.

// NaturalLanguage renders a tables summary's rows as sentences, one per row: the table's name,
// then each field and its value. Lines outside the tables are kept as they are.
func NaturalLanguage(summary string) string {
	var lines []string
	for _, r := range SummaryRows(summary) {
		if r.Table == "" {
			if strings.TrimSpace(r.Text) != "" {
				lines = append(lines, r.Text)
			}
			continue
		}
		fields := make([]string, 0, len(r.Cells))
		for f := range r.Cells {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		parts := make([]string, 0, len(fields))
		for _, f := range fields {
			if v := strings.TrimSpace(r.Cells[f]); v != "" {
				parts = append(parts, f+" is "+v)
			}
		}
		lines = append(lines, "In "+r.Table+": "+strings.Join(parts, "; ")+".")
	}
	return ledger.UnverifiedNotice + "\n\n" + strings.Join(lines, "\n")
}

// ABQuestion is one probe's question and its reference answer (every needle, byte for byte).
type ABQuestion struct {
	Probe     string `json:"probe"`
	Class     Class  `json:"class"`
	Question  string `json:"question"`
	Reference string `json:"reference"`
}

// ABItem is one compaction's summary in one arm with the questions of the probes it removed.
type ABItem struct {
	Seed       int64        `json:"seed"`
	Compaction int          `json:"compaction"`
	Arm        string       `json:"arm"`
	Summary    string       `json:"summary"`
	Questions  []ABQuestion `json:"questions"`
}

// WriteABItems writes both arms of every compaction of runs to path, one item per line, and
// returns how many it wrote.
func WriteABItems(path string, runs []Run) (int, error) {
	f, err := os.Create(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	n := 0
	for _, run := range runs {
		for _, snap := range run.Snapshots {
			var qs []ABQuestion
			for _, p := range run.Script.Probes {
				if p.Turn < snap.Turn {
					qs = append(qs, ABQuestion{Probe: p.Name, Class: p.Class, Reference: strings.Join(p.Needles, " | "),
						Question: "From the memory above, what was recorded about " + p.Needles[0] + "? Quote every exact value."})
				}
			}
			for _, arm := range []struct{ name, text string }{{"tables", snap.Summary}, {"natural", NaturalLanguage(snap.Summary)}} {
				line, err := json.Marshal(ABItem{Seed: run.Script.Seed, Compaction: snap.N, Arm: arm.name, Summary: arm.text, Questions: qs})
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

// SleepFigures are the sleep records the runs' last branches hold, the duplicates they hid and
// the contradictions the joint-consistency check found (the Tier 0 generator plants none, so
// any is a false positive).
func SleepFigures(runs []Run) (sleeps, hidden, contradictions int) {
	for _, run := range runs {
		if len(run.Snapshots) == 0 {
			continue
		}
		branch := run.Snapshots[len(run.Snapshots)-1].Branch
		data, _ := ledger.Sleeps(branch)
		sleeps += len(data)
		st := ledger.SleepStateOf(branch)
		hidden += len(st.Duplicates)
		contradictions += len(st.Contradictions)
	}
	return sleeps, hidden, contradictions
}
