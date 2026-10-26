package memquery

import (
	"reflect"
	"testing"
)

// Results name the records they show, so the harness can check them against the summary.
func TestResultHitsAndParseError(t *testing.T) {
	env := envWith(t)
	if r := run(t, env, `notes | where subject=baking`); !reflect.DeepEqual(r.Hits, []string{obsBake, obsBake2}) {
		t.Fatalf("notes hits %v", r.Hits)
	}
	if r := run(t, env, `reflections`); !reflect.DeepEqual(r.Hits, []string{refBake}) {
		t.Fatalf("reflections hits %v", r.Hits)
	}
	if r := run(t, env, "recall "+obsBake); !reflect.DeepEqual(r.Hits, []string{obsBake}) {
		t.Fatalf("recall hits %v", r.Hits)
	}
	for _, q := range []string{`notes | count by subject`, `history | search "sourdough"`} {
		if r := run(t, env, q); len(r.Hits) != 0 {
			t.Fatalf("%s hits %v", q, r.Hits)
		}
	}
	if r := Run(env, `notes |`); !r.IsError || !r.ParseError {
		t.Fatalf("syntax error %+v", r)
	}
	if r := Run(env, `notes | where colour=red`); !r.IsError || r.ParseError {
		t.Fatalf("a parsed query that fails is not a syntax error: %+v", r)
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
