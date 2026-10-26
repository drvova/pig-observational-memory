package sandbox

import (
	"context"
	"fmt"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/codepolicy"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
)

// DetectorResult is one detector run's outcome (docs/code-tool.md, "Running, and the time
// bound"): a count, or the error that refuses or reverts the detector.
type DetectorResult struct {
	// Outcome is OutcomeOK for a count, else compile, runtime, cancelled or crashed.
	Outcome string
	// Count is what Count() returned (OutcomeOK only).
	Count int
	// Err says why there is no count: the compile failure, the panic, the crash report (a
	// disallowed om method and a protocol line over its bound included), or the cancel.
	Err string
	// Queued marks a run cancelled before its child was spawned (OutcomeCancelled): it never
	// started, so it is no evidence for or against its detector.
	Queued bool
}

// RunDetector runs one detector in a short-lived detector child of its own: start, build the
// one detector's interpreter from src (none of the agent's units, a read-only om table),
// evaluate Count(), return the count, kill the child. The host answers the child's om calls
// through h for the detector's methods only (omwire.DetectorMethods); any other method kills
// the child and is the run's error. The run ends when it returns or when ctx ends, which kills
// the child (OutcomeCancelled): no time bound of its own. src is the detector's source as the
// caller checked it (codepolicy.CheckDetector); the child never receives a path.
func RunDetector(ctx context.Context, src string, h Handler) DetectorResult {
	return runDetector(ctx, Config{}, src, h)
}

// runDetector is RunDetector on a configured supervisor; tests set the binary and fixture modes.
func runDetector(ctx context.Context, cfg Config, src string, h Handler) DetectorResult {
	if cfg.Mode == "" {
		cfg.Mode = ModeDetector
	}
	cfg.Methods = omwire.DetectorMethods
	s := New(cfg)
	defer s.Kill()
	if err := ctx.Err(); err != nil {
		return DetectorResult{Outcome: OutcomeCancelled, Err: err.Error(), Queued: true}
	}
	p, err := s.spawn()
	if err != nil {
		return DetectorResult{Outcome: OutcomeCrashed, Err: "the detector child did not start: " + err.Error()}
	}
	ready, err := s.handshake(ctx, p, msg{T: tInit, Gen: p.gen, Components: []wireComponent{{ID: 0, Units: []Unit{{Name: codepolicy.DetectorEntry, Src: src}}}}})
	if err != nil {
		if ctx.Err() != nil {
			s.drop(p)
			return DetectorResult{Outcome: OutcomeCancelled, Err: ctx.Err().Error()}
		}
		return DetectorResult{Outcome: OutcomeCrashed, Err: s.crashed(p, "").Err}
	}
	if len(ready.Failed) > 0 {
		s.drop(p)
		return DetectorResult{Outcome: OutcomeCompile, Err: ready.Failed[0].Msg}
	}
	s.mu.Lock()
	s.cur = p
	s.mu.Unlock()
	prog, err := codepolicy.Split(codepolicy.DetectorCall)
	if err != nil {
		return DetectorResult{Outcome: OutcomeCrashed, Err: err.Error()}
	}
	res := s.Run(ctx, Request{Program: prog}, h)
	switch {
	case res.Outcome != OutcomeOK:
		return DetectorResult{Outcome: res.Outcome, Err: res.Err}
	case !res.HasVal:
		return DetectorResult{Outcome: OutcomeRuntime, Err: "Count() returned no value"}
	}
	n, err := codepolicy.ParseCount(res.Val)
	if err != nil {
		return DetectorResult{Outcome: OutcomeRuntime, Err: err.Error()}
	}
	return DetectorResult{Outcome: OutcomeOK, Count: n}
}

// Failed reports whether the run gave no count.
func (r DetectorResult) Failed() bool { return r.Outcome != OutcomeOK }

// String is the run's outcome as /om:status and the usage ledger name it.
func (r DetectorResult) String() string {
	if r.Outcome == OutcomeOK {
		return fmt.Sprintf("count %d", r.Count)
	}
	return r.Outcome + ": " + r.Err
}
