package codepolicy

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
)

// One unit per bound name: functions, methods by receiver, each type and constant spec alone;
// constants under iota keep their values; variables and statements are never kept.
func TestUnits(t *testing.T) {
	code := `func up(s string) string { return s }
type Box struct{ n int }
func (b *Box) Twice() int { return 2 * b.n }
type (
	A int
	B[T any] struct{ v T }
)
const (
	K = 1 << (10 * (iota + 1))
	M
	label = "x"
)
const p, q = 1, 2
var v = up("x")
x := 3`
	p, err := Check(Input{Code: code})
	if err != nil {
		t.Fatal(err)
	}
	units, skipped := p.Units()
	got := map[string]string{}
	var names []string
	for _, u := range units {
		got[u.Name] = u.Src
		names = append(names, u.Name+":"+string(u.Kind))
	}
	want := []string{"up:func", "Box:type", "Box.Twice:method", "A:type", "B:type", "K:const", "M:const", "label:const", "p:const", "q:const"}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("units %v; want %v", names, want)
	}
	for name, src := range map[string]string{
		"up":    "func up(s string) string { return s }",
		"A":     "type A int",
		"B":     "type B[T any] struct{ v T }",
		"K":     "const K = 1 << (10 * (0 + 1))",
		"M":     "const M = 1 << (10 * (1 + 1))",
		"label": `const label = "x"`,
		"q":     "const q = 2",
	} {
		if got[name] != src {
			t.Errorf("%s: %q; want %q", name, got[name], src)
		}
	}
	if len(skipped) != 1 || skipped[0].Name != "v" || skipped[0].Reason != ReasonVariable || skipped[0].Line != 14 {
		t.Fatalf("skipped %+v", skipped)
	}
	// Every unit stands alone and passes the load-time check under its name.
	for _, u := range units {
		if err := CheckUnit(u.Name, u.Src); err != nil {
			t.Errorf("%s: %v", u.Name, err)
		}
	}
}

// The load-time check refuses a stored unit that breaks the policy, defines another name,
// holds more than one definition, a variable or a statement, or exceeds one recall page.
func TestCheckUnit(t *testing.T) {
	cases := map[string][2]string{
		"go":         {"f", "func f() { go println(1) }"},
		"init":       {"init", "func init() {}"},
		"label":      {"f", "func f() {\nlen:\n\tfor {\n\t\tbreak len\n\t}\n}"},
		"selector":   {"f", "func f() { strconv.ErrSyntax = nil }"},
		"other name": {"f", "func g() {}"},
		"two":        {"f", "func f() {}\nfunc g() {}"},
		"two consts": {"p", "const p, q = 1, 2"},
		"variable":   {"v", "var v = 1"},
		"statement":  {"x", "x := 1"},
		"import":     {"os", "import \"os\""},
		"array":      {"Big", "type Big [1 << 40]byte"},
		"syntax":     {"f", "func f( {"},
		"too long":   {"f", "func f() {}\n// " + strings.Repeat("x", om.RecallPageChars)},
	}
	for name, c := range cases {
		if err := CheckUnit(c[0], c[1]); err == nil {
			t.Errorf("%s: %q passed", name, c[1])
		}
	}
	var r *Refusal
	if err := CheckUnit("f", "func g() {}"); !errors.As(err, &r) || r.Rule != RuleUnit {
		t.Fatalf("%v", err)
	}
	for name, src := range map[string]string{
		"f":         "// f doubles.\nfunc f(x int) int { return 2 * x }",
		"Box.Twice": "func (b *Box) Twice() int { return 2 * b.n }",
		"T":         "type T struct{ Len int }",
		"N":         "const N = 3",
		"max":       "func max(a, b int) int { if a > b { return a }; return b }",
	} {
		if err := CheckUnit(name, src); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
