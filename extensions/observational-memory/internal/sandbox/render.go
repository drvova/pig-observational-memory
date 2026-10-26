package sandbox

import (
	"cmp"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

// render formats a value for the result, within budget characters, and reports whether it
// stopped short. It never uses fmt's own recursion (which overflows the stack on a cyclic
// value): it walks the value itself, prints <cycle> for a pointer, map or slice already on the
// current path, and calls a String or Error method when the value has one (every om list,
// record and Text does). Each nesting level prints at least one character, so the budget also
// bounds the depth.
func render(v any, budget int) (string, bool) {
	r := &renderer{budget: budget, path: map[visit]bool{}}
	r.value(reflect.ValueOf(v), true)
	return r.b.String(), r.over
}

type visit struct {
	ptr uintptr
	typ reflect.Type
	n   int
}

type renderer struct {
	b      strings.Builder
	budget int
	over   bool
	path   map[visit]bool
}

// write appends s within the budget.
func (r *renderer) write(s string) {
	if r.over {
		return
	}
	n := utf8.RuneCountInString(s)
	if n <= r.budget {
		r.b.WriteString(s)
		r.budget -= n
		return
	}
	s, _ = cutChars(s, r.budget)
	r.b.WriteString(s)
	r.budget = 0
	r.over = true
}

var (
	stringerType = reflect.TypeOf((*fmt.Stringer)(nil)).Elem()
	errorType    = reflect.TypeOf((*error)(nil)).Elem()
)

// method calls a String or Error method, recovering a panic in it.
func (r *renderer) method(v reflect.Value) (string, bool) {
	if !v.IsValid() || !v.CanInterface() {
		return "", false
	}
	t := v.Type()
	if !t.Implements(errorType) && !t.Implements(stringerType) {
		return "", false
	}
	if (v.Kind() == reflect.Pointer || v.Kind() == reflect.Interface) && v.IsNil() {
		return "", false
	}
	var out string
	func() {
		defer func() {
			if p := recover(); p != nil {
				out = fmt.Sprintf("<panic in String: %v>", p)
			}
		}()
		switch x := v.Interface().(type) {
		case error:
			out = x.Error()
		case fmt.Stringer:
			out = x.String()
		}
	}()
	return out, true
}

func (r *renderer) value(v reflect.Value, top bool) {
	if r.over {
		return
	}
	if !v.IsValid() {
		r.write("nil")
		return
	}
	if s, ok := r.method(v); ok {
		r.write(s)
		return
	}
	switch v.Kind() {
	case reflect.Bool:
		r.write(strconv.FormatBool(v.Bool()))
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		r.write(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		r.write(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		r.write(strconv.FormatFloat(v.Float(), 'g', -1, v.Type().Bits()))
	case reflect.Complex64, reflect.Complex128:
		r.write(strconv.FormatComplex(v.Complex(), 'g', -1, v.Type().Bits()))
	case reflect.String:
		if top {
			r.write(v.String())
		} else {
			r.write(strconv.Quote(v.String()))
		}
	case reflect.Interface:
		if v.IsNil() {
			r.write("nil")
			return
		}
		r.value(v.Elem(), top)
	case reflect.Pointer:
		if v.IsNil() {
			r.write("nil")
			return
		}
		key := visit{ptr: v.Pointer(), typ: v.Type()}
		if r.path[key] {
			r.write("<cycle>")
			return
		}
		r.path[key] = true
		r.write("&")
		r.value(v.Elem(), false)
		delete(r.path, key)
	case reflect.Slice, reflect.Array:
		if v.Kind() == reflect.Slice {
			if v.IsNil() {
				r.write("[]")
				return
			}
			key := visit{ptr: v.Pointer(), typ: v.Type(), n: v.Len()}
			if r.path[key] && v.Len() > 0 {
				r.write("<cycle>")
				return
			}
			r.path[key] = true
			defer delete(r.path, key)
		}
		r.write("[")
		for i := 0; i < v.Len() && !r.over; i++ {
			if i > 0 {
				r.write(" ")
			}
			r.value(v.Index(i), false)
		}
		r.write("]")
	case reflect.Map:
		if v.IsNil() {
			r.write("map[]")
			return
		}
		key := visit{ptr: v.Pointer(), typ: v.Type()}
		if r.path[key] {
			r.write("<cycle>")
			return
		}
		r.path[key] = true
		defer delete(r.path, key)
		// The keys are ordered by their values, as fmt orders them, not by their rendered text:
		// rendering every key to sort them would cost keys times the budget (a large map whose
		// keys all reach one big string), while the output holds at most a budget's worth. So
		// the work is the map's size, and only the keys shown are rendered.
		keys := v.MapKeys()
		slices.SortStableFunc(keys, compareKeys)
		r.write("map[")
		for n, k := range keys {
			if r.over {
				break
			}
			if n > 0 {
				r.write(" ")
			}
			r.value(k, false)
			r.write(":")
			r.value(v.MapIndex(k), false)
		}
		r.write("]")
	case reflect.Struct:
		t := v.Type()
		r.write("{")
		for i := 0; i < v.NumField() && !r.over; i++ {
			if i > 0 {
				r.write(" ")
			}
			r.write(t.Field(i).Name + ":")
			r.value(v.Field(i), false)
		}
		r.write("}")
	case reflect.Func:
		if v.IsNil() {
			r.write("nil")
			return
		}
		r.write("<" + v.Type().String() + ">")
	case reflect.Chan:
		if v.IsNil() {
			r.write("nil")
			return
		}
		r.write("<" + v.Type().String() + ">")
	default:
		r.write("<" + v.Type().String() + ">")
	}
}

// compareKeys orders two map keys of one map by value, with the rules of Go's internal/fmtsort
// (the order fmt prints a map in): numbers, strings and bools by value, nil low, pointers and
// channels by address, structs and arrays field by field, interfaces by their dynamic type and
// then by value. It never follows a pointer, so it ends on any key and never calls code.
func compareKeys(a, b reflect.Value) int {
	if a.Type() != b.Type() {
		return -1
	}
	switch a.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return cmp.Compare(a.Int(), b.Int())
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return cmp.Compare(a.Uint(), b.Uint())
	case reflect.String:
		return cmp.Compare(a.String(), b.String())
	case reflect.Float32, reflect.Float64:
		return cmp.Compare(a.Float(), b.Float())
	case reflect.Complex64, reflect.Complex128:
		x, y := a.Complex(), b.Complex()
		if c := cmp.Compare(real(x), real(y)); c != 0 {
			return c
		}
		return cmp.Compare(imag(x), imag(y))
	case reflect.Bool:
		switch x, y := a.Bool(), b.Bool(); {
		case x == y:
			return 0
		case x:
			return 1
		default:
			return -1
		}
	case reflect.Pointer, reflect.UnsafePointer, reflect.Chan:
		return cmp.Compare(a.Pointer(), b.Pointer())
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ {
			if c := compareKeys(a.Field(i), b.Field(i)); c != 0 {
				return c
			}
		}
		return 0
	case reflect.Array:
		for i := 0; i < a.Len(); i++ {
			if c := compareKeys(a.Index(i), b.Index(i)); c != 0 {
				return c
			}
		}
		return 0
	case reflect.Interface:
		switch {
		case a.IsNil() && b.IsNil():
			return 0
		case a.IsNil():
			return -1
		case b.IsNil():
			return 1
		}
		if c := compareKeys(reflect.ValueOf(a.Elem().Type()), reflect.ValueOf(b.Elem().Type())); c != 0 {
			return c
		}
		return compareKeys(a.Elem(), b.Elem())
	}
	return 0
}

// boundedWriter keeps at most max characters of what is written and counts the rest, so a call
// printing without end holds one result's worth.
type boundedWriter struct {
	mu      sync.Mutex
	max     int
	kept    int
	b       strings.Builder
	dropped int
}

func newBoundedWriter(max int) *boundedWriter { return &boundedWriter{max: max} }

func (w *boundedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	s := string(p)
	room := w.max - w.kept
	keep, drop := cutChars(s, room)
	w.b.WriteString(keep)
	w.kept += utf8.RuneCountInString(keep)
	w.dropped += drop
	return len(p), nil
}

func (w *boundedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

func (w *boundedWriter) reset() {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.b.Reset()
	w.kept, w.dropped = 0, 0
}
