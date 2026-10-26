package codepolicy

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
)

// DetectorEntry is the function a Go detector declares and a detector run calls: it returns the
// count (docs/code-tool.md, "A detector is Go").
const DetectorEntry = "Count"

// DetectorOMNames are the om exports a detector child's om table holds (docs/code-tool.md, "The
// detector om"): the four reads a detector run answers (Notes, History, Subjects, NoteByID), the
// options, and the record and list types they return. Every write, Recall, Saved, Info, Layer,
// Retired, Str and the seeded store functions are absent, so code naming them does not compile.
// A record's methods that call back (Note.Recall, Note.CitedBy) still exist on the type; the
// host answers a detector child only for the four reads and kills it on any other method.
var DetectorOMNames = []string{
	// Options (Offset pages Recall only, which a detector cannot call).
	"Opt", "Search", "From", "Through", "Since", "Until", "ByEvent", "AsOf", "Where", "Contains",
	"Kind", "Subject", "WithStatus", "SortBy", "Limit", "Failed", "Worked", "Open",
	// Values and lists.
	"Note", "Entry", "NoteList", "EntryList",
	// Reads.
	"Notes", "History", "Subjects", "NoteByID",
}

// RuleDetector names a refusal of a detector's shape (CheckDetector).
const RuleDetector = "detector"

// CheckDetector is the pure check of a Go detector's source (docs/code-tool.md, "Checks"): at
// most one recall page; the whole code policy (labels and package-selector assignments
// included); declarations only, functions, methods, types and constants, with exactly one
// func Count() int; no free identifier outside the detector's own declarations, Go's
// predeclared names, the allow-listed packages and om; and only names of the detector om table
// (DetectorOMNames). It reads no branch: provenance is checked where a branch exists
// (rules.CheckFromModel at proposal time), never at load.
func CheckDetector(src string) error {
	if strings.TrimSpace(src) == "" {
		return &Refusal{Rule: RuleDetector, Reason: "a detector's code is empty: declare func Count() int"}
	}
	if n := utf8.RuneCountInString(src); n > om.RecallPageChars {
		return &Refusal{Rule: RuleDetector, Reason: fmt.Sprintf("a detector's code is %d characters, and one is at most %d (one recall page)", n, om.RecallPageChars)}
	}
	p, err := Check(Input{Code: src})
	if err != nil {
		return err
	}
	counts := 0
	for i := range p.Chunks {
		c := &p.Chunks[i]
		if c.Kind != Declaration {
			return &Refusal{Rule: RuleDetector, Line: c.Line, Reason: "a detector holds declarations only (functions, methods, types and constants): it runs nothing but Count()"}
		}
		switch d := c.decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && d.Name.Name == DetectorEntry {
				if !isCountSignature(d.Type) {
					return &Refusal{Rule: RuleDetector, Name: DetectorEntry, Line: c.line(d.Pos()), Reason: "a detector's Count is declared func Count() int: no parameters, one int result"}
				}
				counts++
			}
		case *ast.GenDecl:
			if d.Tok == token.VAR {
				return &Refusal{Rule: RuleDetector, Line: c.line(d.Pos()), Reason: "a detector declares no variables: each run starts fresh, so use a constant or a local inside a function"}
			}
		}
	}
	if counts != 1 {
		return &Refusal{Rule: RuleDetector, Name: DetectorEntry, Reason: fmt.Sprintf("a detector declares exactly one func Count() int (this one declares %d)", counts)}
	}
	return checkDetectorNames(src)
}

// isCountSignature reports whether t is func() int.
func isCountSignature(t *ast.FuncType) bool {
	if t.TypeParams != nil && len(t.TypeParams.List) > 0 {
		return false
	}
	if t.Params != nil && len(t.Params.List) > 0 {
		return false
	}
	if t.Results == nil || len(t.Results.List) != 1 || len(t.Results.List[0].Names) > 1 {
		return false
	}
	id, ok := t.Results.List[0].Type.(*ast.Ident)
	return ok && id.Name == "int"
}

// checkDetectorNames refuses a free identifier outside the detector's own declarations, Go's
// predeclared names, the allow-listed packages and om, and an om selector outside the detector
// om table.
func checkDetectorNames(src string) error {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "", "package p;"+src, 0)
	if err != nil {
		return &Refusal{Rule: RuleDetector, Reason: "the detector does not parse: " + err.Error()}
	}
	line := func(pos token.Pos) int { return fset.Position(pos).Line }
	for _, id := range f.Unresolved {
		if id.Name == "_" || id.Name == OMImport {
			continue
		}
		owner, ok := kernel.OwnsCode(id.Name)
		if ok && (owner == kernel.OwnerGoPredeclared || owner == kernel.OwnerCodePackage) {
			continue
		}
		what := "is not declared by the detector"
		if ok {
			what = "is not available to a detector (" + owner + ")"
		}
		return &Refusal{Rule: RuleDetector, Name: id.Name, Line: line(id.Pos()), Reason: fmt.Sprintf("%s %s: a detector refers only to its own declarations, Go's predeclared names, the allow-listed packages and the detector's om reads (%s)", id.Name, what, strings.Join(detectorReads, ", "))}
	}
	var refusal error
	ast.Inspect(f, func(n ast.Node) bool {
		if refusal != nil {
			return false
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*ast.Ident)
		if !ok || x.Name != OMImport || x.Obj != nil {
			return true
		}
		if !slices.Contains(DetectorOMNames, sel.Sel.Name) {
			refusal = &Refusal{Rule: RuleDetector, Name: "om." + sel.Sel.Name, Line: line(sel.Pos()), Reason: fmt.Sprintf("om.%s is not in a detector's om: a detector reads only %s, with the options, and has no side effect", sel.Sel.Name, strings.Join(detectorReads, ", "))}
		}
		return true
	})
	return refusal
}

// detectorReads are the reads a detector calls, as the refusals name them.
var detectorReads = func() []string {
	var out []string
	for _, n := range []string{"Notes", "History", "Subjects", "NoteByID"} {
		out = append(out, "om."+n)
	}
	return out
}()

// DetectorCall is the code a detector run evaluates after the detector's declarations: the
// entry point's call, whose value is the count.
const DetectorCall = DetectorEntry + "()"

// ParseCount reads a detector run's rendered last value as its count.
func ParseCount(val string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(val))
	if err != nil {
		return 0, fmt.Errorf("Count() returned %q, not an int", val)
	}
	return n, nil
}
