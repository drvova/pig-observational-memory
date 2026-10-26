package codepolicy

import (
	"fmt"
	"go/ast"
	"go/token"
	"slices"
	"strconv"
	"strings"
)

// walker checks one chunk at every depth.
type walker struct {
	c     *Chunk
	scope *callScope
	// funcs counts the function bodies (declarations and literals) around the node being
	// visited: a defer inside one is allowed.
	funcs int
	stack []ast.Node
	err   error
}

func (w *walker) walk() error {
	if w.c.Kind == Import {
		return w.imports(w.c.decl.(*ast.GenDecl))
	}
	ast.Inspect(w.c.node(), w.visit)
	return w.err
}

// imports accepts an import of allow-listed paths as a no-op and refuses anything else.
func (w *walker) imports(g *ast.GenDecl) error {
	for _, spec := range g.Specs {
		s := spec.(*ast.ImportSpec)
		path, err := strconv.Unquote(s.Path.Value)
		if err != nil {
			path = s.Path.Value
		}
		line := w.c.line(s.Pos())
		if path != OMImport && !slices.Contains(AllowedImports, path) {
			return &Refusal{Rule: RuleImport, Name: path, Line: line, Reason: fmt.Sprintf("%s is not on the allow-list; code may use only %s and om, each already imported under its own name", strconv.Quote(path), strings.Join(AllowedImports, ", "))}
		}
		if s.Name != nil {
			return &Refusal{Rule: RuleImport, Name: s.Name.Name, Line: line, Reason: fmt.Sprintf("an import may not name %s as %s: every allowed package is already imported under its own name (%s)", strconv.Quote(path), s.Name.Name, PackageName(path))}
		}
	}
	return nil
}

func (w *walker) visit(n ast.Node) bool {
	if w.err != nil {
		return false
	}
	if n == nil {
		top := w.stack[len(w.stack)-1]
		w.stack = w.stack[:len(w.stack)-1]
		if isFuncBody(top) {
			w.funcs--
		}
		return true
	}
	w.stack = append(w.stack, n)
	if isFuncBody(n) {
		w.funcs++
	}
	w.err = w.check(n)
	return w.err == nil
}

// isFuncBody reports whether n holds a function body.
func isFuncBody(n ast.Node) bool {
	switch n.(type) {
	case *ast.FuncDecl, *ast.FuncLit:
		return true
	}
	return false
}

// check applies every rule that n itself can break.
func (w *walker) check(n ast.Node) error {
	line := w.c.line(n.Pos())
	switch n := n.(type) {
	case *ast.GoStmt:
		return &Refusal{Rule: RuleGoStatement, Name: "go", Line: line, Reason: "a go statement starts a goroutine that outlives its call, and two goroutines writing one map crash the namespace: do the work in the call itself"}
	case *ast.DeferStmt:
		// The node itself is on the stack, so a defer inside a function sees funcs > 0.
		if w.funcs == 0 {
			return &Refusal{Rule: RuleTopLevelDefer, Name: "defer", Line: line, Reason: "a defer outside a function runs again at the end of every later call: put it inside a function"}
		}
	case *ast.FuncDecl:
		if n.Recv == nil {
			if n.Name.Name == "init" || n.Name.Name == "main" {
				return &Refusal{Rule: RuleEntryPoint, Name: n.Name.Name, Line: w.c.line(n.Name.Pos()), Reason: fmt.Sprintf("the interpreter runs func %s on its own (init when defined, main after every later evaluation), so it would run at replay and repeat writes: give the function another name and call it", n.Name.Name)}
			}
			return w.bind(n.Name, "the function")
		}
		for _, f := range n.Recv.List {
			if err := w.bindAll(f.Names, "the receiver"); err != nil {
				return err
			}
			if err := w.bindReceiverTypeParams(f.Type); err != nil {
				return err
			}
		}
	case *ast.FuncType:
		// Only a function's own signature binds names. The parameter names of a signature
		// type (an interface method, a func type, a func-typed variable or parameter) are
		// never in scope, so they cannot shadow anything.
		if !w.signatureOfFunc(n) {
			return nil
		}
		for _, l := range []struct {
			fields *ast.FieldList
			what   string
		}{{n.TypeParams, "the type parameter"}, {n.Params, "the parameter"}, {n.Results, "the result"}} {
			if err := w.bindFields(l.fields, l.what); err != nil {
				return err
			}
		}
	case *ast.TypeSpec:
		if err := w.bind(n.Name, "the type"); err != nil {
			return err
		}
		return w.bindFields(n.TypeParams, "the type parameter")
	case *ast.ValueSpec:
		return w.bindAll(n.Names, "the declaration")
	case *ast.AssignStmt:
		if n.Tok == token.DEFINE {
			for _, e := range n.Lhs {
				if id, ok := e.(*ast.Ident); ok {
					if err := w.bind(id, "the short variable declaration"); err != nil {
						return err
					}
				}
			}
			return nil
		}
		for _, e := range n.Lhs {
			if err := w.target(e, "assigns to"); err != nil {
				return err
			}
		}
	case *ast.RangeStmt:
		for _, e := range []ast.Expr{n.Key, n.Value} {
			if e == nil {
				continue
			}
			if n.Tok == token.DEFINE {
				if id, ok := e.(*ast.Ident); ok {
					if err := w.bind(id, "the range variable"); err != nil {
						return err
					}
				}
				continue
			}
			if err := w.target(e, "assigns to"); err != nil {
				return err
			}
		}
	case *ast.IncDecStmt:
		return w.target(n.X, "changes")
	case *ast.UnaryExpr:
		if n.Op == token.AND {
			if root, through := rootIdent(n.X); root != nil && through && isPackage(root.Name) {
				return &Refusal{Rule: RulePackageAssignment, Name: root.Name, Line: line, Reason: fmt.Sprintf("the code takes the address of a value of the package %s, which every later call shares: copy the value into a variable of your own", root.Name)}
			}
		}
	case *ast.LabeledStmt:
		return w.bind(n.Label, "the label")
	case *ast.BranchStmt:
		if n.Label != nil {
			return w.bind(n.Label, "the "+n.Tok.String()+" label")
		}
	case *ast.ArrayType:
		return w.scope.arraySize(w.c, n)
	case *ast.CompositeLit:
		return w.scope.compositeLit(w.c, n)
	}
	return nil
}

// signatureOfFunc reports whether t, the node being visited, is the signature of a function
// declaration or literal, whose parameters, results and type parameters are in scope in its
// body.
func (w *walker) signatureOfFunc(t *ast.FuncType) bool {
	if len(w.stack) < 2 {
		return false
	}
	switch p := w.stack[len(w.stack)-2].(type) {
	case *ast.FuncDecl:
		return p.Type == t
	case *ast.FuncLit:
		return p.Type == t
	}
	return false
}

// bind refuses binding id when the kernel owns its name for code.
func (w *walker) bind(id *ast.Ident, what string) error {
	if id == nil || id.Name == "_" {
		return nil
	}
	if owner, owned := ownsCode(id.Name); owned {
		return ownedRefusal(w.c.line(id.Pos()), what, id.Name, owner)
	}
	return nil
}

func (w *walker) bindAll(ids []*ast.Ident, what string) error {
	for _, id := range ids {
		if err := w.bind(id, what); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) bindFields(l *ast.FieldList, what string) error {
	if l == nil {
		return nil
	}
	for _, f := range l.List {
		if err := w.bindAll(f.Names, what); err != nil {
			return err
		}
	}
	return nil
}

// bindReceiverTypeParams checks the type parameters a generic method's receiver binds
// (func (b Box[T]) M()).
func (w *walker) bindReceiverTypeParams(e ast.Expr) error {
	for {
		switch t := e.(type) {
		case *ast.StarExpr:
			e = t.X
			continue
		case *ast.ParenExpr:
			e = t.X
			continue
		case *ast.IndexExpr:
			if id, ok := t.Index.(*ast.Ident); ok {
				return w.bind(id, "the receiver's type parameter")
			}
		case *ast.IndexListExpr:
			for _, x := range t.Indices {
				if id, ok := x.(*ast.Ident); ok {
					if err := w.bind(id, "the receiver's type parameter"); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
}

// target refuses writing through a package (a selector on a package name, or an index, field
// or dereference of one) and assigning an owned name itself.
func (w *walker) target(e ast.Expr, verb string) error {
	root, through := rootIdent(e)
	if root == nil {
		return nil
	}
	line := w.c.line(e.Pos())
	if through && isPackage(root.Name) {
		return &Refusal{Rule: RulePackageAssignment, Name: root.Name, Line: line, Reason: fmt.Sprintf("the code %s a value of the package %s, which every later call shares: keep the value in a variable of your own", verb, root.Name)}
	}
	if !through {
		if owner, owned := ownsCode(root.Name); owned {
			return &Refusal{Rule: RuleOwnedName, Name: root.Name, Line: line, Reason: fmt.Sprintf("the code %s %s, which %s owns: use a variable of your own", verb, root.Name, owner)}
		}
	}
	return nil
}

// rootIdent is the identifier an assignable expression is rooted in, and whether the expression
// goes through it by a selector, index, slice or dereference rather than being it.
func rootIdent(e ast.Expr) (root *ast.Ident, through bool) {
	for {
		switch t := e.(type) {
		case *ast.Ident:
			return t, through
		case *ast.ParenExpr:
			e = t.X
			continue
		case *ast.SelectorExpr:
			e = t.X
		case *ast.IndexExpr:
			e = t.X
		case *ast.IndexListExpr:
			e = t.X
		case *ast.SliceExpr:
			e = t.X
		case *ast.StarExpr:
			e = t.X
		default:
			return nil, false
		}
		through = true
	}
}
