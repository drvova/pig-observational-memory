package hooks

import (
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
)

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

// MaybeTriggerCompaction is the extension's only compaction trigger: a compaction only where
// PiG never would, with its automatic compaction off, at PiG's own threshold
// (maybeTriggerCompactionV2). The returned channel closes once the deferred step ran; it is
// nil when nothing was scheduled.
func MaybeTriggerCompaction(rt *runtime.Runtime, h host.Host) <-chan struct{} {
	rt.EnsureConfig(h)
	cfg := rt.LiveConfig(h.Cwd())
	if cfg.Passive {
		return nil
	}
	return maybeTriggerCompactionV2(rt, h, cfg)
}
