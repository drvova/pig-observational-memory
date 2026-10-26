package om

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
)

// answer is what the installed fake transport does; the tests set it.
var answer func(method string, out any, args ...any) error

func TestMain(m *testing.M) {
	// With no transport, every call is ErrNoCall: a read panics with it, a call with an error
	// result returns it.
	if _, err := Recall("x"); !errors.Is(err, ErrNoCall()) {
		panic("Recall without a transport: " + errString(err))
	}
	func() {
		defer func() {
			if r := recover(); r == nil || !errors.Is(r.(error), ErrNoCall()) {
				panic("Notes without a transport did not panic with ErrNoCall")
			}
		}()
		Notes()
	}()
	omwire.Install(func(method string, out any, args ...any) error { return answer(method, out, args...) })
	defer func() {
		if recover() == nil {
			panic("a second Install did not panic")
		}
		os.Exit(m.Run())
	}()
	omwire.Install(func(string, any, ...any) error { return nil })
}

func errString(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// Every stub and value method calls a method omwire lists, with arguments that encode, and
// decodes the answer into its result.
func TestStubsCallListedMethods(t *testing.T) {
	var called []string
	answer = func(method string, out any, args ...any) error {
		called = append(called, method)
		if _, err := json.Marshal(args); err != nil {
			t.Errorf("%s: %v", method, err)
		}
		return nil
	}
	Notes()
	Reflections()
	History()
	Recall("a")
	NoteByID("a")
	ReflectionByID("a")
	Files()
	Commits()
	Attempts(Failed())
	Dropped()
	Subjects()
	Saved()
	Info()
	Layer()
	Retired()
	Str("s")
	Save("n", "lesson", "d", Body("b"), Quoting("q"))
	Edit("n", SetBody("b"))
	ForgetMemory("n")
	Feedback("n", "helped", "q")
	LayerForget("detector", "x")
	LayerRollback(1)
	Propose("detector", "x", "t")
	Define("x")
	Note{ID: "a"}.CitedBy()
	Note{ID: "a"}.Recall()
	Reflection{ID: "r"}.Supporting()
	Entry{ID: "e"}.Recall()
	for _, m := range called {
		if !slices.Contains(omwire.Methods, m) {
			t.Errorf("%s is not in omwire.Methods", m)
		}
	}
	for _, m := range omwire.Methods {
		if m != omwire.MMan && m != omwire.MKept && m != omwire.MForget && !slices.Contains(called, m) {
			t.Errorf("no stub calls %s", m)
		}
	}
	// An answer's error: a read panics with it named, a call with an error result returns it.
	answer = func(string, any, ...any) error { return errors.New("nope") }
	if _, err := Save("n", "lesson", "d"); err == nil || err.Error() != "om.Save: nope" {
		t.Fatalf("Save: %v", err)
	}
	func() {
		defer func() {
			if r := recover(); r == nil || r.(error).Error() != "om.Notes: nope" {
				t.Fatalf("Notes panicked with %v", r)
			}
		}()
		Notes()
	}()
}

// Options merge in order, scalars set later winning and searches and filters accumulating, and
// a chained option never changes the one it was chained on.
func TestOptionsMergeAndDoNotAlias(t *testing.T) {
	var got omwire.Options
	answer = func(method string, out any, args ...any) error {
		got = args[0].(omwire.Options)
		return nil
	}
	base := Search("a").Where("kind", "file")
	one := base.Search("b").Limit(5)
	two := base.Search("c").Through("e9")
	Notes(one, two, From("e1"), Limit(50))
	if !slices.Equal(got.Search, []string{"a", "b", "a", "c"}) || len(got.Where) != 2 || got.From != "e1" || got.Through != "e9" || *got.Limit != 50 {
		t.Fatalf("merged %+v", got)
	}
	Notes(base)
	if !slices.Equal(got.Search, []string{"a"}) || len(got.Where) != 1 || got.Limit != nil {
		t.Fatalf("base changed: %+v", got)
	}
	Notes(Kind("file"), Subject("oven"), WithStatus("proposed"), SortBy("when", true), ByEvent(), AsOf(), Worked(), Open(), Offset(3), Contains("content", "x"), Since("2026-01-01"), Until("2026-02-01"))
	if got.Where[0] != (omwire.Pair{Field: "kind", Value: "file"}) || got.Subject != "oven" || got.Status != "proposed" || got.SortField != "when" || !got.SortDesc ||
		!got.ByEvent || !got.AsOf || !got.Worked || !got.Open || *got.Offset != 3 || got.Contains[0].Value != "x" || got.Since != "2026-01-01" || got.Until != "2026-02-01" {
		t.Fatalf("options %+v", got)
	}
}

// The list methods run on the rows held: ids, counts, groups, filters, and the table in the
// rows codec with the counts and the next page when cut.
func TestListMethods(t *testing.T) {
	l := NoteList{Rows: []Note{{ID: "a", Kind: "file", Content: "x, y"}, {ID: "b", Kind: "fact", Content: "z"}, {ID: "c", Kind: "file", Fields: map[string]string{"mood": "ok"}}}, Matched: 5, Showing: 3, Next: "om.Notes(om.Through(`e1`))"}
	if !slices.Equal(l.IDs(), []string{"a", "b", "c"}) || l.Len() != 3 {
		t.Fatal(l.IDs())
	}
	if c := l.Count("kind"); c["file"] != 2 || c["fact"] != 1 {
		t.Fatal(c)
	}
	if c := l.Count("mood"); c["ok"] != 1 {
		t.Fatal(c)
	}
	if g := l.Group("kind"); g["file"].Len() != 2 || g["fact"].IDs()[0] != "b" {
		t.Fatal(g)
	}
	if f := l.Filter(func(n Note) bool { return n.Kind == "file" }); !slices.Equal(f.IDs(), []string{"a", "c"}) || f.Matched != 2 {
		t.Fatal(f)
	}
	s := l.String()
	if !strings.HasPrefix(s, "notes[3]{id,") || !strings.Contains(s, `"x, y"`) || !strings.Contains(s, "matched 5, showing 3") || !strings.Contains(s, "next page: om.Notes(om.Through(`e1`))") {
		t.Fatal(s)
	}
	e := EntryList{Counts: map[string]int{"oven": 2, "rye": 0}}
	if s := e.String(); !strings.Contains(s, "oven: 2, rye: 0") {
		t.Fatal(s)
	}
	if s := (Text{Body: "page", From: 0, To: 4, Total: 10, Next: "om.Recall(`e1`, om.Offset(4))"}).String(); !strings.Contains(s, "[characters 0-4 of 10]") || !strings.Contains(s, "next page: ") {
		t.Fatal(s)
	}
	if s := (StatusInfo{Lines: []string{"a"}, Models: []ModelStatus{{Model: "m", Lines: []string{"b"}}}}).String(); s != "a\nmodel m:\n  b" {
		t.Fatalf("%q", s)
	}
}
