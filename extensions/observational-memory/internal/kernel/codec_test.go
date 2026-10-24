package kernel_test

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
)

// The kernel's own codecs pass the round trip every declared format must pass.
func TestShippedCodecsRoundTripTheTestTables(t *testing.T) {
	for _, c := range tables.Codecs() {
		if err := kernel.AcceptCodec(c); err != nil {
			t.Fatalf("%s: %v", c.Name(), err)
		}
	}
}

// lossy drops every line break: a codec that garbles a value is refused, naming the value.
type lossy struct{ tables.Codec }

func (l lossy) Encode(ts ...tables.Table) string {
	out := []tables.Table{}
	for _, t := range ts {
		c := t
		c.Rows = nil
		for _, row := range t.Rows {
			r := append([]string(nil), row...)
			for i := range r {
				r[i] = strings.ReplaceAll(r[i], "\n", " ")
			}
			c.Rows = append(c.Rows, r)
		}
		out = append(out, c)
	}
	return l.Codec.Encode(out...)
}

func TestAcceptCodecRefusesALossyCodec(t *testing.T) {
	err := kernel.AcceptCodec(lossy{tables.Rows})
	if err == nil || !strings.Contains(err.Error(), "refused") || !strings.Contains(err.Error(), "line") {
		t.Fatalf("err %v", err)
	}
}
