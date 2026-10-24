package agents_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// The build and revision passes' prompt explains the two instructions modes: an addition,
// appended after the seed, is the default; a replacement must say "mode: replace", and the
// kernel's rules (the note fields among them) stand in either mode. A proposal keeps its mode
// line for the layer to lift.
func TestBuilderPromptExplainsInstructionsModes(t *testing.T) {
	text := "mode: replace\nRecord only what the user states."
	rec := &tf.StreamRecorder{Replies: []map[string]any{
		tf.AssistantToolCall("tool-1", "define_harness", map[string]any{"definitions": []any{map[string]any{"kind": "instructions", "name": "observer", "text": text}}}),
		tf.AssistantText("done"),
	}}
	key := "test"
	result, err := agents.RunBuilder(context.Background(), agents.BuilderArgs{
		WorkerArgs: agents.WorkerArgs{Model: tf.Model("p", "m"), APIKey: &key, Stream: rec.Stream},
		ModelKey:   "p/m",
		Seeds:      []agents.BuilderDefinition{{Kind: "instructions", Name: "observer", Text: agents.ObserverInstructions, State: "seed"}},
	})
	if err != nil || len(result.Definitions) != 1 || result.Definitions[0].Text != text {
		t.Fatalf("result %+v err %v", result, err)
	}
	system, _ := rec.Requests[0]["systemPrompt"].(string)
	if !strings.HasPrefix(system, strings.TrimRight(kernel.Text(kernel.TextBuilderRules), "\n")) {
		t.Fatal("the builder prompt does not open with the kernel's builder rules")
	}
	for _, want := range []string{
		"Instructions have two modes",
		"An addition (the default): the seed instructions stay and your text is appended after them",
		"kernel rules, then the seed, then your additions",
		`open the text with the line "mode: replace"`,
		"never the kernel rules'",
		"kept on probation",
		"the observer's note fields (kind, subject, scope, supersedes, rows)",
	} {
		if !strings.Contains(system, want) {
			t.Errorf("the builder rules lack %q", want)
		}
	}
	user := fmt.Sprint(rec.Requests[0]["messages"])
	if !strings.Contains(user, `By default an addition, appended after the seed instructions; a text opening with the line "mode: replace" replaces the seed instructions instead`) {
		t.Errorf("the definition kinds do not explain the modes:\n%s", user)
	}
}
