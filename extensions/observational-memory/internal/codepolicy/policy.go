// Package codepolicy checks the memory tool's Go code before it runs, every unit before it is
// kept, and every unit again when it loads (docs/code-tool.md, "Code policy"). It parses with
// go/parser in the extension, so nothing the policy refused reaches the child process.
//
// The policy is syntactic and walks every depth: no go statement anywhere, no defer outside a
// function, no func init or func main, no binding of a name the kernel owns for code
// (kernel.OwnsCode: Go's predeclared identifiers, the seeded names, the allow-listed packages,
// init and main) at any binding site, labels included, no assignment, increment or address-of
// through a package, no import outside the allow-list, no type redefined with different
// source, no constant array above the result bound in bytes, and no input above the call's
// input bound. The symbol table the child exports is the wall; this policy is the readable
// refusal in front of it, and keeps a statement from breaking the namespace for the session.
//
// The package imports only om (the bounds), kernel (the owned names) and the standard library.
package codepolicy

import (
	"fmt"
	"go/token"
	"maps"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
)

// AllowedImports are the standard packages interpreted code may use (docs/code-tool.md, "The
// allow-list (exact)"), each pre-imported under its own name. An import of one of them, or of
// OMImport, is accepted as a no-op; any other is refused.
var AllowedImports = []string{
	"fmt", "strings", "strconv", "sort", "slices", "maps", "math", "unicode", "unicode/utf8",
	"bytes", "errors", "regexp", "cmp", "text/tabwriter", "time",
}

// OMImport is the domain API's import path, seeded as the package om.
const OMImport = "om"

// SeededFuncs are the functions bound into every namespace from a host package.
var SeededFuncs = []string{"describe", "man", "kept", "forget"}

// CaptureName is the function the child wraps around the call's last expression to render its
// value. It is owned, so code can neither call it by accident nor rebind it.
const CaptureName = "_omCapture"

// HeldName is the function the child reads a held value back with when it declares a top-level
// variable in two steps (the interpreter cannot infer some top-level variables' types from
// their expression). It is owned for the same reason as CaptureName.
const HeldName = "_omHeld"

// PackageName is the name an allow-listed import path is used under: its last element.
func PackageName(path string) string {
	return path[strings.LastIndex(path, "/")+1:]
}

// The rules a refusal names.
const (
	RuleInputBound        = "input bound"
	RuleStringsName       = "strings name"
	RuleGoStatement       = "go statement"
	RuleTopLevelDefer     = "top-level defer"
	RuleEntryPoint        = "init or main"
	RuleOwnedName         = "owned name"
	RulePackageAssignment = "assignment through a package"
	RuleImport            = "import"
	RuleTypeRedefinition  = "type redefinition"
	RuleArraySize         = "array size"
	RuleUnit              = "unit"
)

// Refusal is a policy violation: the rule, the offending name and where it is.
type Refusal struct {
	Rule string
	// Name is the offending identifier, package, path or type, "" when there is none.
	Name string
	// Line is the line in the code's own numbering, from 1; 0 when the refusal is of the
	// whole input.
	Line int
	// Reason says what was refused and what to do instead.
	Reason string
}

func (r *Refusal) Error() string {
	if r.Line > 0 {
		return fmt.Sprintf("line %d: refused (%s): %s", r.Line, r.Rule, r.Reason)
	}
	return fmt.Sprintf("refused (%s): %s", r.Rule, r.Reason)
}

// Input is one call's code with what the policy needs beside it.
type Input struct {
	// Code is the call's Go source.
	Code string
	// Strings are the call's named strings.
	Strings map[string]string
	// Types are the sources of the types the live namespace already defines (kept units and
	// types compiled earlier in the conversation), by name: a type redefined with different
	// source is refused, since the interpreter keeps the first one silently.
	Types map[string]string
}

// Check refuses an input the policy does not allow, and otherwise returns its chunks. A code
// that does not parse returns a *SyntaxError; a violation returns a *Refusal naming the rule,
// the offending name and its line.
func Check(in Input) (Program, error) {
	if err := checkInput(in); err != nil {
		return Program{}, err
	}
	p, err := Split(in.Code)
	if err != nil {
		return Program{}, err
	}
	if err := checkProgram(p, in.Types); err != nil {
		return Program{}, err
	}
	return p, nil
}

// checkInput applies the bounds of the call's input: code and strings together within one
// recall page, and every strings name a Go identifier.
func checkInput(in Input) error {
	n := utf8.RuneCountInString(in.Code)
	for _, v := range in.Strings {
		n += utf8.RuneCountInString(v)
	}
	if n > om.RecallPageChars {
		return &Refusal{Rule: RuleInputBound, Reason: fmt.Sprintf("the code and strings are %d characters together, and a call takes at most %d (one recall page, so the stored call can be recalled whole): split the work into several calls", n, om.RecallPageChars)}
	}
	for _, name := range slices.Sorted(maps.Keys(in.Strings)) {
		if !token.IsIdentifier(name) {
			return &Refusal{Rule: RuleStringsName, Name: name, Reason: fmt.Sprintf("the strings name %s is not a Go identifier: name it as a variable would be named", strconv.Quote(name))}
		}
	}
	return nil
}

// checkProgram walks every chunk, then the program's type declarations against the live
// namespace's.
func checkProgram(p Program, types map[string]string) error {
	scope := newCallScope(p, types)
	for i := range p.Chunks {
		if err := (&walker{c: &p.Chunks[i], scope: scope}).walk(); err != nil {
			return err
		}
	}
	return checkTypes(p, types)
}

// ownedRefusal refuses binding name as what.
func ownedRefusal(line int, what, name, owner string) *Refusal {
	return &Refusal{Rule: RuleOwnedName, Name: name, Line: line, Reason: fmt.Sprintf("%s binds %s, which %s owns: choose another name", what, name, owner)}
}

// isPackage reports whether name is a seeded package: an allow-listed one or om.
func isPackage(name string) bool {
	if name == OMImport {
		return true
	}
	owner, ok := kernel.OwnsCode(name)
	return ok && owner == kernel.OwnerCodePackage
}

// ownsCode is the kernel's predicate for the names code may not bind.
func ownsCode(name string) (owner string, ok bool) { return kernel.OwnsCode(name) }

func itoa(n int) string { return strconv.Itoa(n) }
