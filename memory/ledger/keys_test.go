package ledger

import (
	"slices"
	"testing"
)

func TestRecordKeys(t *testing.T) {
	for _, c := range []struct {
		text string
		want []string
	}{
		{"Don't touch `migrations/`.", []string{"migrations/"}},
		{"Never run `pytest` directly; use `make ci-local`.", []string{"pytest", "make ci-local"}},
		{"Stay out of deploy/ and ./scripts/release.sh, and/or the docs.", []string{"deploy/", "./scripts/release.sh"}},
		{"Leave db/migrations alone (and src/gen/).", []string{"db/migrations", "src/gen/"}},
		{"Use ` ` and `a`, then `a` again.", []string{"a"}},
		{"A 1/2 cup, w/ or a/b.", []string{}},
		{"No keys here.", []string{}},
	} {
		if got := RecordKeys(c.text); !slices.Equal(got, c.want) {
			t.Errorf("%q: %q, want %q", c.text, got, c.want)
		}
	}
	if got := MissingKeys("Don't touch `migrations/` or `db/`.", "Keep out of migrations/ entirely."); !slices.Equal(got, []string{"db/"}) {
		t.Fatalf("missing %q", got)
	}
}
