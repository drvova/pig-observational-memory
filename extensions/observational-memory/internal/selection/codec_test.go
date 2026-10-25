package selection_test

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/selection"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// The summary honours the session model's codec as v1's tables summary does: the native rows
// codec (or none) renders exactly as before, and another codec encodes the tables and opens
// with instructions naming it, still read as a tables summary (ledger.TablesSummaryBody),
// within the same budget.
func TestRenderHonoursTheProfileCodec(t *testing.T) {
	entries := branch(30, func(i int, o *ledger.Observation) bool { return true })
	in := input(entries, tokens.EstimateStringTokens(selection.TablesInstructions)+300)
	native := selection.Render(in)
	in.Codec = tables.Rows
	if rows := selection.Render(in); rows.Text != native.Text {
		t.Fatalf("the rows codec changed the summary:\n%s\nvs\n%s", rows.Text, native.Text)
	}
	if !strings.HasPrefix(native.Text, selection.TablesInstructions) || selection.TablesInstructionsFor(nil) != selection.TablesInstructions {
		t.Fatal("the native instructions changed")
	}
	in.Codec = tables.JSONLines
	jsonl := selection.Render(in)
	if !strings.HasPrefix(jsonl.Text, ledger.TablesSummaryIntro+" in the jsonl format.") || tokens.EstimateStringTokens(jsonl.Text) > in.Budget {
		t.Fatalf("jsonl summary:\n%s", jsonl.Text)
	}
	body, ok := ledger.TablesSummaryBody(jsonl.Text)
	if !ok {
		t.Fatal("not a tables summary")
	}
	decoded, err := tables.JSONLines.Decode(body)
	if err != nil || len(decoded) == 0 {
		t.Fatalf("body does not decode in jsonl: %v\n%s", err, body)
	}
	if len(jsonl.Observations) == 0 || jsonl.Omitted == 0 {
		t.Fatalf("shown %d omitted %d", len(jsonl.Observations), jsonl.Omitted)
	}
	// The lines format ignores the codec.
	in.Format, in.Codec = ledger.FormatLines, tables.JSONLines
	lines := selection.Render(in)
	in.Codec = nil
	if plain := selection.Render(in); plain.Text != lines.Text {
		t.Fatal("the codec changed a lines summary")
	}
}
