package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/scanner"
	"go/token"
	"io"
	"maps"
	"os"
	"reflect"
	"regexp"
	"runtime/debug"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing/fstest"
	"unicode/utf8"

	"github.com/traefik/yaegi/interp"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/codepolicy"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/sandbox/symbols"
)

// The child's exit statuses.
const (
	exitHostGone    = 3 // EOF on the protocol pipe: the extension went away
	exitWriteFailed = 4 // the protocol pipe broke while writing
	exitProtocol    = 5 // a host line over its bound, undecodable, or out of order
	exitSetup       = 6 // the interpreter could not be built
	exitRestore     = 7 // a failed declaration could not be restored
)

// yaegiEnv are the variables yaegi reads from the process environment; with the dot variables
// set it writes files and runs a command. The child's environment is empty anyway; these are
// unset again before the interpreter is built.
var yaegiEnv = []string{"YAEGI_AST_DOT", "YAEGI_CFG_DOT", "YAEGI_DOT_CMD", "YAEGI_NO_RUN", "YAEGI_SPECIAL_STDIO"}

// seedPath is the host package the seeded functions and the capture function are dot-imported
// from. It is not on the allow-list, so code cannot import it again.
const seedPath = "_omseed"

// childHooks let a test fixture change what one child does; the real modes pass nil.
type childHooks struct {
	// beforeEval sees every source the child is about to evaluate.
	beforeEval func(src string)
}

// child is a child's state: the namespace child's, or one detector run's (detector).
type child struct {
	hooks *childHooks
	// detector marks a detector child: its om table is the detector's (codepolicy.DetectorOMNames)
	// and it has no seeded store functions.
	detector bool
	out      io.Writer // the protocol pipe to the host

	sendMu  sync.Mutex
	control chan msg // init, run and define, in order
	replies chan msg // replies to om calls

	i      *interp.Interpreter
	stdout *boundedWriter // what interpreted code prints
	diag   *boundedWriter // the interpreter's own panic locations

	gen uint64
	// defs are the sources that defined each name, kept so a failed redefinition can be
	// restored.
	defs map[string]string

	// The call in flight; inCall is false while replaying and between calls.
	callMu   sync.Mutex
	inCall   bool
	call     uint64
	seq      uint64
	strs     map[string]string
	captured *capture

	// The rescue of a top-level declaration (rescue.go): in hold mode the capture function
	// keeps its values in heldNow rather than rendering them; held are the values the
	// held-value function reads back.
	holding bool
	heldNow []reflect.Value
	held    []reflect.Value

	// While discarding, the capture function counts a statement whose values the code
	// dropped (an expression statement before the last) rather than rendering them, and keeps
	// the failure among them (discardedFailure): discardedErrs holds their texts within
	// ResultChars together, discardedFailed counts them all.
	discarding      bool
	discarded       int
	discardedErrs   []string
	discardedFailed int
}

// capture is the last value the capture function rendered.
type capture struct {
	text string
	cut  bool
}

// namespaceMain is the namespace child's main: it never returns until the host goes away or
// the child must exit.
func namespaceMain(args []string, hooks *childHooks) int {
	return childMain(args, hooks, false)
}

// detectorMain is a detector child's main (docs/code-tool.md, "The detector om"): the same
// protocol, an interpreter built from one detector's source and none of the agent's units, a
// read-only om table and no seeded store functions. It answers one run and is killed.
func detectorMain(args []string, hooks *childHooks) int {
	return childMain(args, hooks, true)
}

// childMain is every child's main.
func childMain(args []string, hooks *childHooks, detector bool) int {
	debug.SetTraceback("none")
	oom := setOOMScore()
	if oom != oomSet {
		fmt.Fprintln(os.Stderr, "om sandbox: oom_score_adj:", oom)
	}
	for _, k := range yaegiEnv {
		_ = os.Unsetenv(k)
	}
	in, out, err := childPipes(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "om sandbox:", err)
		return exitSetup
	}
	c := &child{
		hooks:    hooks,
		detector: detector,
		out:      out,
		control:  make(chan msg, 1),
		replies:  make(chan msg, 1),
		stdout:   newBoundedWriter(ResultChars),
		diag:     newBoundedWriter(ResultChars),
		defs:     map[string]string{},
	}
	// The reader is its own goroutine, so the host's death ends the child even inside a
	// runaway loop.
	go c.readLoop(in)
	if err := c.build(); err != nil {
		fmt.Fprintln(os.Stderr, "om sandbox:", err)
		return exitSetup
	}
	omwire.Install(c.transport)

	first := <-c.control
	if first.T != tInit {
		fmt.Fprintln(os.Stderr, "om sandbox: expected init, got", first.T)
		return exitProtocol
	}
	c.gen = first.Gen
	loaded, failed := c.load(first.Components)
	c.send(msg{T: tReady, Gen: c.gen, Loaded: loaded, Failed: cutFailures(failed), OOM: oom})
	for m := range c.control {
		switch m.T {
		case tRun:
			res := c.run(m)
			c.send(res)
			if res.Exit {
				return exitRestore
			}
		case tDefine:
			loaded, failed := c.load(m.Components)
			c.send(msg{T: tReady, Gen: c.gen, Loaded: loaded, Failed: cutFailures(failed), OOM: oom})
		default:
			fmt.Fprintln(os.Stderr, "om sandbox: unexpected", m.T)
			return exitProtocol
		}
	}
	return exitHostGone
}

// readLoop reads the host's lines: replies go to the call waiting on them, the rest to the main
// loop. EOF means the extension went away, and the child exits.
func (c *child) readLoop(in io.Reader) {
	r := newLineReader(in, HostLineBytes)
	for {
		m, err := r.readMsg()
		if err != nil {
			if errors.Is(err, io.EOF) {
				os.Exit(exitHostGone)
			}
			fmt.Fprintln(os.Stderr, "om sandbox: reading the host:", err)
			os.Exit(exitProtocol)
		}
		if m.T == tReply {
			c.replies <- m
			continue
		}
		c.control <- m
	}
}

// send writes one line to the host. A result the host would refuse is replaced by an error
// result, which answers the same run. Any other message over the bound ends the child: a
// replacement would answer something the host did not ask, and leave the two out of step (an
// om call's line is bounded before it gets here, in transport). A broken pipe ends the child.
func (c *child) send(m msg) {
	b, err := encodeLine(m, ChildLineBytes)
	if err != nil && m.T == tResult {
		b, err = encodeLine(msg{T: tResult, Gen: m.Gen, Call: m.Call, Outcome: OutcomeRuntime, Err: "the child's message was over the protocol bound"}, ChildLineBytes)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "om sandbox: a", m.T, "message over the protocol bound")
		os.Exit(exitProtocol)
	}
	c.writeLine(b)
}

// writeLine writes one encoded line to the host; a broken pipe ends the child.
func (c *child) writeLine(b []byte) {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	if _, err := c.out.Write(b); err != nil {
		os.Exit(exitWriteFailed)
	}
}

// build makes the interpreter: stdin always empty, output bounded, no environment, arguments,
// source filesystem or GOPATH, and exactly the generated tables, the seeded functions and om
// pre-imported.
func (c *child) build() error {
	c.i = interp.New(interp.Options{
		Stdin:                strings.NewReader(""),
		Stdout:               c.stdout,
		Stderr:               c.diag,
		Env:                  []string{},
		Args:                 []string{},
		GoPath:               "/nonexistent",
		SourcecodeFilesystem: fstest.MapFS{},
	})
	if err := c.i.Use(exports(c)); err != nil {
		return err
	}
	var imports []string
	for _, p := range codepolicy.AllowedImports {
		imports = append(imports, fmt.Sprintf("import %q", p))
	}
	imports = append(imports, fmt.Sprintf("import %q", codepolicy.OMImport), fmt.Sprintf("import . %q", seedPath))
	for _, src := range imports {
		if _, err := c.i.Eval(src); err != nil {
			return fmt.Errorf("%s: %w", src, err)
		}
	}
	return nil
}

// exports are the interpreter's symbol tables: the generated ones, each a fresh map, the kept
// variable copies, the seeded package, and the interface mappings that let the fmt printers
// and the capture function call an interpreted String or Error method.
func exports(c *child) interp.Exports {
	ex := interp.Exports{}
	for k, table := range symbols.Symbols {
		m := make(map[string]reflect.Value, len(table))
		for name, v := range table {
			m[name] = v
		}
		ex[k] = m
	}
	for k, table := range symbols.Copies {
		for name, v := range table {
			ex[k][name] = v
		}
	}
	captureFn := reflect.ValueOf(c.captureValues)
	ex[seedPath+"/"+seedPath] = map[string]reflect.Value{
		"describe":             reflect.ValueOf(describe),
		"man":                  reflect.ValueOf(seedMan),
		"kept":                 reflect.ValueOf(seedKept),
		"forget":               reflect.ValueOf(seedForget),
		codepolicy.CaptureName: captureFn,
		codepolicy.HeldName:    reflect.ValueOf(c.heldValue),
	}
	if c.detector {
		// A detector reads, counts and returns: its om holds the detector's reads, options and
		// types only, and it has none of the namespace's seeded functions.
		omKey := codepolicy.OMImport + "/" + codepolicy.OMImport
		for name := range ex[omKey] {
			if !slices.Contains(codepolicy.DetectorOMNames, name) {
				delete(ex[omKey], name)
			}
		}
		for _, name := range codepolicy.SeededFuncs {
			delete(ex[seedPath+"/"+seedPath], name)
		}
	}
	ex["."] = map[string]reflect.Value{"MapTypes": reflect.ValueOf(mapTypes(captureFn))}
	return ex
}

// SeededSymbols are the names the child binds into every namespace beside the allow-listed
// packages: the om package, the seeded functions and the capture function.
func SeededSymbols() []string {
	names := []string{codepolicy.OMImport}
	for name := range exports(&child{})[seedPath+"/"+seedPath] {
		names = append(names, name)
	}
	return names
}

// Packages are the import paths the child's tables make importable, om included.
func Packages() []string {
	var out []string
	for k := range symbols.Symbols {
		out = append(out, k[:strings.LastIndex(k, "/")])
	}
	return out
}

// load evaluates saved components in order, each with its own recover, and reports which
// loaded and which failed.
func (c *child) load(comps []wireComponent) (loaded []int, failed []wireFailure) {
	for _, comp := range comps {
		var srcs []string
		for _, u := range comp.Units {
			srcs = append(srcs, u.Src)
		}
		src := strings.Join(srcs, "\n")
		if _, err := c.eval(src); err != nil {
			failed = append(failed, wireFailure{ID: comp.ID, Msg: err.Error()})
			continue
		}
		for _, u := range comp.Units {
			c.defs[u.Name] = u.Src
		}
		loaded = append(loaded, comp.ID)
	}
	return loaded, failed
}

// cutFailures cuts the failures' messages so all of them together fit one result.
func cutFailures(fs []wireFailure) []wireFailure {
	budget := ResultChars
	for i := range fs {
		fs[i].Msg, _ = cutChars(fs[i].Msg, budget)
		budget -= utf8.RuneCountInString(fs[i].Msg)
	}
	return fs
}

// The phases an evaluation fails in.
const (
	phaseCompile = "compile"
	phaseRun     = "run"
)

// evalError is a failed evaluation and its phase.
type evalError struct {
	phase string
	err   error
}

func (e *evalError) Error() string { return e.err.Error() }

// eval compiles and runs src. A panic while compiling (the interpreter's compiler can panic on
// some input) is a compile failure, and one while running is a runtime failure.
func (c *child) eval(src string) (res reflect.Value, err error) {
	if c.hooks != nil && c.hooks.beforeEval != nil {
		c.hooks.beforeEval(src)
	}
	phase := phaseCompile
	defer func() {
		if r := recover(); r != nil {
			err = &evalError{phase: phase, err: fmt.Errorf("panic: %v", r)}
		}
	}()
	prog, err := c.i.Compile(src)
	if err != nil {
		return res, &evalError{phase: phaseCompile, err: err}
	}
	phase = phaseRun
	res, err = c.i.Execute(prog)
	if err != nil {
		var p interp.Panic
		if errors.As(err, &p) {
			err = fmt.Errorf("panic: %v", p.Value)
		}
		return res, &evalError{phase: phaseRun, err: err}
	}
	return res, nil
}

// group is a run of chunks the child evaluates as one.
type group struct {
	decl    bool
	capture bool // the call's last chunk, an expression whose value is captured
	chunks  []int
}

// groups joins consecutive declaration chunks, so declarations in one call may refer to each
// other in any order. Each statement chunk is a group of its own, run one after the other as Go
// runs statements in sequence: a failure is then that one statement's, and a top-level
// declaration the interpreter cannot type from its expression is rescued alone (rescue.go). The
// call's last chunk, when it is an expression to capture, is a group of its own too.
func groups(chunks []wireChunk, last bool) []group {
	var out []group
	for i, ch := range chunks {
		decl := ch.Kind == kindDeclaration
		if last && i == len(chunks)-1 {
			out = append(out, group{capture: true, chunks: []int{i}})
			continue
		}
		if n := len(out); n > 0 && decl && out[n-1].decl {
			out[n-1].chunks = append(out[n-1].chunks, i)
			continue
		}
		out = append(out, group{decl: decl, chunks: []int{i}})
	}
	return out
}

// run evaluates one call: its groups in order, stopping at the first failure.
func (c *child) run(m msg) msg {
	c.callMu.Lock()
	c.inCall, c.call, c.seq, c.strs, c.captured = true, m.Call, 0, m.Strings, nil
	c.callMu.Unlock()
	c.discarded, c.discardedErrs, c.discardedFailed = 0, nil, 0
	defer func() {
		c.callMu.Lock()
		c.inCall, c.strs = false, nil
		c.callMu.Unlock()
	}()
	c.stdout.reset()
	c.diag.reset()

	mask := []byte(strings.Repeat(string(maskNotRun), len(m.Chunks)))
	res := msg{T: tResult, Gen: c.gen, Call: m.Call, Outcome: OutcomeOK}
	var errText string
	for _, g := range groups(m.Chunks, m.Last) {
		src, lines := groupSource(m.Chunks, g)
		if g.decl {
			before := map[string]string{}
			for _, ci := range g.chunks {
				if isVar(m.Chunks[ci].Src) {
					continue
				}
				for _, name := range m.Chunks[ci].Names {
					if src, ok := c.defs[name]; ok {
						before[name] = src
					}
				}
			}
			_, err := c.eval(src)
			if err != nil && len(g.chunks) == 1 && isVar(src) {
				// A top-level var the interpreter cannot type from its expression.
				switch rerr := c.rescueDefine(src); {
				case errors.Is(rerr, errNoRescue):
				default:
					err = rerr
				}
			}
			if err != nil {
				for _, ci := range g.chunks {
					mask[ci] = maskFailed
				}
				res.Outcome, errText = OutcomeCompile, mapLines(err.Error(), lines)
				var ee *evalError
				if errors.As(err, &ee) && ee.phase == phaseRun {
					res.Outcome = OutcomeRuntime
				}
				if err := c.restore(before); err != nil {
					errText += "\nthe earlier definitions could not be restored, so the namespace restarts from the saved definitions: " + err.Error()
					res.Exit = true
					break
				}
				if res.Outcome == OutcomeCompile {
					// The interpreter keeps what a failed group bound for the first time, half
					// defined: a sibling that was correct on its own, or a function calling
					// into the failed one, returns zero values when called. Each becomes a
					// stub that panics, so a call to it fails rather than answering wrong.
					stubbed, err := c.stubNew(m.Chunks, g, before)
					if err != nil {
						errText += "\n" + err.Error() + ": the namespace restarts from the saved definitions, so unsaved variables are gone"
						res.Exit = true
					} else if len(stubbed) > 0 {
						errText += "\n" + strings.Join(stubbed, ", ") + " did not compile with the rest of the declarations and panic when called, until they are defined again"
					}
				}
				break
			}
			for _, ci := range g.chunks {
				mask[ci] = maskCompiled
				if isVar(m.Chunks[ci].Src) {
					continue
				}
				for _, name := range m.Chunks[ci].Names {
					c.defs[name] = m.Chunks[ci].Src
				}
			}
			continue
		}
		var err error
		if g.capture {
			err = c.evalCapture(m.Chunks[g.chunks[0]].Src)
		} else if ch := m.Chunks[g.chunks[0]]; ch.Expr && c.compiles(wrapCapture(ch.Src)) {
			// An expression statement before the last whose values the code drops: it runs
			// wrapped, so the capture function counts it, and the result says so.
			c.discarding = true
			_, err = c.eval(wrapCapture(ch.Src))
			c.discarding = false
		} else {
			_, err = c.eval(src)
			var ee *evalError
			if errors.As(err, &ee) && ee.phase == phaseCompile {
				switch rerr := c.rescueDefine(src); {
				case errors.Is(rerr, errNoRescue):
				default:
					err = rerr
				}
			}
		}
		if err != nil {
			res.Outcome = OutcomeRuntime
			var ee *evalError
			if errors.As(err, &ee) && ee.phase == phaseCompile {
				res.Outcome = OutcomeCompile
			}
			errText = mapLines(err.Error(), lines)
			if res.Outcome == OutcomeRuntime {
				if where := panicLine(c.diag.String(), lines); where != "" {
					errText = where + ": " + errText
				}
			}
			break
		}
	}
	res.Compiled = string(mask)
	res.Discarded, res.DiscardedErrs, res.DiscardedFailed = c.discarded, c.discardedErrs, c.discardedFailed
	// Printed output, the last value and the error together fit one result; the error is kept
	// first, then the output, then the value.
	var cutErr, cutOut int
	errText, cutErr = cutChars(errText, ResultChars)
	out, cutOut := cutChars(c.stdout.String(), ResultChars-utf8.RuneCountInString(errText))
	res.Err, res.Out = errText, out
	res.Cut = cutErr + cutOut + c.stdout.dropped
	if c.captured != nil {
		val, cutVal := cutChars(c.captured.text, ResultChars-utf8.RuneCountInString(errText)-utf8.RuneCountInString(out))
		res.Val, res.HasVal = val, true
		res.Cut += cutVal
		if c.captured.cut && cutVal == 0 {
			res.Cut++
		}
	}
	return res
}

// wrapCapture wraps an expression in the capture function, closing the call right after the
// expression's last token, so a trailing comment cannot swallow it and no semicolon is inserted
// before it.
func wrapCapture(expr string) string {
	fset := token.NewFileSet()
	var s scanner.Scanner
	s.Init(fset.AddFile("", fset.Base(), len(expr)), []byte(expr), nil, scanner.ScanComments)
	end := len(expr)
	for {
		pos, tok, lit := s.Scan()
		if tok == token.EOF {
			break
		}
		if tok == token.COMMENT || (tok == token.SEMICOLON && lit == "\n") {
			continue
		}
		end = fset.Position(pos).Offset + len(tokenText(tok, lit))
	}
	return codepolicy.CaptureName + "(" + expr[:end] + ")" + expr[end:]
}

// tokenText is a token's source text.
func tokenText(tok token.Token, lit string) string {
	if lit != "" {
		return lit
	}
	return tok.String()
}

// isVar reports whether a declaration chunk declares variables. A variable's earlier source is
// never re-run to restore it: its initializer could call om.
func isVar(src string) bool {
	_, tok := firstTok(src)
	return tok == token.VAR
}

// firstTok is the first token of src.
func firstTok(src string) (int, token.Token) {
	fset := token.NewFileSet()
	var s scanner.Scanner
	s.Init(fset.AddFile("", fset.Base(), len(src)), []byte(src), nil, 0)
	pos, tok, _ := s.Scan()
	return fset.Position(pos).Offset, tok
}

// stubNew replaces each function a failed declaration group bound for the first time (its name
// not in before) with one of the same signature that panics, and returns their names. A
// variable's chunk is left alone, as restore leaves it.
//
// A method cannot be stubbed: the interpreter keeps a method's first definition, and a later
// one never takes effect (probed). A new method that shares the failed group with a function, a
// constant, a variable or another method then answers zero values when called, for good, so
// stubNew returns an error and the child restarts. A new method beside only type declarations
// fails when called (probed), and needs no restart.
func (c *child) stubNew(chunks []wireChunk, g group, before map[string]string) ([]string, error) {
	var methods []string
	others := 0
	for _, ci := range g.chunks {
		ch := chunks[ci]
		if _, tok := firstTok(ch.Src); tok == token.TYPE {
			continue
		}
		others++
		if len(ch.Names) == 1 && strings.Contains(ch.Names[0], ".") {
			if _, ok := before[ch.Names[0]]; !ok {
				methods = append(methods, ch.Names[0])
			}
		}
	}
	if len(methods) > 0 && others > 1 {
		return nil, fmt.Errorf("%s did not compile with the rest of the declarations and cannot be made unusable (the interpreter keeps a method's first definition)", strings.Join(methods, ", "))
	}
	var stubbed []string
	for _, ci := range g.chunks {
		ch := chunks[ci]
		if isVar(ch.Src) || len(ch.Names) != 1 || strings.Contains(ch.Names[0], ".") {
			continue
		}
		name := ch.Names[0]
		if _, ok := before[name]; ok {
			continue
		}
		stub, ok := stubSource(ch.Src, name)
		if !ok {
			continue
		}
		if _, err := c.eval(stub); err != nil {
			return stubbed, fmt.Errorf("%s: %w", name, err)
		}
		stubbed = append(stubbed, name)
	}
	return stubbed, nil
}

// stubSource is a function declaration's source with its body replaced by a panic naming it;
// ok is false when src is not one function declaration.
func stubSource(src, name string) (string, bool) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", "package p;"+src, parser.SkipObjectResolution)
	if err != nil || len(f.Decls) != 1 {
		return "", false
	}
	d, ok := f.Decls[0].(*ast.FuncDecl)
	if !ok {
		return "", false
	}
	why := name + " did not compile: its declaration failed in an earlier call, so it is unusable until it is defined again"
	d.Doc = nil
	d.Body = &ast.BlockStmt{List: []ast.Stmt{&ast.ExprStmt{X: &ast.CallExpr{Fun: ast.NewIdent("panic"), Args: []ast.Expr{&ast.BasicLit{Kind: token.STRING, Value: strconv.Quote(why)}}}}}}
	var b strings.Builder
	if err := printer.Fprint(&b, fset, d); err != nil {
		return "", false
	}
	return b.String(), true
}

// restore re-evaluates the sources that defined names before a failed group, in name order.
func (c *child) restore(before map[string]string) error {
	seen := map[string]bool{}
	for _, name := range slices.Sorted(maps.Keys(before)) {
		src := before[name]
		if seen[src] {
			continue
		}
		seen[src] = true
		if _, err := c.eval(src); err != nil {
			return err
		}
	}
	return nil
}

// evalCapture runs the last expression wrapped in the capture function. A void expression does
// not compile wrapped; it then runs plain and reports no value.
func (c *child) evalCapture(expr string) error {
	wrapped := wrapCapture(expr)
	if c.hooks != nil && c.hooks.beforeEval != nil {
		c.hooks.beforeEval(wrapped)
	}
	if ok := c.compiles(wrapped); ok {
		_, err := c.eval(wrapped)
		return err
	}
	_, err := c.eval(expr)
	return err
}

// compiles reports whether src compiles, recovering a compiler panic.
func (c *child) compiles(src string) (ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	_, err := c.i.Compile(src)
	return err == nil
}

// groupSource joins a group's chunks by newlines, and returns for each line of the joined text
// its line in the call's own numbering.
func groupSource(chunks []wireChunk, g group) (string, []int) {
	var srcs []string
	var lines []int
	for _, ci := range g.chunks {
		ch := chunks[ci]
		srcs = append(srcs, ch.Src)
		for k := 0; k <= strings.Count(ch.Src, "\n"); k++ {
			lines = append(lines, ch.Line+k)
		}
	}
	return strings.Join(srcs, "\n"), lines
}

// position matches an interpreter position: line:column, after an optional file name.
var position = regexp.MustCompile(`(?:_\.go:)?(\d+):(\d+): `)

// mapLines rewrites the interpreter's positions in a message as lines of the call.
func mapLines(text string, lines []int) string {
	return position.ReplaceAllStringFunc(text, func(p string) string {
		sub := position.FindStringSubmatch(p)
		n, err := strconv.Atoi(sub[1])
		if err != nil || n < 1 || n > len(lines) {
			return ""
		}
		return "line " + strconv.Itoa(lines[n-1]) + ": "
	})
}

// panicLine is where the interpreter reported a panic, as a line of the call.
func panicLine(diag string, lines []int) string {
	for _, l := range strings.Split(diag, "\n") {
		if sub := position.FindStringSubmatch(l); sub != nil && strings.Contains(l, "panic") {
			if n, err := strconv.Atoi(sub[1]); err == nil && n >= 1 && n <= len(lines) {
				return "line " + strconv.Itoa(lines[n-1])
			}
		}
	}
	return ""
}

// transport answers every om call the interpreted code makes: Str from the call's strings, the
// rest by one call to the host and its reply.
func (c *child) transport(method string, out any, args ...any) error {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	if method == omwire.MStr {
		name, _ := args[0].(string)
		if !c.inCall {
			return omwire.ErrNoCall()
		}
		v, ok := c.strs[name]
		if !ok {
			return fmt.Errorf("this call has no string named %s", strconv.Quote(name))
		}
		*(out.(*string)) = v
		return nil
	}
	if !c.inCall {
		return omwire.ErrNoCall()
	}
	// The bound is checked on the values before anything is encoded, so an oversized argument
	// costs no copy.
	if n := valueChars(reflect.ValueOf(args)); n > ArgChars {
		return fmt.Errorf("the arguments are %d characters together, and one om call takes at most %d (one recall page)", n, ArgChars)
	}
	raw, err := json.Marshal(args)
	if err != nil {
		return err
	}
	// The characters are within the bound, but many empty or short values (a long variadic
	// list, a chain of Where pairs) encode to far more bytes than characters: the encoded line
	// is bounded too, and refused here, so the call fails and nothing is sent.
	line, err := encodeLine(msg{T: tCall, Gen: c.gen, Call: c.call, Seq: c.seq + 1, M: method, Args: raw}, ChildLineBytes)
	if err != nil {
		return fmt.Errorf("the arguments encode to %d bytes, and one om call's message takes at most %d: pass fewer values", len(raw), ChildLineBytes-envelopeBytes)
	}
	c.seq++
	c.writeLine(line)
	r := <-c.replies
	if r.Gen != c.gen || r.Call != c.call || r.Seq != c.seq {
		fmt.Fprintln(os.Stderr, "om sandbox: a reply out of order")
		os.Exit(exitProtocol)
	}
	if r.Err != "" {
		return errors.New(r.Err)
	}
	if out != nil && len(r.Res) > 0 {
		return json.Unmarshal(r.Res, out)
	}
	return nil
}

// captureValues is the capture function: it renders the last expression's values at once, on
// the evaluating goroutine, within the result bound.
func (c *child) captureValues(vs ...any) {
	if c.holding {
		for _, v := range vs {
			c.heldNow = append(c.heldNow, reflect.ValueOf(v))
		}
		return
	}
	if c.discarding {
		c.discarded++
		if failure := discardedFailure(vs); failure != "" {
			c.discardedFailed++
			used := 0
			for _, e := range c.discardedErrs {
				used += utf8.RuneCountInString(e)
			}
			if text, _ := cutChars(failure, ResultChars-used); text != "" {
				c.discardedErrs = append(c.discardedErrs, text)
			}
		}
		return
	}
	text, cut := renderValues(vs, ResultChars)
	c.captured = &capture{text: text, cut: cut}
}

// discardedFailure is the failure among a dropped expression's values, so a refused write or a
// failed seeded call before the last expression is never silent: a non-nil last error ("error:
// <message>"), or a seeded function's failure string ("error: <function>: <message>", seedCall).
// "" when the values hold none.
func discardedFailure(vs []any) string {
	if len(vs) == 0 {
		return ""
	}
	if err, ok := vs[len(vs)-1].(error); ok && err != nil {
		return "error: " + err.Error()
	}
	if s, ok := vs[0].(string); ok && len(vs) == 1 && strings.HasPrefix(s, "error: ") {
		return s
	}
	return ""
}

// renderValues renders an expression's values within budget characters. A call whose last
// result is an error, as (T, error) is, renders its values alone when the error is nil, and
// "error: <message>" when it is not, after its values only when one of them is not the zero
// value. The capture function takes ...any, so a nil error arrives as a nil interface: a nil
// last value of several is read as a nil error. The interpreter also boxes a typed nil error (a
// nil *T returned as error) as a bare nil, so it renders as success where compiled Go would
// see a non-nil error (docs/code-tool.md, "Result").
func renderValues(vs []any, budget int) (string, bool) {
	var failure any
	if n := len(vs); n >= 2 {
		if last := vs[n-1]; last == nil {
			vs = vs[:n-1]
		} else if _, ok := last.(error); ok {
			failure, vs = last, vs[:n-1]
		}
	}
	var parts []string
	cut := false
	if failure != nil {
		s, over := render(failure, budget-len("error: "))
		parts = append(parts, "error: "+s)
		budget -= utf8.RuneCountInString(s) + len("error: ") + 1
		cut = over
		if allZero(vs) {
			return parts[0], cut
		}
	}
	var values []string
	for _, v := range vs {
		if cut || budget <= 0 {
			cut = true
			break
		}
		s, over := render(v, budget)
		values = append(values, s)
		budget -= utf8.RuneCountInString(s) + 2
		cut = cut || over
	}
	text := strings.Join(values, ", ")
	if failure != nil {
		if text == "" {
			// The error used the whole budget: no value is shown before it.
			return parts[0], cut
		}
		text += "\n" + parts[0]
	}
	return text, cut
}

// allZero reports whether every value is its type's zero value (nil included).
func allZero(vs []any) bool {
	for _, v := range vs {
		if v != nil && !reflect.ValueOf(v).IsZero() {
			return false
		}
	}
	return true
}
