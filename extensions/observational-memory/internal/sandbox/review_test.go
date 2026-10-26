package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
)

// An om call whose arguments are within the character bound but encode to a line over the
// protocol bound (many empty values) fails in the child before anything is sent, and the child
// stays in step: the next call runs on the same child. A read panics with the failure; a seeded
// function returns it as its "error: <function>: <message>" text.
func TestOverBoundArgumentsKeepTheChildInStep(t *testing.T) {
	s := newSup(t, Config{})
	h := notesFake()
	for _, code := range []string{
		"names := make([]string, 60000)\nkept(names...)",
		"o := om.Opt{}\nfor i := 0; i < 10000; i++ {\n\to = o.Where(\"\", \"\")\n}\nom.Notes(o)",
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		r := runCode(t, ctx, s, code, h)
		failed := r.Outcome == OutcomeRuntime && strings.Contains(r.Err, "encode to")
		if strings.HasPrefix(code, "names") {
			failed = r.Outcome == OutcomeOK && strings.HasPrefix(r.Val, "error: kept: ") && strings.Contains(r.Val, "encode to")
		}
		if !failed || r.Restarted {
			cancel()
			t.Fatalf("%q: %+v", code, r)
		}
		pid := livePID(s)
		r = runCode(t, ctx, s, "1 + 1", h)
		cancel()
		if r.Outcome != OutcomeOK || r.Val != "2" || livePID(s) != pid {
			t.Fatalf("the next call after %q: %+v (pid %d, was %d)", code, r, livePID(s), pid)
		}
	}
}

// A new function a failed declaration group bound, a sibling that was correct on its own
// included, panics when called instead of returning zero values; redefining it makes it work.
func TestFailedGroupNewFunctionsPanic(t *testing.T) {
	s := newSup(t, Config{})
	ctx := context.Background()
	r := runCode(t, ctx, s, "func a() int { return 1 }\nfunc b() int { return nope }", nil)
	if r.Outcome != OutcomeCompile || r.Compiled != "xx" || !strings.Contains(r.Err, "a, b did not compile") || r.Restarted {
		t.Fatalf("%+v", r)
	}
	for _, call := range []string{"a()", "b()"} {
		r := runCode(t, ctx, s, call, nil)
		if r.Outcome != OutcomeRuntime || !strings.Contains(r.Err, "did not compile") {
			t.Fatalf("%s: %+v", call, r)
		}
	}
	r = runCode(t, ctx, s, "func d() int { return e() }\nfunc e() int { return nope2 }", nil)
	if r.Outcome != OutcomeCompile {
		t.Fatalf("%+v", r)
	}
	ok(t, runCode(t, ctx, s, "func e() int { return 5 }", nil))
	if r := runCode(t, ctx, s, "d()", nil); r.Outcome != OutcomeRuntime || !strings.Contains(r.Err, "d did not compile") {
		t.Fatalf("d answered after its group failed: %+v", r)
	}
	ok(t, runCode(t, ctx, s, "func a() int { return 1 }", nil))
	if r := runCode(t, ctx, s, "a()", nil); r.Val != "1" {
		t.Fatalf("%+v", r)
	}
}

// A new method cannot be stubbed (the interpreter keeps a method's first definition), so a
// failed group that bound one beside a function restarts the namespace instead of leaving the
// method answering zero values.
func TestFailedGroupNewMethodRestarts(t *testing.T) {
	s := newSup(t, Config{})
	ctx := context.Background()
	ok(t, runCode(t, ctx, s, "keep := 7", nil))
	r := runCode(t, ctx, s, "type X struct{ a int }\nfunc (x X) A() int { return 1 }\nfunc b() int { return nope }", nil)
	if r.Outcome != OutcomeCompile || !r.Restarted || !strings.Contains(r.Err, "X.A did not compile") {
		t.Fatalf("%+v", r)
	}
	if r := runCode(t, ctx, s, "X{}.A()", nil); r.Outcome == OutcomeOK {
		t.Fatalf("the method answered after its group failed: %+v", r)
	}
	if r := runCode(t, ctx, s, "keep", nil); r.Outcome != OutcomeCompile {
		t.Fatalf("the namespace did not restart: %+v", r)
	}
}

// A rescue that ran the right-hand side, a write included, and then could not name a value's
// type is a runtime failure that says the write took effect, not a compile error inviting the
// model to run the write again; the host saw the write once.
func TestFailedRescueAfterAWriteIsARuntimeFailure(t *testing.T) {
	s := newSup(t, Config{})
	var writes atomic.Int32
	h := notesFake()
	h[omwire.MPropose] = func(context.Context, []json.RawMessage) (any, error) {
		writes.Add(1)
		return nil, nil
	}
	r := runCode(t, context.Background(), s, "l := om.Notes()\nn, e := l.Rows[0], om.Propose(\"k\", \"n\", \"t\")", h)
	if writes.Load() != 1 {
		t.Fatalf("the write ran %d times", writes.Load())
	}
	if r.Outcome != OutcomeRuntime || !strings.Contains(r.Err, "took effect") || !strings.Contains(r.Err, "repeats its writes") {
		t.Fatalf("%+v", r)
	}
}

// A map is ordered by its keys' values, as fmt orders it, and only the keys shown are rendered.
func TestRenderMapKeys(t *testing.T) {
	if got, _ := render(map[int]string{10: "a", 2: "b", 1: "c"}, ResultChars); got != `map[1:"c" 2:"b" 10:"a"]` {
		t.Fatalf("%s", got)
	}
	var calls atomic.Int64
	m := map[countedKey]int{}
	for i := range 100_000 {
		m[countedKey{i: i, calls: &calls}] = i
	}
	const budget = 100
	got, over := render(m, budget)
	if !over || len([]rune(got)) != budget {
		t.Fatalf("%q, %v", got, over)
	}
	if n := calls.Load(); n > budget {
		t.Fatalf("rendering %d characters rendered %d keys", budget, n)
	}
}

type countedKey struct {
	i     int
	calls *atomic.Int64
}

func (k countedKey) String() string {
	k.calls.Add(1)
	return fmt.Sprint(k.i)
}

// A Kill while the child boots is not lost: the child booted from the units read before it is
// dropped, and the call runs on one booted from the units after it.
func TestKillDuringBoot(t *testing.T) {
	var s *Supervisor
	var boots atomic.Int32
	s = newSup(t, Config{Units: func() []Component {
		if boots.Add(1) == 1 {
			s.Kill()
			return []Component{{Units: []Unit{{Name: "f", Src: "func f() int { return 1 }"}}}}
		}
		return []Component{{Units: []Unit{{Name: "f", Src: "func f() int { return 2 }"}}}}
	}})
	if r := runCode(t, context.Background(), s, "f()", nil); r.Val != "2" {
		t.Fatalf("%+v", r)
	}
}

// A Kill while the host answers an om call makes the call cancelled, not crashed.
func TestKillDuringACallIsCancelled(t *testing.T) {
	s := newSup(t, Config{})
	h := fake{omwire.MNotes: func(context.Context, []json.RawMessage) (any, error) {
		s.Kill()
		return nil, nil
	}}
	r := runCode(t, context.Background(), s, "om.Notes()", h)
	if r.Outcome != OutcomeCancelled || !r.Restarted {
		t.Fatalf("%+v", r)
	}
	if st := s.Status(); st.LastCrash != "" {
		t.Fatalf("a reset was recorded as a crash: %q", st.LastCrash)
	}
}

// An error that uses the whole budget renders alone: no stray newline stands for the values
// it left no room for.
func TestRenderValuesErrorOverBudgetHasNoLeadingNewline(t *testing.T) {
	text, cut := renderValues([]any{5, errors.New(strings.Repeat("x", 50))}, 30)
	if strings.HasPrefix(text, "\n") || !strings.HasPrefix(text, "error: x") || !cut {
		t.Fatalf("%q (cut %v)", text, cut)
	}
}
