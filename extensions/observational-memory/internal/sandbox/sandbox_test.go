package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
)

// The last expression's value is captured exactly: a constant, nil, every result of a call,
// an interpreted String method, a multi-line expression with a trailing comment, and nothing
// for a void call.
func TestCaptureLastValue(t *testing.T) {
	s := newSup(t, Config{})
	ctx := context.Background()
	cases := []struct {
		code, out, val string
		hasVal         bool
	}{
		{code: "fmt.Println(\"hi\")\n42", out: "hi\n", val: "42", hasVal: true},
		{code: "nil", val: "nil", hasVal: true},
		{code: "func two() (int, string) { return 1, \"a\" }\ntwo()", val: "1, a", hasVal: true},
		{code: "func void() { fmt.Print(\"v\") }\nvoid()", out: "v"},
		{code: "type Bee int\nfunc (b Bee) String() string { return \"bee\" }\nBee(1)", val: "bee", hasVal: true},
		{code: "strings.Join([]string{\n\t\"a\",\n\t\"b\",\n}, \"-\") // joined", val: "a-b", hasVal: true},
		{code: "x := 3\nx * 2", val: "6", hasVal: true},
		{code: "[]string{\"a\", \"b\"}", val: `["a" "b"]`, hasVal: true},
		{code: "map[string]int{\"b\": 2, \"a\": 1}", val: `map["a":1 "b":2]`, hasVal: true},
		{code: "om.Text{Body: \"page\"}", val: "page", hasVal: true},
	}
	for _, c := range cases {
		r := runCode(t, ctx, s, c.code, nil)
		ok(t, r)
		if r.Out != c.out || r.HasVal != c.hasVal || r.Val != c.val {
			t.Errorf("%q: out %q val %q (%v); want out %q val %q (%v)", c.code, r.Out, r.Val, r.HasVal, c.out, c.val, c.hasVal)
		}
	}
}

// A helper defined in one call is usable in the next, and so is a variable; both are gone
// after a reset, except what the saved units define.
func TestNamespacePersistsUntilReset(t *testing.T) {
	units := []Component{{Units: []Unit{{Name: "kept", Src: "func keptOne() int { return 7 }"}}}}
	s := newSup(t, Config{Units: func() []Component { return units }})
	ctx := context.Background()
	ok(t, runCode(t, ctx, s, "func up(s string) string { return strings.ToUpper(s) }\ny := 5", nil))
	r := runCode(t, ctx, s, "up(\"a\") + strconv.Itoa(y+keptOne())", nil)
	ok(t, r)
	if r.Val != "A12" {
		t.Fatalf("val %q", r.Val)
	}
	s.Kill()
	r = runCode(t, ctx, s, "keptOne()", nil)
	if ok(t, r); r.Val != "7" {
		t.Fatalf("after the reset: %q", r.Val)
	}
	r = runCode(t, ctx, s, "y", nil)
	if r.Outcome != OutcomeCompile || !strings.Contains(r.Err, "undefined") {
		t.Fatalf("the variable survived the reset: %+v", r)
	}
	if st := s.Status(); st.Starts != 2 || st.Generation != 2 {
		t.Fatalf("status %+v", st)
	}
}

// A compile error names the line in the call's own numbering, and the declarations before it
// that compiled stay.
func TestCompileErrorsUseCallLines(t *testing.T) {
	s := newSup(t, Config{})
	ctx := context.Background()
	r := runCode(t, ctx, s, "func a() int { return 1 }\n\nb := 2\nfmt.Println(b)\nfmt.Println(nope)", nil)
	if r.Outcome != OutcomeCompile || !strings.Contains(r.Err, "line 5:") || !strings.Contains(r.Err, "undefined: nope") {
		t.Fatalf("%+v", r)
	}
	if r.Compiled != "c---" {
		t.Fatalf("mask %q", r.Compiled)
	}
	if r := runCode(t, ctx, s, "a()", nil); r.Val != "1" {
		t.Fatalf("%+v", r)
	}
}

// A failed redefinition restores the good one, and a declaration beside a statement that panics
// is reported compiled.
func TestFailureInsideTheNamespace(t *testing.T) {
	s := newSup(t, Config{})
	ctx := context.Background()
	ok(t, runCode(t, ctx, s, "func f() int { return 1 }", nil))
	r := runCode(t, ctx, s, "func f() int { return missing }", nil)
	if r.Outcome != OutcomeCompile || r.Compiled != "x" {
		t.Fatalf("%+v", r)
	}
	if r := runCode(t, ctx, s, "f()", nil); r.Val != "1" {
		t.Fatalf("the good definition was not restored: %+v", r)
	}
	r = runCode(t, ctx, s, "func g() int { return 2 }\npanic(\"boom\")", nil)
	if r.Outcome != OutcomeRuntime || r.Compiled != "c-" || !strings.Contains(r.Err, "boom") || !strings.Contains(r.Err, "line 2") {
		t.Fatalf("%+v", r)
	}
	if r := runCode(t, ctx, s, "g()", nil); r.Val != "2" {
		t.Fatalf("%+v", r)
	}
}

// A panic inside the interpreter's compiler (a label named len, which the policy refuses) is a
// compile failure: the earlier definition is restored and the child survives.
func TestCompilerPanicIsACompileFailure(t *testing.T) {
	s := newSup(t, Config{})
	ctx := context.Background()
	ok(t, runCode(t, ctx, s, "func lab(s string) int { return len(s) }", nil))
	pid := livePID(s)
	r := runRaw(ctx, s, []wireChunk{decl("func lab(s string) int { len: for { break len }; return len(s) }", "lab")}, false, nil)
	if r.Outcome != OutcomeCompile || !strings.Contains(r.Err, "panic") {
		t.Fatalf("%+v", r)
	}
	if r := runCode(t, ctx, s, "lab(\"abc\")", nil); r.Val != "3" || livePID(s) != pid {
		t.Fatalf("restore or survival failed: %+v (pid %d, was %d)", r, livePID(s), pid)
	}
}

// Runtime errors are recovered in the child with what was printed before them, and the
// namespace keeps working.
func TestRuntimeErrorsAreRecovered(t *testing.T) {
	s := newSup(t, Config{})
	ctx := context.Background()
	ok(t, runCode(t, ctx, s, "keep := 1", nil))
	pid := livePID(s)
	for _, code := range []string{
		"fmt.Print(\"before\")\nvar p *int\n*p = 1",
		"fmt.Print(\"before\")\nxs := []int{}\n_ = xs[3]",
		"fmt.Print(\"before\")\nz := 0\n_ = 1 / z",
		"fmt.Print(\"before\")\nvar m map[string]int\nm[\"a\"] = 1",
	} {
		r := runCode(t, ctx, s, code, nil)
		if r.Outcome != OutcomeRuntime || r.Out != "before" || r.Err == "" {
			t.Errorf("%q: %+v", code, r)
		}
	}
	if r := runCode(t, ctx, s, "keep", nil); r.Val != "1" || livePID(s) != pid {
		t.Fatalf("%+v", r)
	}
}

// Cancelling kills the child: the old pid is gone, the result says unsaved variables are gone,
// and the next call runs on a new child with the saved units.
func TestCancelKillsTheChild(t *testing.T) {
	units := []Component{{Units: []Unit{{Name: "seven", Src: "func seven() int { return 7 }"}}}}
	s := newSup(t, Config{Units: func() []Component { return units }})
	ok(t, runCode(t, context.Background(), s, "v := 1", nil))
	pid := livePID(s)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	r := runCode(t, ctx, s, "for {}", nil)
	if r.Outcome != OutcomeCancelled || !r.Restarted || !strings.Contains(r.Err, "unsaved variables are gone") {
		t.Fatalf("%+v", r)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("the cancel took %v", d)
	}
	if !gone(pid) {
		t.Fatalf("pid %d still runs", pid)
	}
	r = runCode(t, context.Background(), s, "seven()", nil)
	if ok(t, r); r.Val != "7" || livePID(s) == pid {
		t.Fatalf("%+v", r)
	}
	if r := runCode(t, context.Background(), s, "v", nil); r.Outcome != OutcomeCompile {
		t.Fatalf("the variable survived the cancel: %+v", r)
	}
}

// A call cancelled while it waits for another call stops waiting and never touches the child
// serving the other call.
func TestCancelWhileWaitingLeavesTheChild(t *testing.T) {
	s := newSup(t, Config{})
	ok(t, runCode(t, context.Background(), s, "1", nil))
	pid := livePID(s)
	ctxA, cancelA := context.WithCancel(context.Background())
	done := make(chan Result)
	go func() { done <- runCode(t, ctxA, s, "for {}", nil) }()
	time.Sleep(100 * time.Millisecond)
	ctxB, cancelB := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancelB()
	r := runCode(t, ctxB, s, "2", nil)
	if r.Outcome != OutcomeCancelled || !r.Waited {
		t.Fatalf("%+v", r)
	}
	if gone(pid) || livePID(s) != pid {
		t.Fatal("the waiting call touched the child")
	}
	cancelA()
	if r := <-done; r.Outcome != OutcomeCancelled || r.Waited {
		t.Fatalf("%+v", r)
	}
}

// An om read answering a cancelled call stops on the context, and its answer is dropped.
func TestSlowReadStopsOnCancel(t *testing.T) {
	s := newSup(t, Config{})
	var stopped atomic.Bool
	h := fake{omwire.MNotes: func(ctx context.Context, _ []json.RawMessage) (any, error) {
		select {
		case <-ctx.Done():
			stopped.Store(true)
			return nil, ctx.Err()
		case <-time.After(time.Minute):
			return om.NoteList{}, nil
		}
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	start := time.Now()
	r := runCode(t, ctx, s, "om.Notes()", h)
	if r.Outcome != OutcomeCancelled || time.Since(start) > 10*time.Second || !stopped.Load() {
		t.Fatalf("%+v (stopped %v)", r, stopped.Load())
	}
}

// om calls reach the handler with their arguments; an error becomes a runtime error for a read
// and the returned error for a call that has one; Str reads the call's strings in the child.
func TestOMCalls(t *testing.T) {
	s := newSup(t, Config{})
	ctx := context.Background()
	var gotOpts omwire.Options
	h := fake{
		omwire.MNotes: func(_ context.Context, args []json.RawMessage) (any, error) {
			if err := json.Unmarshal(args[0], &gotOpts); err != nil {
				return nil, err
			}
			return om.NoteList{Rows: []om.Note{{ID: "a1", Content: "x", Fields: map[string]string{"k": "v"}, SourceEntryIDs: []string{"e1"}}}, Matched: 1, Showing: 1}, nil
		},
		omwire.MRecall: func(context.Context, []json.RawMessage) (any, error) {
			return nil, errors.New("no record, entry or memory named zz")
		},
		omwire.MReflections: func(context.Context, []json.RawMessage) (any, error) {
			return nil, errors.New("reflections broke")
		},
	}
	r := runCode(t, ctx, s, "om.Notes(om.From(`e1`).Through(`e9`).Limit(50), om.Search(`oven`)).IDs()", h)
	if ok(t, r); r.Val != `["a1"]` {
		t.Fatalf("%+v", r)
	}
	if gotOpts.From != "e1" || gotOpts.Through != "e9" || gotOpts.Limit == nil || *gotOpts.Limit != 50 || len(gotOpts.Search) != 1 {
		t.Fatalf("options %+v", gotOpts)
	}
	// Reads return copies: mutating a field, a slice element and a map entry changes nothing
	// the next read returns.
	r = runCode(t, ctx, s, "l := om.Notes()\nl.Rows[0].Content = \"changed\"\nl.Rows[0].SourceEntryIDs[0] = \"zz\"\nl.Rows[0].Fields[\"k\"] = \"w\"\nn := om.Notes().Rows[0]\nn.Content + n.SourceEntryIDs[0] + n.Fields[\"k\"]", h)
	if ok(t, r); r.Val != "xe1v" {
		t.Fatalf("%+v", r)
	}
	r = runCode(t, ctx, s, "_, err := om.Recall(`zz`)\nerr", h)
	if ok(t, r); r.Val != "om.Recall: no record, entry or memory named zz" {
		t.Fatalf("%+v", r)
	}
	r = runCode(t, ctx, s, "fmt.Print(\"p\")\nom.Reflections()", h)
	if r.Outcome != OutcomeRuntime || r.Out != "p" || !strings.Contains(r.Err, "om.Reflections: reflections broke") {
		t.Fatalf("%+v", r)
	}
	p := mustCheck(t, "om.Str(`body`) + `!`")
	r = s.Run(ctx, Request{Program: p, Strings: map[string]string{"body": "quote \" and `tick`"}}, h)
	if ok(t, r); r.Val != "quote \" and `tick`!" {
		t.Fatalf("%+v", r)
	}
	if r := runCode(t, ctx, s, "om.Str(`missing`)", h); r.Outcome != OutcomeRuntime || !strings.Contains(r.Err, "no string named") {
		t.Fatalf("%+v", r)
	}
	if r := runCode(t, ctx, s, "om.ErrNoCall() != nil", h); r.Val != "true" {
		t.Fatalf("%+v", r)
	}
}

// A method the child's table does not list kills the child, and the handler never sees it.
func TestDisallowedMethodKillsTheChild(t *testing.T) {
	s := newSup(t, Config{Methods: []string{omwire.MNotes}})
	called := false
	h := fake{omwire.MSave: func(context.Context, []json.RawMessage) (any, error) { called = true; return "saved", nil }}
	r := runCode(t, context.Background(), s, "om.Save(`n`, `lesson`, `d`)", h)
	if r.Outcome != OutcomeCrashed || !strings.Contains(r.Err, `"Save"`) || called {
		t.Fatalf("%+v (called %v)", r, called)
	}
}

// An om call whose arguments exceed one recall page is refused by the child before it is sent,
// as a runtime error, and the extension's memory stays flat.
func TestHugeArgumentRefusedByTheStub(t *testing.T) {
	s := newSup(t, Config{})
	called := false
	h := fake{omwire.MSave: func(context.Context, []json.RawMessage) (any, error) { called = true; return "", nil }}
	before := rssKB(t)
	// Save returns its error; Notes, with no error result, panics with it.
	r := runCode(t, context.Background(), s, "_, err := om.Save(strings.Repeat(`x`, 100<<20), `lesson`, `d`)\nerr", h)
	if ok(t, r); !strings.Contains(r.Val, "one om call takes at most 10000") {
		t.Fatalf("%+v", r)
	}
	r = runCode(t, context.Background(), s, "om.Notes(om.Search(strings.Repeat(`x`, 100<<20)))", h)
	if r.Outcome != OutcomeRuntime || !strings.Contains(r.Err, "at most") {
		t.Fatalf("%+v", r)
	}
	if called {
		t.Fatal("the handler saw the call")
	}
	assertFlat(t, before)
}

// Printed output is bounded with the count of what was left out, a cyclic value renders as
// <cycle>, and a String method that loops is stopped by the abort.
func TestResultBounds(t *testing.T) {
	s := newSup(t, Config{})
	ctx := context.Background()
	r := runCode(t, ctx, s, "for i := 0; i < 3000; i++ { fmt.Print(\"0123456789\") }", nil)
	ok(t, r)
	if n := len([]rune(r.Out)); n != ResultChars || r.Cut != 30000-ResultChars {
		t.Fatalf("out %d characters, cut %d", n, r.Cut)
	}
	r = runCode(t, ctx, s, "type node struct{ Name string; Next *node }\nn := &node{Name: \"a\"}\nn.Next = n\nn", nil)
	if ok(t, r); !strings.Contains(r.Val, "<cycle>") {
		t.Fatalf("%+v", r)
	}
	r = runCode(t, ctx, s, "m := map[string]any{}\nm[\"self\"] = m\nm", nil)
	if ok(t, r); !strings.Contains(r.Val, "<cycle>") {
		t.Fatalf("%+v", r)
	}
	r = runCode(t, ctx, s, "strings.Repeat(\"y\", 30000)", nil)
	if ok(t, r); len(r.Val) != ResultChars || r.Cut == 0 {
		t.Fatalf("val %d, cut %d", len(r.Val), r.Cut)
	}
	actx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	r = runCode(t, actx, s, "type loop int\nfunc (l loop) String() string { for {} }\nloop(1)", nil)
	if r.Outcome != OutcomeCancelled {
		t.Fatalf("%+v", r)
	}
}

// Nothing outside the generated tables is reachable: not omwire, not encoding/json (whose
// UnmarshalTypeError leads to reflect), not a deleted time function, not a dropped variable;
// and a kept copy cannot be assigned through.
func TestSymbolTableIsTheWall(t *testing.T) {
	s := newSup(t, Config{})
	ctx := context.Background()
	for _, src := range []string{
		"omwire.Install(nil)",
		"om.omwire",
		"import \"encoding/json\"",
		"var e json.UnmarshalTypeError\n_ = e.Type.Method(0).Func.UnsafePointer()",
		"import \"om/omwire\"",
		"import \"os\"",
		"import \"reflect\"",
		"import \"unsafe\"",
		"time.Sleep(1)",
		"time.LoadLocation(\"UTC\")",
		"_ = time.Local",
		"_ = unicode.Categories",
		"_ = unicode.Lu",
		"_omseed.Kept",
	} {
		r := runRaw(ctx, s, []wireChunk{stmt(src)}, false, nil)
		if r.Outcome != OutcomeCompile {
			t.Errorf("%q: %+v", src, r)
		}
	}
	r := runRaw(ctx, s, []wireChunk{stmt("strconv.ErrSyntax = nil")}, false, nil)
	if r.Outcome == OutcomeOK {
		r = runCode(t, ctx, s, "strconv.ErrSyntax != nil", nil)
		if r.Val != "true" {
			t.Fatalf("a kept copy was assigned through: %+v", r)
		}
	}
	// The kept copies and the reachable packages work.
	r = runCode(t, ctx, s, "_, err := strconv.Atoi(\"x\")\nerrors.Is(err, strconv.ErrSyntax) && time.UTC != nil && slices.Contains([]int{1}, 1) && cmp.Compare(1, 2) < 0 && unicode.IsUpper('A')", nil)
	if ok(t, r); r.Val != "true" {
		t.Fatalf("%+v", r)
	}
}

// describe lists an om value's fields and methods.
func TestDescribe(t *testing.T) {
	s := newSup(t, Config{})
	r := runCode(t, context.Background(), s, "describe(om.NoteList{})", nil)
	ok(t, r)
	for _, want := range []string{"om.NoteList", "Rows []om.Note", "Len() int", "Count(string) map[string]int", "Filter(func(om.Note) bool) om.NoteList"} {
		if !strings.Contains(r.Val, want) {
			t.Errorf("describe lacks %q:\n%s", want, r.Val)
		}
	}
}

// Kill (session_start, session_shutdown) stops a child serving a call: the call returns
// cancelled, and the next call starts a fresh child.
func TestKillDuringACall(t *testing.T) {
	s := newSup(t, Config{})
	ok(t, runCode(t, context.Background(), s, "1", nil))
	pid := livePID(s)
	done := make(chan Result)
	p := mustCheck(t, "for {}")
	go func() { done <- s.Run(context.Background(), Request{Program: p}, fake{}) }()
	time.Sleep(100 * time.Millisecond)
	s.Kill()
	r := <-done
	if r.Outcome != OutcomeCancelled || !r.Restarted || !gone(pid) {
		t.Fatalf("%+v", r)
	}
	if r := runCode(t, context.Background(), s, "2", nil); r.Val != "2" || livePID(s) == pid {
		t.Fatalf("%+v", r)
	}
	if st := s.Status(); st.Starts != 2 || !st.Live {
		t.Fatalf("%+v", st)
	}
}

// Restoring after a failed group never re-runs a variable's initializer, which could call om.
func TestRestoreNeverRerunsAVariable(t *testing.T) {
	s := newSup(t, Config{})
	calls := 0
	h := notesFake()
	inner := h[omwire.MNotes]
	h[omwire.MNotes] = func(ctx context.Context, args []json.RawMessage) (any, error) { calls++; return inner(ctx, args) }
	ok(t, runCode(t, context.Background(), s, "var vs = om.Notes()", h))
	r := runCode(t, context.Background(), s, "var vs = om.Notes()\nfunc bad() int { return nope }", h)
	if r.Outcome != OutcomeCompile || calls != 1 {
		t.Fatalf("%+v, %d calls", r, calls)
	}
}
