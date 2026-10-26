package sandbox

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"reflect"
	"strconv"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/codepolicy"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
)

// The interpreter's global type analysis cannot infer a top-level variable's type from some
// expressions over host values (an index or a field of a value an om read or a standard
// function returned: l := om.Notes(); n := l.Rows[0] fails with "constant definition loop" or
// panics its compiler), while the same statement inside a function, or an assignment to a
// variable declared with its type, works. So when a top-level := or untyped var fails to
// compile, the child declares it in two steps: it evaluates the right-hand side once, holding
// the values, declares each variable with its value's type, and assigns the held value. The
// rescue runs only for types it can name from the allow-listed packages and om.

// errNoRescue reports a statement that is not a declaration the rescue handles.
var errNoRescue = errors.New("no rescue")

// ranCannotRescue is the failure of a rescue that held the right-hand side's values, so ran it
// with any om write in it, and then could not name a value's type. It is a runtime failure,
// not a compile error: the statement ran, and the hint to restate it must say that running it
// again repeats its writes.
func ranCannotRescue() error {
	return &evalError{phase: phaseRun, err: errors.New("the right-hand side ran (any om write in it took effect), but the interpreter cannot infer this top-level variable's type and the value's type cannot be named here (a nil interface, or a type defined in the namespace), so the variable was not declared: declare it with its type first (var x T), then assign it (x = ...), or work inside a function; running the statement again repeats its writes")}
}

// omPkgPath is om's import path as reflection reports it.
var omPkgPath = reflect.TypeOf(om.Opt{}).PkgPath()

// rescueDefine declares a failed top-level := or var in two steps. It returns errNoRescue when
// the statement is not one it handles, and otherwise the result of the steps.
func (c *child) rescueDefine(src string) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", "package p;func _(){"+src+"\n}", 0)
	if err != nil {
		return errNoRescue
	}
	body := f.Decls[0].(*ast.FuncDecl).Body.List
	if len(body) != 1 {
		return errNoRescue
	}
	text := func(n ast.Node) string {
		var b bytes.Buffer
		_ = printer.Fprint(&b, fset, n)
		return b.String()
	}
	var names []string
	var rhs []ast.Expr
	var typ ast.Expr
	switch s := body[0].(type) {
	case *ast.AssignStmt:
		if s.Tok != token.DEFINE {
			return errNoRescue
		}
		for _, l := range s.Lhs {
			id, ok := l.(*ast.Ident)
			if !ok {
				return errNoRescue
			}
			names = append(names, id.Name)
		}
		rhs = s.Rhs
	case *ast.DeclStmt:
		g, ok := s.Decl.(*ast.GenDecl)
		if !ok || g.Tok != token.VAR || len(g.Specs) != 1 {
			return errNoRescue
		}
		v := g.Specs[0].(*ast.ValueSpec)
		for _, n := range v.Names {
			names = append(names, n.Name)
		}
		rhs, typ = v.Values, v.Type
		if len(rhs) == 0 {
			return errNoRescue
		}
	default:
		return errNoRescue
	}

	// The comma-ok forms: v, ok := x.(T) and v, ok := m[k].
	if len(names) == 2 && len(rhs) == 1 {
		switch e := ast.Unparen(rhs[0]).(type) {
		case *ast.TypeAssertExpr:
			if e.Type == nil {
				return errNoRescue
			}
			return c.declareThenAssign(names, []string{text(e.Type), "bool"}, text(e))
		case *ast.IndexExpr:
			held, err := c.hold(text(e))
			if err != nil {
				return err
			}
			if len(held) != 1 {
				return errNoRescue
			}
			t, ok := typeSource(held[0])
			if !ok {
				return ranCannotRescue()
			}
			return c.declareThenAssign(names, []string{t, "bool"}, text(e))
		}
	}
	if typ != nil {
		types := make([]string, len(names))
		for i := range types {
			types[i] = text(typ)
		}
		var vals []string
		for _, e := range rhs {
			vals = append(vals, text(e))
		}
		return c.declareThenAssign(names, types, strings.Join(vals, ", "))
	}

	// Evaluate every right-hand side once, in order, before any variable is declared, as Go
	// evaluates them before it assigns.
	var held []reflect.Value
	for _, e := range rhs {
		vs, err := c.hold(text(e))
		if err != nil {
			return err
		}
		held = append(held, vs...)
	}
	if len(held) != len(names) {
		return errNoRescue
	}
	types := make([]string, len(names))
	for i, v := range held {
		t, ok := typeSource(v)
		if !ok && names[i] != "_" {
			return ranCannotRescue()
		}
		types[i] = t
	}
	c.held = held
	defer func() { c.held = nil }()
	var assigns []string
	for i, n := range names {
		if n == "_" {
			continue
		}
		if _, err := c.eval("var " + n + " " + types[i]); err != nil {
			return err
		}
		assigns = append(assigns, fmt.Sprintf("%s = %s(%d).(%s)", n, codepolicy.HeldName, i, types[i]))
	}
	for _, a := range assigns {
		if _, err := c.eval(a); err != nil {
			return err
		}
	}
	return nil
}

// declareThenAssign declares each name with its type, then assigns the expression to them all.
func (c *child) declareThenAssign(names, types []string, expr string) error {
	var lhs []string
	for i, n := range names {
		if n != "_" {
			if _, err := c.eval("var " + n + " " + types[i]); err != nil {
				return err
			}
		}
		lhs = append(lhs, n)
	}
	_, err := c.eval(strings.Join(lhs, ", ") + " = " + expr)
	return err
}

// hold evaluates one expression through the capture function in hold mode: its values are kept
// as they are, not rendered.
func (c *child) hold(expr string) ([]reflect.Value, error) {
	c.holding, c.heldNow = true, nil
	defer func() { c.holding = false }()
	if _, err := c.eval(wrapCapture(expr)); err != nil {
		return nil, err
	}
	return c.heldNow, nil
}

// heldValue is the held-value function: the i-th value the rescue held.
func (c *child) heldValue(i int) any {
	if i < 0 || i >= len(c.held) || !c.held[i].IsValid() {
		return nil
	}
	return c.held[i].Interface()
}

// typeSource names a value's dynamic type as source the namespace can compile: predeclared
// types, the allow-listed packages' and om's named types, and pointers, slices, arrays and maps
// of them. A nil interface, an interpreted type or an unexported one has no such name.
func typeSource(v reflect.Value) (string, bool) {
	if !v.IsValid() {
		return "", false
	}
	return typeName(v.Type())
}

func typeName(t reflect.Type) (string, bool) {
	if t.Name() != "" {
		if !token.IsExported(t.Name()) && t.PkgPath() != "" {
			return "", false
		}
		if strings.ContainsAny(t.Name(), "[]") {
			return "", false // an instantiated generic type
		}
		if t.PkgPath() == "" {
			return t.Name(), true
		}
		pkg, ok := packageName(t.PkgPath())
		if !ok {
			return "", false
		}
		return pkg + "." + t.Name(), true
	}
	switch t.Kind() {
	case reflect.Pointer:
		e, ok := typeName(t.Elem())
		return "*" + e, ok
	case reflect.Slice:
		e, ok := typeName(t.Elem())
		return "[]" + e, ok
	case reflect.Array:
		e, ok := typeName(t.Elem())
		return "[" + strconv.Itoa(t.Len()) + "]" + e, ok
	case reflect.Map:
		k, ok1 := typeName(t.Key())
		e, ok2 := typeName(t.Elem())
		return "map[" + k + "]" + e, ok1 && ok2
	case reflect.Interface:
		if t.NumMethod() == 0 {
			return "any", true
		}
	}
	return "", false
}

// packageName is the name an import path's types are written with in the namespace.
func packageName(path string) (string, bool) {
	if path == omPkgPath {
		return codepolicy.OMImport, true
	}
	for _, p := range codepolicy.AllowedImports {
		if p == path {
			return codepolicy.PackageName(p), true
		}
	}
	return "", false
}
