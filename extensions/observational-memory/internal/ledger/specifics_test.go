package ledger

import (
	"reflect"
	"testing"
)

func TestSpecificsFindsNumbersDatesAndNames(t *testing.T) {
	cases := map[string][]string{
		"User got a peace lily two weeks ago and a snake plant last month.": {"two weeks ago", "last month"},
		"User bought a Gucci handbag for $1,200 and boots for $500.":        {"1200", "500", "gucci"},
		"User asked for tips on organizing a closet.":                       {},
		"They visited Rhythm Central. The shop was busy.":                   {"rhythm", "central"},
	}
	for text, want := range cases {
		if got := Specifics(text); !reflect.DeepEqual(got, want) {
			t.Errorf("Specifics(%q) = %q, want %q", text, got, want)
		}
	}
}

func TestMissingSpecifics(t *testing.T) {
	text := "User paid $1,200 for a Gucci handbag yesterday."
	if got := MissingSpecifics(text, []string{"User owns a Gucci handbag bought for $1200 yesterday."}); len(got) != 0 {
		t.Fatalf("a carrier with every specific should leave none missing, got %q", got)
	}
	if got := MissingSpecifics(text, []string{"User owns a designer handbag."}); !reflect.DeepEqual(got, []string{"1200", "yesterday", "gucci"}) {
		t.Fatalf("missing = %q", got)
	}
}
