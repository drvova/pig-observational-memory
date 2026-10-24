package ledger_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
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

// The tables view groups by canonical subject, the qualifier joined into scope; the stored
// records keep their subjects.
func TestTablesFormatGroupsCanonicalSubjects(t *testing.T) {
	observations := []ledger.Observation{}
	for i, subject := range realSessionSubjects {
		subject := subject
		observations = append(observations, tf.Observation(padID(string(rune('a'+i)), 'a'), func(o *ledger.Observation) {
			o.Timestamp = "2026-05-0" + string(rune('1'+i)) + " 10:00"
			o.Content = "Note about " + subject + "."
			o.Kind, o.Subject = "fact", subject
			if subject == "running shoes" {
				o.Scope = "trail"
			}
		}))
	}
	stored := append([]ledger.Observation{}, observations...)
	text := ledger.RenderSummaryWithOptions(nil, observations, 0, ledger.RenderOptions{}).Text
	if !reflect.DeepEqual(observations, stored) {
		t.Fatal("rendering changed the records")
	}
	decoded, err := tables.Rows.Decode(strings.TrimPrefix(text, ledger.TablesUsageInstructions+"\n\n"))
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	var running tables.Table
	for _, table := range decoded {
		names = append(names, table.Name)
		if table.Name == "notes.running" {
			running = table
		}
	}
	if got := strings.Join(names, " "); got != "notes.running notes.half_marathon_training notes.fitness_routine notes.health_metrics" {
		t.Fatalf("tables %q", got)
	}
	scopes := []string{}
	for r := range running.Rows {
		scope, _ := running.Cell(r, "scope")
		scopes = append(scopes, scope)
	}
	if got := strings.Join(scopes, ","); got != ",training,gear,trail; shoes" {
		t.Fatalf("scopes %q", got)
	}
}

// The observer's memory lists the canonical subjects most used first, counted over every
// observation; within a budget it lists only what the kept observations do not show.
func TestBoundObserverMemoryListsCanonicalSubjects(t *testing.T) {
	observations := []ledger.Observation{}
	for i, subject := range append(realSessionSubjects, "running", "") {
		observations = append(observations, tf.Observation(padID(string(rune('a'+i)), 'a'), func(o *ledger.Observation) { o.Subject = subject }))
	}
	memory := ledger.BoundObserverMemory(nil, observations, 0)
	lines := []string{}
	for _, s := range memory.Subjects {
		lines = append(lines, s.Line())
	}
	if got := strings.Join(lines, "|"); got != "running: 5|fitness routine: 1|half marathon training: 1|health metrics: 1" {
		t.Fatalf("subjects %q", got)
	}
	// Every observation fits: only the subject with several notes is worth a line.
	roomy := ledger.BoundObserverMemory(nil, observations, 2000)
	if len(roomy.Subjects) != 1 || roomy.Subjects[0] != (ledger.SubjectCount{Subject: "running", Count: 5}) || roomy.OmittedSubjects != 3 {
		t.Fatalf("roomy subjects %+v omitted %d", roomy.Subjects, roomy.OmittedSubjects)
	}
	// Below the header's cost there is no list.
	if tight := ledger.BoundObserverMemory(nil, observations, 20); tight.Subjects != nil || tight.OmittedSubjects != 4 {
		t.Fatalf("tight subjects %+v omitted %d", tight.Subjects, tight.OmittedSubjects)
	}
	// The observer reads the kept observations with their canonical subject.
	for _, line := range roomy.ObserverLines() {
		if strings.Contains(line, "subject: running shoes") {
			t.Fatalf("raw nested subject shown: %s", line)
		}
	}
	// Without subjects the slice is the worker memory's.
	plain := []ledger.Observation{tf.Observation(padID("a", 'a'))}
	if got, want := ledger.BoundObserverMemory(nil, plain, 50), ledger.BoundWorkerMemory(nil, plain, 50, false); got.Subjects != nil || len(got.Observations) != len(want.Observations) {
		t.Fatalf("plain %+v", got)
	}
}

// Many distinct subjects do not crowd out the reflections, and the rendered prompt (subject
// header included) stays within the budget.
func TestBoundObserverMemorySubjectsKeepReflections(t *testing.T) {
	observations := []ledger.Observation{}
	for i := range 400 {
		observations = append(observations, tf.Observation(fmt.Sprintf("%012x", i+1), func(o *ledger.Observation) {
			o.Subject = fmt.Sprintf("topic %d", i)
			o.Content = "Short note."
		}))
	}
	reflections := []ledger.Reflection{}
	for i := range 20 {
		reflections = append(reflections, ledger.Reflection{ID: fmt.Sprintf("r%011x", i+1), Content: "A durable reflection about the user."})
	}
	const budget = 2000
	memory := ledger.BoundObserverMemory(reflections, observations, budget)
	if len(memory.Reflections) != len(reflections) {
		t.Fatalf("kept %d of %d reflections", len(memory.Reflections), len(reflections))
	}
	if len(memory.Subjects) == 0 {
		t.Fatal("no subject listed")
	}
	tokens := 0
	for _, r := range memory.Reflections {
		tokens += tokens4(ledger.ReflectionToSummaryLine(r))
	}
	for _, line := range memory.ObserverLines() {
		tokens += tokens4(line)
	}
	tokens += tokens4(ledger.SubjectsHeader) + 1
	for _, s := range memory.Subjects {
		tokens += tokens4(s.Line())
	}
	if tokens > budget {
		t.Fatalf("rendered %d tokens, budget %d", tokens, budget)
	}
}

// tokens4 is the worker memory's line cost.
func tokens4(line string) int { return (len([]rune(line))+3)/4 + 1 }

// The observer line shows the canonical subject, and what the written subject adds after the
// stored scope.
func TestCanonicalObserverLine(t *testing.T) {
	o := tf.Observation(padID("a", 'a'), func(o *ledger.Observation) { o.Kind, o.Subject, o.Scope = "fact", "running shoes", "trail" })
	canonical := ledger.CanonicalSubjects([]string{"running", "running shoes"})
	if got := ledger.ObservationToCanonicalObserverLine(o, canonical[o.Subject]); !strings.HasSuffix(got, "{kind: fact; subject: running; scope: trail; shoes}") {
		t.Fatalf("line %q", got)
	}
	if got, want := ledger.ObservationToObserverLine(o), ledger.ObservationToSummaryLine(o)+" {kind: fact; subject: running shoes; scope: trail}"; got != want {
		t.Fatalf("raw line %q, want %q", got, want)
	}
}
