package codepolicy

import (
	"reflect"
	"strings"
	"testing"
)

func kinds(p Program) []Kind {
	var out []Kind
	for _, c := range p.Chunks {
		out = append(out, c.Kind)
	}
	return out
}

// Declarations and statements in any order are cut at depth-0 semicolons, never inside a
// control statement's header, and the last expression is reported.
func TestSplit(t *testing.T) {
	code := `// a helper
func up(s string) string { return strings.ToUpper(s) }
x := up("a"); y := 2
for i := 0; i < 3; i++ {
	y += i
}
type T struct{ a, b int }
import "fmt"
for _, v := range []int{1, 2} { y += v }
if z := y; z > 0 {
	y--
} else {
	y++
}
switch v := y; {
case v > 0:
}
func() { println(x) }()
L:
for {
	break L
}
up(x)`
	p, err := Split(code)
	if err != nil {
		t.Fatal(err)
	}
	want := []Kind{Declaration, Statement, Statement, Statement, Declaration, Import, Statement, Statement, Statement, Statement, Statement, Statement}
	if got := kinds(p); !reflect.DeepEqual(got, want) {
		var srcs []string
		for _, c := range p.Chunks {
			srcs = append(srcs, c.Src)
		}
		t.Fatalf("kinds %v; want %v\n%s", got, want, strings.Join(srcs, "\n---\n"))
	}
	if !p.LastExpr {
		t.Fatal("the last expression was not reported")
	}
	lines := []int{2, 3, 3, 4, 7, 8, 9, 10, 15, 18, 19, 23}
	for i, c := range p.Chunks {
		if c.Line != lines[i] {
			t.Errorf("chunk %d %q at line %d; want %d", i, c.Src, c.Line, lines[i])
		}
	}
	if p.Chunks[0].Src != "// a helper\nfunc up(s string) string { return strings.ToUpper(s) }" {
		t.Fatalf("first chunk %q", p.Chunks[0].Src)
	}
	groups := p.Groups()
	var gk []Kind
	for _, g := range groups {
		gk = append(gk, g.Kind)
	}
	// The import is a no-op and leaves the group; the statements around it join.
	if !reflect.DeepEqual(gk, []Kind{Declaration, Statement, Declaration, Statement}) {
		t.Fatalf("groups %v", gk)
	}
	if groups[1].Src != "x := up(\"a\")\ny := 2\nfor i := 0; i < 3; i++ {\n\ty += i\n}" {
		t.Fatalf("group %q", groups[1].Src)
	}
}

// A last unit that is not an expression is not captured, and empty code has no chunks.
func TestSplitLastValue(t *testing.T) {
	for code, want := range map[string]bool{
		"42":                        true,
		"x := 1; x":                 true,
		"x := 1":                    false,
		"func f() {}":               false,
		"func() int { return 1 }()": true,
		"for {\n\tbreak\n}":         false,
	} {
		p, err := Split(code)
		if err != nil {
			t.Fatalf("%q: %v", code, err)
		}
		if p.LastExpr != want {
			t.Errorf("%q: last expression %v", code, p.LastExpr)
		}
	}
	for _, code := range []string{"", "  \n\t", "// nothing\n/* here */", ";;"} {
		p, err := Split(code)
		if err != nil || len(p.Chunks) != 0 || p.LastExpr {
			t.Errorf("%q: %+v %v", code, p, err)
		}
	}
}
