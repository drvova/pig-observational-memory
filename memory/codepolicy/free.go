package codepolicy

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"slices"
	"strconv"
	"strings"
)

// FreeNames are the identifiers a unit's source refers to but does not declare, sorted and
// unique (docs/code-tool.md, "What is a definition"): what the body of a function or method, a
// type's field and parameter types, or a constant's expression names from outside the unit.
// Parameters, results, receivers, type parameters, locals, labels, struct fields and interface
// methods are declared by the unit, and a selector's name (b.Twice, strings.Fields) is never
// free, only what it selects from. A composite literal's bare key is not collected: the parser
// cannot tell a struct field from a map key there, so a key naming a variable is caught at
// replay as a load error rather than here. The unit's own name is declared, so a recursive
// function refers to nothing. A source that does not parse returns nil.
func FreeNames(src string) []string {
	f, err := parser.ParseFile(token.NewFileSet(), "", "package p;"+src, 0)
	if err != nil {
		return nil
	}
	var out []string
	for _, id := range f.Unresolved {
		if id.Name != "_" && !slices.Contains(out, id.Name) {
			out = append(out, id.Name)
		}
	}
	slices.Sort(out)
	return out
}

// Seeded reports whether a free name is one every namespace provides: an owned name
// (kernel.OwnsCode: a predeclared identifier, a seeded function, an allow-listed package, om)
// or the om package itself. A unit may refer to these without keeping them.
func Seeded(name string) bool {
	if name == OMImport {
		return true
	}
	_, ok := ownsCode(name)
	return ok
}

// ReceiverType is the type a method unit's name is stored under (Box for Box.Twice), "" for a
// unit that is not a method.
func ReceiverType(name string) string {
	if i := strings.IndexByte(name, '.'); i > 0 {
		return name[:i]
	}
	return ""
}

// ForgottenStub is the source that shadows a forgotten function in the live namespace until its
// next restart (docs/code-tool.md, "Lifetime", option B): the same signature with a body that
// panics naming it, so a call to it fails at once while the conversation's variables survive.
// ok is false for anything but a plain function (a method, type or constant cannot be stubbed,
// so forgetting one restarts the namespace).
func ForgottenStub(name, src string) (string, bool) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", "package p;"+src, parser.SkipObjectResolution)
	if err != nil || len(f.Decls) != 1 {
		return "", false
	}
	d, ok := f.Decls[0].(*ast.FuncDecl)
	if !ok || d.Recv != nil || d.Name.Name != name {
		return "", false
	}
	why := name + " was forgotten: it is no longer kept, and calls to it fail until it is defined again"
	d.Doc = nil
	d.Body = &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(why)}}}}}}
	var b strings.Builder
	if err := printer.Fprint(&b, fset, d); err != nil {
		return "", false
	}
	return b.String(), true
}
