package harness_test

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// switch-codec and long-spec rules make the profile switches section 6 would make.
func TestProfileAppliesSwitchRules(t *testing.T) {
	h, store, _ := session(t)
	cfg := config.Defaults
	if p := profileFor(h, cfg, h.Model); p.Codec != tables.Rows.Name() || p.LongSpec {
		t.Fatalf("default profile %+v", p)
	}
	for _, remedy := range []string{rules.RemedySwitchCodec, rules.RemedyLongSpec} {
		if _, err := rules.Add(store, rules.Rule{Detector: rules.DetectDecodeFailure, Remedy: remedy, Models: []string{"p/m"}, Engine: curated.EngineV2}); err != nil {
			t.Fatal(err)
		}
	}
	p := profileFor(h, cfg, h.Model)
	if p.Codec != tables.JSONLines.Name() || !p.LongSpec || len(p.Switches) != 2 || !strings.Contains(p.Switches[0].Reason, "rule ") {
		t.Fatalf("profile %+v", p)
	}
	if other := profileFor(h, cfg, tf.Model("p", "other")); other.Codec != tables.Rows.Name() || other.LongSpec {
		t.Fatalf("another model's profile %+v", other)
	}
}

// profileFor is model's profile in h's store, the default profile when there is no ledger.
func profileFor(h host.Host, cfg config.Config, model host.Model) harness.Profile {
	if l, ok := harness.For(h, cfg, model); ok {
		return l.Profile()
	}
	return harness.Default(harness.ModelKey(model))
}
