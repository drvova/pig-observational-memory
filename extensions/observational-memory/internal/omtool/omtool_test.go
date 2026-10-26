package omtool_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omtool"
)

// asDecoded is v as a renderer receives it: decoded JSON.
func asDecoded(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// A call and result written before the code tool (a query argument, the query language's
// details) render as they did; a code call renders its first lines and a header from its
// details.
func TestTuiRendersOldAndNewCalls(t *testing.T) {
	if got := omtool.FormatCallForTui(map[string]any{"query": "notes | count\nmore"}); got != "memory notes | count" {
		t.Fatalf("old call %q", got)
	}
	old := asDecoded(t, map[string]any{"query": "notes", "kind": "notes", "rows": 20, "matched": 30})
	if omtool.DetailsFrom(old) != nil || omtool.LegacyDetailsFrom(old) == nil {
		t.Fatal("old details read as a code call's")
	}
	body := strings.Repeat("row\n", 12) + "matched 30, showing 20"
	collapsed := omtool.FormatRenderedResultForTui(old, body, false)
	if !strings.HasPrefix(collapsed, "\n✓ notes · showing 20 of 30\n\n") || !strings.HasSuffix(collapsed, "(Ctrl+O to expand)") {
		t.Fatalf("old result %q", collapsed)
	}
	if got := omtool.FormatRenderedResultForTui(asDecoded(t, map[string]any{"query": "bogus", "error": true}), "Error: x", false); !strings.HasPrefix(got, "\n× failure") {
		t.Fatalf("old failure %q", got)
	}

	if got := omtool.FormatCallForTui(map[string]any{"code": "a := 1\nb := 2\nc := 3\nd := 4"}); got != "memory a := 1\nb := 2\nc := 3\n…" {
		t.Fatalf("code call %q", got)
	}
	if got := omtool.FormatCallForTui(map[string]any{"code": "  "}); got != "memory (manual)" {
		t.Fatalf("manual call %q", got)
	}
	d := asDecoded(t, omtool.Details{Code: "x", Outcome: omtool.OutcomeOK, Writes: []omtool.Write{{Op: "save", Name: "n"}}, Kept: []string{"up"}})
	if omtool.LegacyDetailsFrom(d) != nil || omtool.DetailsFrom(d) == nil {
		t.Fatal("code details read as old ones")
	}
	if got := omtool.FormatRenderedResultForTui(d, "done", false); got != "\n✓ ok · 1 write · kept up\n\ndone" {
		t.Fatalf("code result %q", got)
	}
	if got := omtool.FormatRenderedResultForTui(asDecoded(t, omtool.Details{Code: "x", Outcome: omtool.OutcomeCrashed}), "fatal", false); !strings.HasPrefix(got, "\n× crashed") {
		t.Fatalf("crash %q", got)
	}
}
