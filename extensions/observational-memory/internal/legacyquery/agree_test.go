package legacyquery_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/legacyquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/memquery"
)

// Until the query language is deleted (docs/code-tool.md, "Order of work", step 10), the frozen
// recognizer is a copy of memquery's parser: the same vocabulary, and the same verdict and
// error on every query. This test goes with memquery.
func TestFrozenRecognizerAgreesWithMemquery(t *testing.T) {
	for name, pair := range map[string][2][]string{
		"sources": {legacyquery.Sources, memquery.Sources},
		"stages":  {legacyquery.Stages, memquery.Stages},
		"writes":  {legacyquery.Writes, memquery.Writes},
	} {
		if !slices.Equal(pair[0], pair[1]) {
			t.Errorf("%s: frozen %v, memquery %v", name, pair[0], pair[1])
		}
	}
	if legacyquery.MaxLimit != memquery.MaxLimit || legacyquery.RecallOffset != memquery.RecallOffset {
		t.Fatal("bounds or fixed words differ")
	}
	for _, query := range []string{
		"", "notes", `notes | search "sourdough" | where kind=event | count by subject`,
		`history | search "india online store" | limit 5`, "recall 3f9a2c1d7b4e", "recall e42 offset 10000",
		`notes | since 2023-05-01 event | until 2023-05-31 asof`, `notes | from e1 | through e9 asof`,
		`save type=user name=prefers-tabs description="The user indents with tabs."`,
		`define bake-count = notes | where subject=baking | count "How many times"`,
		`harness`, `harness | where state=held`, `harness forget kind=format name=rows`, `rollback 3 model="p/m"`,
		`propose detector=thin-chunk remedy=prompt-line`, "notes | limit 201", "notes | filter x", `notes | search "open`,
		"notes | group subject", "memories", `notes | until 2023-05-31 event asof`,
	} {
		lq, lerr := legacyquery.Parse(query)
		mq, merr := memquery.Parse(query)
		if (lerr == nil) != (merr == nil) {
			t.Errorf("%q: frozen %v, memquery %v", query, lerr, merr)
			continue
		}
		if lerr != nil {
			if lerr.Error() != merr.Error() {
				t.Errorf("%q: frozen %q, memquery %q", query, lerr, merr)
			}
			continue
		}
		if lq.Source != mq.Source || lq.Write != mq.Write || lq.ID != mq.ID || lq.Offset != mq.Offset || len(lq.Stages) != len(mq.Stages) || !reflect.DeepEqual(lq.Args, mq.Args) {
			t.Errorf("%q: frozen %+v, memquery %+v", query, lq, mq)
		}
	}
}
