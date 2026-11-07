package omhost_test

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/om/omwire"
	"github.com/drvova/pig-observational-memory/memory/omhost"
)

// om.Correct records a correction write, never an edit: quoted by the user's words it is marked
// so level 0 makes it a supersession; quoting words no user wrote, or nothing, it is a possible
// update; an unknown record or empty text is refused.
func TestCorrectThroughTheQuoteGate(t *testing.T) {
	c := &omhost.Call{Entries: fixture(), Record: &omhost.Record{}}
	quote := "I bake sourdough every week with rye flour"
	if s := must[string](t, c, omwire.MCorrect, obsBake, "User bakes sourdough every two weeks.", omwire.SaveOptions{Quoting: &quote}); !strings.Contains(s, "supersedes it") {
		t.Fatal(s)
	}
	forged := "the user said to bake every day now"
	if s := must[string](t, c, omwire.MCorrect, obsBake, "User bakes daily.", omwire.SaveOptions{Quoting: &forged}); !strings.Contains(s, "not a user's words") {
		t.Fatal(s)
	}
	if s := must[string](t, c, omwire.MCorrect, obsBake, "User bakes monthly.", omwire.SaveOptions{}); !strings.Contains(s, "no user's words were quoted") {
		t.Fatal(s)
	}
	if _, err := call(t, c, omwire.MCorrect, "cccccccccccc", "x", omwire.SaveOptions{}); err == nil {
		t.Fatal("an unknown record was corrected")
	}
	if _, err := call(t, c, omwire.MCorrect, obsBake, "  ", omwire.SaveOptions{}); err == nil {
		t.Fatal("empty text was accepted")
	}
	got := c.Record.Snapshot().Writes
	if len(got) != 3 || got[0].Op != omhost.WriteCorrect || got[0].Type != omhost.CorrectQuoted || got[1].Type != "" || got[2].Type != "" ||
		got[0].Name != obsBake || got[0].Description != "User bakes sourdough every two weeks." {
		t.Fatalf("%+v", got)
	}
}
