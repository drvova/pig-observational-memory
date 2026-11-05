package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// registrySchemaPath is the registry file's JSON Schema.
var registrySchemaPath = filepath.Join("..", "..", "contracts", "schemas", "registry.schema.json")

// schemaValidator checks a document against the subset of JSON Schema (2020-12) the registry's
// schema uses: type, enum, const, properties, required, additionalProperties, propertyNames,
// minProperties, items, minItems, minLength, pattern, allOf, oneOf, if/then and local $refs.
// An unknown keyword is an error, so the schema never says more than this checks.
type schemaValidator struct {
	root map[string]any
}

var schemaAnnotations = map[string]bool{"$schema": true, "$id": true, "$defs": true, "title": true, "description": true}

func (v schemaValidator) validate(schema any, doc any, at string) []string {
	s, ok := schema.(map[string]any)
	if !ok {
		if b, ok := schema.(bool); ok && !b {
			return []string{at + ": not allowed"}
		}
		return nil
	}
	var errs []string
	fail := func(format string, args ...any) { errs = append(errs, at+": "+fmt.Sprintf(format, args...)) }
	keys := make([]string, 0, len(s))
	for k := range s {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		kw := s[k]
		switch k {
		case "$ref":
			ref, _ := kw.(string)
			target, err := v.resolve(ref)
			if err != nil {
				fail("%v", err)
				continue
			}
			errs = append(errs, v.validate(target, doc, at)...)
		case "type":
			if !schemaType(kw.(string), doc) {
				fail("want %s, have %s", kw, jsonType(doc))
			}
		case "enum":
			found := false
			for _, e := range kw.([]any) {
				found = found || jsonEqual(e, doc)
			}
			if !found {
				fail("%v is not one of %v", doc, kw)
			}
		case "const":
			if !jsonEqual(kw, doc) {
				fail("%v is not %v", doc, kw)
			}
		case "properties":
			obj, ok := doc.(map[string]any)
			if !ok {
				continue
			}
			for name, sub := range kw.(map[string]any) {
				if val, ok := obj[name]; ok {
					errs = append(errs, v.validate(sub, val, at+"."+name)...)
				}
			}
		case "required":
			obj, ok := doc.(map[string]any)
			if !ok {
				continue
			}
			for _, name := range kw.([]any) {
				if _, ok := obj[name.(string)]; !ok {
					fail("missing %s", name)
				}
			}
		case "additionalProperties":
			obj, ok := doc.(map[string]any)
			if !ok {
				continue
			}
			props, _ := s["properties"].(map[string]any)
			for name, val := range obj {
				if _, known := props[name]; !known {
					if b, isBool := kw.(bool); isBool && !b {
						fail("unknown field %q", name)
					} else {
						errs = append(errs, v.validate(kw, val, at+"."+name)...)
					}
				}
			}
		case "propertyNames":
			if obj, ok := doc.(map[string]any); ok {
				for name := range obj {
					errs = append(errs, v.validate(kw, name, at+" (key "+name+")")...)
				}
			}
		case "minProperties":
			if obj, ok := doc.(map[string]any); ok && float64(len(obj)) < kw.(float64) {
				fail("fewer than %v fields", kw)
			}
		case "items":
			if list, ok := doc.([]any); ok {
				for i, item := range list {
					errs = append(errs, v.validate(kw, item, fmt.Sprintf("%s[%d]", at, i))...)
				}
			}
		case "minItems":
			if list, ok := doc.([]any); ok && float64(len(list)) < kw.(float64) {
				fail("fewer than %v items", kw)
			}
		case "minLength":
			if str, ok := doc.(string); ok && float64(len([]rune(str))) < kw.(float64) {
				fail("shorter than %v", kw)
			}
		case "pattern":
			if str, ok := doc.(string); ok && !regexp.MustCompile(kw.(string)).MatchString(str) {
				fail("%q does not match %s", str, kw)
			}
		case "allOf":
			for _, sub := range kw.([]any) {
				errs = append(errs, v.validate(sub, doc, at)...)
			}
		case "oneOf":
			n := 0
			for _, sub := range kw.([]any) {
				if len(v.validate(sub, doc, at)) == 0 {
					n++
				}
			}
			if n != 1 {
				fail("matches %d of oneOf, want 1", n)
			}
		case "if":
			if len(v.validate(kw, doc, at)) == 0 {
				if then, ok := s["then"]; ok {
					errs = append(errs, v.validate(then, doc, at)...)
				}
			}
		case "then":
		default:
			if !schemaAnnotations[k] {
				fail("unsupported schema keyword %q", k)
			}
		}
	}
	return errs
}

func (v schemaValidator) resolve(ref string) (any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("only local $refs: %q", ref)
	}
	var node any = v.root
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		m, _ := node.(map[string]any)
		next, ok := m[part]
		if !ok {
			return nil, fmt.Errorf("unresolved $ref %q", ref)
		}
		node = next
	}
	return node, nil
}

func schemaType(t string, doc any) bool {
	switch t {
	case "integer":
		f, ok := doc.(float64)
		return ok && f == math.Trunc(f)
	case "number":
		_, ok := doc.(float64)
		return ok
	}
	return jsonType(doc) == t
}

func jsonType(doc any) string {
	switch doc.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", doc)
}

func jsonEqual(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

func loadSchema(t *testing.T) schemaValidator {
	t.Helper()
	data, err := os.ReadFile(registrySchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatal(err)
	}
	return schemaValidator{root: root}
}

// The registry file is valid against its schema.
func TestRegistryMatchesSchema(t *testing.T) {
	v := loadSchema(t)
	data, err := assets.ReadFile("assets/harnesses.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	for _, e := range v.validate(v.root, doc, "harnesses.json") {
		t.Error(e)
	}
}

// The validator rejects what the schema forbids, so a passing file means something.
func TestRegistrySchemaRejects(t *testing.T) {
	v := loadSchema(t)
	for name, doc := range map[string]string{
		"unknown field":      `[{"name": "x", "title": "X", "class": "none", "colour": 1}]`,
		"unknown class":      `[{"name": "x", "title": "X", "class": "maybe"}]`,
		"file without entry": `[{"name": "x", "title": "X", "class": "file", "path": {"linux": "~/x"}, "format": "json", "key": ["k"]}]`,
		"unknown OS":         `[{"name": "x", "title": "X", "class": "own", "path": {"plan9": "~/x"}, "content": "c"}]`,
		"unknown om event":   `[{"name": "x", "title": "X", "class": "none", "hooks": {"events": {"turn": "Stop"}, "transcript": "generic"}}]`,
		"unknown resolver":   `[{"name": "x", "title": "X", "class": "none", "hooks": {"events": {"turn-end": "Stop"}, "transcript": "generic", "transcriptFrom": [{"resolver": "guess"}]}}]`,
		"path and resolver":  `[{"name": "x", "title": "X", "class": "none", "hooks": {"events": {"turn-end": "Stop"}, "transcript": "generic", "transcriptFrom": [{"resolver": "crush-db", "path": "~/x"}]}}]`,
		"bad record role":    `[{"name": "x", "title": "X", "class": "none", "hooks": {"events": {"turn-end": "Stop"}, "transcript": "generic", "record": {"fields": {"p": "system"}}}}]`,
		"step without path":  `[{"name": "x", "title": "X", "class": "none", "install": [{"kind": "text-file", "content": "c"}]}]`,
		"unknown step kind":  `[{"name": "x", "title": "X", "class": "none", "install": [{"kind": "curl"}]}]`,
		"bad name":           `[{"name": "X Y", "title": "X", "class": "none"}]`,
	} {
		var d any
		if err := json.Unmarshal([]byte(doc), &d); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if errs := v.validate(v.root, d, "doc"); len(errs) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
}

// The schema's named kinds are exactly the ones the Go interpreter knows.
func TestRegistrySchemaNamesMatchCode(t *testing.T) {
	v := loadSchema(t)
	enum := func(ref string) []string {
		node, err := v.resolve(ref)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, e := range node.(map[string]any)["enum"].([]any) {
			out = append(out, e.(string))
		}
		sort.Strings(out)
		return out
	}
	sortedKeys := func(m any) []string {
		var out []string
		switch x := m.(type) {
		case map[string]func(cwd, sessionID string) string:
			for k := range x {
				out = append(out, k)
			}
		case map[string]func(raw map[string]any) string:
			for k := range x {
				out = append(out, k)
			}
		case map[string]func(raw map[string]any) []map[string]any:
			for k := range x {
				out = append(out, k)
			}
		}
		sort.Strings(out)
		return out
	}
	var kinds []string
	for k := range installKinds {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var events []string
	for ev := range hookTimeouts {
		events = append(events, ev)
	}
	sort.Strings(events)
	for _, c := range []struct {
		ref  string
		want []string
	}{
		{"#/$defs/pathCandidate/properties/resolver", sortedKeys(transcriptResolvers)},
		{"#/$defs/sessionCandidate/properties/resolver", sortedKeys(sessionResolvers)},
		{"#/$defs/record/properties/mapper", sortedKeys(recordMappers)},
		{"#/$defs/step/properties/kind", kinds},
		{"#/$defs/hooks/properties/events/propertyNames", events},
	} {
		if got := enum(c.ref); strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: schema %v, code %v", c.ref, got, c.want)
		}
	}
	// Every field the loader reads is in the schema, and no other.
	for _, c := range []struct {
		ref string
		typ reflect.Type
	}{
		{"#/$defs/harness", reflect.TypeOf(harnessData{})},
		{"#/$defs/hooks", reflect.TypeOf(hookData{})},
		{"#/$defs/pathCandidate", reflect.TypeOf(pathCandidate{})},
		{"#/$defs/sessionCandidate", reflect.TypeOf(sessionCandidate{})},
		{"#/$defs/record", reflect.TypeOf(recordData{})},
		{"#/$defs/step", reflect.TypeOf(installStep{})},
	} {
		node, err := v.resolve(c.ref)
		if err != nil {
			t.Fatal(err)
		}
		var schema, code []string
		for name := range node.(map[string]any)["properties"].(map[string]any) {
			schema = append(schema, name)
		}
		for i := 0; i < c.typ.NumField(); i++ {
			code = append(code, strings.Split(c.typ.Field(i).Tag.Get("json"), ",")[0])
		}
		sort.Strings(schema)
		sort.Strings(code)
		if strings.Join(schema, ",") != strings.Join(code, ",") {
			t.Errorf("%s: schema fields %v, loader fields %v", c.ref, schema, code)
		}
	}
}
