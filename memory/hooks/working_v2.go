package hooks

import (
	"github.com/drvova/pig-observational-memory/memory/config"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/selection"
	"github.com/drvova/pig-observational-memory/memory/tokens"
)

// The session model's working-memory caps, raw floor and quality trigger (docs/memory-v2.md,
// "Working memory" and "The raw floor and the quality trigger").

// V2Slots are the session model's working-memory caps (config.Config.LimitsFor).
func V2Slots(cfg config.Config, model host.Model) selection.Slots {
	l := cfg.LimitsFor(host.ModelProvider(model), host.ModelID(model))
	return selection.Slots{Constraints: l.Constraints, Scopes: l.ScopeExclusions, Actions: l.Actions}
}

// BelowRawFloor reports a compaction level 1 consolidates nothing at (decision 15): the raw
// floor is on and PiG's tokensBefore is known and below the session model's floor.
func BelowRawFloor(cfg config.Config, model host.Model, tokensBefore int) bool {
	if !cfg.RawFloor || tokensBefore <= 0 {
		return false
	}
	return tokensBefore < cfg.LimitsFor(host.ModelProvider(model), host.ModelID(model)).RawFloorTokens
}

// RawTokensSinceCompaction are the estimated tokens of the source entries after the branch's
// latest compaction entry (all of them with none): the raw context the quality trigger counts,
// in local estimates, since PiG reports no per-entry usage.
func RawTokensSinceCompaction(entries []ledger.Entry) int {
	total := 0
	for _, entry := range entries[ledger.FindLastCompactionIndex(entries)+1:] {
		if ledger.IsSourceEntry(entry) {
			total += tokens.EstimateEntryTokens(entry)
		}
	}
	return total
}

// QualityTrigger is the quality trigger's reading at one check: the raw tokens since the last
// compaction and the session model's threshold and floor.
type QualityTrigger struct {
	Raw, Threshold, Floor int
}

// ReadQualityTrigger reads the quality trigger for entries; ok is false with the trigger off (and
// the host is then not asked for the session model).
func ReadQualityTrigger(cfg config.Config, h host.Host, entries []ledger.Entry) (QualityTrigger, bool) {
	if !cfg.QualityTrigger {
		return QualityTrigger{}, false
	}
	model := h.SessionModel()
	l := cfg.LimitsFor(host.ModelProvider(model), host.ModelID(model))
	return QualityTrigger{Raw: RawTokensSinceCompaction(entries), Threshold: l.QualityTriggerTokens, Floor: l.RawFloorTokens}, true
}

// Due reports a compaction the quality trigger asks for: PiG's usage is known, the reported
// context is above the floor (nothing below it is consolidated) and the raw tokens since the
// last compaction exceed the threshold. Window - reserve stays the hard backstop
// (V2Threshold.Exceeded).
func (q QualityTrigger) Due(t V2Threshold) bool {
	return t.Known() && *t.Tokens > q.Floor && q.Raw > q.Threshold
}
