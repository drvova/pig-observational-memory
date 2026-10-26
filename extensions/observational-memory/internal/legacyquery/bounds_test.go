package legacyquery

import (
	"strconv"
	"strings"
	"testing"
)

// The defined-query bounds stay the load checks of stored defined queries.
func TestCheckDefinitionBounds(t *testing.T) {
	if err := CheckDefinitionBounds(`notes | search "sourdough" | count`); err != nil {
		t.Fatal(err)
	}
	long := "notes | search " + strconv.Quote(strings.Repeat("x", MaxDefinitionLiteralChars+1))
	if err := CheckDefinitionBounds(long); err == nil || !strings.Contains(err.Error(), "quoted text") {
		t.Fatalf("long literal: %v", err)
	}
	if err := CheckDefinitionBounds("notes" + strings.Repeat(" | count", MaxDefinitionQueryChars)); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Fatalf("long query: %v", err)
	}
	// An unterminated text runs to the end, and an escaped quote stays inside its text.
	if got := QuotedTexts(`a "b\"c" "d`); len(got) != 2 || got[0] != `b\"c` || got[1] != "d" {
		t.Fatalf("quoted texts %q", got)
	}
}

func TestSubstitute(t *testing.T) {
	cases := []struct{ query, input, want string }{
		{`recall $input`, "3f9a2c1d7b4e", `recall 3f9a2c1d7b4e`},
		{`notes | where subject=$input`, "gaming hours", `notes | where subject="gaming hours"`},
		{`notes | search "$input"`, `say "hi" \ now`, `notes | search "say \"hi\" \\ now"`},
		{`notes | search $input`, "a | forget name=x", `notes | search "a | forget name=x"`},
		{`notes | search "\"$input\""`, "x", `notes | search "\"x\""`},
		{`notes | limit 5`, "ignored", `notes | limit 5`},
		{`recall $input`, "", `recall ""`},
	}
	for _, c := range cases {
		if got := Substitute(c.query, c.input); got != c.want {
			t.Errorf("Substitute(%q, %q) = %q, want %q", c.query, c.input, got, c.want)
		}
	}
	// An input never turns a read into a write or adds stages.
	q, err := Parse(Substitute(`notes | search $input`, `x" | forget name=y`))
	if err != nil || q.Write != "" || len(q.Stages) != 1 {
		t.Fatalf("injected query %+v, %v", q, err)
	}
}
