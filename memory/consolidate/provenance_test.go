package consolidate

import (
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
)

// A record replacing others never has more provenance than the least of them
// (docs/brain-memory.md, "Provenance, not detection"): whatever a rule built, accept holds its
// origin to the lowest source's and its FromUser to every source standing for the user.
func TestLeastProvenance(t *testing.T) {
	user := ledger.Observation{ID: "u", Kind: "correction", Origin: ledger.OriginCode}
	tool := ledger.Observation{ID: "t", Kind: "fact", Origin: ledger.OriginCode}
	host := ledger.Observation{ID: "h", Kind: "fact", Origin: ledger.OriginHost}
	merged := ledger.Observation{ID: "m", Kind: "fact", Origin: ledger.OriginModel, FromUser: true}
	legacy := ledger.Observation{ID: "l", Kind: "fact"}
	v := &validator{shown: map[string]ledger.Observation{"u": user, "t": tool, "h": host, "m": merged, "l": legacy}}
	for _, c := range []struct {
		name     string
		record   ledger.Observation
		sources  []string
		origin   string
		fromUser bool
	}{
		{"a claimed user merge of user and tool records", ledger.Observation{Origin: ledger.OriginCode, FromUser: true}, []string{"u", "t"}, ledger.OriginCode, false},
		{"code over a host summary", ledger.Observation{Origin: ledger.OriginCode}, []string{"t", "h"}, ledger.OriginHost, false},
		{"a model merge stays the model's", ledger.Observation{Origin: ledger.OriginModel, FromUser: true}, []string{"u", "m"}, ledger.OriginModel, true},
		{"a record with no origin is the lowest", ledger.Observation{Origin: ledger.OriginCode}, []string{"t", "l"}, "", false},
		{"a label keeps its candidate's", ledger.Observation{Origin: ledger.OriginHost}, []string{"h"}, ledger.OriginHost, false},
	} {
		r := c.record
		v.leastProvenance(&r, c.sources)
		if r.Origin != c.origin || r.FromUser != c.fromUser {
			t.Errorf("%s: origin %q fromUser %v, want %q %v", c.name, r.Origin, r.FromUser, c.origin, c.fromUser)
		}
	}
}
