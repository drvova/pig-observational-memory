// Package symgen generates the sandbox child's interpreter symbol tables (docs/code-tool.md,
// "The allow-list (exact)"): one table per allow-listed standard package and one for om, written
// into internal/sandbox/symbols by go generate ./internal/sandbox.
//
// Each table is yaegi extract's output for its package with three changes made here, never by
// hand: no exported variable is settable (every one is dropped, except the copies in
// KeptCopies, exported as non-addressable values in a table of their own), time's blocking,
// goroutine-starting and disk-reading functions are left out, and the om table holds only the
// names in OMNames. Before writing anything, Walk checks that no exported API a table reaches
// mentions a type from an unreachable package (reflect and unsafe above all), so a package that
// leaks one stays off the list.
//
// The package is used only by its own test, which go generate runs with -write, and by the
// tests; nothing the extension's binary links imports it.
package symgen

import (
	"bytes"
	"errors"
	"fmt"
	"go/format"
	"go/importer"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/traefik/yaegi/extract"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/codepolicy"
)

// OMPath is the om package's import path; the interpreter imports it as codepolicy.OMImport.
const OMPath = "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"

// OMNames are the om exports the om table holds: the reads, the options, the writes, the value
// and list types (their methods come with them) and ErrNoCall. Nothing else of om is reachable
// from interpreted code: not its constants and field lists, and never om/omwire.
var OMNames = []string{
	// Options.
	"Opt", "Search", "From", "Through", "Since", "Until", "ByEvent", "AsOf", "Where", "Contains",
	"Kind", "Subject", "WithStatus", "SortBy", "Limit", "Offset", "Failed", "Worked", "Open",
	// Values and lists.
	"Note", "Reflection", "Entry", "Memory", "Text", "LayerRow", "StatusInfo", "ModelStatus",
	"RetiredQuery", "NoteList", "ReflectionList", "EntryList", "MemoryList", "LayerList",
	"RetiredList",
	// Reads.
	"Notes", "Reflections", "History", "Recall", "NoteByID", "ReflectionByID", "Files", "Commits",
	"Attempts", "Dropped", "Subjects", "Saved", "Info", "Layer", "Retired", "Str", "ErrNoCall",
	// Writes.
	"SaveOpt", "Body", "Scope", "Quoting", "EditOpt", "SetType", "SetDescription", "SetBody",
	"Save", "Edit", "ForgetMemory", "Feedback", "LayerForget", "LayerRollback", "Propose", "Define",
}

// TimeDeleted are time's functions left out of its table: they block or start goroutines
// (Sleep, After, Tick, NewTimer, NewTicker, AfterFunc) or read zoneinfo from disk
// (LoadLocation, LoadLocationFromTZData).
var TimeDeleted = []string{"Sleep", "After", "Tick", "NewTimer", "NewTicker", "AfterFunc", "LoadLocation", "LoadLocationFromTZData"}

// KeptCopies are the exported variables kept, by package, each as a non-addressable copy: the
// sentinel errors code compares against, and time.UTC (a pointer, so writing through it is left
// to the code policy's selector rule). Every other exported variable is dropped: a copy of a map
// such as unicode.Categories would still share its contents.
var KeptCopies = map[string][]string{
	"bytes":   {"ErrTooLarge"},
	"errors":  {"ErrUnsupported"},
	"strconv": {"ErrRange", "ErrSyntax"},
	"time":    {"UTC"},
}

// Unreachable are the packages no interpreted code may reach, by import or through a type an
// allow-listed API mentions (docs/code-tool.md, "What is unreachable").
var Unreachable = []string{
	"os", "os/exec", "net", "net/http", "syscall", "unsafe", "reflect", "runtime", "plugin",
	"embed", "io/ioutil", "io/fs", "bufio", "os/signal", "path/filepath", "log", "C",
	"github.com/traefik/yaegi/interp", "github.com/traefik/yaegi/stdlib",
}

// Reviewed are the packages on neither list that an allow-listed API names, allowed only as
// this explicit set and walked like the rest: io's Reader, Writer and RuneScanner interfaces
// (through fmt, bytes, strings and text/tabwriter) and iter's sequences (strings and bytes
// return them).
var Reviewed = []string{"io", "iter"}

// Table is one generated file: its name under internal/sandbox/symbols and its source.
type Table struct {
	File string
	Src  []byte
}

// Generate builds every table, after walking each package. It runs where go/build can find the
// module's packages (the module's directory or below).
func Generate() ([]Table, error) {
	fset := token.NewFileSet()
	imp := importer.ForCompiler(fset, "source", nil)
	var out []Table
	for _, path := range codepolicy.AllowedImports {
		pkg, err := imp.Import(path)
		if err != nil {
			return nil, fmt.Errorf("symgen: %s: %w", path, err)
		}
		exported := exportedNames(pkg, path)
		if err := Walk(pkg, exported); err != nil {
			return nil, err
		}
		src, err := extractTable(path, path, exported, pkg)
		if err != nil {
			return nil, err
		}
		out = append(out, Table{File: fileName(path), Src: src})
	}
	om, err := imp.Import(OMPath)
	if err != nil {
		return nil, fmt.Errorf("symgen: om: %w", err)
	}
	for _, name := range OMNames {
		if om.Scope().Lookup(name) == nil {
			return nil, fmt.Errorf("symgen: om has no %s", name)
		}
	}
	if err := Walk(om, OMNames); err != nil {
		return nil, err
	}
	src, err := extractTable(OMPath, codepolicy.OMImport, OMNames, om)
	if err != nil {
		return nil, err
	}
	out = append(out, Table{File: "om_gen.go", Src: src})
	copies, err := copiesTable()
	if err != nil {
		return nil, err
	}
	out = append(out, Table{File: "copies_gen.go", Src: copies})
	return out, nil
}

// Write generates every table into dir, removing generated files no table is written to.
func Write(dir string) error {
	tables, err := Generate()
	if err != nil {
		return err
	}
	old, _ := filepath.Glob(filepath.Join(dir, "*_gen.go"))
	keep := map[string]bool{}
	for _, t := range tables {
		keep[filepath.Join(dir, t.File)] = true
		if err := os.WriteFile(filepath.Join(dir, t.File), t.Src, 0o644); err != nil {
			return err
		}
	}
	for _, f := range old {
		if !keep[f] {
			if err := os.Remove(f); err != nil {
				return err
			}
		}
	}
	return nil
}

// fileName is a package's table file: its path with / as _.
func fileName(path string) string { return strings.ReplaceAll(path, "/", "_") + "_gen.go" }

// exportedNames are the names a package's table exports: every exported name yaegi extract
// keeps (no generic function or type), less every variable but the kept copies (which go in
// their own table) and time's deleted functions.
func exportedNames(pkg *types.Package, path string) []string {
	var out []string
	for _, name := range pkg.Scope().Names() {
		o := pkg.Scope().Lookup(name)
		if !o.Exported() || generic(o) {
			continue
		}
		if path == "time" && slices.Contains(TimeDeleted, name) {
			continue
		}
		if _, ok := o.(*types.Var); ok {
			continue
		}
		out = append(out, name)
	}
	return out
}

// generic reports whether extract leaves o out: a generic function or type, or an interface
// that only constrains type parameters.
func generic(o types.Object) bool {
	switch o := o.(type) {
	case *types.Func:
		s := o.Type().(*types.Signature)
		return s.TypeParams().Len() > 0
	case *types.TypeName:
		if n, ok := o.Type().(*types.Named); ok && n.TypeParams().Len() > 0 {
			return true
		}
		if i, ok := o.Type().Underlying().(*types.Interface); ok && i.NumMethods() == 0 && i.NumEmbeddeds() != 0 {
			return true
		}
	}
	return false
}

// extractTable runs yaegi extract on one package, keeping only names, and fixes its output: no
// build tag (the module's Go version is the floor), the generator named in the header, and the
// table keyed by the path the interpreter imports it under.
func extractTable(importPath, interpPath string, names []string, pkg *types.Package) ([]byte, error) {
	include := make([]string, len(names))
	for i, n := range names {
		include[i] = "^" + regexp.QuoteMeta(n) + "$"
	}
	if len(include) == 0 {
		// Only generic exports (slices, maps, cmp): the interpreter compiles those from yaegi's
		// stdlib/generic sources, and the table only makes the package importable.
		return format.Source([]byte(fmt.Sprintf("// Code generated by symgen (go generate ./internal/sandbox) from yaegi extract %s. DO NOT EDIT.\n\npackage symbols\n\nimport \"reflect\"\n\nfunc init() {\n\tSymbols[%q] = map[string]reflect.Value{}\n}\n", interpPath, interpPath+"/"+pkg.Name())))
	}
	e := extract.Extractor{Dest: "symbols", Include: include}
	var b bytes.Buffer
	if _, err := e.Extract(importPath, importPath, &b); err != nil {
		return nil, fmt.Errorf("symgen: extract %s: %w", importPath, err)
	}
	src := b.String()
	var lines []string
	for _, l := range strings.Split(src, "\n") {
		if strings.HasPrefix(l, "//go:build") || strings.HasPrefix(l, "// +build") {
			continue
		}
		if strings.HasPrefix(l, "// Code generated by") {
			l = "// Code generated by symgen (go generate ./internal/sandbox) from yaegi extract " + interpPath + ". DO NOT EDIT."
		}
		lines = append(lines, l)
	}
	src = strings.Join(lines, "\n")
	oldKey := fmt.Sprintf("Symbols[%q]", importPath+"/"+pkg.Name())
	newKey := fmt.Sprintf("Symbols[%q]", interpPath+"/"+pkg.Name())
	if !strings.Contains(src, oldKey) {
		return nil, fmt.Errorf("symgen: %s: no %s in the extracted table", importPath, oldKey)
	}
	src = strings.Replace(src, oldKey, newKey, 1)
	if strings.Contains(src, "reflect.ValueOf(&") {
		return nil, fmt.Errorf("symgen: %s: the table exports a settable variable", importPath)
	}
	out, err := format.Source([]byte(src))
	if err != nil {
		return nil, fmt.Errorf("symgen: %s: %w", importPath, err)
	}
	return out, nil
}

// copiesTable is the table of the kept variables, each a non-addressable copy.
func copiesTable() ([]byte, error) {
	pkgs := make([]string, 0, len(KeptCopies))
	for p := range KeptCopies {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	var b bytes.Buffer
	b.WriteString("// Code generated by symgen (go generate ./internal/sandbox). DO NOT EDIT.\n\npackage symbols\n\nimport (\n")
	for _, p := range pkgs {
		fmt.Fprintf(&b, "\t%q\n", p)
	}
	b.WriteString("\t\"reflect\"\n)\n\n")
	b.WriteString("// Copies are the exported variables kept, as values that cannot be assigned through: the\n// interpreter shares a package variable with every interpreter in the process, and a copy keeps\n// interpreted code from changing the child's own.\n")
	b.WriteString("var Copies = map[string]map[string]reflect.Value{\n")
	for _, p := range pkgs {
		fmt.Fprintf(&b, "\t%q: {\n", p+"/"+codepolicy.PackageName(p))
		for _, n := range KeptCopies[p] {
			fmt.Fprintf(&b, "\t\t%q: reflect.ValueOf(%s.%s),\n", n, codepolicy.PackageName(p), n)
		}
		b.WriteString("\t},\n")
	}
	b.WriteString("}\n")
	return format.Source(b.Bytes())
}

// Walk checks the exported API names of pkg reach no type from an unreachable package: every
// function's parameters and results, every constant's and kept variable's type, every type's
// underlying type, exported fields (embedded ones at any export, since they promote), and
// method sets of the type and its pointer, transitively. A named type from a package that is
// neither pkg, an allow-listed package, om, nor in Reviewed fails too, so an unreviewed package
// cannot slip in.
func Walk(pkg *types.Package, names []string) error {
	w := &walker{root: pkg.Path(), seen: map[types.Type]bool{}}
	for _, name := range names {
		o := pkg.Scope().Lookup(name)
		if o == nil {
			return fmt.Errorf("symgen: %s has no %s", pkg.Path(), name)
		}
		w.where = pkg.Path() + "." + name
		w.typ(o.Type())
		if w.err != nil {
			return w.err
		}
	}
	for path, vars := range KeptCopies {
		if path != pkg.Path() {
			continue
		}
		for _, name := range vars {
			o := pkg.Scope().Lookup(name)
			if o == nil {
				return fmt.Errorf("symgen: %s has no %s", path, name)
			}
			w.where = path + "." + name
			w.typ(o.Type())
			if w.err != nil {
				return w.err
			}
		}
	}
	return nil
}

type walker struct {
	root  string
	where string
	seen  map[types.Type]bool
	err   error
}

func (w *walker) fail(format string, args ...any) {
	if w.err == nil {
		w.err = fmt.Errorf("symgen: %s: "+format, append([]any{w.where}, args...)...)
	}
}

// allowedPkg reports whether a named type's package may be reached.
func (w *walker) allowedPkg(path string) error {
	if slices.Contains(Unreachable, path) {
		return fmt.Errorf("reaches %s, which is unreachable", path)
	}
	if path == w.root || path == OMPath || slices.Contains(codepolicy.AllowedImports, path) || slices.Contains(Reviewed, path) {
		return nil
	}
	return fmt.Errorf("reaches %s, which is neither allow-listed nor reviewed", path)
}

func (w *walker) typ(t types.Type) {
	if t == nil || w.err != nil || w.seen[t] {
		return
	}
	w.seen[t] = true
	switch t := t.(type) {
	case *types.Basic:
		if t.Kind() == types.UnsafePointer {
			w.fail("reaches unsafe.Pointer")
		}
	case *types.Named:
		if obj := t.Obj(); obj.Pkg() != nil {
			if err := w.allowedPkg(obj.Pkg().Path()); err != nil {
				w.fail("type %s %v", types.TypeString(t, nil), err)
				return
			}
		}
		for i := 0; i < t.TypeArgs().Len(); i++ {
			w.typ(t.TypeArgs().At(i))
		}
		w.typ(t.Underlying())
		w.methods(t)
		w.methods(types.NewPointer(t))
	case *types.Alias:
		w.typ(types.Unalias(t))
	case *types.Pointer:
		w.typ(t.Elem())
	case *types.Slice:
		w.typ(t.Elem())
	case *types.Array:
		w.typ(t.Elem())
	case *types.Map:
		w.typ(t.Key())
		w.typ(t.Elem())
	case *types.Chan:
		w.typ(t.Elem())
	case *types.Signature:
		w.tuple(t.Params())
		w.tuple(t.Results())
	case *types.Tuple:
		w.tuple(t)
	case *types.Struct:
		for i := 0; i < t.NumFields(); i++ {
			if f := t.Field(i); f.Exported() || f.Embedded() {
				w.typ(f.Type())
			}
		}
	case *types.Interface:
		for i := 0; i < t.NumMethods(); i++ {
			if m := t.Method(i); m.Exported() {
				w.typ(m.Type())
			}
		}
		for i := 0; i < t.NumEmbeddeds(); i++ {
			w.typ(t.EmbeddedType(i))
		}
	case *types.TypeParam:
		w.typ(t.Constraint())
	case *types.Union:
		for i := 0; i < t.Len(); i++ {
			w.typ(t.Term(i).Type())
		}
	default:
		w.fail("unexpected type %T", t)
	}
}

func (w *walker) tuple(t *types.Tuple) {
	for i := 0; i < t.Len(); i++ {
		w.typ(t.At(i).Type())
	}
}

func (w *walker) methods(t types.Type) {
	ms := types.NewMethodSet(t)
	for i := 0; i < ms.Len(); i++ {
		if f := ms.At(i).Obj(); f.Exported() {
			w.typ(f.Type())
		}
	}
}

// ErrDiff is returned by Check when a regenerated table differs from the one in the tree.
var ErrDiff = errors.New("symgen: the generated tables differ from internal/sandbox/symbols: run go generate ./internal/sandbox")

// Check regenerates the tables and compares them with the files in dir.
func Check(dir string) error {
	tables, err := Generate()
	if err != nil {
		return err
	}
	have, _ := filepath.Glob(filepath.Join(dir, "*_gen.go"))
	if len(have) != len(tables) {
		return fmt.Errorf("%w (%d generated files in the tree, %d generated)", ErrDiff, len(have), len(tables))
	}
	for _, t := range tables {
		old, err := os.ReadFile(filepath.Join(dir, t.File))
		if err != nil || !bytes.Equal(old, t.Src) {
			return fmt.Errorf("%w (%s)", ErrDiff, t.File)
		}
	}
	return nil
}
