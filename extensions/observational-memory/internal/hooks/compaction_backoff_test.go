package hooks

import (
	"errors"
	"strings"
	"testing"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/testfix"
)

// A compaction the host refuses is not requested again on every settle: the
// trigger waits for another threshold's worth of source, a new compaction
// boundary, or another session (upstream issue #47, failed-attempt half).
func TestTriggerBacksOffAfterRefusedCompaction(t *testing.T) {
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(dueBranch())
	refuse := func(callbacks host.CompactCallbacks) {
		callbacks.OnError(errors.New("Nothing to compact (session too small)"))
	}
	h.CompactFn = refuse

	fireTrigger(t, rt, h)
	if h.CompactCount() != 1 || rt.CompactionBackoff() == nil || rt.CompactInFlight() {
		t.Fatalf("compact calls %d backoff %+v in flight %v", h.CompactCount(), rt.CompactionBackoff(), rt.CompactInFlight())
	}
	notifications := len(h.Notifications)

	// Same branch, nothing new: no request, no notification.
	fireTrigger(t, rt, h)
	if h.CompactCount() != 1 || len(h.Notifications) != notifications {
		t.Fatalf("compact calls %d notifications %+v", h.CompactCount(), h.Notifications)
	}

	// Another threshold's worth of observed source: ask again.
	grown := covered([]ledger.Entry{text("raw-1", "aaaaaaaaaaaa"), text("raw-2", "bbbbbbbbbbbbbbbbbbbbbbbb")}, "")
	h.BranchQueue = [][]ledger.Entry{grown}
	h.CompactFn = func(callbacks host.CompactCallbacks) { callbacks.OnComplete() }
	fireTrigger(t, rt, h)
	if h.CompactCount() != 2 || rt.CompactionBackoff() != nil {
		t.Fatalf("compact calls %d backoff %+v", h.CompactCount(), rt.CompactionBackoff())
	}
}

func TestCompactionBackoffClearsOnNewBoundaryOrSession(t *testing.T) {
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(dueBranch())
	h.CompactFn = func(callbacks host.CompactCallbacks) { callbacks.OnError(errors.New("Already compacted")) }
	fireTrigger(t, rt, h)
	if rt.CompactionBackoff() == nil {
		t.Fatal("no backoff recorded")
	}

	// Another session: the backoff does not apply.
	h.ID = "session-2"
	fireTrigger(t, rt, h)
	if h.CompactCount() != 2 {
		t.Fatalf("compact calls %d", h.CompactCount())
	}

	// A new compaction boundary on the branch: the backoff does not apply.
	withBoundary := covered([]ledger.Entry{text("raw-0", "zzzz"), testfix.CompactionEntry("cmp-new", testfix.CompactionArgs{FirstKeptEntryID: "raw-1"}), text("raw-1", "aaaaaaaaaaaa")}, "")
	h.BranchQueue = [][]ledger.Entry{withBoundary}
	fireTrigger(t, rt, h)
	if h.CompactCount() != 3 {
		t.Fatalf("compact calls %d", h.CompactCount())
	}
}

// A compaction our own hook cancelled is not a refusal.
func TestCancelledCompactionDoesNotBackOff(t *testing.T) {
	rt, h := triggerRuntime(triggerArgs{}), triggerHost(dueBranch())
	h.CompactFn = func(callbacks host.CompactCallbacks) { callbacks.OnError(errors.New("Compaction cancelled")) }
	fireTrigger(t, rt, h)
	fireTrigger(t, rt, h)
	if h.CompactCount() != 2 || rt.CompactionBackoff() != nil {
		t.Fatalf("compact calls %d backoff %+v", h.CompactCount(), rt.CompactionBackoff())
	}
	for _, n := range h.Notifications {
		if strings.Contains(n.Message, "cancelled") {
			t.Fatalf("notifications %+v", h.Notifications)
		}
	}
}
