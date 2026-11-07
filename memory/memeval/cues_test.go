package memeval

import "testing"

// Phase 8's cue figures on the Tier 0 generator (scenario 28): every exact repeat of a standing
// cueable record's key gets a cue, no one-character look-alike of a call or of a repeat ever
// does, and the session's own calls never get more cues than they match.
func TestCueRates(t *testing.T) {
	cfg := DefaultConfig(1)
	cfg.Turns = 120
	tally := CueRates(cfg)
	if tally.Calls == 0 || tally.Repeats == 0 || tally.RepeatCued != tally.Repeats {
		t.Fatalf("repeats cued %d of %d over %d calls: %+v", tally.RepeatCued, tally.Repeats, tally.Calls, tally)
	}
	if tally.FalseCues != 0 || tally.RepeatFalse != 0 {
		t.Fatalf("look-alikes got cues: %+v", tally)
	}
	if tally.Cued > tally.Eligible {
		t.Fatalf("more cues than matched calls: %+v", tally)
	}
	t.Logf("\n%s", tally.Table())
}
