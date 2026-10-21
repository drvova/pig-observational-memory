package agents_test

import (
	"context"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// The dropper honours the turn cap, and a failed turn ends the run without
// counting as a turn (upstream issue #82, kept in the port's own loop).
func TestDropperTurnCap(t *testing.T) {
	rec := &tf.StreamRecorder{Replies: []map[string]any{dropCall("aaaaaaaaaaaa"), dropCall("bbbbbbbbbbbb"), dropCall("cccccccccccc"), tf.AssistantText("done")}}
	args := dropperArgs(rec, tf.Model("p", "m"))
	args.MaxTurns = 2
	if _, err := agents.RunDropper(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if rec.Calls() != 2 {
		t.Fatalf("turn cap ignored: %d calls", rec.Calls())
	}

	rec = &tf.StreamRecorder{Replies: []map[string]any{tf.AssistantError("error", "boom"), dropCall("aaaaaaaaaaaa")}}
	args = dropperArgs(rec, tf.Model("p", "m"))
	args.MaxTurns = 5
	_, _ = agents.RunDropper(context.Background(), args)
	if rec.Calls() != 1 {
		t.Fatalf("loop continued after a failed turn: %d calls", rec.Calls())
	}
}
