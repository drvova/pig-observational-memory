package enginerpc_test

import (
	"encoding/json"
	"strings"
)

// asGo is v as the Go engine writes it: the one member the image writes differently by design is
// a record's writer engine (contracts/encoding.md), so "lisp" reads as "go", in decoded objects and
// in JSON text held in strings alike. Both sides of a comparison go through it (a JSON round trip).
func asGo(v any) any {
	encoded, err := json.Marshal(v)
	if err != nil {
		return v
	}
	text := strings.ReplaceAll(string(encoded), `"engine":"lisp"`, `"engine":"go"`)
	text = strings.ReplaceAll(text, `\"engine\":\"lisp\"`, `\"engine\":\"go\"`)
	var out any
	if json.Unmarshal([]byte(text), &out) != nil {
		return v
	}
	return out
}
