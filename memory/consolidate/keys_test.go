package consolidate_test

import (
	"testing"

	"github.com/drvova/pig-observational-memory/memory/consolidate"
	"github.com/drvova/pig-observational-memory/memory/ledger"
)

// A user rule or a decision carries no specific when it names a path in lower case, so the
// specifics check alone would let level 1 merge it or let a reflection drop it into words that
// no longer name the path: its keys (ledger.RecordKeys) must be kept too. A plain fact's keys
// are not checked.
func TestValidateKeepsRuleKeys(t *testing.T) {
	fence := record("c10000000001", "Don't touch `migrations/` while you work on this.", func(o *ledger.Observation) { o.Kind = "preference" })
	other := record("c10000000002", "Leave the vendor directory alone.", func(o *ledger.Observation) { o.Kind = "preference" })
	fact := record("c10000000003", "The handler lives in `internal/api/`.")
	note := record("c10000000004", "The handler is small.")
	in := inputOf([]ledger.Observation{fence, other, fact, note}, nil, nil)
	merge := func(content string, ids ...any) map[string]any {
		return map[string]any{"merges": []any{map[string]any{"ids": ids, "content": content}}}
	}
	refused := []map[string]any{
		merge("Leave the migrations and vendor directories alone.", fence.ID, other.ID),
		{"reflections": []any{map[string]any{"content": "The user wants the schema left alone.", "supportingIds": []any{fence.ID}}}},
	}
	for _, out := range refused {
		result := consolidate.Validate(args(out), in)
		if result.Failure != consolidate.FailureNoneValid || len(result.Refusals) != 1 {
			t.Fatalf("%v: result %+v", out, result)
		}
	}
	if result := consolidate.Validate(args(merge("Leave `migrations/` and the vendor directory alone.", fence.ID, other.ID)), in); result.Merges != 1 {
		t.Fatalf("a merge keeping the key was refused: %+v", result)
	}
	if result := consolidate.Validate(args(merge("The handler is small and lives in the api package.", fact.ID, note.ID)), in); result.Merges != 1 {
		t.Fatalf("a fact's keys were checked: %+v", result)
	}
	forged := consolidate.Result{Records: []ledger.Observation{{ID: "c1000000000f", Content: "Leave the schema alone."}}, Replaced: map[string]string{fence.ID: "c1000000000f"}}
	if drop, _, kept := consolidate.Union(forged, []ledger.Observation{fence}); len(drop) != 0 || kept != 1 {
		t.Fatalf("union drop %v kept %d", drop, kept)
	}
}
