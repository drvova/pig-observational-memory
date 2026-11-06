package memeval

import (
	"fmt"
	"strings"
	"sync"

	"github.com/drvova/pig-observational-memory/memory/debuglog"
)

// Status is a suite row's verdict.
type Status string

// The verdicts: a check that holds, one that does not (a finding, with the phase that fixes it),
// and one whose behaviour is not built yet.
const (
	Pass     Status = "pass"
	Fail     Status = "fail"
	NotBuilt Status = "not built"
)

// SuiteRow is one check of the forgetting (Tier 0b) or sharing (Tier 0c) suite: what was
// checked, the counts it rests on and its verdict. Phase names the phase that builds or fixes
// it, for a failing or not-built row.
type SuiteRow struct {
	Part   string
	Check  string
	Count  string
	Status Status
	Phase  string
}

// Suite is one suite's rows, the model requests made while it ran, and the scripted level-1
// replies the fake host served among them (no model is called for those).
type Suite struct {
	Name     string
	Seeds    int
	Rows     []SuiteRow
	Requests int
	Scripted int
	// Extra are further tables (markdown) the suite prints after its rows.
	Extra []string
}

func (s *Suite) add(part, check string, st Status, phase, count string, args ...any) {
	if st == Pass {
		phase = ""
	}
	s.Rows = append(s.Rows, SuiteRow{Part: part, Check: check, Count: fmt.Sprintf(count, args...), Status: st, Phase: phase})
}

// verdict is Pass when ok, else Fail.
func verdict(ok bool) Status {
	if ok {
		return Pass
	}
	return Fail
}

// Row is the row whose check is check; ok false when there is none.
func (s Suite) Row(check string) (SuiteRow, bool) {
	for _, r := range s.Rows {
		if r.Check == check {
			return r, true
		}
	}
	return SuiteRow{}, false
}

// Table is the suite's rows as a markdown table.
func (s Suite) Table() string {
	var b strings.Builder
	b.WriteString("| part | check | counts | result |\n|---|---|---|---|\n")
	for _, r := range s.Rows {
		result := "**" + string(r.Status) + "**"
		if r.Phase != "" {
			result += " (" + r.Phase + ")"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s |\n", r.Part, esc(r.Check), esc(r.Count), result)
	}
	return b.String()
}

func esc(s string) string { return strings.ReplaceAll(s, "|", "\\|") }

// countRequests counts the model requests logged while fn runs (as testfix.CountRequests does)
// and the scripted level-1 replies fake hosts served meanwhile.
func countRequests(fn func() error) (int, int, error) {
	served := scriptedServed.Load()
	var mu sync.Mutex
	n := 0
	unsubscribe := debuglog.Subscribe(func(event string, data map[string]any) {
		if debuglog.IsRequest(data) {
			mu.Lock()
			n++
			mu.Unlock()
		}
	})
	err := fn()
	unsubscribe()
	mu.Lock()
	defer mu.Unlock()
	return n, int(scriptedServed.Load() - served), err
}
