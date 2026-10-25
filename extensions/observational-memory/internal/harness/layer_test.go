package harness_test

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// The floor is lowered only by the user's configuration, recorded with who asked and why
// before it takes effect; a request without both is refused and the floor stays engaged; and
// it is engaged again when the configuration stops asking.
func TestConfiguredFloorIsRecordedBeforeItLowers(t *testing.T) {
	h := tf.NewFakeHost()
	h.Home = t.TempDir()
	h.Model = tf.Model("p", "m")
	cfg := config.Defaults
	memoryTool := kernel.Def{Kind: layer.KindTool, Name: "memory", Text: "definition[1]{name}:\n  memory"}

	cfg.KernelFloor = &config.FloorRequest{RequestedBy: "drvova"}
	store, ok := harness.StoreOf(h, cfg)
	if !ok {
		t.Fatal("no store")
	}
	if store.Floor.Check(memoryTool) == nil {
		t.Fatal("a lowering without a reason took effect")
	}
	if line := harness.FloorStatus(store); !strings.Contains(line, "refused") {
		t.Fatalf("status %q", line)
	}
	if record, _ := store.Layer().FloorRecord(); len(record) != 0 {
		t.Fatalf("a refused lowering was recorded: %+v", record)
	}

	cfg.KernelFloor = &config.FloorRequest{RequestedBy: "drvova", Reason: "rebuilding the memory tool"}
	store, _ = harness.StoreOf(h, cfg)
	if err := store.Floor.Check(memoryTool); err != nil {
		t.Fatalf("the configured lowering did not take effect: %v", err)
	}
	record, _ := store.Layer().FloorRecord()
	if len(record) != 1 || record[0].RequestedBy != "drvova" || record[0].Reason != "rebuilding the memory tool" {
		t.Fatalf("record %+v", record)
	}
	if line := harness.FloorStatus(store); !strings.Contains(line, "LOWERED by drvova") {
		t.Fatalf("status %q", line)
	}
	// The same request again records nothing new.
	harness.StoreOf(h, cfg)
	if record, _ := store.Layer().FloorRecord(); len(record) != 1 {
		t.Fatalf("a standing lowering was recorded again: %+v", record)
	}
	// A changed request is recorded anew (engage, then the new lowering) and status names it.
	cfg.KernelFloor = &config.FloorRequest{RequestedBy: "bob", Reason: "migration"}
	store, _ = harness.StoreOf(h, cfg)
	if err := store.Floor.Check(memoryTool); err != nil {
		t.Fatalf("the changed lowering did not take effect: %v", err)
	}
	record, _ = store.Layer().FloorRecord()
	if len(record) != 3 || record[1].Op != kernel.OpEngage || record[2].RequestedBy != "bob" || record[2].Reason != "migration" {
		t.Fatalf("record after a changed request %+v", record)
	}
	if line := harness.FloorStatus(store); !strings.Contains(line, "LOWERED by bob") || !strings.Contains(line, "migration") {
		t.Fatalf("status %q", line)
	}
	// A changed request the floor refuses leaves it engaged, not lowered under the old one.
	cfg.KernelFloor = &config.FloorRequest{RequestedBy: "carol"}
	store, _ = harness.StoreOf(h, cfg)
	if store.Floor.Check(memoryTool) == nil {
		t.Fatal("a refused changed request left the floor lowered")
	}
	if line := harness.FloorStatus(store); !strings.Contains(line, "refused") {
		t.Fatalf("status %q", line)
	}

	cfg.KernelFloor = nil
	store, _ = harness.StoreOf(h, cfg)
	if store.Floor.Check(memoryTool) == nil {
		t.Fatal("the floor stayed lowered after the configuration stopped asking")
	}
	if record, _ := store.Layer().FloorRecord(); len(record) != 4 || record[3].Op != kernel.OpEngage {
		t.Fatalf("record %+v", record)
	}
}

// A model's instructions definition reaches its worker stage; a stale one does not, and the
// layer shows in /om:status.
func TestLayerInstructionsAndStatus(t *testing.T) {
	h := tf.NewFakeHost()
	h.Home = t.TempDir()
	h.Model = tf.Model("p", "m")
	cfg := config.Defaults
	store, _ := harness.StoreOf(h, cfg)
	l := harness.Ledger{Store: store, Model: "p/m"}
	if got := l.Layer().Instructions("observer"); got != "" {
		t.Fatalf("instructions before any definition: %q", got)
	}
	ls := store.Layer()
	if _, err := ls.Put(layer.Definition{Kind: layer.KindInstructions, Name: "observer", Model: "p/m", Text: "Record every figure with its unit.", Session: "s1"}); err != nil {
		t.Fatal(err)
	}
	if got := l.Layer().Instructions("observer"); got != "Record every figure with its unit." {
		t.Fatalf("instructions %q", got)
	}
	if _, err := ls.Hold(layer.Definition{Kind: layer.KindInstructions, Name: "dropper", Model: "p/m", Text: "Keep every plan.", Session: "s1"}); err != nil {
		t.Fatal(err)
	}
	status := strings.Join(harness.StatusLines(h, cfg), "\n")
	for _, want := range []string{"Kernel floor: engaged", "Layer (p/m): generation 1", "held: instructions/dropper"} {
		if !strings.Contains(status, want) {
			t.Fatalf("status lacks %q:\n%s", want, status)
		}
	}
	// A stale replacement (its seed changed) is skipped: the seed stands, and status says so.
	// An addition never goes stale.
	if _, err := ls.Put(layer.Definition{Kind: layer.KindInstructions, Name: "observer", Model: "p/m", Text: "mode: replace\nRecord every figure with its unit.", Session: "s1"}); err != nil {
		t.Fatal(err)
	}
	ls.Seeds = layer.Shipped()
	ls.Seeds[layer.SeedKey(layer.KindInstructions, "observer")] = "a changed seed"
	if h, reports, _ := ls.Load("p/m"); len(reports) != 1 || !reports[0].Stale || reports[0].Name != "observer" {
		t.Fatalf("reports %+v", reports)
	} else if d, _ := h.Resolve(layer.KindInstructions, "observer"); d.State != layer.StateSeed {
		t.Fatalf("a stale override stood: %+v", d)
	}
}

// The profile reads its codecs from the model's layer: a format the model declared under a new
// name is the next codec after the shipped ones, and a shipped name it redeclared stands for
// its own declaration on every surface that reads the profile's codec.
func TestProfileCodecsComeFromTheLayer(t *testing.T) {
	h := tf.NewFakeHost()
	h.Home = t.TempDir()
	cfg := config.Defaults
	store, _ := harness.StoreOf(h, cfg)
	l := harness.Ledger{Store: store, Model: "p/m"}
	if p := l.Profile(); p.CodecValue() != tables.Rows {
		t.Fatalf("seed codec %v", p.CodecValue())
	}
	semi := tables.Format{Name: "semi", Header: tables.HeaderCounted, Delimiter: ';', Quote: '\'', Escape: '^', Escapes: "nr", Indent: 2}
	if _, err := store.Layer().Put(layer.Definition{Kind: layer.KindFormat, Name: "semi", Model: "p/m", Text: semi.Declaration()}); err != nil {
		t.Fatal(err)
	}
	for _, codec := range []string{"rows", "jsonl"} {
		for i := 0; i < harness.MinEvidence; i++ {
			_ = store.RecordModel(curated.ModelEvent{Model: "p/m", Kind: curated.ModelKindDecode, Outcome: curated.OutcomeError, Codec: codec, Engine: curated.EngineV2})
			_ = store.RecordModel(curated.ModelEvent{Model: "p/m", Kind: curated.ModelKindPass, Stage: "compaction", Engine: curated.EngineV2, Tokens: 100})
		}
	}
	p := l.Profile()
	if f, ok := tables.DeclaredFormat(p.CodecValue()); p.Codec != "semi" || !ok || f.Delimiter != ';' {
		t.Fatalf("profile codec %s %v", p.Codec, p.CodecValue())
	}
	if !strings.Contains(p.Spec(), "separated by ;") {
		t.Fatalf("spec %q", p.Spec())
	}
	// Another model never switches to it.
	other := harness.Ledger{Store: store, Model: "q/other"}
	for _, codec := range []string{"rows", "jsonl"} {
		for i := 0; i < harness.MinEvidence; i++ {
			_ = store.RecordModel(curated.ModelEvent{Model: "q/other", Kind: curated.ModelKindDecode, Outcome: curated.OutcomeError, Codec: codec, Engine: curated.EngineV2})
			_ = store.RecordModel(curated.ModelEvent{Model: "q/other", Kind: curated.ModelKindPass, Stage: "compaction", Engine: curated.EngineV2, Tokens: 100})
		}
	}
	if p := other.Profile(); p.Codec != "jsonl" || p.CodecValue() != tables.JSONLines {
		t.Fatalf("other profile %s", p.Codec)
	}
}
