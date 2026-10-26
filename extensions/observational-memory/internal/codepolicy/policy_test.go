package codepolicy

import (
	"errors"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
)

// refusal runs the policy on code and returns its refusal, failing when there is none.
func refusal(t *testing.T, in Input) *Refusal {
	t.Helper()
	_, err := Check(in)
	var r *Refusal
	if !errors.As(err, &r) {
		t.Fatalf("%q: %v; want a refusal", in.Code, err)
	}
	return r
}

// Every refusal the design lists, at the depth it names, with the rule and the offending name.
func TestPolicyRefusals(t *testing.T) {
	cases := []struct {
		name, code, rule, offender string
		line                       int
	}{
		// go statements, anywhere.
		{"go at top level", "go println(1)", RuleGoStatement, "go", 1},
		{"go in a function", "func f() {\n\tgo println(1)\n}", RuleGoStatement, "go", 2},
		{"go in a literal", "x := func() { go println(1) }\n_ = x", RuleGoStatement, "go", 1},
		// A defer outside a function, at any depth of the top level.
		{"top-level defer", "defer println(1)", RuleTopLevelDefer, "defer", 1},
		{"defer in a top-level block", "if true {\n\tdefer println(1)\n}", RuleTopLevelDefer, "defer", 2},
		// init and main.
		{"func init", "func init() {}", RuleEntryPoint, "init", 1},
		{"func main", "func main() { println(1) }", RuleEntryPoint, "main", 1},
		// Predeclared names bound at depth 0.
		{"var true", "var true = false", RuleOwnedName, "true", 1},
		{"func len", "func len(s string) int { return 0 }", RuleOwnedName, "len", 1},
		{"type string", "type string int", RuleOwnedName, "string", 1},
		{"const nil", "const nil = 7", RuleOwnedName, "nil", 1},
		// Statement bindings that would shadow a package for the session.
		{"fmt :=", "fmt := 3\n_ = fmt", RuleOwnedName, "fmt", 1},
		{"for variable strings", "for strings := 0; strings < 1; strings++ {}", RuleOwnedName, "strings", 1},
		{"range variable", "for len := range []int{1} { _ = len }", RuleOwnedName, "len", 1},
		{"if init", "if cap := 1; cap > 0 {}", RuleOwnedName, "cap", 1},
		{"switch init", "switch new := 1; new {}", RuleOwnedName, "new", 1},
		{"type switch binding", "var x any = 1\nswitch error := x.(type) { default: _ = error }", RuleOwnedName, "error", 2},
		{"select receive", "ch := make(chan int)\nselect {\ncase copy := <-ch:\n\t_ = copy\ndefault:\n}", RuleOwnedName, "copy", 3},
		{"var statement", "var time = 1\n_ = time", RuleOwnedName, "time", 1},
		// Inside function bodies.
		{"local :=", "func f() int {\n\tlen := 1\n\treturn len\n}", RuleOwnedName, "len", 2},
		{"local var", "func f() {\n\tvar om int\n\t_ = om\n}", RuleOwnedName, "om", 2},
		{"local type", "func f() {\n\ttype int8 struct{}\n}", RuleOwnedName, "int8", 2},
		{"parameter", "func f(len int) {}", RuleOwnedName, "len", 1},
		{"result", "func f() (cap int) { return }", RuleOwnedName, "cap", 1},
		{"receiver", "type B int\nfunc (append B) M() {}", RuleOwnedName, "append", 2},
		{"type parameter", "func f[string any](x string) {}", RuleOwnedName, "string", 1},
		{"type's type parameter", "type Box[any any] struct{ v any }", RuleOwnedName, "any", 1},
		{"receiver type parameter", "type Box[T any] struct{ v T }\nfunc (b Box[byte]) M() {}", RuleOwnedName, "byte", 2},
		{"literal parameter", "f := func(panic int) {}\n_ = f", RuleOwnedName, "panic", 1},
		// Labels, and the targets of break, continue and goto.
		{"label len", "func lab(s string) int {\nlen:\n\tfor {\n\t\tbreak len\n\t}\n\treturn 0\n}", RuleOwnedName, "len", 2},
		{"label fmt", "fmt:\nfor {\n\tbreak\n}", RuleOwnedName, "fmt", 1},
		{"label om", "func f() {\nom:\n\tfor {\n\t\tbreak\n\t}\n}", RuleOwnedName, "om", 2},
		{"goto target", "func f() {\n\tgoto print\n}", RuleOwnedName, "print", 2},
		// Seeded names and the capture function.
		{"seeded func", "func describe(x any) string { return \"\" }", RuleOwnedName, "describe", 1},
		{"seeded variable", "kept := 1\n_ = kept", RuleOwnedName, "kept", 1},
		{"forget", "func forget(name string) string { return name }", RuleOwnedName, "forget", 1},
		{"capture", "func " + CaptureName + "(v ...any) {}", RuleOwnedName, CaptureName, 1},
		{"held", HeldName + " := 1", RuleOwnedName, HeldName, 1},
		{"om", "var om = 1", RuleOwnedName, "om", 1},
		// Assigning an owned name itself.
		{"assign true", "true = false", RuleOwnedName, "true", 1},
		// Settable package variables, through a selector, index or dereference.
		{"ErrSyntax", "strconv.ErrSyntax = nil", RulePackageAssignment, "strconv", 1},
		{"Categories", "unicode.Categories[\"Lu\"] = unicode.Digit", RulePackageAssignment, "unicode", 1},
		{"deref UTC", "*time.UTC = time.Location{}", RulePackageAssignment, "time", 1},
		{"Local", "func f() {\n\ttime.Local = nil\n}", RulePackageAssignment, "time", 2},
		{"address of om", "p := &om.X\n_ = p", RulePackageAssignment, "om", 1},
		{"increment", "math.MaxInt++", RulePackageAssignment, "math", 1},
		{"range assign", "for strconv.ErrRange = range []error{nil} {}", RulePackageAssignment, "strconv", 1},
		// Imports.
		{"os", "import \"os\"", RuleImport, "os", 1},
		{"net/http", "import \"net/http\"", RuleImport, "net/http", 1},
		{"unsafe", "import (\n\t\"fmt\"\n\t\"unsafe\"\n)", RuleImport, "unsafe", 3},
		{"reflect", "import \"reflect\"", RuleImport, "reflect", 1},
		{"relative", "import \"./srcpkg/evil\"", RuleImport, "./srcpkg/evil", 1},
		{"renamed", "import f \"fmt\"", RuleImport, "f", 1},
		{"dot import", "import . \"strings\"", RuleImport, ".", 1},
		// Constant arrays above the result bound in bytes.
		{"huge var", "var b [1 << 40]byte", RuleArraySize, "", 1},
		{"array type", "type Big [20000]byte", RuleArraySize, "", 1},
		{"composite literal", "x := [2000]int{}\n_ = x", RuleArraySize, "", 1},
		{"constant from the call", "const N = 1 << 20\nvar b [N]byte", RuleArraySize, "", 2},
		{"iota constant", "const (\n\tA = 1 << (10 * iota)\n\tK\n\tM\n)\nvar b [M]byte", RuleArraySize, "", 6},
		{"element type from the call", "type Row [100]int64\nvar rows [100]Row", RuleArraySize, "", 2},
		{"struct element", "var rows [1000]struct{ a, b int64 }", RuleArraySize, "", 1},
		{"nested", "var grid [100][100]int", RuleArraySize, "", 1},
		{"in a function", "func f() {\n\tvar b [1 << 30]byte\n\t_ = b\n}", RuleArraySize, "", 2},
		{"keyed array literal", "x := [...]byte{1 << 40: 1}\n_ = x", RuleArraySize, "", 1},
		{"keyed slice literal", "y := []byte{1 << 40: 1}\n_ = y", RuleArraySize, "", 1},
		{"keyed var literal", "var x = [...]int{1<<30: 0}", RuleArraySize, "", 1},
		{"positional after a key", "x := []int64{1249: 0, 1, 2}\n_ = x", RuleArraySize, "", 1},
		{"elided inner literal", "x := [][]byte{{1 << 40: 1}}\n_ = x", RuleArraySize, "", 1},
		{"map value literal", "m := map[string][]int{\"a\": {1 << 20: 1}}\n_ = m", RuleArraySize, "", 1},
		{"declared slice type", "type B []byte\nb := B{1 << 40: 1}\n_ = b", RuleArraySize, "", 2},
		{"element type from a kept unit", "var rows [100]Row", RuleArraySize, "", 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := refusal(t, Input{Code: c.code, Types: map[string]string{"Row": "type Row [100]int64"}})
			if r.Rule != c.rule || r.Line != c.line || (c.offender != "" && r.Name != c.offender) {
				t.Fatalf("%q: %+v; want rule %q, name %q, line %d", c.code, r, c.rule, c.offender, c.line)
			}
			if c.offender != "" && !strings.Contains(r.Error(), c.offender) {
				t.Fatalf("the refusal does not name %q: %v", c.offender, r)
			}
		})
	}
}

// Owned-name refusals name the owner and ask for another name.
func TestOwnedNameRefusalNamesOwner(t *testing.T) {
	r := refusal(t, Input{Code: "fmt := 3"})
	if !strings.Contains(r.Reason, kernel.OwnerCodePackage) || !strings.Contains(r.Reason, "another name") {
		t.Fatal(r)
	}
	r = refusal(t, Input{Code: "func f(len int) {}"})
	if !strings.Contains(r.Reason, kernel.OwnerGoPredeclared) {
		t.Fatal(r)
	}
}

// What the policy allows: names reached only through selectors, case-different names, the
// builtins the interpreter lacks, defers in functions, allowed imports, small arrays and
// writes to the code's own values.
func TestPolicyAllows(t *testing.T) {
	for _, code := range []string{
		"type Box struct {\n\tLen    int\n\tstring\n\terror error\n}",
		"type Box struct{ n int }\nfunc (b Box) String() string { return \"box\" }\nfunc (b Box) len() int { return b.n }\nfunc (b *Box) append(x int) { b.n += x }",
		"type Sizer interface {\n\tlen() int\n\tcap(x int) int\n}",
		"Len := 1\nError := Len\n_ = Error",
		"func max(a, b int) int {\n\tif a > b {\n\t\treturn a\n\t}\n\treturn b\n}\nfunc min(a, b int) int { return -max(-a, -b) }\nfunc clear(m map[string]int) { for k := range m { delete(m, k) } }\nmax(1, 2)",
		"func f() {\n\tdefer println(1)\n}",
		"x := func() { defer println(1) }\nx()",
		"import \"fmt\"\nimport (\n\t\"strings\"\n\t\"unicode/utf8\"\n\t\"om\"\n)\nfmt.Println(strings.ToUpper(\"a\"), utf8.RuneLen('a'))",
		"var b [100]byte\n_ = b",
		"x := [...]int{1, 2, 3}\n_ = x",
		"var b [10000]byte\n_ = b",
		"n := 1 << 40\ns := make([]byte, 0, n)\n_ = s",
		"type Box struct{ n int }\nb := Box{}\nb.n = 3\np := &b\np.n++",
		"s := strings.Builder{}\ns.WriteString(\"x\")\np := &strings.Builder{}\n_ = p",
		"loop:\nfor i := 0; i < 3; i++ {\n\tif i == 1 {\n\t\tcontinue loop\n\t}\n}",
		"v := time.UTC\n_ = v",
		"x := 1\nx = 2\nm := map[string]int{}\nm[\"a\"]++",
		"for i := []int{1}[0]; i < 3; i++ {\n}\nswitch x := struct{ a int }{1}; x.a {\n}",
		"func f[T any](x T) T { return x }",
		// A signature type's parameter names are never in scope.
		"type I interface{ M(len int) }",
		"type F func(string int)",
		"type G func() (len int)",
		"var f func(len int)\n_ = f",
		"func apply(g func(cap int) int) int { return g(1) }",
		// Literals whose length the elements set, within the bound.
		"x := []int64{1249: 0}\n_ = x",
		"x := [][]byte{{9999: 1}}\n_ = x",
		// A zero-size element takes nothing, wherever it is defined.
		"type E struct{}\nvar s [20000]E\n_ = s",
		// A size the call cannot see is not guessed.
		"func f[T any]() { var a [20000]T; _ = a }",
		"",
		"// only a comment",
	} {
		if _, err := Check(Input{Code: code}); err != nil {
			t.Errorf("%q: %v", code, err)
		}
	}
}

// A zero-size type a kept unit or an earlier call defined takes no bytes in an array; a type
// the namespace does not show is not guessed at.
func TestArraySizeLiveTypes(t *testing.T) {
	live := map[string]string{"Empty": "type Empty struct{}"}
	for _, code := range []string{"var seen [20000]Empty\n_ = seen", "var seen [20000]Unknown\n_ = seen"} {
		if _, err := Check(Input{Code: code, Types: live}); err != nil {
			t.Errorf("%q: %v", code, err)
		}
	}
}

// A type redefined with different source is refused, in the call and against the namespace;
// the same source, however spelled, is not.
func TestTypeRedefinition(t *testing.T) {
	live := map[string]string{"Box": "type Box struct{ n int }"}
	r := refusal(t, Input{Code: "type Box struct {\n\tn, m int\n}", Types: live})
	if r.Rule != RuleTypeRedefinition || r.Name != "Box" || !strings.Contains(r.Reason, "forget it first") {
		t.Fatal(r)
	}
	if _, err := Check(Input{Code: "type Box struct {\n\tn int // the count\n}", Types: live}); err != nil {
		t.Fatalf("the same type was refused: %v", err)
	}
	r = refusal(t, Input{Code: "type A int\ntype A string"})
	if r.Rule != RuleTypeRedefinition || r.Line != 2 {
		t.Fatal(r)
	}
}

// The call's input bound covers code and strings together, and strings names are identifiers.
func TestInputBounds(t *testing.T) {
	big := strings.Repeat("x", om.RecallPageChars)
	r := refusal(t, Input{Code: "_ = 1", Strings: map[string]string{"text": big}})
	if r.Rule != RuleInputBound {
		t.Fatal(r)
	}
	r = refusal(t, Input{Code: "// " + big})
	if r.Rule != RuleInputBound {
		t.Fatal(r)
	}
	if _, err := Check(Input{Code: "_ = 1", Strings: map[string]string{"text": big[:om.RecallPageChars-5]}}); err != nil {
		t.Fatalf("an input at the bound was refused: %v", err)
	}
	r = refusal(t, Input{Code: "_ = 1", Strings: map[string]string{"not a name": "x"}})
	if r.Rule != RuleStringsName {
		t.Fatal(r)
	}
	// Characters, not bytes.
	if _, err := Check(Input{Code: "_ = 1", Strings: map[string]string{"text": strings.Repeat("é", om.RecallPageChars-5)}}); err != nil {
		t.Fatalf("multibyte characters counted as bytes: %v", err)
	}
}

// Code that does not parse is a syntax error with its line, and nothing of it is checked.
func TestSyntaxErrors(t *testing.T) {
	for code, line := range map[string]int{
		"x := 1\ny := (2 +\n":             2,
		"func f( {}":                      1,
		"x := 1\nfor i := 0; i < {":       2,
		"type T struct {":                 1,
		"x := 1\n\nvar = 3":               3,
		"notes | from e1 | through e9":    1,
		"import \"fmt\" \"strings\"":      1,
		"func f() int {\n\treturn 1 +\n}": 3,
	} {
		_, err := Check(Input{Code: code})
		var se *SyntaxError
		if !errors.As(err, &se) {
			t.Errorf("%q: %v; want a syntax error", code, err)
			continue
		}
		if se.Line != line {
			t.Errorf("%q: line %d (%v); want %d", code, se.Line, se, line)
		}
	}
}
