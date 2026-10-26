package sandbox

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
)

// mapTypes are the interface mappings the interpreter applies when interpreted values reach a
// host function taking any: the fmt printers and the capture function then call an interpreted
// String or Error method, and the scanners an interpreted Scan.
func mapTypes(capture reflect.Value) map[reflect.Value][]reflect.Type {
	print := []reflect.Type{reflect.TypeOf((*fmt.Formatter)(nil)).Elem(), stringerType}
	scan := []reflect.Type{reflect.TypeOf((*fmt.Scanner)(nil)).Elem()}
	m := map[reflect.Value][]reflect.Type{capture: {stringerType, errorType}}
	for _, f := range []any{fmt.Errorf, fmt.Fprint, fmt.Fprintf, fmt.Fprintln, fmt.Print, fmt.Printf, fmt.Println, fmt.Sprint, fmt.Sprintf, fmt.Sprintln} {
		m[reflect.ValueOf(f)] = print
	}
	for _, f := range []any{fmt.Scan, fmt.Scanf, fmt.Scanln} {
		m[reflect.ValueOf(f)] = scan
	}
	return m
}

// describe prints a value's type and one line per exported field and method. The docs come from
// om's generated doc table (docs/code-tool.md, "describe(x) and its docs"), which arrives with
// the om API; until then, and for a type the code defined, a line says "no doc".
func describe(x any) string {
	if x == nil {
		return "nil"
	}
	t := reflect.TypeOf(x)
	var b strings.Builder
	b.WriteString(t.String())
	st := t
	for st.Kind() == reflect.Pointer {
		st = st.Elem()
	}
	if st.Kind() == reflect.Struct {
		for i := 0; i < st.NumField(); i++ {
			f := st.Field(i)
			if f.IsExported() {
				fmt.Fprintf(&b, "\n  %s %s", f.Name, f.Type)
			}
		}
	}
	for i := 0; i < t.NumMethod(); i++ {
		m := t.Method(i)
		sig := strings.TrimPrefix(m.Type.String(), "func")
		// Drop the receiver from the method expression's signature.
		if open := strings.Index(sig, "("); open >= 0 {
			if comma := strings.Index(sig, ", "); comma >= 0 && comma < strings.Index(sig, ")") {
				sig = "(" + sig[comma+2:]
			} else if close := strings.Index(sig, ")"); close >= 0 {
				sig = "()" + sig[close+1:]
			}
		}
		fmt.Fprintf(&b, "\n  %s%s // no doc", m.Name, sig)
	}
	return b.String()
}

// seedCall is a seeded function's call back to the extension: an error panics, recovered in the
// child as a runtime error.
func seedCall(method string, args ...any) string {
	var s string
	if err := omwire.Call(method, &s, args...); err != nil {
		panic(fmt.Errorf("%s: %w", method, err))
	}
	return s
}

// seedMan is man(): the manual, which holds the store's part, so the extension writes it.
func seedMan() string { return seedCall(omwire.MMan) }

// seedKept is kept(): the kept units, or one unit's source.
func seedKept(names ...string) string { return seedCall(omwire.MKept, names) }

// seedForget is forget(name): it forgets one kept unit.
func seedForget(name string) string { return seedCall(omwire.MForget, name) }
