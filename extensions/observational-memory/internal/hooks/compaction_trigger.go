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

// MaybeTriggerCompaction triggers proactive compaction when the observed-source
// threshold is reached and Pi is idle. The returned channel closes once the
// deferred step ran; it is nil when nothing was scheduled.
func MaybeTriggerCompaction(rt *runtime.Runtime, h host.Host) <-chan struct{} {
	rt.EnsureConfig(h.Cwd())
	cfg := rt.Config()
	if cfg.Passive || rt.CompactInFlight() {
		return nil
	}
	entries, err := h.GetBranch()
	if err != nil {
		return nil
	}
	progress := CompactionProgress(cfg, entries)
	threshold := config.ResolveCompactAfterTokens(cfg, host.ModelContextWindow(h.SessionModel()))
	if progress < threshold {
		return nil
	}
	notifyUI(h, fmt.Sprintf("Observational memory: compaction threshold reached (~%s %s source tokens); triggering compaction", jsfmt.Locale(progress), ProgressLabel(cfg)), "info")
	rt.SetCompactInFlight(true)
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
		if CompactionProgress(cfg, currentEntries) < threshold {
			rt.SetCompactInFlight(false)
			notifyUI(h, "Observational memory: compaction skipped — another compaction already ran before deferred compaction", "info")
			return
		}
		h.Compact(host.CompactCallbacks{
			OnComplete: func() {
				rt.SetCompactInFlight(false)
				notifyUI(h, "Observational memory: compaction complete", "info")
			},
			OnError: func(err error) {
				rt.SetCompactInFlight(false)
				if err != nil && err.Error() == "Compaction cancelled" {
					// The hook already told the user the real reason before cancelling.
					return
				}
				message := "unknown error"
				if err != nil {
					message = err.Error()
				}
				notifyUI(h, "Observational memory: "+message, "error")
			},
		})
	}()
	return done
}
