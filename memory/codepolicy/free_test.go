package codepolicy

import (
	"slices"
	"strings"
	"testing"
)

// Free names are what a unit names from outside itself: never its own name, its parameters,
// locals, type parameters, labels, fields, selected names or composite-literal keys.
func TestFreeNames(t *testing.T) {
	for src, want := range map[string][]string{
		"func f(n int) int { if n == 0 { return 0 }; return f(n-1) + x }":                   {"int", "x"},
		"func (b Box) Twice() int { return b.N * helper(b) }":                               {"Box", "helper", "int"},
		"func g[T any](v T) T { l: for { break l }; return v }":                             {"any"},
		"type T struct{ A int; B Other }":                                                   {"Other", "int"},
		"const C = iota + K":                                                                {"K", "iota"},
		"func h() P { return P{Field: 1, Other: strings.ToUpper(\"a\")} }":                  {"P", "strings"},
		"func k() { var y = om.Notes(); _ = y; z := 3; _ = z; for i := range 3 { _ = i } }": {"om"},
		"type I interface{ M() R }":                                                         {"R"},
		"func m() { defer func() { recover() }() }":                                         {"recover"},
		"func n() { switch v := any(1).(type) { case int: _ = v } }":                        {"any", "int"},
	} {
		if got := FreeNames(src); !slices.Equal(got, want) {
			t.Errorf("%s: %v, want %v", src, got, want)
		}
	}
	if FreeNames("func (") != nil {
		t.Error("a source that does not parse")
	}
}

func TestSeededAndReceiver(t *testing.T) {
	for _, n := range []string{"om", "fmt", "len", "int", "kept", "describe", "nil"} {
		if !Seeded(n) {
			t.Errorf("%s not seeded", n)
		}
	}
	for _, n := range []string{"max", "x", "Box"} {
		if Seeded(n) {
			t.Errorf("%s seeded", n)
		}
	}
	if ReceiverType("Box.Twice") != "Box" || ReceiverType("f") != "" {
		t.Error("receiver")
	}
}

// The stub keeps a function's signature and panics naming it; nothing else gets one.
func TestForgottenStub(t *testing.T) {
	stub, ok := ForgottenStub("f", "// doc\nfunc f(a int, b string) (int, error) { return a, nil }")
	if !ok || !strings.HasPrefix(stub, "func f(a int, b string) (int, error) {") || !strings.Contains(stub, `panic("f was forgotten`) {
		t.Fatalf("%q", stub)
	}
	if _, err := Check(Input{Code: stub}); err != nil {
		t.Fatalf("the stub does not pass the policy: %v", err)
	}
	for name, src := range map[string]string{"Box.M": "func (Box) M() {}", "T": "type T int", "C": "const C = 1", "g": "func f() {}"} {
		if _, ok := ForgottenStub(name, src); ok {
			t.Errorf("%s stubbed", name)
		}
	}
}
