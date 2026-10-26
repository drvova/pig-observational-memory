package codepolicy

import (
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"strings"
)

// Kind is what a chunk of a call's code is.
type Kind int

const (
	// Statement is a chunk the interpreter runs inside its main function: a statement, an
	// expression, or a function literal.
	Statement Kind = iota
	// Declaration is a chunk parsed as a file-level declaration: a func, method, type, const or
	// var declaration.
	Declaration
	// Import is an import declaration of allow-listed paths only: a no-op, since every
	// allow-listed package is pre-imported under its own name.
	Import
)

// String names the kind for messages and tests.
func (k Kind) String() string {
	switch k {
	case Declaration:
		return "declaration"
	case Import:
		return "import"
	}
	return "statement"
}

// Chunk is one top-level unit of a call's code, as Split cut it.
type Chunk struct {
	Kind Kind
	// Src is the chunk's text, trimmed, without its closing semicolon.
	Src string
	// Line is the line of its first token in the call's own numbering, from 1.
	Line int

	fset *token.FileSet
	// base is the call's line of the parsed text's first line: the wrapper and the chunk's
	// first line share line 1.
	base int
	decl ast.Decl // a Declaration or Import chunk
	stmt ast.Stmt // a Statement chunk
}

// node is the chunk's syntax tree.
func (c *Chunk) node() ast.Node {
	if c.decl != nil {
		return c.decl
	}
	return c.stmt
}

// line maps a position of the chunk's parsed text to the call's own line numbering.
func (c *Chunk) line(pos token.Pos) int {
	if !pos.IsValid() {
		return c.Line
	}
	return c.base + c.fset.Position(pos).Line - 1
}

// Program is a call's code cut into chunks, in order.
type Program struct {
	Chunks []Chunk
	// LastExpr reports whether the last chunk is an expression statement, whose value the
	// child captures and renders.
	LastExpr bool
}

// Group is a run of consecutive chunks of one kind, which the child evaluates as one.
type Group struct {
	Kind Kind
	// Src is the chunks' texts joined by newlines.
	Src string
	// Line is the first chunk's line.
	Line int
	// Chunks are the indexes of the group's chunks in Program.Chunks.
	Chunks []int
}

// Groups joins consecutive chunks of one kind, in order. Import chunks are no-ops and are left
// out.
func (p Program) Groups() []Group {
	var out []Group
	for i, c := range p.Chunks {
		if c.Kind == Import {
			continue
		}
		if n := len(out); n > 0 && out[n-1].Kind == c.Kind {
			out[n-1].Src += "\n" + c.Src
			out[n-1].Chunks = append(out[n-1].Chunks, i)
			continue
		}
		out = append(out, Group{Kind: c.Kind, Src: c.Src, Line: c.Line, Chunks: []int{i}})
	}
	return out
}

// SyntaxError is code that does not parse: nothing it holds can be checked, so nothing runs.
type SyntaxError struct {
	// Line is the line in the call's own numbering, from 1.
	Line int
	Msg  string
}

func (e *SyntaxError) Error() string {
	return "line " + itoa(e.Line) + ": syntax error: " + e.Msg
}

// The prefixes a chunk is parsed under: a declaration as a file, a statement as the body of a
// function. Each is one line with no newline, so the chunk's first line is the parsed text's
// first line.
const (
	declPrefix = "package p;"
	stmtPrefix = "package p;func _(){"
	stmtSuffix = "\n}"
)

// cut is one depth-0 semicolon, explicit or inserted: the chunk before it ends at end, and the
// next starts at next.
type cut struct{ end, next int }

// Split cuts code into chunks at its depth-0 semicolons, explicit and inserted, but not inside
// the header of a for, if, switch or select statement, and parses each (docs/code-tool.md,
// "Splitting and the last value"). yaegi picks its parse mode from a snippet's first token, so a
// chunk that starts with a declaration keyword and parses as one file-level declaration is a
// Declaration (an Import for import), and anything else is a Statement, func literals
// included. The chunks the child runs are exactly the chunks the policy checks.
func Split(code string) (Program, error) {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(code))
	var errs scanner.ErrorList
	var s scanner.Scanner
	s.Init(file, []byte(code), func(pos token.Position, msg string) { errs.Add(pos, msg) }, 0)

	type depthZero struct {
		off int
		tok token.Token
	}
	var cuts []cut
	var tops []depthZero // tokens at depth 0, to find control-statement headers
	depth := 0
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		off := file.Offset(pos)
		if depth == 0 {
			tops = append(tops, depthZero{off, tok})
		}
		switch tok {
		case token.LPAREN, token.LBRACK, token.LBRACE:
			depth++
		case token.RPAREN, token.RBRACK, token.RBRACE:
			depth--
		case token.SEMICOLON:
			if depth == 0 {
				next := off
				if lit == ";" {
					next = off + 1
				}
				cuts = append(cuts, cut{end: off, next: next})
			}
		}
	}
	if errs.Len() > 0 {
		e := errs[0]
		return Program{}, &SyntaxError{Line: e.Pos.Line, Msg: e.Msg}
	}
	if n := len(cuts); n == 0 || cuts[n-1].next < len(code) {
		cuts = append(cuts, cut{end: len(code), next: len(code)})
	}
	lineOf := func(off int) int { return file.Position(file.Pos(off)).Line }
	header := func(from, to int) bool {
		for _, t := range tops {
			if t.off >= from && t.off < to {
				switch t.tok {
				case token.FOR, token.IF, token.SWITCH, token.SELECT:
					return true
				}
			}
		}
		return false
	}

	var prog Program
	start := 0
	for i := 0; i < len(cuts); i++ {
		text := code[start:cuts[i].end]
		firstOff, first := firstToken(text)
		if first == token.EOF {
			start = cuts[i].next
			continue
		}
		base := lineOf(start)
		chunk := Chunk{Line: lineOf(start + firstOff), base: base}
		var best error
		bestAt := -1
		switch first {
		case token.FUNC, token.TYPE, token.CONST, token.VAR, token.IMPORT:
			cf := token.NewFileSet()
			f, err := parser.ParseFile(cf, "", declPrefix+text, parser.ParseComments|parser.SkipObjectResolution)
			if err == nil && len(f.Decls) == 1 {
				chunk.fset, chunk.decl, chunk.Kind = cf, f.Decls[0], Declaration
				if g, ok := f.Decls[0].(*ast.GenDecl); ok && g.Tok == token.IMPORT {
					chunk.Kind = Import
				}
				chunk.Src = strings.TrimSpace(text)
				prog.Chunks = append(prog.Chunks, chunk)
				start = cuts[i].next
				continue
			}
			if first != token.FUNC {
				if err == nil {
					return Program{}, &SyntaxError{Line: chunk.Line, Msg: "expected one declaration"}
				}
				return Program{}, syntaxError(err, base, text)
			}
			// A func that is not one declaration may be a func literal statement; if it is
			// neither, the error that got further is reported.
			if err != nil {
				best, bestAt = err, errorOffset(err)-len(declPrefix)
			}
		}
		// A statement. Its end is the first cut at which it parses as one statement; only a
		// control statement's header holds depth-0 semicolons, so only then are later cuts
		// tried.
		last := i
		if header(start, cuts[i].end) {
			last = len(cuts) - 1
		}
		parsed := false
		for j := i; j <= last; j++ {
			text := code[start:cuts[j].end]
			cf := token.NewFileSet()
			f, err := parser.ParseFile(cf, "", stmtPrefix+text+stmtSuffix, parser.ParseComments|parser.SkipObjectResolution)
			if err == nil {
				body := f.Decls[0].(*ast.FuncDecl).Body.List
				if len(body) == 1 {
					chunk.fset, chunk.stmt, chunk.Kind = cf, body[0], Statement
					chunk.Src = strings.TrimSpace(text)
					prog.Chunks = append(prog.Chunks, chunk)
					start, i, parsed = cuts[j].next, j, true
					break
				}
				continue
			}
			at := errorOffset(err) - len(stmtPrefix)
			if at > bestAt {
				best, bestAt = err, at
			}
			// An error before the candidate's end is in its own text, which no later cut
			// can repair: only a header cut short fails at the end.
			if at >= 0 && at < len(strings.TrimRight(text, " \t\r\n")) {
				break
			}
		}
		if !parsed {
			if best == nil {
				return Program{}, &SyntaxError{Line: chunk.Line, Msg: "expected one statement"}
			}
			return Program{}, syntaxError(best, base, code[start:])
		}
	}
	if n := len(prog.Chunks); n > 0 && prog.Chunks[n-1].Kind == Statement {
		_, prog.LastExpr = prog.Chunks[n-1].stmt.(*ast.ExprStmt)
	}
	return prog, nil
}

// firstToken is the offset and kind of text's first token, comments skipped; token.EOF when it
// holds none.
func firstToken(text string) (int, token.Token) {
	fset := token.NewFileSet()
	f := fset.AddFile("", fset.Base(), len(text))
	var s scanner.Scanner
	s.Init(f, []byte(text), func(token.Position, string) {}, 0)
	for {
		pos, tok, lit := s.Scan()
		// A semicolon inserted at the end of the text is not a token of it.
		if tok == token.SEMICOLON && lit != ";" {
			continue
		}
		if tok == token.EOF {
			return len(text), token.EOF
		}
		return f.Offset(pos), tok
	}
}

// syntaxError maps a parse error of a chunk's parsed text to the call's own line numbering. An
// error found in the wrapper's closing line (an unfinished expression) is reported at the
// text's last line.
func syntaxError(err error, base int, text string) error {
	var list scanner.ErrorList
	if l, ok := err.(scanner.ErrorList); ok {
		list = l
	}
	if len(list) == 0 {
		return &SyntaxError{Line: base, Msg: err.Error()}
	}
	last := base + strings.Count(strings.TrimRight(text, " \t\r\n"), "\n")
	return &SyntaxError{Line: min(base+list[0].Pos.Line-1, last), Msg: list[0].Msg}
}

// errorOffset is how far into its text a parse got before its first error.
func errorOffset(err error) int {
	if l, ok := err.(scanner.ErrorList); ok && len(l) > 0 {
		return l[0].Pos.Offset
	}
	return -1
}

// DropsValue reports whether the chunk is an expression statement whose values, when it has
// any, are not the code's: before the call's last chunk they are dropped (and the result says
// how many such statements there were). A call of a fmt printer is not one: its byte count and
// error are not the code's value.
func (c Chunk) DropsValue() bool {
	s, ok := c.stmt.(*ast.ExprStmt)
	return ok && !isPrintCall(s.X)
}

// PrintsOnly reports whether src is one call of a fmt printer (fmt.Print, Printf, Println,
// Fprint, Fprintf, Fprintln), whose results are not the code's value.
func PrintsOnly(src string) bool {
	e, err := parser.ParseExpr(src)
	return err == nil && isPrintCall(e)
}

func isPrintCall(e ast.Expr) bool {
	call, ok := e.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "fmt" {
		return false
	}
	switch sel.Sel.Name {
	case "Print", "Printf", "Println", "Fprint", "Fprintf", "Fprintln":
		return true
	}
	return false
}
