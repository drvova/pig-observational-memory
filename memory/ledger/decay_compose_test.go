package ledger_test

import (
	"fmt"
	"maps"
	"math/rand"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
	tf "github.com/drvova/pig-observational-memory/memory/testfix"
)

// randomDecayLedger is a branch of n steps drawn from seed: the person's turns, assistant replies,
// compactions, level-0 batches whose records share keys from a small pool and link to earlier
// records, drops, level-1 merges that replace and drop their sources, and forgets.
func randomDecayLedger(seed int64, n int) []session.Entry {
	rng := rand.New(rand.NewSource(seed))
	var out []session.Entry
	var ids []string
	turn := ""
	keys := []string{"a.go", "b.go", "c.go", "make ci", "go test"}
	kinds := []string{"fact", "attempt", "preference", "decision", "fact", "fact"}
	sources := []string{ledger.SourceUserSaid, ledger.SourceToolObserved, ledger.SourceAssistantClaim, ""}
	record := func(i int, origin string) ledger.Observation {
		id := fmt.Sprintf("%012x", seed*1000+int64(i))
		o := ledger.Observation{ID: id, Content: "record " + id, Timestamp: tf.DefaultTimestamp, Relevance: "medium", SourceEntryIDs: []string{turn}, TokenCount: 5,
			Origin: origin, Kind: ledger.ObservationKind(kinds[rng.Intn(len(kinds))]), Source: sources[rng.Intn(len(sources))]}
		if o.Kind == "attempt" {
			o.Outcome = []string{ledger.OutcomeWorked, ledger.OutcomeFailed}[rng.Intn(2)]
		}
		for range rng.Intn(3) {
			k := keys[rng.Intn(len(keys))]
			kind := ledger.KeyPaths
			if k == "make ci" || k == "go test" {
				kind = ledger.KeyCommands
			}
			o.Keys.Add(kind, k)
		}
		if len(ids) > 0 && rng.Intn(3) == 0 {
			target := ids[rng.Intn(len(ids))]
			switch rng.Intn(4) {
			case 0:
				o.Supersedes = []string{target}
			case 1:
				o.PossiblyUpdates = []string{target}
			case 2:
				o.Conflicts = []string{target}
			default:
				o.RespondsTo = []string{target}
			}
		}
		return o
	}
	for i := range n {
		id := fmt.Sprintf("e%03d", i)
		switch r := rng.Intn(10); {
		case r < 3 || turn == "":
			turn = id
			out = append(out, tf.RawMessage(id, "message "+id))
		case r == 3:
			out = append(out, tf.MessageEntry(id, map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "ok"}}}))
		case r == 4:
			out = append(out, tf.CompactionEntry(id, tf.CompactionArgs{FirstKeptEntryID: turn}))
		case r < 8:
			var batch []ledger.Observation
			for j := range 1 + rng.Intn(3) {
				batch = append(batch, record(i*4+j, ledger.OriginCode))
			}
			for _, o := range batch {
				ids = append(ids, o.ID)
			}
			out = append(out, recorded(id, turn, false, batch...))
		case r == 8 && len(ids) > 1:
			// Level 1 merges two records and drops them, or drops one.
			a, b := ids[rng.Intn(len(ids))], ids[rng.Intn(len(ids))]
			if rng.Intn(2) == 0 && a != b {
				m := record(i*4, ledger.OriginModel)
				m.Replaces = []string{a, b}
				ids = append(ids, m.ID)
				out = append(out, recorded(id, turn, true, m), droppedEntry(id+"d", turn, true, a, b))
			} else {
				out = append(out, droppedEntry(id, turn, false, a))
			}
		case len(ids) > 0:
			target := ids[rng.Intn(len(ids))]
			f := record(i*4, ledger.OriginCode)
			f.Kind, f.Source, f.Keys = ledger.KindForget, ledger.SourceUserSaid, ledger.Keys{}
			f.Supersedes, f.PossiblyUpdates, f.Conflicts, f.RespondsTo = nil, nil, nil, nil
			f.Forgets = []ledger.Tombstone{{ID: target}}
			out = append(out, recorded(id, turn, false, f))
		default:
			out = append(out, tf.RawMessage(id, "message "+id))
		}
	}
	return out
}

// TestDecayComposes is the semigroup property decay keeps (docs/brain-memory.md, decisions 6 and
// 12): decay worked out from a fold checkpoint over the first k entries, advanced over the rest,
// is decay over the whole branch, for random ledgers, every checkpoint position and both clocks.
// lisp/tests/truth.lisp checks the same of the image.
func TestDecayComposes(t *testing.T) {
	cases, nonempty := 0, 0
	for seed := int64(1); seed <= 60; seed++ {
		entries := randomDecayLedger(seed, 30+int(seed%40))
		folded := ledger.FoldLedger(entries, "")
		active := folded.ActiveObservations
		for _, clock := range []string{ledger.DecayCompactions, ledger.DecayTurns} {
			for _, horizon := range []int{1, 2, 4} {
				full := ledger.Decayed(entries, active, clock, horizon)
				if len(full) > 0 {
					nonempty++
				}
				for k := 0; k <= len(entries); k++ {
					checkpoint := ledger.NewDecayFold().Advance(entries[:k])
					before := checkpoint.Clone()
					got := checkpoint.Advance(entries[k:]).Decayed(active, folded.ReplacedBy, clock, horizon)
					if !maps.Equal(got, full) {
						t.Fatalf("seed %d, %s:%d, checkpoint at %d of %d: %v, want %v", seed, clock, horizon, k, len(entries), got, full)
					}
					// Advancing a checkpoint in two batches is the same as in one.
					if k > 0 && k < len(entries) {
						mid := k + (len(entries)-k)/2
						again := before.Advance(entries[k:mid]).Advance(entries[mid:]).Decayed(active, folded.ReplacedBy, clock, horizon)
						if !maps.Equal(again, full) {
							t.Fatalf("seed %d, %s:%d, checkpoint at %d then %d: %v, want %v", seed, clock, horizon, k, mid, again, full)
						}
					}
					cases++
				}
			}
		}
	}
	if cases < 10_000 || nonempty < 60 {
		t.Fatalf("only %d cases, %d with a decayed record", cases, nonempty)
	}
}

// TestDecayEventSurvivesItsRecord: a user-said record sharing a key keeps another record fresh
// even after a later drop takes it out of the active records; the event was the branch's.
func TestDecayEventSurvivesItsRecord(t *testing.T) {
	old := obs("0000000000d1", "The staging bucket is `ops-stage-7`.", func(o *ledger.Observation) { fact(o); o.Keys.Add(ledger.KeyPaths, "ops/stage") })
	old.SourceEntryIDs = []string{"ud1"}
	mention := obs("0000000000d2", "Synced `ops/stage`.", func(o *ledger.Observation) {
		fact(o)
		o.Source = ledger.SourceUserSaid
		o.Keys.Add(ledger.KeyPaths, "ops/stage")
	})
	mention.SourceEntryIDs = []string{"ud3"}
	branch := append(users("ud1"), recorded("r1", "ud1", false, old), tf.CompactionEntry("c1", tf.CompactionArgs{FirstKeptEntryID: "ud1"}),
		tf.RawMessage("ud3", "message ud3"), recorded("r2", "ud3", false, mention), tf.CompactionEntry("c2", tf.CompactionArgs{FirstKeptEntryID: "ud3"}),
		droppedEntry("d1", "ud3", false, mention.ID))
	active := ledger.FoldLedger(branch, "").ActiveObservations
	if len(active) != 1 || active[0].ID != old.ID {
		t.Fatalf("active %v", idsOf(active))
	}
	if ledger.Decayed(branch, active, ledger.DecayCompactions, 2)[old.ID] {
		t.Fatal("the dropped record's event no longer counts: decay would depend on the final active set")
	}
	if !ledger.Decayed(branch, active, ledger.DecayCompactions, 1)[old.ID] {
		t.Fatal("one compaction after the event: decayed at horizon 1")
	}
}
