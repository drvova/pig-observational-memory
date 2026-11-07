package consolidate

import (
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
)

// Authority only goes down and hedges only up through level 1 (contracts/encoding.md): a merge
// starts at the top and takes the least authority and the strongest hedge of its sources, a
// source written before the fields existed leaves it none, and the writer is the session's.
func TestLeastAuthorityThroughLevel1(t *testing.T) {
	user := ledger.Observation{ID: "u", Authority: ledger.AuthorityUser, Hedge: ledger.HedgeStated}
	tool := ledger.Observation{ID: "t", Authority: ledger.AuthorityTool, Hedge: ledger.HedgeUnverified}
	legacy := ledger.Observation{ID: "l"}
	writer := ledger.Writer{Harness: "pig", Session: "s", Engine: ledger.EngineGo}
	v := &validator{shown: map[string]ledger.Observation{"u": user, "t": tool, "l": legacy}, in: Input{Writer: writer}}
	for _, c := range []struct {
		name             string
		sources          []string
		authority, hedge string
	}{
		{"user and tool", []string{"u", "t"}, ledger.AuthorityTool, ledger.HedgeUnverified},
		{"user alone", []string{"u"}, ledger.AuthorityUser, ledger.HedgeHedged},
		{"a legacy source", []string{"u", "l"}, "", ledger.HedgeHedged},
	} {
		r := ledger.Observation{Authority: ledger.AuthorityUser, Hedge: ledger.HedgeHedged}
		v.leastProvenance(&r, c.sources)
		if r.Authority != c.authority || r.Hedge != c.hedge || r.Writer != writer {
			t.Errorf("%s: authority %q hedge %q writer %+v", c.name, r.Authority, r.Hedge, r.Writer)
		}
	}
}
