package hooks

import (
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/config"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/jsfmt"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/runtime"
)

// V2Threshold is PiG's own compaction threshold for the session (docs/memory-v2.md,
// "Auto-compaction off"): PiG compacts when the reported context exceeds window -
// reserveTokens (compaction.ShouldCompact), with reserveTokens resolved for the session model
// from PiG's settings (config.PiCompactionTokens.ReserveTokensFor).
type V2Threshold struct {
	// Tokens is the host's reported context; nil while usage is unknown (PiG reports none
	// after a compaction until an assistant response reports usage again).
	Tokens *int
	// ContextWindow is the reported window, else the session model's; 0 when unknown.
	ContextWindow int
	// ReserveTokens is PiG's reserveTokens for the session model.
	ReserveTokens int
}

// Limit is window - reserveTokens, the context PiG's inequality compares against.
func (t V2Threshold) Limit() int { return t.ContextWindow - t.ReserveTokens }

// Known reports whether both the context and the window are known.
func (t V2Threshold) Known() bool { return t.Tokens != nil && t.ContextWindow > 0 }

// Exceeded is PiG's inequality, context > window - reserveTokens; false while either is
// unknown.
func (t V2Threshold) Exceeded() bool { return t.Known() && *t.Tokens > t.Limit() }

// ReadV2Threshold reads PiG's threshold inputs from the host under cfg (a LiveConfig, so
// reserveTokens is the one PiG's settings hold now).
func ReadV2Threshold(cfg config.Config, h host.Host) V2Threshold {
	model := h.SessionModel()
	threshold := V2Threshold{
		ContextWindow: host.ModelContextWindow(model),
		ReserveTokens: cfg.PiCompaction.ReserveTokensFor(host.ModelProvider(model), host.ModelID(model)),
	}
	if usage, err := h.GetContextUsage(); err == nil && usage != nil {
		if usage.ContextWindow > 0 {
			threshold.ContextWindow = usage.ContextWindow
		}
		if usage.Tokens != nil {
			tokens := *usage.Tokens
			threshold.Tokens = &tokens
		}
	}
	return threshold
}

// nothingToCompact reports a refusal PiG gives before any compaction that asking again at the
// same tip cannot change: nothing to cut ("Nothing to compact (session too small)", "Already
// compacted"), or an invalid compaction setting PiG rejects every compaction on ("Invalid
// compaction.reserveTokens setting: ...", which the extension reads as unset and so still sees
// the threshold exceeded).
func nothingToCompact(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.HasPrefix(message, "Nothing to compact") || strings.HasPrefix(message, "Already compacted") || strings.HasPrefix(message, "Invalid compaction.")
}

// ActiveV2CompactionSuspension is memory v2's trigger suspension that applies to this branch,
// or nil: a "Nothing to compact" answer holds until the branch tip changes.
func ActiveV2CompactionSuspension(rt *runtime.Runtime, h host.Host, entries []ledger.Entry) *runtime.CompactionBackoff {
	backoff := rt.CompactionBackoff()
	if backoff == nil || backoff.TipID == "" || backoff.SessionIdentity != sessionIdentityOf(h) || backoff.TipID != branchLeafID(entries) {
		return nil
	}
	return backoff
}

// maybeTriggerCompactionV2 is memory v2's only compaction trigger (docs/memory-v2.md,
// "Auto-compaction off"). PiG never compacts on its own with compaction.enabled false, not
// even on overflow, so at agent_settled, with the agent idle, the extension asks PiG for a
// compaction when the reported context exceeds PiG's own threshold (window - reserveTokens),
// never while usage is unknown. With auto-compaction on it never triggers. A "Nothing to
// compact" answer suspends the trigger until the branch tip changes. The compaction is level
// 0 only (the hook makes no request). The returned channel closes once the deferred step ran;
// it is nil when nothing was scheduled.
func maybeTriggerCompactionV2(rt *runtime.Runtime, h host.Host, cfg config.Config) <-chan struct{} {
	if cfg.PiAutoCompactionEnabled || !rt.TryStartCompact() {
		return nil
	}
	deferred := false
	defer func() {
		if !deferred {
			rt.SetCompactInFlight(false)
		}
	}()
	entries, err := h.GetBranch()
	if err != nil || ActiveV2CompactionSuspension(rt, h, entries) != nil {
		return nil
	}
	threshold := ReadV2Threshold(cfg, h)
	// The quality trigger (off by default; working_v2.go) may ask before PiG's threshold, which
	// stays the hard backstop.
	quality, qualityOn := ReadQualityTrigger(cfg, h, entries)
	due := func(t V2Threshold, q QualityTrigger) bool { return t.Exceeded() || qualityOn && q.Due(t) }
	if !due(threshold, quality) {
		return nil
	}
	rt.SetCompactionBackoff(nil)
	sessionIdentity := sessionIdentityOf(h)
	if threshold.Exceeded() {
		notifyUI(h, fmt.Sprintf("Observational memory: context ~%s tokens is over PiG's threshold %s (window - reserveTokens) and PiG's auto-compaction is off; compacting (level 0)", jsfmt.Locale(*threshold.Tokens), jsfmt.Locale(threshold.Limit())), "info")
	} else {
		notifyUI(h, fmt.Sprintf("Observational memory: ~%s raw tokens since the last compaction are over the quality threshold %s (context ~%s tokens); compacting (level 0)", jsfmt.Locale(quality.Raw), jsfmt.Locale(quality.Threshold), jsfmt.Locale(*threshold.Tokens)), "info")
	}
	deferred = true
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if recovered := recover(); recovered != nil {
				rt.SetCompactInFlight(false)
				notifyUI(h, fmt.Sprintf("Observational memory: compact threw: %v", recovered), "error")
			}
		}()
		if !waitForIdle(h) {
			rt.SetCompactInFlight(false)
			notifyUI(h, "Observational memory: compaction deferred — agent became busy before compaction", "info")
			return
		}
		currentEntries, err := h.GetBranch()
		if err != nil {
			rt.SetCompactInFlight(false)
			return
		}
		current, _ := ReadQualityTrigger(cfg, h, currentEntries)
		if !due(ReadV2Threshold(cfg, h), current) {
			rt.SetCompactInFlight(false)
			notifyUI(h, "Observational memory: compaction skipped — the context is no longer over PiG's threshold", "info")
			return
		}
		tip := branchLeafID(currentEntries)
		h.Compact(host.CompactCallbacks{
			OnComplete: func() {
				rt.SetCompactionBackoff(nil)
				rt.SetCompactInFlight(false)
				notifyUI(h, "Observational memory: compaction complete", "info")
			},
			OnError: func(err error) {
				message := "unknown error"
				if err != nil {
					message = err.Error()
				}
				if nothingToCompact(err) {
					// PiG found no range to cut, or rejects its own settings: asking again cannot
					// succeed until the branch grows.
					rt.SetCompactionBackoff(&runtime.CompactionBackoff{SessionIdentity: sessionIdentity, BoundaryID: latestCompactionID(currentEntries), Reason: message, TipID: tip})
				}
				rt.SetCompactInFlight(false)
				notifyUI(h, "Observational memory: "+message, "error")
			},
		})
	}()
	return done
}
