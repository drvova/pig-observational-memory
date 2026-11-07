package memeval

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/harness"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/rules"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// The no-op definition replay (docs/brain-memory.md, Phase 0 and scenario 30; decision 24's
// null arm): a held definition that changes nothing is replayed through settlement over the
// usage ledger a simulated session recorded, held before each of its compactions in turn and run
// by every later one, and judged once MinEvidence compactions ran under it. Whatever settles it
// is noise. The rule before decision 24 (any compaction under it with source tokens and no loss
// writes it, LegacySettle) is replayed beside the current one (rules.SettleV2). No model is
// asked: the sessions run at level 0 and the replay reads only recorded events.

// NoOpDefinition is the definition the replay holds: a keep-head remedy catalogue that orders
// the one detector keep-head answers as the seed already does, so it changes nothing, and a
// remedy costs no request tokens (the cheapest no-op, the one noise settles most easily).
var NoOpDefinition = layer.Definition{Kind: layer.KindRemedy, Name: rules.RemedyKeepHead, Model: "eval/session",
	Text: "catalogue[1]{detector,rank}:\n  uncited-assistant-specifics,0", Engine: curated.EngineV2, State: layer.StateHeld}

// SettleReplay is the replay's tally over every hold point of every seed.
type SettleReplay struct {
	Seeds, Compactions, HoldPoints int
	// Before is the hold points the rule before decision 24 settled as clean; After the ones
	// rules.SettleV2 settles as clean, at MinEvidence compactions under the definition.
	Before, After int
	// Reasons are what SettleV2 decided at the others, by its reason's first words.
	Reasons map[string]int
	// WithArm is the hold points with 2·MinEvidence independent compactions before them (a full
	// null arm), and AfterWithArm the ones of those it settled.
	WithArm, AfterWithArm int
	// Failing is the compactions that counted a failure the definition is judged by
	// (rules.FailureCounts), the noise a no-op could settle on.
	Failing int
}

// LegacySettle is rules.SettleV2 for a remedy before decision 24: decided clean once one
// non-empty compaction after the hold ran under it with source tokens.
func LegacySettle(events []curated.ModelEvent, d layer.Definition) bool {
	id := d.HeldID()
	for _, e := range events {
		if !rules.IsV2Compaction(e) || e.Outcome == curated.OutcomeEmpty || e.Tokens == 0 {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, e.Time)
		if err != nil || !at.After(d.Time) {
			continue
		}
		for _, r := range e.RanHeld {
			if r == id {
				return true
			}
		}
	}
	return false
}

// NoiseMax bounds the noise arm: each compaction's failures drawn again, uniformly from 0 to
// NoiseMax, seeded, so the replay measures what noise alone settles (the recorded Tier 0
// sessions count no failure at level 0).
const NoiseMax = 3

// WithNoise is events with every compaction pass's uncited-assistant-specifics count drawn from
// rng (0 to NoiseMax): failures that owe nothing to any definition.
func WithNoise(events []curated.ModelEvent, rng *rand.Rand) []curated.ModelEvent {
	out := make([]curated.ModelEvent, len(events))
	copy(out, events)
	for i := range out {
		if !rules.IsV2Compaction(out[i]) {
			continue
		}
		counts := map[string]int{}
		for k, n := range out[i].Counts {
			counts[k] = n
		}
		counts[rules.DetectUncitedAssistantSpecifics] = rng.IntN(NoiseMax + 1)
		out[i].Counts = counts
	}
	return out
}

// ReplayNoOp replays NoOpDefinition over one model's usage ledger events (above) into tally.
func ReplayNoOp(events []curated.ModelEvent, tally *SettleReplay) {
	units := []int{}
	for i, e := range events {
		if rules.IsV2Compaction(e) && e.Outcome != curated.OutcomeEmpty && e.Harm != nil {
			units = append(units, i)
		}
	}
	tally.Compactions += len(units)
	for _, i := range units {
		for _, k := range rules.FailureCounts(NoOpDefinition.Kind) {
			if events[i].Counts[k] > 0 {
				tally.Failing++
				break
			}
		}
	}
	if tally.Reasons == nil {
		tally.Reasons = map[string]int{}
	}
	for k := 0; k+rules.MinEvidence <= len(units); k++ {
		at, err := time.Parse(time.RFC3339Nano, events[units[k]].Time)
		if err != nil {
			continue
		}
		d := NoOpDefinition
		d.Time = at.Add(-time.Nanosecond)
		id := d.HeldID()
		// Every compaction from the hold point on ran under it; the ledger ends at the
		// MinEvidence-th, where the replay judges it.
		last := units[k+rules.MinEvidence-1]
		replay := make([]curated.ModelEvent, last+1)
		copy(replay, events[:last+1])
		for _, i := range units[k:] {
			if i > last {
				break
			}
			replay[i].RanHeld = append(append([]string{}, replay[i].RanHeld...), id)
		}
		tally.HoldPoints++
		if LegacySettle(replay, d) {
			tally.Before++
		}
		st := rules.SettleV2(replay, d)
		arm := independentBefore(events, units[:k]) >= 2*rules.MinEvidence
		if arm {
			tally.WithArm++
		}
		switch {
		case st.Clean:
			tally.After++
			if arm {
				tally.AfterWithArm++
			}
			tally.Reasons["settled"]++
		case st.Decided:
			tally.Reasons[reasonHead(st.Reason)]++
		default:
			tally.Reasons["still held"]++
		}
	}
}

// independentBefore counts the units among indexes whose roots were in no earlier one.
func independentBefore(events []curated.ModelEvent, indexes []int) int {
	seen, n := map[string]bool{}, 0
	for _, i := range indexes {
		fresh := false
		for _, r := range events[i].Roots {
			if !seen[r] {
				fresh, seen[r] = true, true
			}
		}
		if fresh {
			n++
		}
	}
	return n
}

// reasonHead is a settlement reason's head: its words up to the first colon.
func reasonHead(reason string) string {
	if head, _, ok := strings.Cut(reason, ":"); ok {
		return head
	}
	if strings.Contains(reason, "independent evidence") {
		return "too few independent evidence roots"
	}
	return reason
}

// NoOpSettleRate simulates each seed's session at level 0 with a curated store (its usage
// ledger) and replays NoOpDefinition over it as recorded (recorded) and with the noise arm's
// failures (noisy, WithNoise seeded by the seed).
func NoOpSettleRate(ctx context.Context, cfg Config) (recorded, noisy SettleReplay, err error) {
	recorded, noisy = SettleReplay{Reasons: map[string]int{}}, SettleReplay{Reasons: map[string]int{}}
	tally := &recorded
	for _, seed := range cfg.Seeds {
		home, err := os.MkdirTemp("", "memeval-settle-")
		if err != nil {
			return recorded, noisy, err
		}
		opt := cfg.Options
		opt.Turns = cfg.Turns
		_, err = SimulateWith(ctx, Generate(seed, opt), cfg.Window, cfg.MaxCompactions, Sim{Home: home, Session: fmt.Sprintf("seed-%d", seed)})
		if err == nil {
			store := curated.New(curated.DefaultDir(home))
			var events []curated.ModelEvent
			if events, err = store.ModelEvents(harness.ModelKey(testfix.Model("eval", "session", nil))); err == nil {
				ReplayNoOp(events, tally)
				ReplayNoOp(WithNoise(events, rand.New(rand.NewPCG(uint64(seed), 24))), &noisy)
				noisy.Seeds++
			}
		}
		os.RemoveAll(home)
		if err != nil {
			return recorded, noisy, fmt.Errorf("seed %d: %w", seed, err)
		}
		tally.Seeds++
	}
	return recorded, noisy, nil
}

// Table is the replay as the baseline page prints it.
func (r SettleReplay) Table() string {
	pct := func(n, of int) string {
		if of == 0 {
			return "-"
		}
		return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(of))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "| rule | hold points | settled | rate |\n|---|---|---|---|\n")
	fmt.Fprintf(&b, "| before decision 24 (first compaction under it) | %d | %d | %s |\n", r.HoldPoints, r.Before, pct(r.Before, r.HoldPoints))
	fmt.Fprintf(&b, "| decision 24 (null arm, cost, independent roots, harm) | %d | %d | %s |\n", r.HoldPoints, r.After, pct(r.After, r.HoldPoints))
	fmt.Fprintf(&b, "| decision 24, hold points with a full null arm | %d | %d | %s |\n", r.WithArm, r.AfterWithArm, pct(r.AfterWithArm, r.WithArm))
	names := make([]string, 0, len(r.Reasons))
	for k := range r.Reasons {
		names = append(names, k)
	}
	sort.Strings(names)
	parts := []string{}
	for _, k := range names {
		parts = append(parts, fmt.Sprintf("%s %d", k, r.Reasons[k]))
	}
	fmt.Fprintf(&b, "\nDecision 24's verdicts: %s. %d of the %d compactions counted a failure the no-op is judged by.\n", strings.Join(parts, ", "), r.Failing, r.Compactions)
	return b.String()
}
