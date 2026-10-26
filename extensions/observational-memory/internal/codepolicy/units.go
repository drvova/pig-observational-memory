package codepolicy

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"strconv"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
)

// UnitKind is what a unit defines.
type UnitKind string

const (
	UnitFunc   UnitKind = "func"
	UnitMethod UnitKind = "method"
	UnitType   UnitKind = "type"
	UnitConst  UnitKind = "const"
)

// Unit is one definition that can be kept (docs/code-tool.md, "What is a definition"): one
// bound name with a source that stands alone.
type Unit struct {
	// Name is f for a function, Recv.Name for a method (Box.Twice), and the name of a type or
	// constant.
	Name string
	Kind UnitKind
	// Src is the unit's source: a function or method as written, a type or constant spec as a
	// declaration of its own.
	Src string
	// Chunk is the index of the declaration chunk it came from in Program.Chunks.
	Chunk int
	// Line is its line in the call's own numbering.
	Line int
}

// Skipped is a bound name a declaration chunk defines that is never kept, with the reason.
type Skipped struct {
	Name, Reason string
	Line         int
}

// ReasonVariable is why a variable is never kept.
const ReasonVariable = "a variable is never kept: it lives in the namespace for this conversation (a function literal to keep is written as a func declaration)"

// Units splits the declaration chunks of p into one unit per bound name: a function, a method,
// and each spec of a type or constant declaration, split out of grouped declarations and
// rendered alone. A constant declared under iota, or repeating the expression before it, is
// rendered with that iota's value, so it means alone what it meant in its group. Variables are
// returned as skipped.
func (p Program) Units() (units []Unit, skipped []Skipped) {
	for i := range p.Chunks {
		c := &p.Chunks[i]
		if c.Kind != Declaration {
			continue
		}
		switch d := c.decl.(type) {
		case *ast.FuncDecl:
			u := Unit{Name: d.Name.Name, Kind: UnitFunc, Src: c.Src, Chunk: i, Line: c.line(d.Pos())}
			if d.Recv != nil && len(d.Recv.List) == 1 {
				u.Name, u.Kind = receiverTypeName(d.Recv.List[0].Type)+"."+d.Name.Name, UnitMethod
			}
			units = append(units, u)
		case *ast.GenDecl:
			alone := len(d.Specs) == 1 && !d.Lparen.IsValid()
			switch d.Tok {
			case token.VAR:
				for _, spec := range d.Specs {
					for _, n := range spec.(*ast.ValueSpec).Names {
						if n.Name != "_" {
							skipped = append(skipped, Skipped{Name: n.Name, Reason: ReasonVariable, Line: c.line(n.Pos())})
						}
					}
				}
			case token.TYPE:
				for _, spec := range d.Specs {
					t := spec.(*ast.TypeSpec)
					src := c.Src
					if !alone {
						src = renderSpec(c.fset, token.TYPE, t)
					}
					units = append(units, Unit{Name: t.Name.Name, Kind: UnitType, Src: src, Chunk: i, Line: c.line(t.Pos())})
				}
			case token.CONST:
				var typ ast.Expr
				var values []ast.Expr
				for si, spec := range d.Specs {
					v := spec.(*ast.ValueSpec)
					if len(v.Values) > 0 {
						typ, values = v.Type, v.Values
					}
					for ni, n := range v.Names {
						if n.Name == "_" || ni >= len(values) {
							continue
						}
						src := c.Src
						if !alone || len(v.Names) > 1 {
							src = renderConst(c.fset, n.Name, typ, values[ni], si)
						}
						units = append(units, Unit{Name: n.Name, Kind: UnitConst, Src: src, Chunk: i, Line: c.line(n.Pos())})
					}
				}
			}
		}
	}
	return units, skipped
}

// renderConst renders one constant as a declaration of its own, with iota replaced by the value
// it had at index of its group. iota is owned, so no code can rebind it, and the replacement
// cannot change what another name means.
func renderConst(fset *token.FileSet, name string, typ, value ast.Expr, index int) string {
	expr := render(fset, value)
	if mentionsIota(value) {
		if fresh, err := parser.ParseExpr(expr); err == nil {
			ast.Inspect(fresh, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && id.Name == "iota" {
					id.Name = strconv.Itoa(index)
				}
				return true
			})
			expr = render(token.NewFileSet(), fresh)
		}
	}
	if typ != nil {
		return "const " + name + " " + render(fset, typ) + " = " + expr
	}
	return "const " + name + " = " + expr
}

func mentionsIota(e ast.Expr) bool {
	found := false
	ast.Inspect(e, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok && id.Name == "iota" {
			found = true
		}
		return !found
	})
	return found
}

func render(fset *token.FileSet, n ast.Node) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, fset, n); err != nil {
		return ""
	}
	return b.String()
}

// receiverTypeName is the type a method's receiver names, pointer and type parameters aside.
func receiverTypeName(e ast.Expr) string {
	for {
		switch t := e.(type) {
		case *ast.StarExpr:
			e = t.X
		case *ast.ParenExpr:
			e = t.X
		case *ast.IndexExpr:
			e = t.X
		case *ast.IndexListExpr:
			e = t.X
		case *ast.Ident:
			return t.Name
		default:
			return "?"
		}
	}
}

// CheckUnit is the code policy's part of the load-time check of a stored unit
// (docs/code-tool.md, "Load-time check"): the source is at most one recall page, parses as
// exactly one function, method, type or constant spec bound to name, and passes the whole
// policy. Free identifiers, the digest and provenance are checked by the store.
func CheckUnit(name, src string) error {
	if n := utf8.RuneCountInString(src); n > om.RecallPageChars {
		return &Refusal{Rule: RuleUnit, Name: name, Reason: fmt.Sprintf("the unit %s is %d characters, and a kept unit is at most %d (one recall page, so kept(%s) shows it whole)", name, n, om.RecallPageChars, strconv.Quote(name))}
	}
	p, err := Check(Input{Code: src})
	if err != nil {
		return err
	}
	units, skipped := p.Units()
	if len(p.Chunks) != 1 || p.Chunks[0].Kind != Declaration || len(skipped) > 0 || len(units) != 1 {
		return &Refusal{Rule: RuleUnit, Name: name, Reason: fmt.Sprintf("the unit %s is not exactly one function, method, type or constant declaration", name)}
	}
	if units[0].Name != name {
		return &Refusal{Rule: RuleUnit, Name: name, Reason: fmt.Sprintf("the unit stored as %s defines %s", name, units[0].Name)}
	}
	return nil
}

// Names are the names a declaration chunk binds: a function, a method as Recv.Name, and every
// type, constant and variable it declares; nil for a statement or an import. The child keeps the
// earlier definition of each, to restore it when the chunk's group fails to compile.
func (c Chunk) Names() []string {
	switch d := c.decl.(type) {
	case *ast.FuncDecl:
		if d.Recv != nil && len(d.Recv.List) == 1 {
			return []string{receiverTypeName(d.Recv.List[0].Type) + "." + d.Name.Name}
		}
		return []string{d.Name.Name}
	case *ast.GenDecl:
		if d.Tok == token.IMPORT {
			return nil
		}
		var out []string
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.TypeSpec:
				out = append(out, s.Name.Name)
			case *ast.ValueSpec:
				for _, n := range s.Names {
					if n.Name != "_" {
						out = append(out, n.Name)
					}
				}
			}
		}
		return out
	}
	return nil
}
