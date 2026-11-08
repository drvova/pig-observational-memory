package sleep

import (
	"reflect"
	"testing"
)

// Substitutions reads the preference forms the joint-consistency check orders; the contract
// cases (contracts/sleep.jsonl) hold the cycles both engines find.
func TestSubstitutions(t *testing.T) {
	for _, c := range []struct {
		text string
		want []Preference
	}{
		{"Use `make ci`, not `make test`.", []Preference{{"make ci", "make test"}}},
		{"Run `go test ./...` instead of `make test`.", []Preference{{"go test ./...", "make test"}}},
		{"Prefer `chi` over `gin`.", []Preference{{"chi", "gin"}}},
		{"Use `rg` rather than `grep`.", []Preference{{"rg", "grep"}}},
		{"Never run `pytest` directly here; always use `make ci-local` instead.", []Preference{{"make ci-local", "pytest"}}},
		{"Don't call `fmt.Println`; use `log.Printf`.", []Preference{{"log.Printf", "fmt.Println"}}},
		{"Avoid `fmt.Println`. Use `log.Printf` here.", []Preference{{"log.Printf", "fmt.Println"}}},
		{"Never edit `gen.go`; run `make gen` and commit `gen.go`.", nil},
		{"Edit `a.go` and `b.go`.", nil},
		{"Never touch `db/static/` while you work on this.", nil},
		{"Use `x`, not `x`.", nil},
		{"Use `a`\n, not `b`.", []Preference{{"a", "b"}}},
		{"Use `a\n`, not `b`.", nil},
	} {
		if got := Substitutions(c.text); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: %v, want %v", c.text, got, c.want)
		}
	}
}
