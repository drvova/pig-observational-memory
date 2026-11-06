package selection_test

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
)

// The rules transcript (testdata/lispparity/rules.jsonl, also a Lisp parity case) records
// the user's scope exclusions and standing rules as preferences, the user's decision as a
// decision, the assistant's as its own record, the repository files it read as conventions (a
// changed lockfile superseding the first read, a failed read none), and none of the question;
// a retraction withdraws only the decision stated as a plan.
func TestEncodingTranscriptRecords(t *testing.T) {
	for _, mode := range []string{"turns", "whole"} {
		lines := goPipeline(t, parityCase{"synthetic-rules", "generic", filepath.Join("testdata", "lispparity", "rules.jsonl"), "/home/dev/billing", mode, nil})
		var got []string
		for _, line := range lines {
			raw, ok := strings.CutPrefix(line, "record ")
			if !ok {
				continue
			}
			var o ledger.Observation
			if err := json.Unmarshal([]byte(raw), &o); err != nil {
				t.Fatal(err)
			}
			if o.Kind == "file" || o.Kind == "attempt" {
				continue
			}
			got = append(got, string(o.Kind)+"/"+o.Status+": "+o.Content)
		}
		want := []string{
			"preference/: Don't touch `db/migrations/` while you work on this.",
			"preference/: Leave the vendor directory alone.",
			"convention/: package manager pnpm (repo file `pnpm-lock.yaml`, sha256 ",
			"convention/: CI runs `pnpm install --frozen-lockfile`; `pnpm lint`; `pnpm test -- --coverage` (repo file `.github/workflows/ci.yml`, sha256 ",
			"convention/: make targets build, test, lint (repo file `Makefile`, sha256 ",
			"decision/: We decided to use pnpm over npm because the workspace needs strict peer dependencies.",
			"preference/: From now on, run `pnpm lint` before you commit.",
			"assistant/proposed: I'll use vitest because it shares the Vite config, and keep jest out.",
			"preference/: Use `zod` instead of `yup` for validation.",
			"preference/: We always squash merge.",
			"convention/: editorconfig indent_style=space, indent_size=2 (repo file `.editorconfig`, sha256 ",
			"decision/proposed: Let's go with Stripe Checkout because it handles SCA for us.",
			"correction/retracted: Never mind, scrap that.",
			"convention/: package manager pnpm (repo file `pnpm-lock.yaml`, sha256 ",
			"preference/: Only modify files under `services/billing/`.",
		}
		ok := len(got) == len(want)
		for i := 0; ok && i < len(want); i++ {
			ok = strings.HasPrefix(got[i], want[i])
		}
		if !ok {
			t.Fatalf("%s: records\n%s", mode, strings.Join(got, "\n"))
		}
		// The retraction withdraws the decision stated as a plan, and only it.
		var retraction, stripe ledger.Observation
		for _, line := range lines {
			raw, _ := strings.CutPrefix(line, "record ")
			var o ledger.Observation
			if json.Unmarshal([]byte(raw), &o) == nil && strings.Contains(o.Content, "scrap that") {
				retraction = o
			} else if strings.Contains(o.Content, "Stripe") {
				stripe = o
			}
		}
		if !slices.Equal(retraction.Supersedes, []string{stripe.ID}) {
			t.Fatalf("%s: the retraction withdraws %v, want the Stripe decision %s", mode, retraction.Supersedes, stripe.ID)
		}
	}
}
