package layer

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// ClaimHeld settles only what the pass ran with: a definition held again after the pass read
// the layer waits for a pass of its own.
func TestClaimHeldSettlesOnlyWhatThePassRan(t *testing.T) {
	s := newStore(t, nil)
	ran, err := s.Hold(Definition{Kind: KindInstructions, Name: "observer", Model: model, Text: "first try"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.Hold(Definition{Kind: KindInstructions, Name: "dropper", Model: model, Text: "dropper try"})
	if err != nil {
		t.Fatal(err)
	}
	// The observer's definition changes after the pass read it.
	if _, err := s.Hold(Definition{Kind: KindInstructions, Name: "observer", Model: model, Text: "second try"}); err != nil {
		t.Fatal(err)
	}
	notices, err := s.ClaimHeld([]Definition{ran, other}, true, "")
	if err != nil || len(notices) != 1 || notices[0].Name != "dropper" || !notices[0].Written {
		t.Fatalf("notices %+v %v", notices, err)
	}
	if held, _ := s.Held(model); len(held) != 1 || held[0].Text != "second try" {
		t.Fatalf("held %+v", held)
	}
}

// A build or revision pass is marked without touching a file; probation's verdict is recorded:
// established, a generation; retired, what stood before the last put comes back.
func TestMarkAndSettle(t *testing.T) {
	s := newStore(t, map[string]string{SeedKey(KindInstructions, "observer"): "the seed"})
	if _, err := s.Mark(model, Generation{Op: OpPut}); err == nil {
		t.Fatal("Mark wrote a put")
	}
	if _, err := s.Mark(model, Generation{Op: OpBuild, Evidence: "abc", Events: 4}); err != nil {
		t.Fatal(err)
	}
	if last, ok, _ := s.LastPass(model); !ok || last.Evidence != "abc" || last.Events != 4 {
		t.Fatalf("last pass %+v", last)
	}
	if _, err := s.Put(Definition{Kind: KindInstructions, Name: "observer", Model: model, Text: "mine one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(Definition{Kind: KindInstructions, Name: "observer", Model: model, Text: "mine two"}); err != nil {
		t.Fatal(err)
	}
	undecided, _ := s.Undecided(model)
	if len(undecided) != 1 || undecided[0].Put.N != 3 {
		t.Fatalf("undecided %+v", undecided)
	}
	g, err := s.Settle(model, KindInstructions, "observer", false, "rose")
	if err != nil || g.Op != OpRevert {
		t.Fatalf("settle %+v %v", g, err)
	}
	if d, _ := s.Get(model, KindInstructions, "observer"); d.Text != "mine one" {
		t.Fatalf("after retiring, %q stands", d.Text)
	}
	if undecided, _ := s.Undecided(model); len(undecided) != 0 {
		t.Fatalf("undecided after the verdict %+v", undecided)
	}
	// The restored definition is not what any last put wrote now: nothing to settle.
	if _, err := s.Settle(model, KindInstructions, "observer", true, "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("settled a reverted name: %v", err)
	}
	if _, err := s.Put(Definition{Kind: KindInstructions, Name: "observer", Model: model, Text: "mine three"}); err != nil {
		t.Fatal(err)
	}
	if g, err := s.Settle(model, KindInstructions, "observer", true, "did not rise"); err != nil || g.Op != OpEstablish {
		t.Fatalf("establish %+v %v", g, err)
	}
	if d, _ := s.Get(model, KindInstructions, "observer"); d.Text != "mine three" {
		t.Fatalf("established %q", d.Text)
	}
	// A rollback over these generations still replays: the marks change no file.
	if _, _, err := s.Rollback(model, 1, "s"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(model, KindInstructions, "observer"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("after rolling back to the build mark: %v", err)
	}
}

// A build or revision pass holds its definitions and appends its mark together: a refused
// definition is skipped with its error, the rest are held, and only a pass mark is accepted.
func TestHoldPassHoldsAndMarksTogether(t *testing.T) {
	s := newStore(t, nil)
	if _, _, _, err := s.HoldPass(model, nil, Generation{Op: OpPut}); err == nil {
		t.Fatal("HoldPass marked a put")
	}
	defs := []Definition{
		{Kind: KindInstructions, Name: "observer", Model: model, Text: "mine"},
		{Kind: KindInstructions, Name: "dropper", Model: "other/model", Text: "not this pass's"},
		{Kind: KindInstructions, Name: "reflector", Model: model, Text: "mine too"},
	}
	held, refused, gen, err := s.HoldPass(model, defs, Generation{Op: OpBuild, Evidence: "e"})
	if err != nil || len(held) != 2 || refused[0] != nil || refused[1] == nil || refused[2] != nil || gen.Op != OpBuild {
		t.Fatalf("held %+v refused %v gen %+v %v", held, refused, gen, err)
	}
	if last, ok, _ := s.LastPass(model); !ok || last.N != gen.N {
		t.Fatalf("last pass %+v", last)
	}
	if h, _ := s.Held(model); len(h) != 2 {
		t.Fatalf("held on disk %+v", h)
	}
}

// A hold is its engine's: a store loads, reports and settles only the held definitions marked
// with its engine (Store.Engine), a v1 hold's file is what it was before engines (no engine
// column), a written definition carries none, and a newer hold of a name the other engine held
// replaces it with the original fallback.
func TestHeldDefinitionsAreTheirEngines(t *testing.T) {
	v1 := newStore(t, nil)
	v2 := v1
	v2.Engine = "v2"
	if _, err := v1.Hold(instr("observer", "v1 text")); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(v1.heldPath(model, KindInstructions, "observer")); err != nil || strings.Contains(string(data), engineField) {
		t.Fatalf("a v1 held file carries an engine column (%v):\n%s", err, data)
	}
	held, err := v2.Hold(instr("consolidation", "v2 text"))
	if err != nil || held.Engine != "v2" {
		t.Fatalf("v2 hold %+v (%v)", held, err)
	}
	for _, c := range []struct {
		store Store
		want  string
	}{{v1, "observer"}, {v2, "consolidation"}} {
		got, err := c.store.Held(model)
		if err != nil || len(got) != 1 || got[0].Name != c.want {
			t.Fatalf("engine %q holds %+v (%v), want only %s", c.store.Engine, got, err, c.want)
		}
		h, reports, err := c.store.Load(model)
		if err != nil || len(reports) != 0 {
			t.Fatalf("load: %v %+v", err, reports)
		}
		if n := len(h.List(KindInstructions)); n != 1 {
			t.Fatalf("engine %q loads %d instructions, want its own hold only", c.store.Engine, n)
		}
	}
	notices, err := v1.Claim(model, true, "")
	if err != nil || len(notices) != 1 || notices[0].Name != "observer" {
		t.Fatalf("v1's Claim settled %+v (%v), want only its own", notices, err)
	}
	if n, err := v1.ClaimHeld([]Definition{held}, true, ""); err != nil || len(n) != 0 {
		t.Fatalf("v1's ClaimHeld settled a v2 hold: %+v (%v)", n, err)
	}
	if n, err := v2.ClaimHeld([]Definition{held}, true, ""); err != nil || len(n) != 1 || !n[0].Written {
		t.Fatalf("v2's ClaimHeld: %+v (%v)", n, err)
	}
	written, err := v1.Get(model, KindInstructions, "consolidation")
	if err != nil || written.Engine != "" || written.Text != "v2 text" {
		t.Fatalf("written %+v (%v): a written definition is shared and carries no engine", written, err)
	}
	if _, err := v1.Hold(instr("reflector", "v1 hold")); err != nil {
		t.Fatal(err)
	}
	if _, err := v2.Hold(instr("reflector", "v2 hold")); err != nil {
		t.Fatal(err)
	}
	if got, _ := v1.Held(model); len(got) != 0 {
		t.Fatalf("a v2 hold of a name v1 held did not replace it: %+v", got)
	}
}
