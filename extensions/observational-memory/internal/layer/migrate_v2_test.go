package layer

import (
	"strings"
	"testing"
)

// Memory v2's migration folds the observer's and reflector's additions into the consolidation
// instructions after any addition already there, retires a replace-mode text and the dropper's,
// keeps every v1 file, marks each content once (an edit under v1 is migrated again), and waits
// while the consolidation instructions are held.
func TestMigrateToV2(t *testing.T) {
	s := newStore(t, map[string]string{SeedKey(KindInstructions, "consolidation"): "seed"})
	mustPut(t, s, instr("consolidation", "Keep versions whole."))
	mustPut(t, s, instr("observer", "Note every port."))
	mustPut(t, s, instr("reflector", "Mode: replace\nOnly deploys."))
	mustPut(t, s, instr("dropper", "Never drop a port."))
	extend, retire := []string{"observer", "reflector"}, []string{"dropper"}

	got, err := s.MigrateToV2(model, "consolidation", extend, retire)
	if err != nil || len(got) != 3 || !got[0].Folded || got[0].Name != "observer" || got[1].Name != "reflector" || got[2].Name != "dropper" || got[1].Folded || got[2].Folded {
		t.Fatalf("migrations %+v (%v)", got, err)
	}
	c, err := s.Get(model, KindInstructions, "consolidation")
	if err != nil || c.Text != "Keep versions whole.\n\n"+block("observer", "Note every port.") || c.Mode != ModeExtend {
		t.Fatalf("consolidation %+v (%v)", c, err)
	}
	for _, name := range []string{"observer", "reflector", "dropper"} {
		if _, err := s.Get(model, KindInstructions, name); err != nil {
			t.Fatalf("%s removed: %v", name, err)
		}
	}
	if !strings.Contains(got[1].Message(), "retires") || !strings.Contains(got[0].Message(), "folded") {
		t.Fatalf("messages %q / %q", got[0].Message(), got[1].Message())
	}
	if again, _ := s.MigrateToV2(model, "consolidation", extend, retire); len(again) != 0 {
		t.Fatalf("migrated twice: %+v", again)
	}
	gens, _ := s.Generations(model)
	if last := gens[len(gens)-1]; last.Op != OpMigrateV2 || !strings.Contains(last.Describe(), "memory v2 migration of instructions/dropper") {
		t.Fatalf("last generation %+v", last)
	}

	// An edit under v1 is migrated again, but not while the consolidation instructions are held,
	// and then replaces the observer's block: one block, the new text, after the addition there.
	mustPut(t, s, instr("observer", "Note every port and host."))
	held, err := s.Hold(Definition{Kind: KindInstructions, Name: "consolidation", Model: model, Text: "Held addition."})
	if err != nil {
		t.Fatal(err)
	}
	if waiting, _ := s.MigrateToV2(model, "consolidation", extend, retire); len(waiting) != 0 {
		t.Fatalf("folded under a held definition: %+v", waiting)
	}
	if _, err := s.ClaimHeld([]Definition{held}, false, "not proved"); err != nil {
		t.Fatal(err)
	}
	if later, _ := s.MigrateToV2(model, "consolidation", extend, retire); len(later) != 1 || !later[0].Folded {
		t.Fatalf("edit not migrated: %+v", later)
	}
	if c, _ := s.Get(model, KindInstructions, "consolidation"); c.Text != "Keep versions whole.\n\n"+block("observer", "Note every port and host.") {
		t.Fatalf("consolidation %q", c.Text)
	}

	// A v1 forget of the source removes its block, once, and leaves the rest.
	if _, _, err := s.Forget(model, KindInstructions, "observer", ""); err != nil {
		t.Fatal(err)
	}
	removed, _ := s.MigrateToV2(model, "consolidation", extend, retire)
	if len(removed) != 1 || !removed[0].Removed || removed[0].Name != "observer" || !strings.Contains(removed[0].Message(), "removed the observer block") {
		t.Fatalf("removals %+v", removed)
	}
	if c, _ := s.Get(model, KindInstructions, "consolidation"); c.Text != "Keep versions whole." {
		t.Fatalf("consolidation %q", c.Text)
	}
	if again, _ := s.MigrateToV2(model, "consolidation", extend, retire); len(again) != 0 {
		t.Fatalf("removed twice: %+v", again)
	}
}

// block is a v1 stage's folded block.
func block(name, text string) string { return foldHeader(name) + "\n" + text + "\n" + foldFooter(name) }

// A model's own consolidation instructions replace the all-models ones entirely, so a model's
// fold carries what v1 had it inherit: with a reflector addition kept for all models and the
// model's own observer addition, the model's consolidation holds both, the all-models one only
// the reflector's; a model with nothing of its own writes none and keeps inheriting.
func TestMigrateToV2KeepsInheritedAdditions(t *testing.T) {
	s := newStore(t, map[string]string{SeedKey(KindInstructions, "consolidation"): "seed"})
	all := func(name, text string) Definition {
		d := instr(name, text)
		d.Model = AllModels
		return d
	}
	mustPut(t, s, all("consolidation", "Keep versions whole."))
	mustPut(t, s, all("reflector", "Reflect on deploys."))
	mustPut(t, s, instr("observer", "Note every port."))
	extend, retire := []string{"observer", "reflector"}, []string{"dropper"}
	if got, err := s.MigrateToV2(AllModels, "consolidation", extend, retire); err != nil || len(got) != 1 || got[0].Name != "reflector" {
		t.Fatalf("all models %+v (%v)", got, err)
	}
	got, err := s.MigrateToV2(model, "consolidation", extend, retire)
	if err != nil || len(got) != 2 || got[0].Name != "observer" || got[0].From != model || got[1].Name != "reflector" || got[1].From != AllModels || !strings.Contains(got[1].Message(), "kept for all models") {
		t.Fatalf("model %+v (%v)", got, err)
	}
	if c, _ := s.Get(AllModels, KindInstructions, "consolidation"); c.Text != "Keep versions whole.\n\n"+block("reflector", "Reflect on deploys.") {
		t.Fatalf("all-models consolidation %q", c.Text)
	}
	h, _, _ := s.Load(model)
	effective, ok := h.Get(KindInstructions, "consolidation")
	if !ok || effective.Model != model || effective.Text != "Keep versions whole.\n\n"+block("reflector", "Reflect on deploys.")+"\n\n"+block("observer", "Note every port.") {
		t.Fatalf("the model's effective consolidation %+v", effective)
	}
	if again, _ := s.MigrateToV2(model, "consolidation", extend, retire); len(again) != 0 {
		t.Fatalf("migrated twice: %+v", again)
	}

	// A later all-models edit reaches the model's own consolidation too, replacing its block.
	mustPut(t, s, all("reflector", "Reflect on deploys and releases."))
	s.MigrateToV2(AllModels, "consolidation", extend, retire)
	if again, _ := s.MigrateToV2(model, "consolidation", extend, retire); len(again) != 1 || again[0].Name != "reflector" {
		t.Fatalf("edit kept for all models not migrated: %+v", again)
	}
	if c, _ := s.Get(model, KindInstructions, "consolidation"); strings.Count(c.Text, foldHeader("reflector")) != 1 || !strings.Contains(c.Text, "Reflect on deploys and releases.") {
		t.Fatalf("consolidation %q", c.Text)
	}

	// A model with nothing of its own writes no consolidation of its own.
	other := "p/other"
	if got, _ := s.MigrateToV2(other, "consolidation", extend, retire); len(got) != 0 {
		t.Fatalf("a model with nothing of its own migrated %+v", got)
	}
	if _, err := s.Get(other, KindInstructions, "consolidation"); err == nil {
		t.Fatal("a model with nothing of its own got a consolidation of its own")
	}
}
