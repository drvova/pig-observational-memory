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

func TestAddedSpecifics(t *testing.T) {
	user := []string{"Lisbon is my race: the half-marathon on March 8. I run long on Sunday and have €1,400 saved."}
	// A name opening the user's sentence, a number with a separator and a possessive are given.
	if got := AddedSpecifics("Your Lisbon's half on March 8 and the 1400 euros, with Sunday long runs.", user); len(got) != 0 {
		t.Fatalf("an echo added %v", got)
	}
	got := AddedSpecifics("For the Lisbon half, follow the Hanson plan for 18 weeks.", user)
	if !reflect.DeepEqual(got, []string{"18", "hanson"}) {
		t.Fatalf("added %v", got)
	}
	// Whole items only: "8" in the carrier does not give "18", nor "18" give "8".
	if got := AddedSpecifics("Race on the 8th.", []string{"race on the 18th"}); !reflect.DeepEqual(got, []string{"8"}) {
		t.Fatalf("substring matched: %v", got)
	}
}

// A carrier's lowercase words give no name: a common word never cancels a name the reply
// introduced, and a common noun never cancels a name ending in s. A capitalized word does, also
// opening a sentence, and a name gives its plural name.
func TestAddedSpecificsCommonWordsGiveNoName(t *testing.T) {
	cases := []struct {
		text, carrier string
		want          []string
	}{
		{"Fly to the US.", "Can you tell us where to fly?", []string{"us"}},
		{"I'd try Nice instead.", "Somewhere nice and warm please", []string{"nice"}},
		{"See you in May then", "may I go in spring", []string{"may"}},
		{"Ask at Banks for a loan.", "which bank should I use?", []string{"banks"}},
		{"Walk in the Woods.", "I like the woods", []string{"woods"}},
		// A name the user wrote in lowercase is not given either: names compare as names.
		{"Race in Lisbon then.", "the race is in lisbon", []string{"lisbon"}},
		{"Ask at Banks then.", "Banks is my local branch", []string{}},
		{"Long runs on a Sunday.", "Sundays are my long days", []string{}},
		{"Long runs on Sundays.", "My long day is Sunday", []string{}},
	}
	for _, c := range cases {
		if got := AddedSpecifics(c.text, []string{c.carrier}); !reflect.DeepEqual(got, c.want) {
			t.Errorf("AddedSpecifics(%q, %q) = %q, want %q", c.text, c.carrier, got, c.want)
		}
	}
}

// Numbers match as Specifics normalizes them, never by paraphrase: a number written as a word
// or in another unit form is a different item (pinned, so a change to the matching is a
// deliberate one).
func TestAddedSpecificsNumbersMatchOnlyAsWritten(t *testing.T) {
	cases := []struct {
		text, carrier string
		want          []string
	}{
		{"Book it for 2 adults.", "a room for two adults", []string{"2"}},
		{"That leaves $1.4k.", "my budget is 1,400", []string{"1.4"}},
		{"Under €140 then.", "about 140 euros", []string{}},
	}
	for _, c := range cases {
		if got := AddedSpecifics(c.text, []string{c.carrier}); !reflect.DeepEqual(got, c.want) {
			t.Errorf("AddedSpecifics(%q, %q) = %q, want %q", c.text, c.carrier, got, c.want)
		}
	}
}
