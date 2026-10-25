package consolidate_test

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/consolidate"
)

// offer is a definition offer like the hook's.
func offer() *consolidate.DefinitionOffer {
	return &consolidate.DefinitionOffer{
		Kinds:     []string{"format", "instructions", "detector", "remedy"},
		Detectors: []string{"uncited-assistant-specifics"},
		Remedies:  []string{"keep-head"},
		Evidence:  "Counted over your last 3 compactions (900 source tokens level 0 read):\n- level1-union-kept: 2\n",
	}
}

// Definitions are in the tool only with an offer; without one, validation refuses every
// definition and rule, and with one it keeps the structurally valid ones and refuses the rest.
func TestValidateDefinitionsOnlyWhenOffered(t *testing.T) {
	if _, ok := consolidate.Tool().Parameters["properties"].(map[string]any)["definitions"]; ok {
		t.Fatal("the tool offers definitions without an offer")
	}
	props := consolidate.ToolFor(offer()).Parameters["properties"].(map[string]any)
	if _, ok := props["definitions"]; !ok {
		t.Fatal("the offered tool has no definitions")
	}
	if _, ok := props["rules"]; !ok {
		t.Fatal("the offered tool has no rules")
	}

	output := args(map[string]any{
		"definitions": []any{
			map[string]any{"kind": "instructions", "name": "consolidation", "text": "Prefer merging duplicate deploy notes."},
			map[string]any{"kind": "summary", "name": "default", "text": "notes"},
			map[string]any{"kind": "instructions", "name": "consolidation", "text": "Again."},
			map[string]any{"kind": "format", "name": "pipes", "text": "  "},
		},
		"rules": []any{
			map[string]any{"detector": "uncited-assistant-specifics", "remedy": "keep-head"},
			map[string]any{"detector": "uncited-assistant-specifics", "remedy": "require-kind"},
		},
	})
	in := fixtureInput()
	refused := consolidate.Validate(output, in)
	if len(refused.Definitions)+len(refused.Rules) != 0 || refused.Failure != consolidate.FailureNoneValid {
		t.Fatalf("without an offer: %+v", refused)
	}
	for _, r := range refused.Refusals {
		if r.Reason != consolidate.ReasonNotOffered {
			t.Fatalf("refusal %+v, want %q", r, consolidate.ReasonNotOffered)
		}
	}

	in.Offer = offer()
	result := consolidate.Validate(output, in)
	if result.Failure != "" || len(result.Definitions) != 1 || result.Definitions[0].Name != "consolidation" || len(result.Rules) != 1 || result.Rules[0].Remedy != "keep-head" {
		t.Fatalf("with an offer: %+v", result)
	}
	reasons := map[string]int{}
	for _, r := range result.Refusals {
		reasons[r.Reason]++
	}
	if reasons[consolidate.ReasonNotOffered] != 2 || reasons[consolidate.ReasonDefinedTwice] != 1 || reasons[consolidate.ReasonEmpty] != 1 {
		t.Fatalf("refusals %+v", result.Refusals)
	}
	if result.Accounted() != 2 {
		t.Fatalf("accounted %d", result.Accounted())
	}
}

// The harness evidence follows the curated memories and comes before PiG's instructions, while
// room remains; when it does not fit, a line says so and nothing may be defined.
func TestBuildPromptShowsHarnessEvidence(t *testing.T) {
	_, in := promptBranch(6, 2)
	in.Instructions = "Keep the deploy notes."
	in.Harness = offer()
	full, ok := consolidate.BuildPrompt(in)
	if !ok || !full.HarnessShown || !strings.Contains(full.Text, "level1-union-kept: 2") {
		t.Fatalf("harness shown %v:\n%s", full.HarnessShown, full.Text)
	}
	harness, instructions := strings.Index(full.Text, "HARNESS EVIDENCE"), strings.Index(full.Text, "INSTRUCTIONS FOR THIS COMPACTION")
	if harness < 0 || instructions < harness {
		t.Fatal("the harness evidence does not come before the instructions")
	}
	sawHidden := false
	for room := full.Tokens; room > 0; room -= 7 {
		in.Available = room
		p, ok := consolidate.BuildPrompt(in)
		if !ok {
			continue
		}
		if p.Tokens > room {
			t.Fatalf("room %d: prompt of %d tokens", room, p.Tokens)
		}
		if !strings.Contains(p.Text, "HARNESS EVIDENCE") {
			t.Fatalf("room %d: the harness evidence is left out silently", room)
		}
		if !p.HarnessShown {
			sawHidden = true
			if !strings.Contains(p.Text, "define nothing this time") {
				t.Fatalf("room %d: hidden evidence not announced", room)
			}
		}
	}
	if !sawHidden {
		t.Fatal("the sweep never left the harness evidence out")
	}
	in.Harness, in.Available = nil, full.Tokens
	if p, _ := consolidate.BuildPrompt(in); strings.Contains(p.Text, "HARNESS EVIDENCE") || p.HarnessShown {
		t.Fatal("harness evidence without an offer")
	}
}
