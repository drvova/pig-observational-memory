package hooks

import (
	"fmt"
	"time"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/jsfmt"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/runtime"
)

// CompactionProgress is the source tokens counted toward the proactive
// threshold: observed tokens normally, raw tokens when Pi's own automatic
// compaction is disabled and nothing else would compact a stalled session.
func CompactionProgress(cfg config.Config, entries []ledger.Entry) int {
	if !cfg.PiAutoCompactionEnabled {
		return ledger.RawTokensSinceLastCompaction(entries)
	}
	return ledger.ObservedTokensSinceLastCompaction(entries)
}

// ProgressLabel names what CompactionProgress counts.
func ProgressLabel(cfg config.Config) string {
	if !cfg.PiAutoCompactionEnabled {
		return "estimated"
	}
	return "observed"
}

// IdleRecheckAttempts and IdleRecheckDelay bound how long the deferred step
// waits for the host to report idle before giving up: another extension's
// settled handler may still be running when the step fires, and sampling once
// deferred every compaction (upstream issue #41).
var (
	IdleRecheckAttempts = 10
	IdleRecheckDelay    = 100 * time.Millisecond
)

// waitForIdle polls the host until it reports idle, within the bounds above.
func waitForIdle(h host.Host) bool {
	attempts := max(1, IdleRecheckAttempts)
	for attempt := 0; attempt < attempts; attempt++ {
		idle, err := h.IsIdle()
		if err != nil {
			return false
		}
		if idle {
			return true
		}
		if attempt+1 < attempts {
			time.Sleep(IdleRecheckDelay)
		}
	}
	return false
}

func latestCompactionID(entries []ledger.Entry) string {
	if idx := ledger.FindLastCompactionIndex(entries); idx >= 0 {
		return entries[idx].ID
	}
	return ""
}

// ActiveCompactionBackoff is the backoff that applies to this branch, or nil.
// A refused compaction is not asked for again until the session or the
// compaction boundary changes, or another threshold's worth of source arrives
// (the failed-attempt half of upstream issue #47).
func ActiveCompactionBackoff(rt *runtime.Runtime, h host.Host, entries []ledger.Entry) *runtime.CompactionBackoff {
	backoff := rt.CompactionBackoff()
	if backoff == nil || backoff.SessionIdentity != sessionIdentityOf(h) || backoff.BoundaryID != latestCompactionID(entries) {
		return nil
	}
	return backoff
}

// MaybeTriggerCompaction triggers proactive compaction when the observed-source
// threshold is reached and Pi is idle. The returned channel closes once the
// deferred step ran; it is nil when nothing was scheduled.
func MaybeTriggerCompaction(rt *runtime.Runtime, h host.Host) <-chan struct{} {
	rt.EnsureConfig(h.Cwd())
	cfg := rt.LiveConfig(h.Cwd())
	if cfg.Passive || !rt.TryStartCompact() {
		return nil
	}
	// The claim is released on every return until the deferred step owns it.
	deferred := false
	defer func() {
		if !deferred {
			rt.SetCompactInFlight(false)
		}
	}()
	entries, err := h.GetBranch()
	if err != nil {
		return nil
	}
	progress := CompactionProgress(cfg, entries)
	threshold := config.ResolveCompactAfterTokens(cfg, host.ModelContextWindow(h.SessionModel()))
	if progress < threshold {
		return nil
	}
	if backoff := ActiveCompactionBackoff(rt, h, entries); backoff != nil {
		if progress < backoff.Progress+threshold {
			return nil
		}
	}
	rt.SetCompactionBackoff(nil)
	sessionIdentity := sessionIdentityOf(h)
	notifyUI(h, fmt.Sprintf("Observational memory: compaction threshold reached (~%s %s source tokens); triggering compaction", jsfmt.Locale(progress), ProgressLabel(cfg)), "info")
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
		currentProgress := CompactionProgress(cfg, currentEntries)
		if currentProgress < threshold {
			rt.SetCompactInFlight(false)
			notifyUI(h, "Observational memory: compaction skipped — another compaction already ran before deferred compaction", "info")
			return
		}
		boundaryID := latestCompactionID(currentEntries)
		h.Compact(host.CompactCallbacks{
			OnComplete: func() {
				rt.SetCompactionBackoff(nil)
				rt.SetCompactInFlight(false)
				notifyUI(h, "Observational memory: compaction complete", "info")
			},
			OnError: func(err error) {
				if err != nil && err.Error() == "Compaction cancelled" {
					// The hook already told the user the real reason before cancelling.
					rt.SetCompactInFlight(false)
					return
				}
				message := "unknown error"
				if err != nil {
					message = err.Error()
				}
				// Asking again on the next settle would fail the same way and
				// notify again; wait for the branch to change.
				rt.SetCompactionBackoff(&runtime.CompactionBackoff{SessionIdentity: sessionIdentity, BoundaryID: boundaryID, Progress: currentProgress, Reason: message})
				rt.SetCompactInFlight(false)
				notifyUI(h, "Observational memory: "+message, "error")
			},
		})
	}()
	return done
}
