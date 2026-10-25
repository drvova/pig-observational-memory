package memquery

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// rangeBranch is four turns with one note anchored at each and a reflection citing the last
// two notes.
func rangeBranch() []session.Entry {
	note := func(id, source, content string) ledger.Observation {
		return tf.Observation(id, func(o *ledger.Observation) { o.SourceEntryIDs, o.Content = []string{source}, content })
	}
	return []session.Entry{
		tf.RawMessage("r1", "first turn"),
		tf.RawMessage("r2", "second turn"),
		tf.RawMessage("r3", "third turn"),
		tf.RawMessage("r4", "fourth turn"),
		tf.ObservationsRecordedEntry("om", []ledger.Observation{
			note("f1aaaaaaaaaa", "r1", "note one"),
			note("f2aaaaaaaaaa", "r2", "note two"),
			note("f3aaaaaaaaaa", "r3", "note three"),
			note("f4aaaaaaaaaa", "r4", "note four"),
			note("f5aaaaaaaaaa", "elsewhere", "note off the branch"),
		}, "r4"),
		tf.ReflectionsRecordedEntry("rf", []ledger.Reflection{tf.Reflection("f9aaaaaaaaaa", []string{"f4aaaaaaaaaa", "f3aaaaaaaaaa"})}, "r4"),
	}
}

func TestFromThroughBoundByBranchPosition(t *testing.T) {
	env := Env{Entries: rangeBranch()}
	cases := []struct {
		query     string
		want, not []string
	}{
		{"notes | from r2 | through r3", []string{"note two", "note three"}, []string{"note one", "note four", "off the branch"}},
		{"notes | from r3", []string{"note three", "note four"}, []string{"note two", "off the branch"}},
		{"notes | through r1", []string{"note one"}, []string{"note two", "off the branch"}},
		{"history | from r2 | through r3", []string{"second turn", "third turn"}, []string{"first turn", "fourth turn"}},
		// A reflection sits at its earliest cited note.
		{"reflections | from r3 | through r3", []string{"f9aaaaaaaaaa"}, nil},
		{"reflections | through r2", nil, []string{"f9aaaaaaaaaa"}},
		{`notes | from "r2" | through r2 | count`, []string{"1"}, nil},
	}
	for _, c := range cases {
		res := Run(env, c.query)
		if res.IsError {
			t.Fatalf("%s: %s", c.query, res.Text)
		}
		for _, w := range c.want {
			if !strings.Contains(res.Text, w) {
				t.Errorf("%s lacks %q:\n%s", c.query, w, res.Text)
			}
		}
		for _, n := range c.not {
			if strings.Contains(res.Text, n) {
				t.Errorf("%s has %q:\n%s", c.query, n, res.Text)
			}
		}
	}
}

func TestFromThroughErrors(t *testing.T) {
	env := Env{Entries: rangeBranch()}
	for query, want := range map[string]string{
		"notes | from":               "from needs an entry id",
		"notes | from gone":          "entry gone is not on this branch",
		"notes | count | through r2": "through filters records, not counts",
		"harness | from r1":          "the harness layer is not available",
	} {
		res := Run(env, query)
		if !res.IsError || !strings.Contains(res.Text, want) {
			t.Errorf("%s: %s, want %q", query, res.Text, want)
		}
	}
	// With a source that has no branch position.
	env.Layer = fakeLayerer{}
	if res := Run(env, "harness | from r1"); !res.IsError || !strings.Contains(res.Text, "from filters notes, reflections and history") {
		t.Errorf("harness from: %s", res.Text)
	}
}

// fakeLayerer is a layer with nothing in it.
type fakeLayerer struct{}

func (fakeLayerer) Define(string, string, string) (string, error) { return "", nil }
func (fakeLayerer) Forget(string, string) (string, error)         { return "", nil }
func (fakeLayerer) Rollback(int, string) (string, error)          { return "", nil }
func (fakeLayerer) Listing() ([]HarnessRow, error)                { return nil, nil }

// A memory given directly (Env.Records) is placed on the branch too.
func TestFromThroughOverRecords(t *testing.T) {
	entries := rangeBranch()
	folded := ledger.FoldLedger(entries, "")
	env := Env{Entries: entries, Records: &Records{Observations: folded.ActiveObservations[:2], Reflections: folded.Reflections}}
	res := Run(env, "notes | from r2")
	if res.IsError || !strings.Contains(res.Text, "note two") || strings.Contains(res.Text, "note one") || strings.Contains(res.Text, "note three") {
		t.Fatalf("%s", res.Text)
	}
}
