package ledger_test

import (
	"math/rand"
	"reflect"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
)

// realSessionSubjects are the subjects an observer wrote for related notes in a real session.
var realSessionSubjects = []string{"running", "running training", "running gear", "running shoes", "half marathon training", "fitness routine", "health metrics"}

// The real session: the four running subjects group under running with their qualifier as
// scope; half marathon training does not contain running and stays separate, as do the
// unrelated subjects.
func TestCanonicalSubjectsRealSession(t *testing.T) {
	got := ledger.CanonicalSubjects(realSessionSubjects)
	want := map[string]ledger.Canonical{
		"running":                {Subject: "running"},
		"running training":       {Subject: "running", ExtraScope: "training"},
		"running gear":           {Subject: "running", ExtraScope: "gear"},
		"running shoes":          {Subject: "running", ExtraScope: "shoes"},
		"half marathon training": {Subject: "half marathon training"},
		"fitness routine":        {Subject: "fitness routine"},
		"health metrics":         {Subject: "health metrics"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CanonicalSubjects = %#v\nwant %#v", got, want)
	}
}

func TestSubjectKeyFoldsPluralsAndPunctuation(t *testing.T) {
	// The key is the subject's own words: case, punctuation between words and a possessive do
	// not matter, symbols inside a word do, and plurals are left to CanonicalSubjects.
	cases := map[string]string{
		"Running-Shoes":     "running shoes",
		"running   shoe":    "running shoe",
		"health metrics!":   "health metrics",
		"taxes & buzzes":    "taxes buzzes",
		"--":                "",
		"churches/branches": "churches branches",
		"user's dog":        "user dog",
		"User’s dog":        "user dog",
		"'quoted'":          "quoted",
		"o'brien":           "o'brien",
		"c++":               "c++",
		"c#":                "c#",
		"r&d budget":        "r&d budget",
		"c":                 "c",
	}
	for in, want := range cases {
		if got := ledger.SubjectKey(in); got != want {
			t.Errorf("SubjectKey(%q) = %q, want %q", in, got, want)
		}
	}
	// Variants of one word set group under the lexicographically smallest spelling, adding no
	// scope; a plural folds into a singular another subject uses, and a plural or punctuated
	// longer subject still contains the shorter one.
	got := ledger.CanonicalSubjects([]string{"running shoes", "running shoe", "running-shoes", "shoelaces", "trail running shoes!"})
	want := map[string]ledger.Canonical{
		"running shoes":        {Subject: "running shoe"},
		"running shoe":         {Subject: "running shoe"},
		"running-shoes":        {Subject: "running shoe"},
		"shoelaces":            {Subject: "shoelaces"},
		"trail running shoes!": {Subject: "running shoe", ExtraScope: "trail"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CanonicalSubjects = %#v\nwant %#v", got, want)
	}
}

// A plural folds only into a singular some subject says, by the suffix rules; nothing is
// stripped on its own, so unrelated words stay apart.
func TestCanonicalSubjectsFoldPluralsOnlyIntoPresentWords(t *testing.T) {
	cases := []struct {
		subjects []string
		want     map[string]ledger.Canonical
	}{
		// A possessive is part of its word, not a word "s".
		{[]string{"user", "user's dog"}, map[string]ledger.Canonical{"user": {Subject: "user"}, "user's dog": {Subject: "user", ExtraScope: "dog"}}},
		// "news" is not a plural of "new".
		{[]string{"news", "new york trip"}, map[string]ledger.Canonical{"news": {Subject: "news"}, "new york trip": {Subject: "new york trip"}}},
		// Symbols inside a word keep it apart.
		{[]string{"c", "c++", "c#"}, map[string]ledger.Canonical{"c": {Subject: "c"}, "c++": {Subject: "c++"}, "c#": {Subject: "c#"}}},
		{[]string{"r", "r&d budget"}, map[string]ledger.Canonical{"r": {Subject: "r"}, "r&d budget": {Subject: "r&d budget"}}},
		// Without its singular present a plural stays as written.
		{[]string{"shoes", "boxes"}, map[string]ledger.Canonical{"shoes": {Subject: "shoes"}, "boxes": {Subject: "boxes"}}},
		// With it present, the suffix forms fold.
		{[]string{"status", "statuses"}, map[string]ledger.Canonical{"status": {Subject: "status"}, "statuses": {Subject: "status"}}},
		// One word set is spelled as its lexicographically smallest subject, plural or not.
		{[]string{"analysis", "analyses"}, map[string]ledger.Canonical{"analysis": {Subject: "analyses"}, "analyses": {Subject: "analyses"}}},
		{[]string{"story", "bedtime stories"}, map[string]ledger.Canonical{"story": {Subject: "story"}, "bedtime stories": {Subject: "story", ExtraScope: "bedtime"}}},
		{[]string{"box", "moving boxes"}, map[string]ledger.Canonical{"box": {Subject: "box"}, "moving boxes": {Subject: "box", ExtraScope: "moving"}}},
		// A bare "s" folds only onto a singular of four letters or more.
		{[]string{"car", "cars"}, map[string]ledger.Canonical{"car": {Subject: "car"}, "cars": {Subject: "cars"}}},
	}
	for _, c := range cases {
		if got := ledger.CanonicalSubjects(c.subjects); !reflect.DeepEqual(got, c.want) {
			t.Errorf("CanonicalSubjects(%q) = %#v\nwant %#v", c.subjects, got, c.want)
		}
	}
}

// The result depends only on the set: shuffling (and repeating) the input changes nothing.
func TestCanonicalSubjectsOrderIndependent(t *testing.T) {
	want := ledger.CanonicalSubjects(realSessionSubjects)
	r := rand.New(rand.NewSource(1))
	for range 50 {
		shuffled := append(append([]string{}, realSessionSubjects...), realSessionSubjects[r.Intn(len(realSessionSubjects))], "")
		r.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		if got := ledger.CanonicalSubjects(shuffled); !reflect.DeepEqual(got, want) {
			t.Fatalf("order %q: %#v\nwant %#v", shuffled, got, want)
		}
	}
}

// Canonicalizing the canonical subjects maps each to itself.
func TestCanonicalSubjectsIdempotent(t *testing.T) {
	for _, set := range [][]string{realSessionSubjects, {"a b", "b c", "a b c", "c", "b c d"}, {"running shoes", "running shoe", "trail running shoes"}} {
		canonical := map[string]bool{}
		for _, c := range ledger.CanonicalSubjects(set) {
			canonical[c.Subject] = true
		}
		again := []string{}
		for s := range canonical {
			again = append(again, s)
		}
		for s, c := range ledger.CanonicalSubjects(again) {
			if c.Subject != s || c.ExtraScope != "" {
				t.Errorf("set %q: canonical %q maps to %#v", set, s, c)
			}
		}
	}
}

// Ties between contained subjects go to the fewest words, then the smallest key; subjects
// with one word set are one subject.
func TestCanonicalSubjectsTies(t *testing.T) {
	got := ledger.CanonicalSubjects([]string{"trail running shoes", "trail", "running", "running shoes", "trail running"})
	if c := got["trail running shoes"]; c != (ledger.Canonical{Subject: "running", ExtraScope: "trail shoes"}) {
		t.Fatalf("trail running shoes: %#v", c)
	}
	if c := got["trail running"]; c != (ledger.Canonical{Subject: "running", ExtraScope: "trail"}) {
		t.Fatalf("trail running: %#v", c)
	}
	// The same words in another order, or repeated, are one subject, spelled as the smallest.
	got = ledger.CanonicalSubjects([]string{"running gear", "gear running", "marathon training", "training marathon", "new york", "new new york"})
	want := map[string]ledger.Canonical{
		"running gear":      {Subject: "gear running"},
		"gear running":      {Subject: "gear running"},
		"marathon training": {Subject: "marathon training"},
		"training marathon": {Subject: "marathon training"},
		"new york":          {Subject: "new new york"},
		"new new york":      {Subject: "new new york"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CanonicalSubjects = %#v\nwant %#v", got, want)
	}
}

// With no subject containing another, every subject maps to itself.
func TestCanonicalSubjectsNoNestingIsIdentity(t *testing.T) {
	set := []string{"baking", "gaming hours", "online courses", "fitness routine", "health metrics"}
	for s, c := range ledger.CanonicalSubjects(set) {
		if c.Subject != s || c.ExtraScope != "" {
			t.Errorf("%q maps to %#v", s, c)
		}
	}
}
