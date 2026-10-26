package legacyquery

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
)

// A legacy read becomes the om call that lists the same rows, written by the strict formatter;
// a write, a $input query and a form with no exact equivalent get a reason and no snippet.
func TestHint(t *testing.T) {
	for query, want := range map[string]string{
		"notes | from 0123456789ab | through deadbeef":            "om.Notes(om.From(`0123456789ab`).Through(`deadbeef`))",
		`history | search "sourdough rye" | limit 5`:              `om.History(om.Search("sourdough rye").Limit(5))`,
		"notes | where kind=file | sort when desc":                `om.Notes(om.Where("kind", "file").SortBy("when", true))`,
		`history | where content~"oven"`:                          `om.History(om.Contains("text", "oven"))`,
		"notes | until 2026-01-02 asof":                           "om.Notes(om.Until(`2026-01-02`).AsOf())",
		"notes | since 2026-01-01 event | until 2026-02-01 event": "om.Notes(om.Since(`2026-01-01`).Until(`2026-02-01`).ByEvent())",
		"notes | count":                "om.Notes().Matched",
		"recall 0123456789ab":          "om.Recall(`0123456789ab`)",
		"recall deadbeef offset 10000": "om.Recall(`deadbeef`, om.Offset(10000))",
		"saved":                        "om.Saved()",
		"harness | where model=p/m":    `om.Layer(om.Where("model", "p/m"))`,
	} {
		got, reason := Hint(query)
		if got != want {
			t.Errorf("%s: %q (%s), want %q", query, got, reason, want)
		}
		if err := om.CheckSnippet(got); err != nil {
			t.Errorf("%s: %v", got, err)
		}
	}
	for query, why := range map[string]string{
		`save name=x type=user description="d"`:             "write (save); its replacement is om.Save",
		"recall $input":                                     "$input",
		"notes | count by kind":                             "count by",
		"notes | group by kind":                             "group by",
		"notes | limit 5 | sort when":                       "sort after limit",
		"notes | limit 5 | count":                           "count after limit",
		"notes | sort when | where kind=file":               "filter after",
		"notes | since 2026-01-01 | since 2026-01-02":       "given twice",
		"notes | from e1":                                   "not a record or entry id",
		"notes | since 2026-01-01 event | until 2026-02-01": "only one of since and until",
		"notes | until 2026-01-01 | through deadbeef asof":  "both until and through",
		"not a query at all |":                              "does not parse",
	} {
		got, reason := Hint(query)
		if got != "" || !strings.Contains(reason, why) {
			t.Errorf("%s: %q, reason %q, want %q", query, got, reason, why)
		}
	}
}

// Words and ids with backticks, quotes, newlines and ")" never escape the read-call shape.
func TestHintEscapes(t *testing.T) {
	for _, q := range []string{`notes | search "a\"); om.Save(\"x"`, "notes | search \"`)\\n.Save(`\"", `notes | where kind="x\ny"`} {
		got, reason := Hint(q)
		if got == "" {
			t.Fatalf("%s: %s", q, reason)
		}
		if err := om.CheckSnippet(got); err != nil {
			t.Fatalf("%s: %s: %v", q, got, err)
		}
	}
}
