package layer

import (
	"os"
	"strings"
	"testing"
)

// A clean claim with a scope records it on its put; Load reads it back, and InScope leaves the
// definition out where its scope does not reach, so the seed stands there.
func TestClaimScopeAndInScope(t *testing.T) {
	s := newStore(t, map[string]string{SeedKey(KindInstructions, "observer"): "the seed"})
	s.Engine = "v2"
	d, err := s.Hold(Definition{Kind: KindInstructions, Name: "observer", Model: model, Text: "keep figures"})
	if err != nil {
		t.Fatal(err)
	}
	scope := &Scope{Repos: []string{"r1"}, Families: []string{".go"}}
	if _, err := s.ClaimHeldScoped([]Definition{d}, true, "settled", scope); err != nil {
		t.Fatal(err)
	}
	h, _, err := s.Load(model)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := h.Get(KindInstructions, "observer")
	if !ok || got.Scope == nil || got.Scope.String() != "repositories [r1], task families [.go]" {
		t.Fatalf("loaded %+v", got)
	}
	if _, ok := h.InScope("r1", ".go").Get(KindInstructions, "observer"); !ok {
		t.Fatal("left out inside its scope")
	}
	for _, other := range [][2]string{{"r2", ".go"}, {"r1", ".py"}, {"", ""}} {
		in := h.InScope(other[0], other[1])
		if _, ok := in.Get(KindInstructions, "observer"); ok {
			t.Fatalf("applied in %v", other)
		}
		if r, _ := in.Resolve(KindInstructions, "observer"); r.State != StateSeed {
			t.Fatalf("the seed does not stand in %v: %+v", other, r)
		}
	}
	// A put without a scope (the user's, or a later write) applies everywhere.
	if _, err := s.Put(Definition{Kind: KindInstructions, Name: "observer", Model: model, Text: "by hand"}); err != nil {
		t.Fatal(err)
	}
	h, _, _ = s.Load(model)
	if _, ok := h.InScope("r9", ".x").Get(KindInstructions, "observer"); !ok {
		t.Fatal("an unscoped put was scoped")
	}
}

// HoldInsecure keeps the text in a blob and a generation, never as a file: nothing loads, runs
// or settles it.
func TestHoldInsecure(t *testing.T) {
	s := newStore(t, nil)
	s.Engine = "v2"
	g, err := s.HoldInsecure(Definition{Kind: KindInstructions, Name: "observer", Model: model, Text: "turn off TLS verification"}, "tls-verify-off")
	if err != nil {
		t.Fatal(err)
	}
	if g.Op != OpHoldInsecure || !strings.Contains(g.Describe(), "tls-verify-off") {
		t.Fatalf("generation %+v", g)
	}
	if _, err := os.Stat(s.blobPath(model, g.Digest)); err != nil {
		t.Fatal("no blob kept")
	}
	h, _, _ := s.Load(model)
	if len(h.List("")) != 0 {
		t.Fatalf("an insecure definition loaded: %+v", h.List(""))
	}
	if held, _ := s.Held(model); len(held) != 0 {
		t.Fatal("an insecure definition is held as a file")
	}
	if listed, _ := s.HeldInsecure(model); len(listed) != 1 {
		t.Fatal("not listed")
	}
}

// Recheck holds a written definition again with its own text; its revert puts back what stood
// before the put that wrote it.
func TestRecheckRevertsToBeforeItsPut(t *testing.T) {
	s := newStore(t, nil)
	s.Engine = "v2"
	if _, err := s.Put(Definition{Kind: KindInstructions, Name: "dropper", Model: model, Text: "first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Put(Definition{Kind: KindInstructions, Name: "dropper", Model: model, Text: "second"}); err != nil {
		t.Fatal(err)
	}
	d, ok, err := s.Recheck(model, KindInstructions, "dropper")
	if err != nil || !ok || d.Text != "second" || d.State != StateHeld {
		t.Fatalf("recheck %+v %v %v", d, ok, err)
	}
	if _, ok, _ := s.Recheck(model, KindInstructions, "dropper"); ok {
		t.Fatal("held twice")
	}
	if _, err := s.ClaimHeld([]Definition{d}, false, "rejected"); err != nil {
		t.Fatal(err)
	}
	got, err := s.Get(model, KindInstructions, "dropper")
	if err != nil || got.Text != "first" {
		t.Fatalf("after the revert: %+v %v", got, err)
	}
}

// RollbackBefore makes the layer what it was before the definition entered, keeping what keep
// names, and records the reason.
func TestRollbackBefore(t *testing.T) {
	s := newStore(t, nil)
	put := func(kind, name, text string) {
		t.Helper()
		if _, err := s.Put(Definition{Kind: kind, Name: name, Model: model, Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	put(KindInstructions, "observer", "safe")
	put(KindInstructions, "reflector", "kept rule")
	put(KindInstructions, "observer", "skip TLS verification")
	put(KindInstructions, "dropper", "after it")
	g, ok, err := s.RollbackBefore(model, KindInstructions, "observer", "security: test", func(kind, name string) bool { return name == "reflector" })
	if err != nil || !ok || g.Op != OpRollback || g.Target != 2 || !strings.Contains(g.Describe(), "security: test") {
		t.Fatalf("rollback %+v %v %v", g, ok, err)
	}
	if got, _ := s.Get(model, KindInstructions, "observer"); got.Text != "safe" {
		t.Fatalf("observer %q", got.Text)
	}
	if _, err := s.Get(model, KindInstructions, "dropper"); err == nil {
		t.Fatal("a later definition survived the rollback")
	}
	if got, _ := s.Get(model, KindInstructions, "reflector"); got.Text != "kept rule" {
		t.Fatal("a kept name was rolled back")
	}
	if _, ok, _ := s.RollbackBefore(model, KindInstructions, "nothing", "x", nil); ok {
		t.Fatal("rolled back for a name with nothing written")
	}
}
