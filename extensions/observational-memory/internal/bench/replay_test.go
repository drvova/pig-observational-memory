package bench

import (
	"reflect"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

func TestApplyDropFloorKeepsUncarriedSpecifics(t *testing.T) {
	content := func(text string) func(*ledger.Observation) { return func(o *ledger.Observation) { o.Content = text } }
	generic := tf.Observation("aaaaaaaaaaa1", content("User asked how to tidy a closet."))
	priced := tf.Observation("aaaaaaaaaaa2", content("User paid $30 for a candle."))
	entries := []session.Entry{
		tf.ObservationsRecordedEntry("obs", []ledger.Observation{generic, priced}, "raw-1"),
		tf.ReflectionsRecordedEntry("ref", []ledger.Reflection{tf.Reflection("eeeeeeeeeee1", []string{generic.ID, priced.ID}, func(r *ledger.Reflection) { r.Content = "User is tidying up." })}, "obs"),
		tf.ObservationsDroppedEntry("drop", []string{generic.ID, priced.ID}, "ref"),
	}
	out, stats := ApplyDropFloor(entries, config.Defaults)
	folded := ledger.FoldLedger(out, "")
	if got := tf.IDs(folded.ActiveObservations); !reflect.DeepEqual(got, []string{priced.ID}) {
		t.Fatalf("active %v: the priced observation, whose $30 no reflection carries, must survive the drop", got)
	}
	if stats != (ReplayStats{DropEntries: 1, ProposedDrops: 2, KeptDrops: 1}) {
		t.Fatalf("stats = %+v", stats)
	}
	onlyPriced := []session.Entry{entries[0], entries[1], tf.ObservationsDroppedEntry("drop", []string{priced.ID}, "ref")}
	if out, _ := ApplyDropFloor(onlyPriced, config.Defaults); len(out) != 2 {
		t.Fatalf("a drop entry left empty must be removed, got %d entries", len(out))
	}
}
