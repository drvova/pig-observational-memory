package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/codepolicy"
)

// fake is a call handler answering from functions by method; a method it lacks is an error.
type fake map[string]func(ctx context.Context, args []json.RawMessage) (any, error)

func (f fake) Call(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	fn, ok := f[method]
	if !ok {
		return nil, fmt.Errorf("the fake has no %s", method)
	}
	var args []json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	v, err := fn(ctx, args)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// newSup makes a supervisor of the test binary, killed at the end of the test.
func newSup(t *testing.T, cfg Config) *Supervisor {
	t.Helper()
	s := New(cfg)
	t.Cleanup(s.Kill)
	return s
}

// runCode checks code with the policy and runs it.
func runCode(t *testing.T, ctx context.Context, s *Supervisor, code string, h Handler) Result {
	t.Helper()
	p, err := codepolicy.Check(codepolicy.Input{Code: code})
	if err != nil {
		t.Fatalf("policy: %v\n%s", err, code)
	}
	if h == nil {
		h = fake{}
	}
	return s.Run(ctx, Request{Program: p}, h)
}

// runRaw runs code the policy would refuse, as statement chunks or one declaration chunk.
func runRaw(ctx context.Context, s *Supervisor, chunks []wireChunk, last bool, h Handler) Result {
	if h == nil {
		h = fake{}
	}
	return s.run(ctx, chunks, last, nil, h)
}

func stmt(src string) wireChunk { return wireChunk{Kind: kindStatement, Src: src, Line: 1} }
func decl(src string, names ...string) wireChunk {
	return wireChunk{Kind: kindDeclaration, Src: src, Line: 1, Names: names}
}

// livePID is the running child's pid, 0 when none runs.
func livePID(s *Supervisor) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur == nil {
		return 0
	}
	return s.cur.pid()
}

// waitGone waits up to d for a process to stop running.
func waitGone(pid int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if gone(pid) {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return gone(pid)
}

func ok(t *testing.T, r Result) {
	t.Helper()
	if r.Outcome != OutcomeOK {
		t.Fatalf("outcome %s: %s\nout: %s", r.Outcome, r.Err, r.Out)
	}
}

func mustCheck(t *testing.T, code string) codepolicy.Program {
	t.Helper()
	p, err := codepolicy.Check(codepolicy.Input{Code: code})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// rssKB is this process's resident set, 0 where /proc is absent.
func rssKB(t *testing.T) int {
	b, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "VmRSS:") {
			var n int
			fmt.Sscan(strings.Fields(l)[1], &n)
			return n
		}
	}
	return 0
}

// assertFlat checks the extension's resident set did not take in a large message: it grew by
// far less than the 100 MB the child tried to send.
func assertFlat(t *testing.T, beforeKB int) {
	t.Helper()
	if beforeKB == 0 {
		return
	}
	if grew := rssKB(t) - beforeKB; grew > 50<<10 {
		t.Fatalf("the extension's RSS grew by %d kB", grew)
	}
}
