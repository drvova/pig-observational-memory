// Package jsonschema is the small JSON Schema (draft 2020-12) validator the engine's contract
// tests use (contracts/README.md): only the keywords the schemas under contracts/schemas use,
// so the engine takes no dependency for it.
//
// Supported: type (a name or a list of names), enum, const, required, properties,
// additionalProperties (a boolean or a schema), items, minItems, minLength, pattern, minimum,
// anyOf, $ref (to "#/$defs/<name>" in the same document or "<file>#/$defs/<name>" in another
// document of the Set), $defs. Annotations ($schema, $id, title, description, $comment,
// examples, format) are accepted and ignored. Any other keyword is an error when the schema is
// loaded, so a schema never silently relies on a keyword this validator does not check.
package jsonschema

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

// Set is a directory of schema documents, by file name, that can refer to each other.
type Set struct {
	docs     map[string]map[string]any
	patterns map[string]*regexp.Regexp
}

var known = map[string]bool{
	"type": true, "enum": true, "const": true, "required": true, "properties": true,
	"additionalProperties": true, "items": true, "minItems": true, "minLength": true,
	"pattern": true, "minimum": true, "anyOf": true, "$ref": true, "$defs": true,
	"$schema": true, "$id": true, "title": true, "description": true, "$comment": true,
	"examples": true, "format": true,
}

// Load reads every *.schema.json file in dir and checks that each uses only supported keywords.
func Load(dir string) (*Set, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.schema.json"))
	if err != nil {
		return nil, err
	}
	s := &Set{docs: map[string]map[string]any{}, patterns: map[string]*regexp.Regexp{}}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var doc map[string]any
		if err := json.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		s.docs[filepath.Base(f)] = doc
	}
	for name, doc := range s.docs {
		if err := s.check(name, doc, ""); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Names are the documents' file names, sorted.
func (s *Set) Names() []string {
	out := make([]string, 0, len(s.docs))
	for n := range s.docs {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// check verifies a schema's keywords, its regexes and its references, recursively.
func (s *Set) check(doc string, schema map[string]any, path string) error {
	for k, v := range schema {
		if !known[k] {
			return fmt.Errorf("%s%s: unsupported keyword %q", doc, path, k)
		}
		switch k {
		case "properties", "$defs":
			m, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("%s%s/%s: not an object", doc, path, k)
			}
			for name, sub := range m {
				subm, ok := sub.(map[string]any)
				if !ok {
					return fmt.Errorf("%s%s/%s/%s: not a schema", doc, path, k, name)
				}
				if err := s.check(doc, subm, path+"/"+k+"/"+name); err != nil {
					return err
				}
			}
		case "items", "additionalProperties":
			if subm, ok := v.(map[string]any); ok {
				if err := s.check(doc, subm, path+"/"+k); err != nil {
					return err
				}
			} else if _, ok := v.(bool); !ok || k == "items" {
				return fmt.Errorf("%s%s/%s: not a schema", doc, path, k)
			}
		case "anyOf":
			list, ok := v.([]any)
			if !ok || len(list) == 0 {
				return fmt.Errorf("%s%s/anyOf: not a list of schemas", doc, path)
			}
			for i, sub := range list {
				subm, ok := sub.(map[string]any)
				if !ok {
					return fmt.Errorf("%s%s/anyOf/%d: not a schema", doc, path, i)
				}
				if err := s.check(doc, subm, fmt.Sprintf("%s/anyOf/%d", path, i)); err != nil {
					return err
				}
			}
		case "pattern":
			p, _ := v.(string)
			re, err := regexp.Compile(p)
			if err != nil {
				return fmt.Errorf("%s%s/pattern: %w", doc, path, err)
			}
			s.patterns[p] = re
		case "$ref":
			ref, _ := v.(string)
			if _, _, err := s.resolve(doc, ref); err != nil {
				return fmt.Errorf("%s%s: %w", doc, path, err)
			}
		}
	}
	return nil
}

// resolve finds the schema a $ref names, and the document it is in.
func (s *Set) resolve(doc, ref string) (string, map[string]any, error) {
	file, pointer, _ := strings.Cut(ref, "#")
	if file != "" {
		doc = file
	}
	root, ok := s.docs[doc]
	if !ok {
		return "", nil, fmt.Errorf("$ref %q: no document %q", ref, doc)
	}
	if pointer == "" {
		return doc, root, nil
	}
	name, ok := strings.CutPrefix(pointer, "/$defs/")
	if !ok || strings.Contains(name, "/") {
		return "", nil, fmt.Errorf("$ref %q: only #/$defs/<name> pointers are supported", ref)
	}
	defs, _ := root["$defs"].(map[string]any)
	target, ok := defs[name].(map[string]any)
	if !ok {
		return "", nil, fmt.Errorf("$ref %q: no definition %q in %s", ref, name, doc)
	}
	return doc, target, nil
}

// Validate checks a decoded JSON value (encoding/json's any) against the document named doc,
// returning every violation found (nil when it validates).
func (s *Set) Validate(doc string, value any) []error {
	root, ok := s.docs[doc]
	if !ok {
		return []error{fmt.Errorf("no schema %q", doc)}
	}
	var errs []error
	s.validate(doc, root, value, "", &errs)
	return errs
}

// ValidateJSON decodes data and validates it (Validate).
func (s *Set) ValidateJSON(doc string, data []byte) []error {
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return []error{err}
	}
	return s.Validate(doc, v)
}

func typeOf(v any) string {
	switch n := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case float64:
		if n == math.Trunc(n) && !math.IsInf(n, 0) {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	}
	return fmt.Sprintf("%T", v)
}

func typeMatches(want, got string) bool {
	return want == got || want == "number" && got == "integer"
}

func (s *Set) validate(doc string, schema map[string]any, v any, path string, errs *[]error) {
	fail := func(format string, args ...any) {
		where := path
		if where == "" {
			where = "/"
		}
		*errs = append(*errs, fmt.Errorf("%s: %s", where, fmt.Sprintf(format, args...)))
	}
	if ref, ok := schema["$ref"].(string); ok {
		d, target, err := s.resolve(doc, ref)
		if err != nil {
			fail("%v", err)
			return
		}
		s.validate(d, target, v, path, errs)
	}
	got := typeOf(v)
	if t, ok := schema["type"]; ok {
		var names []string
		switch tt := t.(type) {
		case string:
			names = []string{tt}
		case []any:
			for _, n := range tt {
				name, _ := n.(string)
				names = append(names, name)
			}
		}
		match := false
		for _, n := range names {
			match = match || typeMatches(n, got)
		}
		if !match {
			fail("type %s, want %s", got, strings.Join(names, " or "))
			return
		}
	}
	if c, ok := schema["const"]; ok && !equal(c, v) {
		fail("value %s, want %s", short(v), short(c))
	}
	if list, ok := schema["enum"].([]any); ok {
		in := false
		for _, e := range list {
			in = in || equal(e, v)
		}
		if !in {
			fail("value %s is not one of %s", short(v), short(list))
		}
	}
	if list, ok := schema["anyOf"].([]any); ok {
		matched := false
		var first []error
		for i, sub := range list {
			var subErrs []error
			s.validate(doc, sub.(map[string]any), v, path, &subErrs)
			if len(subErrs) == 0 {
				matched = true
				break
			}
			if i == 0 || len(subErrs) < len(first) {
				first = subErrs
			}
		}
		if !matched {
			fail("matches none of anyOf (closest: %v)", first)
		}
	}
	switch val := v.(type) {
	case string:
		if n, ok := schema["minLength"].(float64); ok && utf8.RuneCountInString(val) < int(n) {
			fail("string shorter than %d", int(n))
		}
		if p, ok := schema["pattern"].(string); ok && !s.patterns[p].MatchString(val) {
			fail("%s does not match %s", short(val), p)
		}
	case float64:
		if n, ok := schema["minimum"].(float64); ok && val < n {
			fail("%v is below %v", val, n)
		}
	case []any:
		if n, ok := schema["minItems"].(float64); ok && len(val) < int(n) {
			fail("%d items, want at least %d", len(val), int(n))
		}
		if items, ok := schema["items"].(map[string]any); ok {
			for i, item := range val {
				s.validate(doc, items, item, fmt.Sprintf("%s/%d", path, i), errs)
			}
		}
	case map[string]any:
		if req, ok := schema["required"].([]any); ok {
			for _, r := range req {
				if _, present := val[r.(string)]; !present {
					fail("missing required member %q", r)
				}
			}
		}
		props, _ := schema["properties"].(map[string]any)
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if sub, ok := props[k].(map[string]any); ok {
				s.validate(doc, sub, val[k], path+"/"+k, errs)
				continue
			}
			switch ap := schema["additionalProperties"].(type) {
			case bool:
				if !ap {
					fail("member %q is not allowed", k)
				}
			case map[string]any:
				s.validate(doc, ap, val[k], path+"/"+k, errs)
			}
		}
	}
}

func equal(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func short(v any) string {
	b, _ := json.Marshal(v)
	if len(b) > 80 {
		return string(b[:77]) + "..."
	}
	return string(b)
}
