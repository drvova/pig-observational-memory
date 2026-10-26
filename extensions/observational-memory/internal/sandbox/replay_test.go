package sandbox

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
)

// notesFake answers Notes with one record.
func notesFake() fake {
	return fake{omwire.MNotes: func(context.Context, []json.RawMessage) (any, error) {
		return om.NoteList{Rows: []om.Note{{ID: "a1", Content: "x", Fields: map[string]string{"k": "v"}}}, Matched: 1, Showing: 1}, nil
	}}
}

// One broken unit is listed as a load error with its message and its dependent as "depends on",
// leaves no callable symbol of its own or of the dependent, and the other units load.
func TestReplayIsolatesABrokenComponent(t *testing.T) {
	comps := []Component{
		{Units: []Unit{{Name: "a", Src: "func a() int { return 1 }"}}},
		{Units: []Unit{{Name: "b", Src: "func b() int { return missing }"}}},
		{Units: []Unit{{Name: "c", Src: "func c() int { return b() + 1 }"}}, Deps: []int{1}},
		{Units: []Unit{{Name: "d", Src: "func d() int { return a() + 3 }"}}, Deps: []int{0}},
		{Units: []Unit{{Name: "Even", Src: "func Even(n int) bool { if n == 0 { return true }; return Odd(n - 1) }"}, {Name: "Odd", Src: "func Odd(n int) bool { if n == 0 { return false }; return Even(n - 1) }"}}},
	}
	s := newSup(t, Config{Units: func() []Component { return comps }})
	ctx := context.Background()
	r := runCode(t, ctx, s, "fmt.Sprint(a(), d(), Even(4))", nil)
	if ok(t, r); r.Val != "1 4 true" {
		t.Fatalf("%+v", r)
	}
	for _, name := range []string{"b", "c"} {
		if r := runCode(t, ctx, s, name+"()", nil); r.Outcome != OutcomeCompile {
			t.Errorf("%s is callable: %+v", name, r)
		}
	}
	st := s.Status()
	if len(st.LoadErrors) != 2 || st.LoadErrors[0].Units[0] != "b" || !strings.Contains(st.LoadErrors[0].Msg, "missing") ||
		st.LoadErrors[1].Units[0] != "c" || st.LoadErrors[1].Msg != "depends on b" {
		t.Fatalf("load errors %+v", st.LoadErrors)
	}
	// Two starts: the first reported b, the second loaded the rest.
	if st.Starts != 2 {
		t.Fatalf("starts %d", st.Starts)
	}
}

// A unit that crashes the child at replay is quarantined, its dependent is listed, and the
// others load; a redefinition of it is tried again.
func TestReplayQuarantinesACrashingComponent(t *testing.T) {
	comps := []Component{
		{Units: []Unit{{Name: "a", Src: "func a() int { return 1 }"}}},
		{Units: []Unit{{Name: "boom", Src: "func boom() int { return 2 } // CRASHME"}}},
		{Units: []Unit{{Name: "after", Src: "func after() int { return boom() }"}}, Deps: []int{1}},
		{Units: []Unit{{Name: "z", Src: "func z() int { return 26 }"}}},
	}
	s := newSup(t, Config{Mode: "fixture-crash-on-marker", Units: func() []Component { return comps }})
	ctx := context.Background()
	r := runCode(t, ctx, s, "a() + z()", nil)
	if ok(t, r); r.Val != "27" {
		t.Fatalf("%+v", r)
	}
	st := s.Status()
	if len(st.Quarantined) != 1 || st.Quarantined[0].Units[0] != "boom" || !strings.Contains(st.Quarantined[0].Msg, "fixture crash at replay") {
		t.Fatalf("quarantined %+v", st.Quarantined)
	}
	if len(st.LoadErrors) != 1 || st.LoadErrors[0].Units[0] != "after" || st.LoadErrors[0].Msg != "depends on boom" {
		t.Fatalf("load errors %+v", st.LoadErrors)
	}
	if r := runCode(t, ctx, s, "boom()", nil); r.Outcome != OutcomeCompile {
		t.Fatalf("the quarantined unit is callable: %+v", r)
	}
	// Restarting keeps it quarantined without crashing again; a redefinition loads.
	s.Kill()
	before := s.Status().Starts
	if r := runCode(t, ctx, s, "a()", nil); r.Val != "1" || s.Status().Starts != before+1 {
		t.Fatalf("%+v, starts %d -> %d", r, before, s.Status().Starts)
	}
	comps[1].Units[0].Src = "func boom() int { return 2 }"
	s.Kill()
	if r := runCode(t, ctx, s, "after()", nil); r.Val != "2" {
		t.Fatalf("the redefinition did not load: %+v", r)
	}
}

// Declaring a top-level variable from an expression over host values works, though the
// interpreter cannot infer its type in one step: the child declares it in two (rescue.go).
func TestTopLevelDeclarationsOverHostValues(t *testing.T) {
	s := newSup(t, Config{})
	h := notesFake()
	ctx := context.Background()
	for _, c := range []struct{ code, val string }{
		{"l := om.Notes()\nn := l.Rows[0]\nn.ID", "a1"},
		{"n2 := om.Notes().Rows[0]\nn2.Content", "x"},
		{"s1 := strings.Fields(\"a b\")\nw := s1[0]\nw", "a"},
		{"m := map[string]int{\"a\": 3}\nks := strings.Fields(\"a\")\nv, found := m[ks[0]]\nfmt.Sprint(v, found)", "3 true"},
		{"l := om.Notes()\nfs := l.Rows[0].Fields\nv2, ok2 := fs[\"k\"]\nfmt.Sprint(v2, ok2)", "vtrue"},
		{"l := om.Notes()\nvar y = l.Rows[0].ID\ny", "a1"},
		{"l := om.Notes()\nvar z string = l.Rows[0].ID\nz", "a1"},
		{"l := om.Notes()\na, b := l.Rows[0].ID, l.Rows[0].Content\na + b", "a1x"},
		{"var anyv any = om.Notes()\nnl, ok3 := anyv.(om.NoteList)\nfmt.Sprint(nl.Len(), ok3)", "1 true"},
		{"ids := om.Notes().IDs()\nfirst := ids[0]\nfirst", "a1"},
	} {
		r := runCode(t, ctx, s, c.code, h)
		if r.Outcome != OutcomeOK || r.Val != c.val {
			t.Errorf("%q: %s %q %q", c.code, r.Outcome, r.Val, r.Err)
		}
	}
	// The right-hand side runs once.
	calls := 0
	h2 := notesFake()
	inner := h2["Notes"]
	h2["Notes"] = func(ctx context.Context, args []json.RawMessage) (any, error) { calls++; return inner(ctx, args) }
	if r := runCode(t, ctx, s, "q := om.Notes().Rows[0]\nq.ID", h2); r.Val != "a1" || calls != 1 {
		t.Fatalf("%+v, %d calls", r, calls)
	}
}

// A call the handler finds undecodable (omwire.ErrBadArgs: the stubs never send one, so the
// child is corrupted) kills the child and the call returns crashed; the next call starts anew.
func TestBadArgumentsKillTheChild(t *testing.T) {
	s := newSup(t, Config{})
	h := fake{omwire.MNotes: func(context.Context, []json.RawMessage) (any, error) {
		return nil, fmt.Errorf("Notes: %w", omwire.ErrBadArgs)
	}}
	ok(t, runCode(t, context.Background(), s, "1", nil))
	pid := livePID(s)
	r := runCode(t, context.Background(), s, "om.Notes()", h)
	if r.Outcome != OutcomeCrashed || !strings.Contains(r.Err, "do not decode") || !gone(pid) {
		t.Fatalf("%+v", r)
	}
	if r := runCode(t, context.Background(), s, "2", nil); r.Val != "2" {
		t.Fatalf("%+v", r)
	}
}
