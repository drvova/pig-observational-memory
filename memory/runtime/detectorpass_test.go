package runtime

import "testing"

type fakePass struct {
	stopped []string
	done    chan struct{}
}

func (p *fakePass) Stop(reason string) {
	p.stopped = append(p.stopped, reason)
	if p.done != nil {
		close(p.done)
		p.done = nil
	}
}
func (p *fakePass) Done() <-chan struct{} { return p.done }

// One detector pass runs at a time: a new one stops the previous as the next compaction's, a
// stop takes it out, and a pass that ended on its own clears only itself.
func TestDetectorPassSlot(t *testing.T) {
	rt := New()
	first, second := &fakePass{done: make(chan struct{})}, &fakePass{done: make(chan struct{})}
	rt.SwapDetectorPass(first)
	rt.SwapDetectorPass(second)
	if len(first.stopped) != 1 || first.stopped[0] != DetectorStopCompaction || rt.DetectorPassInFlight() != DetectorPass(second) {
		t.Fatalf("swap: %v", first.stopped)
	}
	rt.ClearDetectorPass(first)
	if rt.DetectorPassInFlight() != DetectorPass(second) {
		t.Fatal("an ended pass cleared a newer one")
	}
	rt.StopDetectorPass(DetectorStopSessionEnd)
	if len(second.stopped) != 1 || second.stopped[0] != DetectorStopSessionEnd || rt.DetectorPassInFlight() != nil {
		t.Fatalf("stop: %v", second.stopped)
	}
	rt.StopDetectorPass(DetectorStopSessionEnd) // nothing in flight
}
