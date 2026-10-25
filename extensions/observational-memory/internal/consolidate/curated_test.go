package consolidate_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/consolidate"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// citationInput shows a record of the user's words (its source a user message) and one of the
// assistant's, and lists two curated memories.
func citationInput() (consolidate.Input, ledger.Observation, ledger.Observation) {
	user := record("d10000000001", "Always deploy with make deploy", func(o *ledger.Observation) { o.SourceEntryIDs = []string{"u1"} })
	assistant := record("d10000000002", "Deployed with make deploy", func(o *ledger.Observation) { o.SourceEntryIDs = []string{"a1"} })
	in := inputOf([]ledger.Observation{user, assistant}, nil, nil)
	in.Branch = []ledger.Entry{
		testfix.RawMessage("u1", "Always deploy with make deploy", nil),
		testfix.MessageEntry("a1", map[string]any{"role": "assistant", "content": "Deployed with make deploy"}, nil),
	}
	in.Curated = []consolidate.CuratedMemory{{Name: "deploy-with-make", Type: "feedback", Description: "Deploy with make deploy.", Staged: true}, {Name: "tabs", Type: "user", Description: "Tabs."}}
	return in, user, assistant
}

func cite(name string, ids ...string) map[string]any {
	list := make([]any, len(ids))
	for i, id := range ids {
		list[i] = id
	}
	return map[string]any{"name": name, "supportingIds": list}
}

// Level 1 cites a listed curated memory with shown records, at least one of the user's own
// words; an unlisted name, a foreign id, support only from the assistant, or a second citation
// of one memory is refused. A citation alone accounts for the output.
func TestValidateCuratedCitations(t *testing.T) {
	in, user, assistant := citationInput()
	result := consolidate.Validate(args(map[string]any{"curated": []any{cite("deploy-with-make", user.ID, assistant.ID)}}), in)
	if result.Failure != "" || len(result.Cited) != 1 || result.Cited[0].Name != "deploy-with-make" || len(result.Records) != 0 {
		t.Fatalf("citation %+v", result)
	}
	for _, c := range []struct {
		name   string
		item   map[string]any
		reason string
	}{
		{"unlisted memory", cite("other", user.ID), consolidate.ReasonNotCurated},
		{"foreign id", cite("tabs", "999999999999"), consolidate.ReasonForeignID},
		{"no ids", cite("tabs"), consolidate.ReasonForeignID},
		{"assistant support only", cite("tabs", assistant.ID), consolidate.ReasonNoUserSupport},
	} {
		result := consolidate.Validate(args(map[string]any{"curated": []any{c.item}}), in)
		if result.Failure != consolidate.FailureNoneValid || len(result.Refusals) != 1 || result.Refusals[0].Reason != c.reason || result.Refusals[0].What != "curated" {
			t.Errorf("%s: %+v", c.name, result)
		}
	}
	twice := consolidate.Validate(args(map[string]any{"curated": []any{cite("tabs", user.ID), cite("tabs", user.ID)}}), in)
	if len(twice.Cited) != 1 || len(twice.Refusals) != 1 || twice.Refusals[0].Reason != consolidate.ReasonUsed {
		t.Fatalf("a second citation %+v", twice)
	}
	// A model-written record from a user message is not the user's own words.
	in.Candidates[0].Origin = ledger.OriginModel
	if result := consolidate.Validate(args(map[string]any{"curated": []any{cite("tabs", user.ID)}}), in); len(result.Cited) != 0 {
		t.Fatalf("a model record supported a citation: %+v", result)
	}
}

// The level-1 input lists the curated memories after the subjects, each while room remains,
// staged ones marked, and says how many it left out; every prompt fits its room.
func TestBuildPromptListsCuratedMemories(t *testing.T) {
	_, in := promptBranch(6, 2)
	for i := range 30 {
		in.Curated = append(in.Curated, consolidate.CuratedMemory{Name: fmt.Sprintf("memory-%02d", i), Type: "lesson", Description: "A lesson about the deploy step " + fmt.Sprint(i) + ".", Staged: i%2 == 0})
	}
	full, ok := consolidate.BuildPrompt(in)
	if !ok || len(full.Curated) != 30 || full.OmittedCurated != 0 || !strings.Contains(full.Text, "- memory-00 [lesson] (staged): ") || !strings.Contains(full.Text, "- memory-01 [lesson]: ") {
		t.Fatalf("full prompt curated %d omitted %d", len(full.Curated), full.OmittedCurated)
	}
	if subjects, curated := strings.Index(full.Text, "CURRENT SUBJECTS:"), strings.Index(full.Text, "CURATED MEMORY"); subjects < 0 || curated < subjects {
		t.Fatal("curated memories follow the subjects")
	}
	sawOmitted := false
	for room := full.Tokens; room > 0; room -= 13 {
		in.Available = room
		p, ok := consolidate.BuildPrompt(in)
		if !ok {
			continue
		}
		if p.Tokens > room {
			t.Fatalf("room %d: prompt of %d tokens", room, p.Tokens)
		}
		if len(p.Curated)+p.OmittedCurated != 30 {
			t.Fatalf("room %d: %d listed + %d omitted", room, len(p.Curated), p.OmittedCurated)
		}
		if p.OmittedCurated > 0 {
			sawOmitted = true
			if !strings.Contains(p.Text, fmt.Sprintf("omitted: %d more curated memories", p.OmittedCurated)) {
				t.Fatalf("room %d: omitted curated memories not announced", room)
			}
		}
	}
	if !sawOmitted {
		t.Fatal("the sweep never left a curated memory out")
	}
}
