package memeval

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/tables"
)

// The promise syndrome under fault injection (docs/memory-v2.md, "The promise syndrome"; the
// quantum report's change 7): every compaction of the runs is checked as written (any finding
// is a false alarm), and again with one row showing a protected record dropped from its summary
// at random, the table's count fixed so the text still decodes (the loss must be located: the
// check names that record).

// PromiseTally is the syndrome's figures over runs.
type PromiseTally struct {
	// Compactions checked as written, those with a finding (false alarms), and those whose text
	// could not be read.
	Checked, FalseAlarms, Unread int
	// Promised and Shown are the protected records the checked summaries promise and show.
	Promised, Shown int
	// Injected are the summaries with a protected row dropped; Detected those whose check names
	// the dropped record; Skipped the compactions that show no protected row.
	Injected, Detected, Skipped int
	// Stored counts the compactions whose details recheck the previous one (PromiseSyndrome.Check)
	// and StoredFindings those of them with a finding.
	Stored, StoredFindings int
}

// PromiseFigures checks every compaction of runs as written and with one protected row dropped,
// drawn from seed.
func PromiseFigures(runs []Run, seed int64) PromiseTally {
	var t PromiseTally
	rng := rand.New(rand.NewSource(seed))
	for _, run := range runs {
		for _, snap := range run.Snapshots {
			branch := snap.Branch
			at := len(branch) - 1
			if s := ledger.PromiseSyndromeOf(branch[at].Details); s != nil && s.Check != nil {
				t.Stored++
				if !s.Check.Clean() {
					t.StoredFindings++
				}
			}
			c, ok := ledger.CheckPromise(branch, at)
			if !ok {
				continue
			}
			t.Checked++
			if c.Unread {
				t.Unread++
				continue
			}
			if !c.Clean() {
				t.FalseAlarms++
			}
			if s := ledger.PromiseSyndromeOf(branch[at].Details); s != nil {
				t.Promised += s.Records
				t.Shown += s.Shown
			}
			id, faulty, ok := dropProtectedRow(branch, at, rng)
			if !ok {
				t.Skipped++
				continue
			}
			t.Injected++
			if got, ok := ledger.CheckPromise(faulty, at); ok && slices.Contains(got.Lost, id) {
				t.Detected++
			}
		}
	}
	return t
}

// dropProtectedRow is branch with one row that shows a protected record dropped from the summary
// of the compaction at index at, chosen with rng, and that record's id; ok is false when the
// summary shows no protected record in a row it can drop.
func dropProtectedRow(branch []session.Entry, at int, rng *rand.Rand) (string, []session.Entry, bool) {
	e := branch[at]
	summary, _ := e.Summary.(string)
	body, ok := ledger.TablesSummaryBody(summary)
	if !ok {
		return "", nil, false
	}
	decoded, err := tables.Rows.Decode(body)
	if err != nil {
		return "", nil, false
	}
	details, _ := ledger.MemoryDetailsFrom(e.Details)
	shown := map[string]bool{}
	for _, id := range details.Shown {
		shown[id] = true
	}
	positions := ledger.EntryIndexByID(branch[:at])
	type row struct{ table, row int }
	var rows []row
	var ids []string
	for _, p := range ledger.PromisedAt(branch, at) {
		if !shown[p.ID] || p.Class == ledger.PromiseHeld {
			continue
		}
		anchor, _, anchored := ledger.ObservationAnchor(p.Observation, positions)
		for ti, t := range decoded {
			id, content, source := slices.Index(t.Fields, "id"), slices.Index(t.Fields, "content"), slices.Index(t.Fields, "source")
			for ri, cells := range t.Rows {
				match := id >= 0 && cells[id] == p.ID
				if t.Name == "constraints" && content >= 0 && source >= 0 {
					match = (!anchored && cells[source] == "" || anchored && cells[source] == anchor) && strings.HasPrefix(cells[content], p.Content)
				}
				if match {
					rows, ids = append(rows, row{ti, ri}), append(ids, p.ID)
				}
			}
		}
	}
	if len(rows) == 0 {
		return "", nil, false
	}
	k := rng.Intn(len(rows))
	r := rows[k]
	t := &decoded[r.table]
	t.Rows = slices.Delete(slices.Clone(t.Rows), r.row, r.row+1)
	if t.Absent != nil {
		t.Absent = slices.Delete(slices.Clone(t.Absent), r.row, r.row+1)
	}
	rewritten := strings.Replace(summary, body, tables.Rows.Encode(decoded...), 1)
	if rewritten == summary {
		return "", nil, false
	}
	out := slices.Clone(branch)
	faulty := e
	faulty.Summary = rewritten
	out[at] = faulty
	return ids[k], out, true
}

// Table is the tally as a markdown table.
func (t PromiseTally) Table() string {
	var b strings.Builder
	b.WriteString("| check | count |\n|---|---|\n")
	fmt.Fprintf(&b, "| compactions checked as written | %d (%d unread) |\n", t.Checked, t.Unread)
	fmt.Fprintf(&b, "| protected records promised / shown | %d / %d |\n", t.Promised, t.Shown)
	fmt.Fprintf(&b, "| false alarms (a finding on an unmodified summary) | %d |\n", t.FalseAlarms)
	fmt.Fprintf(&b, "| rechecks stored in the next compaction's details, with a finding | %d, %d |\n", t.Stored, t.StoredFindings)
	fmt.Fprintf(&b, "| summaries with one protected row dropped | %d (%d compactions showed none) |\n", t.Injected, t.Skipped)
	fmt.Fprintf(&b, "| drops detected and located (the check names the record) | %d (%s) |\n", t.Detected, pct(t.Detected, t.Injected))
	return b.String()
}
