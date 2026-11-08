package memeval

import (
	"context"
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// The Zeno arm (the quantum report's change 6; docs/brain-memory.md, decision 6): decay on, and an
// agent that, in the first turn after each compaction, reads every path and runs every command
// the summary showed. If looking at a record is what keeps it fresh, a record the summary shows
// can never decay: the arm measures how many non-protected records the agent's reaction to the
// summary keeps alive, against the plain agent on the same scripts.

// zenoCalls are the Zeno agent's calls after a compaction: a read of every path and a run of every
// command the shown records carry as keys, in the order the summary's records hold them.
func zenoCalls(details ledger.MemoryDetails) []Call {
	shown := map[string]bool{}
	for _, id := range details.Shown {
		shown[id] = true
	}
	seen := map[string]bool{}
	var out []Call
	for _, o := range details.Observations {
		if !shown[o.ID] {
			continue
		}
		for _, p := range o.Keys.Get(ledger.KeyPaths) {
			if !seen["p"+p] {
				seen["p"+p] = true
				out = append(out, Call{Name: "read", Args: map[string]any{"path": p}, Result: "package " + strings.ReplaceAll(p, "/", "_") + "\n\nfunc init() {}\n"})
			}
		}
		for _, c := range o.Keys.Get(ledger.KeyCommands) {
			if !seen["c"+c] {
				seen["c"+c] = true
				out = append(out, Call{Name: "bash", Args: map[string]any{"command": c}, Result: "ok"})
			}
		}
	}
	return out
}

// ZenoTally is one arm's decay figures over its runs: the script's own non-protected records
// (none of the Zeno agent's) that were ever active at a compaction, and those decay ever lowered.
type ZenoTally struct {
	Records, Decayed int
	// Calls are the Zeno agent's calls (0 for the plain agent).
	Calls int
}

// zenoTally counts runs' non-protected script records and those decayed at some compaction, by
// content (the arms' record ids can differ where a turn's text moved).
func zenoTally(runs []Run, clock string, horizon int, counted func([]session.Entry, []ledger.Observation, string, int) map[string]bool) ZenoTally {
	var t ZenoTally
	for _, run := range runs {
		records, decayed := map[string]bool{}, map[string]bool{}
		for _, snap := range run.Snapshots {
			branch := snap.Branch
			active := ledger.FoldLedger(branch, "").ActiveObservations
			superseded := map[string]bool{}
			for _, o := range active {
				for _, id := range o.Supersedes {
					superseded[id] = true
				}
			}
			down := counted(branch, active, clock, horizon)
			for _, o := range active {
				if zenoRecord(o) || ledger.DecayProtected(o, superseded) {
					continue
				}
				key := string(o.Kind) + "\x00" + o.Content
				records[key] = true
				if down[o.ID] {
					decayed[key] = true
				}
			}
		}
		t.Records += len(records)
		t.Decayed += len(decayed)
		t.Calls += run.ZenoCalls
	}
	return t
}

// withoutZenoEvidence is branch with the records the Zeno agent's calls gave taken out of their
// batches, every entry kept in place, so decay over it is decay on the script's own evidence.
func withoutZenoEvidence(branch []session.Entry) []session.Entry {
	out := make([]session.Entry, len(branch))
	for i, e := range branch {
		out[i] = e
		if e.Type != "custom" || e.CustomType != ledger.OMObservationsRecorded {
			continue
		}
		data, ok := ledger.ObservationsRecordedDataFrom(e.Data)
		if !ok {
			continue
		}
		kept := []ledger.Observation{}
		for _, o := range data.Observations {
			if !zenoRecord(o) {
				kept = append(kept, o)
			}
		}
		if len(kept) == len(data.Observations) {
			continue
		}
		m := map[string]any{}
		for k, v := range e.Data.(map[string]any) {
			m[k] = v
		}
		m["observations"] = testfix.ToJSONValue(kept)
		e.Data = m
		out[i] = e
	}
	return out
}

// zenoKeptAlive counts, over every compaction of runs, the script's non-protected records that are
// stale on the script's own evidence (decay lowers them once the agent's reads are taken out) and,
// of those, the ones decay leaves fresh because a read of what a summary showed reset them.
func zenoKeptAlive(runs []Run, clock string, horizon int, counted func([]session.Entry, []ledger.Observation, string, int) map[string]bool) (stale, kept int) {
	for _, run := range runs {
		for _, snap := range run.Snapshots {
			branch := snap.Branch
			active := ledger.FoldLedger(branch, "").ActiveObservations
			down := counted(branch, active, clock, horizon)
			own := ledger.Decayed(withoutZenoEvidence(branch), active, clock, horizon)
			for _, o := range active {
				if zenoRecord(o) || !own[o.ID] {
					continue
				}
				stale++
				if !down[o.ID] {
					kept++
				}
			}
		}
	}
	return stale, kept
}

// zenoRecord is a record the Zeno agent's own calls gave.
func zenoRecord(o ledger.Observation) bool {
	for _, id := range o.SourceEntryIDs {
		if strings.HasPrefix(id, "z") {
			return true
		}
	}
	return false
}

// MaterialZeno is the share of stale records kept fresh above which the Zeno effect is
// material: the summary's own reads would then need the rule of decision 6 that a read of a key
// the summary showed is no evidence for the records it showed.
const MaterialZeno = 0.01

// zenoRows runs the Zeno arm beside plain, the Tier 0 scripts with decay on the compaction clock
// at horizon, and adds its rows to part (c): how many records stale on the script's own evidence
// the agent's reads keep fresh, and whether the sleep records move the decay clock.
func zenoRows(ctx context.Context, s *Suite, plain []Run, horizon int) error {
	var zeno []Run
	for _, base := range plain {
		w := base.Window
		w.Zeno = true
		z, err := Simulate(ctx, base.Script, w, len(base.Snapshots))
		if err != nil {
			return err
		}
		zeno = append(zeno, z)
	}
	const part = "c. protected"
	clock := ledger.DecayCompactions
	base, arm := zenoTally(plain, clock, horizon, ledger.Decayed), zenoTally(zeno, clock, horizon, ledger.Decayed)
	stale, kept := zenoKeptAlive(zeno, clock, horizon, ledger.Decayed)
	s.add(part, "Zeno arm (decay on, compactions clock): reads of what the summary showed keep stale records fresh at most 1% of the time", verdict(float64(kept) <= MaterialZeno*float64(stale)), "decision 6",
		"%d calls; %d of %d record-compactions stale on the script's own evidence kept fresh (%s); ever decayed: plain %s, Zeno %s",
		arm.Calls, kept, stale, pct(kept, stale), pct(base.Decayed, base.Records), pct(arm.Decayed, arm.Records))
	moved, cells := sleepMovesDecay(plain, clock, horizon)
	s.add(part, "the sleep pass's records never move the decay clock", verdict(moved == 0 && cells > 0), "",
		"%d record-compactions, %d decayed differently with the om.sleep entries made inert", cells, moved)
	s.Extra = append(s.Extra, fmt.Sprintf("Zeno arm (decay on, compactions clock, horizon %d, %d seeds): the plain agent against one that reads every path and runs every command the summary showed, in the first turn after each compaction (%d calls)\n\n| agent | non-protected records | ever decayed | record-compactions stale on the script's evidence | kept fresh by the reads |\n|---|---|---|---|---|\n| plain | %d | %d (%s) | | |\n| Zeno | %d | %d (%s) | %d | %d (%s) |\n",
		horizon, len(plain), arm.Calls, base.Records, base.Decayed, pct(base.Decayed, base.Records), arm.Records, arm.Decayed, pct(arm.Decayed, arm.Records), stale, kept, pct(kept, stale)))
	return nil
}

// sleepMovesDecay counts the record-compactions of runs whose decay changes when every om.sleep
// entry is made inert (kept in place, so positions hold): a duplicate, link or verification record
// is a truth check, never an evidence event.
func sleepMovesDecay(runs []Run, clock string, horizon int) (moved, cells int) {
	for _, run := range runs {
		for _, snap := range run.Snapshots {
			branch := snap.Branch
			inert := make([]session.Entry, len(branch))
			for i, e := range branch {
				inert[i] = e
				if e.Type == "custom" && e.CustomType == ledger.OMSleep {
					inert[i].CustomType, inert[i].Data = "memeval.inert", map[string]any{}
				}
			}
			active := ledger.FoldLedger(branch, "").ActiveObservations
			with, without := ledger.Decayed(branch, active, clock, horizon), ledger.Decayed(inert, active, clock, horizon)
			for _, o := range active {
				cells++
				if with[o.ID] != without[o.ID] {
					moved++
				}
			}
		}
	}
	return moved, cells
}
