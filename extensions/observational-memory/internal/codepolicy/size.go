package codepolicy

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
)

// callScope is what the array-size rule can see of a call: the constants and types it declares,
// at any depth, and the types the live namespace already defines (Input.Types). It is best
// effort by design (docs/code-tool.md, "What is refused"): a constant from an earlier call or a
// kept unit is unknown, and so is a size computed at run time.
type callScope struct {
	consts map[string]constSpec
	types  map[string]ast.Expr
	// evaluating guards against a constant or type defined in terms of itself.
	evaluating map[string]bool
}

// constSpec is a declared constant's expression with the iota it was declared under.
type constSpec struct {
	expr ast.Expr
	iota int64
}

// maxSizeDepth bounds the nesting the size computation follows, so a deep type cannot make
// the check itself deep; a type nested further counts as unknown.
const maxSizeDepth = 64

func newCallScope(p Program, live map[string]string) *callScope {
	s := &callScope{consts: map[string]constSpec{}, types: map[string]ast.Expr{}, evaluating: map[string]bool{}}
	for _, c := range p.Chunks {
		ast.Inspect(c.node(), func(n ast.Node) bool {
			g, ok := n.(*ast.GenDecl)
			if !ok {
				return true
			}
			switch g.Tok {
			case token.CONST:
				var last []ast.Expr
				for i, spec := range g.Specs {
					v := spec.(*ast.ValueSpec)
					if len(v.Values) > 0 {
						last = v.Values
					}
					for j, name := range v.Names {
						if _, seen := s.consts[name.Name]; !seen && j < len(last) {
							s.consts[name.Name] = constSpec{expr: last[j], iota: int64(i)}
						}
					}
				}
			case token.TYPE:
				for _, spec := range g.Specs {
					t := spec.(*ast.TypeSpec)
					if _, seen := s.types[t.Name.Name]; !seen {
						s.types[t.Name.Name] = t.Type
					}
				}
			}
			return true
		})
	}
	// The live namespace's types, so an element type a kept unit or an earlier call defined
	// is measured rather than guessed; the call's own declaration wins (checkTypes refuses one
	// that differs).
	for name, src := range live {
		if _, seen := s.types[name]; seen {
			continue
		}
		if t := parseTypeSpec(name, src); t != nil {
			s.types[name] = t
		}
	}
	return s
}

// parseTypeSpec is the type expression of src's declaration of name, nil when src does not
// parse as one.
func parseTypeSpec(name, src string) ast.Expr {
	f, err := parser.ParseFile(token.NewFileSet(), "", declPrefix+src, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.TYPE {
			continue
		}
		for _, spec := range g.Specs {
			if t := spec.(*ast.TypeSpec); t.Name.Name == name {
				return t.Type
			}
		}
	}
	return nil
}

// arraySize refuses an array type whose constant size, in bytes, is above the result bound:
// such an array saves nothing a slice made at run time would not, and a huge one runs the child
// out of memory before any code runs.
func (s *callScope) arraySize(c *Chunk, a *ast.ArrayType) error {
	if a.Len == nil {
		return nil
	}
	if _, ok := a.Len.(*ast.Ellipsis); ok {
		return nil
	}
	size, ok := s.sizeOf(a, 0)
	if !ok {
		return nil
	}
	return sizeRefusal(c, a, "array", size)
}

// sizeRefusal refuses what, at n's position, when size is above the result bound in bytes.
func sizeRefusal(c *Chunk, n ast.Node, what string, size constant.Value) error {
	limit := constant.MakeInt64(om.RecallPageChars)
	if constant.Compare(size, token.GTR, limit) {
		return &Refusal{Rule: RuleArraySize, Line: c.line(n.Pos()), Reason: fmt.Sprintf("the %s takes %s bytes, above the bound of %d bytes (one result): use a slice made at run time, or a smaller array", what, size.ExactString(), om.RecallPageChars)}
	}
	return nil
}

// compositeLit refuses a literal whose length is set by its elements, not its type (an [...]
// array or a slice), when its constant indices make it larger than the result bound in bytes:
// [...]byte{1 << 40: 1} declares an array as large as var b [1 << 40]byte. A literal with an
// explicit length is the array type's rule. Elements whose type the literal elides
// ([][]byte{{1 << 40: 1}}) are checked against the element type the outer literal gives them.
func (s *callScope) compositeLit(c *Chunk, lit *ast.CompositeLit) error {
	if lit.Type == nil {
		// Checked through the literal that holds it, which knows its type.
		return nil
	}
	return s.literal(c, lit, lit.Type, 0)
}

func (s *callScope) literal(c *Chunk, lit *ast.CompositeLit, typ ast.Expr, depth int) error {
	if depth > maxSizeDepth {
		return nil
	}
	typ = s.underlying(typ, depth+1)
	var elided func(e ast.Expr) ast.Expr
	switch t := typ.(type) {
	case *ast.ArrayType:
		_, ellipsis := t.Len.(*ast.Ellipsis)
		if t.Len == nil || ellipsis {
			if elem, ok := s.sizeOf(t.Elt, depth+1); ok {
				what := "slice literal"
				if ellipsis {
					what = "array literal"
				}
				n := s.literalLen(lit, depth+1)
				if err := sizeRefusal(c, lit, what, constant.BinaryOp(n, token.MUL, elem)); err != nil {
					return err
				}
			}
		}
		elided = func(ast.Expr) ast.Expr { return t.Elt }
	case *ast.MapType:
		elided = func(e ast.Expr) ast.Expr {
			if e == nil {
				return t.Value
			}
			return t.Key
		}
	default:
		return nil
	}
	for _, e := range lit.Elts {
		key, value := ast.Expr(nil), e
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			key, value = kv.Key, kv.Value
			if inner, ok := key.(*ast.CompositeLit); ok && inner.Type == nil {
				if err := s.literal(c, inner, elided(key), depth+1); err != nil {
					return err
				}
			}
		}
		inner, ok := value.(*ast.CompositeLit)
		if ok && inner.Type == nil {
			if err := s.literal(c, inner, elided(nil), depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// literalLen is the length an [...] array or slice literal has: one past its highest index,
// where a keyed element sets the index and an unkeyed one takes the next. An index the call
// does not show as a constant stops the count, so the length is never more than the real one.
func (s *callScope) literalLen(lit *ast.CompositeLit, depth int) constant.Value {
	one := constant.MakeInt64(1)
	next, length := constant.MakeInt64(0), constant.MakeInt64(0)
	for _, e := range lit.Elts {
		if kv, ok := e.(*ast.KeyValueExpr); ok {
			idx, ok := s.constInt(kv.Key, 0, depth)
			if !ok || constant.Sign(idx) < 0 {
				break
			}
			next = idx
		}
		next = constant.BinaryOp(next, token.ADD, one)
		if constant.Compare(next, token.GTR, length) {
			length = next
		}
	}
	return length
}

// underlying follows a type name the scope knows to its declaration, so a literal of a declared
// slice or array type is measured; anything else is returned as it is.
func (s *callScope) underlying(e ast.Expr, depth int) ast.Expr {
	for ; depth <= maxSizeDepth; depth++ {
		switch t := e.(type) {
		case *ast.ParenExpr:
			e = t.X
			continue
		case *ast.Ident:
			if decl, ok := s.types[t.Name]; ok {
				e = decl
				continue
			}
		}
		return e
	}
	return e
}

// The sizes of the predeclared types on a 64-bit machine.
var basicSizes = map[string]int64{
	"bool": 1, "int8": 1, "uint8": 1, "byte": 1,
	"int16": 2, "uint16": 2,
	"int32": 4, "uint32": 4, "rune": 4, "float32": 4,
	"int64": 8, "uint64": 8, "float64": 8, "complex64": 8, "int": 8, "uint": 8, "uintptr": 8,
	"complex128": 16, "string": 16, "error": 16, "any": 16,
}

// sizeOf is a type's size in bytes, as far as the call and the live namespace show it: a known
// array's length times its element, a struct's fields summed (alignment aside), a pointer, map,
// channel or func a word, and a type of an allow-listed package or om one byte (none of them
// exports a zero-size type). So the size is never more than the real one, and the rule refuses
// nothing that fits. ok is false when the size is unknown: an array's length that is not a
// constant the call shows, a name neither the call nor the live namespace declares (a type
// parameter, say, which may be zero-size), or a type nested past maxSizeDepth.
func (s *callScope) sizeOf(e ast.Expr, depth int) (constant.Value, bool) {
	one := constant.MakeInt64(1)
	if depth > maxSizeDepth {
		return nil, false
	}
	switch t := e.(type) {
	case *ast.ParenExpr:
		return s.sizeOf(t.X, depth+1)
	case *ast.Ident:
		if n, ok := basicSizes[t.Name]; ok {
			return constant.MakeInt64(n), true
		}
		decl, ok := s.types[t.Name]
		if !ok || s.evaluating["type "+t.Name] {
			return nil, false
		}
		s.evaluating["type "+t.Name] = true
		defer delete(s.evaluating, "type "+t.Name)
		return s.sizeOf(decl, depth+1)
	case *ast.SelectorExpr:
		return one, true
	case *ast.StarExpr, *ast.MapType, *ast.ChanType, *ast.FuncType:
		return constant.MakeInt64(8), true
	case *ast.InterfaceType:
		return constant.MakeInt64(16), true
	case *ast.ArrayType:
		if t.Len == nil {
			return constant.MakeInt64(24), true
		}
		if _, ok := t.Len.(*ast.Ellipsis); ok {
			// The length is the literal's (compositeLit), not the type's.
			return nil, false
		}
		n, ok := s.constInt(t.Len, 0, depth+1)
		if !ok || constant.Sign(n) < 0 {
			return nil, false
		}
		elem, ok := s.sizeOf(t.Elt, depth+1)
		if !ok {
			return nil, false
		}
		return constant.BinaryOp(n, token.MUL, elem), true
	case *ast.StructType:
		total := constant.MakeInt64(0)
		for _, f := range t.Fields.List {
			size, ok := s.sizeOf(f.Type, depth+1)
			if !ok {
				return nil, false
			}
			count := int64(len(f.Names))
			if count == 0 {
				count = 1
			}
			total = constant.BinaryOp(total, token.ADD, constant.BinaryOp(size, token.MUL, constant.MakeInt64(count)))
		}
		return total, true
	}
	// A generic instantiation, say, whose size the syntax does not show.
	return nil, false
}

// constInt evaluates an integer constant expression from literals and the call's constants;
// ok is false for anything else.
func (s *callScope) constInt(e ast.Expr, iota int64, depth int) (constant.Value, bool) {
	if depth > maxSizeDepth {
		return nil, false
	}
	v, ok := s.constValue(e, iota, depth)
	if !ok {
		return nil, false
	}
	v = constant.ToInt(v)
	if v.Kind() != constant.Int {
		return nil, false
	}
	return v, true
}

func (s *callScope) constValue(e ast.Expr, iota int64, depth int) (constant.Value, bool) {
	if depth > maxSizeDepth {
		return nil, false
	}
	switch t := e.(type) {
	case *ast.BasicLit:
		v := constant.MakeFromLiteral(t.Value, t.Kind, 0)
		return v, v.Kind() != constant.Unknown
	case *ast.ParenExpr:
		return s.constValue(t.X, iota, depth+1)
	case *ast.Ident:
		if t.Name == "iota" {
			return constant.MakeInt64(iota), true
		}
		spec, ok := s.consts[t.Name]
		if !ok || s.evaluating["const "+t.Name] {
			return nil, false
		}
		s.evaluating["const "+t.Name] = true
		defer delete(s.evaluating, "const "+t.Name)
		return s.constValue(spec.expr, spec.iota, depth+1)
	case *ast.UnaryExpr:
		x, ok := s.constValue(t.X, iota, depth+1)
		if !ok {
			return nil, false
		}
		switch t.Op {
		case token.ADD, token.SUB, token.XOR:
			if x.Kind() != constant.Int && t.Op == token.XOR {
				return nil, false
			}
			return constant.UnaryOp(t.Op, x, 0), true
		}
	case *ast.BinaryExpr:
		x, ok := s.constValue(t.X, iota, depth+1)
		if !ok {
			return nil, false
		}
		y, ok := s.constValue(t.Y, iota, depth+1)
		if !ok {
			return nil, false
		}
		switch t.Op {
		case token.SHL, token.SHR:
			shift, exact := constant.Uint64Val(constant.ToInt(y))
			// A shift past the bound's bit length is above it already; a cap keeps the
			// arithmetic small.
			if !exact || shift > 1<<10 {
				if t.Op == token.SHR {
					return constant.MakeInt64(0), true
				}
				shift = 1 << 10
			}
			x = constant.ToInt(x)
			if x.Kind() != constant.Int {
				return nil, false
			}
			return constant.Shift(x, t.Op, uint(shift)), true
		case token.ADD, token.SUB, token.MUL, token.AND, token.OR, token.XOR, token.AND_NOT, token.REM:
			if (t.Op != token.ADD && t.Op != token.SUB && t.Op != token.MUL) && (constant.ToInt(x).Kind() != constant.Int || constant.ToInt(y).Kind() != constant.Int) {
				return nil, false
			}
			if t.Op == token.REM && constant.Sign(y) == 0 {
				return nil, false
			}
			return constant.BinaryOp(x, t.Op, y), true
		case token.QUO:
			if constant.Sign(y) == 0 {
				return nil, false
			}
			if x.Kind() == constant.Int && y.Kind() == constant.Int {
				return constant.BinaryOp(x, token.QUO_ASSIGN, y), true
			}
			return constant.BinaryOp(x, token.QUO, y), true
		}
	}
	return nil, false
}

// checkTypes refuses a type declared twice with different sources, in the call or against the
// live namespace: the interpreter keeps the first struct silently, so new fields would be
// unknown.
func checkTypes(p Program, live map[string]string) error {
	seen := map[string]string{}
	for i := range p.Chunks {
		c := &p.Chunks[i]
		g, ok := c.decl.(*ast.GenDecl)
		if !ok || g.Tok != token.TYPE {
			continue
		}
		for _, spec := range g.Specs {
			t := spec.(*ast.TypeSpec)
			src := tokensOf(renderSpec(c.fset, token.TYPE, t))
			name := t.Name.Name
			before, inCall := seen[name]
			if !inCall {
				if prior, ok := live[name]; ok {
					before, inCall = normalizeType(name, prior), true
				}
			}
			if inCall && before != src {
				return &Refusal{Rule: RuleTypeRedefinition, Name: name, Line: c.line(t.Pos()), Reason: fmt.Sprintf("the type %s is already defined differently, and the interpreter would keep the first definition silently: forget it first, or choose another name", name)}
			}
			seen[name] = src
		}
	}
	return nil
}

// tokensOf is src's tokens, comments and layout aside, so two layouts of one declaration
// compare equal.
func tokensOf(src string) string {
	fset := token.NewFileSet()
	f := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(f, []byte(src), func(token.Position, string) {}, 0)
	var b strings.Builder
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			return b.String()
		}
		if tok == token.SEMICOLON && lit != ";" {
			continue
		}
		if lit == "" {
			lit = tok.String()
		}
		b.WriteString(lit)
		b.WriteByte(' ')
	}
}

// renderSpec prints one spec as a declaration of its own, comments aside.
func renderSpec(fset *token.FileSet, tok token.Token, spec ast.Spec) string {
	var b bytes.Buffer
	if err := printer.Fprint(&b, fset, &ast.GenDecl{Tok: tok, Specs: []ast.Spec{spec}}); err != nil {
		return ""
	}
	return b.String()
}

// normalizeType reduces a type's stored source to the tokens of its spec, as checkTypes reduces
// the call's, so two layouts of one type compare equal; a source that does not parse as that
// type compares as its own tokens.
func normalizeType(name, src string) string {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", declPrefix+src, parser.SkipObjectResolution)
	if err != nil {
		return src
	}
	for _, d := range f.Decls {
		g, ok := d.(*ast.GenDecl)
		if !ok || g.Tok != token.TYPE {
			continue
		}
		for _, spec := range g.Specs {
			if t := spec.(*ast.TypeSpec); t.Name.Name == name {
				return tokensOf(renderSpec(fset, token.TYPE, t))
			}
		}
	}
	return tokensOf(src)
}
