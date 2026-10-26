package symgen

import (
	"flag"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
)

// write makes TestTablesAreUpToDate write the tables instead of checking them: go generate
// ./internal/sandbox runs it so (the module may hold no main package, which PiG refuses).
var write = flag.Bool("write", false, "write the tables into ../symbols")

// The tables in the tree are what the generator writes now: regenerating them changes nothing.
func TestTablesAreUpToDate(t *testing.T) {
	dir := filepath.Join("..", "symbols")
	if *write {
		if err := Write(dir); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := Check(dir); err != nil {
		t.Fatal(err)
	}
}

// No table exports a settable variable: every value is a function, a constant, a type or a
// non-addressable copy.
func TestTablesExportNoSettableVariable(t *testing.T) {
	files, _ := filepath.Glob(filepath.Join("..", "symbols", "*_gen.go"))
	if len(files) == 0 {
		t.Fatal("no generated tables")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "reflect.ValueOf(&") {
			t.Errorf("%s exports a settable variable", f)
		}
	}
}

// tableKeys are the names a generated table file exports, by table.
func tableKeys(t *testing.T, file string) map[string][]string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join("..", "symbols", file), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]string{}
	ast.Inspect(f, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 {
			return true
		}
		idx, ok := as.Lhs[0].(*ast.IndexExpr)
		if !ok {
			return true
		}
		key, _ := strconv.Unquote(idx.Index.(*ast.BasicLit).Value)
		out[key] = []string{}
		for _, e := range as.Rhs[0].(*ast.CompositeLit).Elts {
			name, _ := strconv.Unquote(e.(*ast.KeyValueExpr).Key.(*ast.BasicLit).Value)
			out[key] = append(out[key], name)
		}
		return false
	})
	return out
}

// The om table holds exactly the listed names under the path the interpreter imports, with no
// omwire symbol, no variable, and no setter or installer: the only Set* names are EditOpt's
// constructors, and no function or method takes or returns a function or an interface but
// error and the lists' Filter predicates.
func TestOMTableHoldsOnlyTheListedNames(t *testing.T) {
	keys := tableKeys(t, "om_gen.go")
	got := keys["om/om"]
	if len(keys) != 1 || !slices.Equal(slices.Sorted(slices.Values(got)), slices.Sorted(slices.Values(OMNames))) {
		t.Fatalf("om table %v", keys)
	}
	src, _ := os.ReadFile(filepath.Join("..", "symbols", "om_gen.go"))
	if strings.Contains(string(src), "omwire") {
		t.Fatal("the om table names omwire")
	}
	pkg, err := importer.ForCompiler(token.NewFileSet(), "source", nil).Import(OMPath)
	if err != nil {
		t.Fatal(err)
	}
	editOpt := pkg.Scope().Lookup("EditOpt").Type()
	errType := types.Universe.Lookup("error").Type()
	checkSig := func(where string, sig *types.Signature) {
		for _, tup := range []*types.Tuple{sig.Params(), sig.Results()} {
			for i := 0; i < tup.Len(); i++ {
				pt := tup.At(i).Type()
				if s, ok := pt.Underlying().(*types.Slice); ok && sig.Variadic() && tup == sig.Params() && i == tup.Len()-1 {
					pt = s.Elem()
				}
				switch u := pt.Underlying().(type) {
				case *types.Signature:
					if !strings.HasSuffix(where, ".Filter") {
						t.Errorf("%s takes or returns a function", where)
					}
					_ = u
				case *types.Interface:
					if !types.Identical(pt, errType) {
						t.Errorf("%s takes or returns the interface %s", where, pt)
					}
				}
			}
		}
	}
	for _, name := range OMNames {
		o := pkg.Scope().Lookup(name)
		switch o := o.(type) {
		case *types.Var:
			t.Errorf("%s is a variable", name)
		case *types.Func:
			sig := o.Type().(*types.Signature)
			if strings.HasPrefix(name, "Install") {
				t.Errorf("%s is an installer", name)
			}
			if strings.HasPrefix(name, "Set") && (sig.Results().Len() != 1 || !types.Identical(sig.Results().At(0).Type(), editOpt)) {
				t.Errorf("%s is a setter", name)
			}
			checkSig(name, sig)
		case *types.TypeName:
			for _, typ := range []types.Type{o.Type(), types.NewPointer(o.Type())} {
				ms := types.NewMethodSet(typ)
				for i := 0; i < ms.Len(); i++ {
					m := ms.At(i).Obj()
					if !m.Exported() {
						continue
					}
					if strings.HasPrefix(m.Name(), "Set") || strings.HasPrefix(m.Name(), "Install") {
						t.Errorf("%s.%s is a setter or installer", name, m.Name())
					}
					checkSig(name+"."+m.Name(), m.Type().(*types.Signature))
				}
			}
		}
	}
}

// The time table leaves out the functions that block, start goroutines or read the disk, and the
// unicode table its shared maps and tables.
func TestTablesLeaveOutDeletedSymbols(t *testing.T) {
	tm := tableKeys(t, "time_gen.go")["time/time"]
	for _, name := range append(slices.Clone(TimeDeleted), "Local", "UTC") {
		if slices.Contains(tm, name) {
			t.Errorf("time exports %s", name)
		}
	}
	uni := tableKeys(t, "unicode_gen.go")["unicode/unicode"]
	for _, name := range []string{"Categories", "Scripts", "Properties", "Lu", "Latin", "Upper"} {
		if slices.Contains(uni, name) {
			t.Errorf("unicode exports %s", name)
		}
	}
	if !slices.Contains(uni, "IsUpper") {
		t.Error("unicode lost IsUpper")
	}
}

// The type walk refuses a package whose exported API reaches reflect or unsafe, through a
// field, a result, a method or an embedded type, and an unreviewed package.
func TestWalkRefusesUnreachableTypes(t *testing.T) {
	for name, src := range map[string]string{
		"field":      "package leak; import \"reflect\"; type E struct{ Type reflect.Type }",
		"result":     "package leak; import \"reflect\"; func V() reflect.Value { return reflect.Value{} }",
		"method":     "package leak; import \"unsafe\"; type T int; func (T) P() unsafe.Pointer { return nil }",
		"embedded":   "package leak; import \"reflect\"; type inner struct{ reflect.StructTag }; type T struct{ inner }",
		"unsafe":     "package leak; import \"unsafe\"; var P unsafe.Pointer",
		"unreviewed": "package leak; import \"go/token\"; func P() token.Pos { return 0 }",
		"deep":       "package leak; import \"reflect\"; type A struct{ B *B }; type B struct{ C map[string][]C }; type C struct{ T reflect.Type }",
	} {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, "leak.go", src, 0)
		if err != nil {
			t.Fatal(err)
		}
		pkg, err := (&types.Config{Importer: importer.ForCompiler(fset, "source", nil)}).Check("leak", fset, []*ast.File{f}, nil)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, n := range pkg.Scope().Names() {
			if token.IsExported(n) {
				names = append(names, n)
			}
		}
		if err := Walk(pkg, names); err == nil {
			t.Errorf("%s: the walk passed", name)
		}
	}
	// And it passes a package that reaches only allow-listed and reviewed ones.
	fset := token.NewFileSet()
	f, _ := parser.ParseFile(fset, "ok.go", "package fine; import (\"io\"; \"time\"); type T struct{ W io.Writer; At time.Time }", 0)
	pkg, err := (&types.Config{Importer: importer.ForCompiler(fset, "source", nil)}).Check("fine", fset, []*ast.File{f}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := Walk(pkg, []string{"T"}); err != nil {
		t.Fatal(err)
	}
}

// The extension's binary does not link yaegi's stdlib package, which wraps every standard
// package (it grew the probe's binary from 2.4 MB to 28.0 MB); the sandbox links only the
// generated tables.
func TestYaegiStdlibIsNotLinked(t *testing.T) {
	gobin, err := exec.LookPath("go")
	if err != nil {
		t.Skip("no go command")
	}
	const module = "github.com/drvova/pig-observational-memory/extensions/observational-memory"
	out, err := exec.Command(gobin, "list", "-deps", module, module+"/internal/sandbox").CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	deps := strings.Fields(string(out))
	if !slices.Contains(deps, "github.com/traefik/yaegi/interp") {
		t.Fatal("the sandbox does not link the interpreter")
	}
	for _, d := range deps {
		if d == "github.com/traefik/yaegi/stdlib" || strings.HasPrefix(d, "github.com/traefik/yaegi/stdlib/") && d != "github.com/traefik/yaegi/stdlib/generic" {
			t.Errorf("%s is linked", d)
		}
		if strings.HasSuffix(d, "/internal/sandbox/symgen") || strings.Contains(d, "github.com/traefik/yaegi/extract") {
			t.Errorf("%s is linked", d)
		}
	}
}

// The manual lists exactly the om table's names, so every entry point code can reach has its
// line and doc there, and nothing the table lacks is offered.
func TestManualListsTheOMTable(t *testing.T) {
	var listed []string
	for _, g := range om.ManualGroups {
		listed = append(listed, g.Names...)
	}
	slices.Sort(listed)
	names := slices.Clone(OMNames)
	slices.Sort(names)
	if !slices.Equal(listed, names) {
		t.Fatalf("manual %v\ntable  %v", listed, names)
	}
}
