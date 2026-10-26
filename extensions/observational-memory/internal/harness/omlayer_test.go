package harness_test

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
)

// The code tool's layer access forgets and rolls back the session model's layer only, never a
// rule, its result names the model, and its listing is every model's layer labelled with its
// model, the same under any session model.
func TestOMLayer(t *testing.T) {
	h, store, _ := session(t)
	ls := store.Layer()
	for _, d := range []layer.Definition{
		{Kind: layer.KindInstructions, Name: "consolidation", Model: "p/m", Text: "Merge one deploy target per line, with its port and its date."},
		{Kind: layer.KindInstructions, Name: "consolidation", Model: "p/m", Text: "Merge one deploy target per line, and name the scope that tells two apart."},
		{Kind: layer.KindInstructions, Name: "consolidation", Model: "q/n", Text: "Keep the user's exact figures in every merged record."},
	} {
		if _, err := ls.Put(d); err != nil {
			t.Fatal(err)
		}
	}
	access := harness.OMLayer(h, store, "p/m")
	rows, err := access.Listing()
	if err != nil {
		t.Fatal(err)
	}
	if other, _ := harness.OMLayer(h, store, "q/n").Listing(); len(other) != len(rows) {
		t.Fatal("the listing depends on the session model")
	}
	models := map[string]int{}
	for _, r := range rows {
		models[r.Model]++
		if r.Model != "" && r.State == layer.StateSeed {
			t.Fatalf("a seed labelled with a model: %+v", r)
		}
	}
	if models[""] == 0 || models["p/m"] != 1 || models["q/n"] != 1 {
		t.Fatalf("models %v", models)
	}
	var qn om.LayerRow
	for _, r := range rows {
		if r.Model == "q/n" {
			qn = r
		}
	}
	if qn.Text != "Keep the user's exact figures in every merged record." {
		t.Fatalf("row %+v", qn)
	}
	s, err := access.Rollback(1)
	if err != nil || !strings.Contains(s, "p/m's harness rolled back to generation 1") {
		t.Fatalf("%s %v", s, err)
	}
	if _, err := access.Forget(layer.KindRemedy, "protect-something"); err == nil || !strings.Contains(err.Error(), "is a rule") {
		t.Fatalf("a rule was forgotten: %v", err)
	}
	if _, err := access.Forget(layer.KindTool, "bakes"); err == nil || !strings.Contains(err.Error(), "retired") {
		t.Fatalf("a tool: %v", err)
	}
	s, err = access.Forget(layer.KindInstructions, "consolidation")
	if err != nil || !strings.Contains(s, "for p/m") {
		t.Fatalf("%s %v", s, err)
	}
	if _, err := ls.Get("q/n", layer.KindInstructions, "consolidation"); err != nil {
		t.Fatalf("another model's layer changed: %v", err)
	}
	if _, err := harness.OMLayer(h, store, "").Rollback(1); err == nil {
		t.Fatal("a rollback with no session model")
	}
}

// om.LayerRollback(gen) makes the layer what it was at generation gen, as its manual line and
// its result say: with three generations, 1 restores the first text (undoing two changes), and 0
// is before the first change.
func TestOMLayerRollbackIsToAGeneration(t *testing.T) {
	h, store, _ := session(t)
	ls := store.Layer()
	texts := []string{
		"Merge one deploy target per line, with its port and its date.",
		"Merge one deploy target per line, and name the scope that tells two apart.",
		"Merge one deploy target per line, and keep the user's exact figures.",
	}
	for _, text := range texts {
		if _, err := ls.Put(layer.Definition{Kind: layer.KindInstructions, Name: "consolidation", Model: "p/m", Text: text}); err != nil {
			t.Fatal(err)
		}
	}
	access := harness.OMLayer(h, store, "p/m")
	s, err := access.Rollback(1)
	if err != nil || !strings.Contains(s, "rolled back to generation 1") {
		t.Fatalf("%s %v", s, err)
	}
	if d, err := ls.Get("p/m", layer.KindInstructions, "consolidation"); err != nil || d.Text != texts[0] {
		t.Fatalf("after rollback to generation 1: %+v %v", d, err)
	}
	if s, err := access.Rollback(0); err != nil || !strings.Contains(s, "rolled back to generation 0") {
		t.Fatalf("%s %v", s, err)
	}
	if d, err := ls.Get("p/m", layer.KindInstructions, "consolidation"); err == nil && d.Model == "p/m" && strings.Contains(d.Text, "deploy target") {
		t.Fatalf("generation 0 still holds the model's definition: %+v", d)
	}
}
