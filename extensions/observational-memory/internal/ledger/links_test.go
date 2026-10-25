package ledger_test

import (
	"encoding/json"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// An event date is the one day a record's relative phrases resolve to, never a span, never one
// of two dates, never a guess.
func TestEventDate(t *testing.T) {
	cases := []struct{ in, want string }{
		{"The release slipped to tomorrow.", "2023-05-25"},
		{"User ran last Tuesday.", "2023-05-23"},
		{"We met yesterday and again yesterday.", "2023-05-23"},
		{"The demo is due in 3 days.", "2023-05-27"},
		{"It took two weeks.", ""},               // a duration, not an offset
		{"We fly out next week.", ""},            // a week is a span
		{"The trip is next month.", ""},          // so is a month
		{"Yesterday we planned tomorrow.", ""},   // two different dates
		{"The release is on 2023-06-01.", ""},    // absolute dates are not resolved
		{"Ship it yesterday [=2023-05-01].", ""}, // already annotated: left alone
		{`She said "tomorrow" was fine.`, ""},    // quoted
		{"Plain text with no date at all.", ""},
	}
	for _, c := range cases {
		if got := ledger.EventDate(c.in, relAnchor); got != c.want {
			t.Errorf("EventDate(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	if got := ledger.EventDate("tomorrow", time.Time{}); got != "" {
		t.Errorf("zero anchor gave %q", got)
	}
}

// A possible update gives a different value of one specific type; a value added beside the old
// one, or a value of another type, updates nothing.
func TestUpdatesSpecific(t *testing.T) {
	cases := []struct {
		name, later, earlier string
		want                 bool
	}{
		{"number", "The pool size is 12.", "The pool size is 8.", true},
		{"amount", "The budget is $6,000.", "The budget is $5,000.", true},
		{"amount word", "The budget is 6000 USD.", "The budget is 5000 USD.", true},
		{"date", "The launch moved to 2026-06-02.", "The launch is on 2026-05-30.", true},
		{"path", "The config lives in deploy/prod.yaml.", "The config lives in deploy/staging.yaml.", true},
		{"same value", "The pool size is 8.", "The pool size is 8 for now.", false},
		{"added value", "The pool size is 8, with 2 spares.", "The pool size is 8.", false},
		{"other type", "The budget is $6000.", "The pool size is 8.", false},
		{"no specifics", "The pool is large.", "The pool is small.", false},
		{"annotated dates", "We ship tomorrow [=2026-05-02].", "We ship tomorrow [=2026-05-01].", true},
	}
	for _, c := range cases {
		if got := ledger.UpdatesSpecific(c.later, c.earlier); got != c.want {
			t.Errorf("%s: UpdatesSpecific(%q, %q) = %v, want %v", c.name, c.later, c.earlier, got, c.want)
		}
	}
}

// A negation attaches to the first specific after it in its clause.
func TestNegatedSpecifics(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"We use Redis, not Memcached.", []string{"memcached"}},
		{"We never deploy on Friday or Monday.", []string{"friday"}},
		{"The release does not ship with Postgres but with MySQL.", []string{"postgres"}},
		{"The team didn’t move to Kafka.", []string{"kafka"}},
		{"The pool is no longer 8.", []string{"8"}},
		{"The pool size is 5,000 and not 6,000.", []string{"6000"}},
		{"We deploy on Friday.", nil},
		{"Not now; we deploy on Friday.", nil}, // the negation's clause ends first
	}
	for _, c := range cases {
		got := ledger.NegatedSpecifics(c.in)
		want := map[string]bool{}
		for _, s := range c.want {
			want[s] = true
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("NegatedSpecifics(%q) = %v, want %v", c.in, got, want)
		}
	}
}

func TestNegationConflict(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"The release ships on Friday.", "The release does not ship on Friday.", true},
		{"We use Redis, not Memcached.", "The cache runs on Memcached.", true},
		{"We do not use Memcached.", "We never use Memcached.", false}, // both negate it
		{"The release ships on Friday.", "The release ships on Friday at noon.", false},
		{"We do not use Memcached.", "The release ships on Friday.", false}, // nothing shared
	}
	for _, c := range cases {
		if got := ledger.NegationConflict(c.a, c.b); got != c.want {
			t.Errorf("NegationConflict(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// A retraction withdraws what it supersedes; nothing else reads as retracted, and a retraction
// never holds the user class.
func TestRetractedIDsAndUserPriority(t *testing.T) {
	plan := tf.Observation("aaaaaaaaaaa1", func(o *ledger.Observation) { o.Kind, o.Status = "fact", ledger.StatusProposed })
	fact := tf.Observation("aaaaaaaaaaa2", func(o *ledger.Observation) { o.Kind = "fact" })
	retraction := tf.Observation("aaaaaaaaaaa3", func(o *ledger.Observation) {
		o.Kind, o.Status, o.Origin, o.Supersedes = "correction", ledger.StatusRetracted, ledger.OriginCode, []string{plan.ID}
	})
	correction := tf.Observation("aaaaaaaaaaa4", func(o *ledger.Observation) {
		o.Kind, o.Origin, o.Supersedes = "correction", ledger.OriginCode, []string{fact.ID}
	})
	got := ledger.RetractedIDs([]ledger.Observation{plan, fact, retraction, correction})
	if !reflect.DeepEqual(got, map[string]bool{plan.ID: true}) {
		t.Fatalf("retracted %v", got)
	}
	if ledger.UserPriority(retraction) || !ledger.UserPriority(correction) {
		t.Fatal("a retraction holds the user class, or a correction does not")
	}
}

// The level-0 fields round-trip through the ledger, and malformed ones are cleared without
// losing the record.
func TestLevel0FieldsRoundTrip(t *testing.T) {
	o := tf.Observation("aaaaaaaaaaa1", func(o *ledger.Observation) {
		o.EventDate, o.Status = "2026-05-02", ledger.StatusHypothetical
		o.PossiblyUpdates, o.Conflicts = []string{"bbbbbbbbbbb1"}, []string{"bbbbbbbbbbb2", "bbbbbbbbbbb3"}
	})
	raw, err := json.Marshal(o)
	if err != nil {
		t.Fatal(err)
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	back, ok := ledger.ObservationFrom(decoded)
	if !ok || !reflect.DeepEqual(back, o) {
		t.Fatalf("round trip %+v, want %+v", back, o)
	}
	m := decoded.(map[string]any)
	m["eventDate"], m["status"] = "next week", "done"
	m["possiblyUpdates"] = []any{"bbbbbbbbbbb1", "not-an-id", o.ID, "bbbbbbbbbbb1", 7}
	m["conflicts"] = "bbbbbbbbbbb2"
	back, ok = ledger.ObservationFrom(m)
	if !ok || back.EventDate != "" || back.Status != "" || !reflect.DeepEqual(back.PossiblyUpdates, []string{"bbbbbbbbbbb1"}) || back.Conflicts != nil {
		t.Fatalf("lenient read %+v ok=%v", back, ok)
	}
	// A record written before the fields existed reads back without them.
	plain := tf.Observation("aaaaaaaaaaa2")
	raw, _ = json.Marshal(plain)
	var written map[string]any
	_ = json.Unmarshal(raw, &written)
	for _, key := range []string{"eventDate", "status", "possiblyUpdates", "conflicts"} {
		if _, has := written[key]; has {
			t.Fatalf("empty %s is written: %s", key, raw)
		}
	}
}

// cited_by is computed by the fold from the reflections' citations, in ledger order, and is
// never stored.
func TestFoldCitedBy(t *testing.T) {
	a := tf.Observation("aaaaaaaaaaa1")
	b := tf.Observation("aaaaaaaaaaa2")
	r1 := ledger.Reflection{ID: "ccccccccccc1", Content: "one", SupportingObservationIDs: []string{a.ID, b.ID}, TokenCount: 1}
	r2 := ledger.Reflection{ID: "ccccccccccc2", Content: "two", SupportingObservationIDs: []string{a.ID, a.ID}, TokenCount: 1}
	entries := []ledger.Entry{
		tf.RawMessage("u1", "hello"),
		tf.ObservationsRecordedEntry("om1", []ledger.Observation{a, b}, "u1"),
		tf.ReflectionsRecordedEntry("rf1", []ledger.Reflection{r1, r2}, "u1"),
	}
	folded := ledger.FoldLedger(entries, "")
	want := map[string][]string{a.ID: {r1.ID, r2.ID}, b.ID: {r1.ID}}
	if !reflect.DeepEqual(folded.CitedBy, want) {
		t.Fatalf("cited by %v, want %v", folded.CitedBy, want)
	}
	if got := ledger.FoldLedger(entries[:2], "").CitedBy; len(got) != 0 {
		t.Fatalf("cited by without reflections %v", got)
	}
}

// UpdatedSpecifics are the earlier record's specifics the later one's update replaces: the parts
// of its values of the updated type, never another type's or a name.
func TestUpdatedSpecifics(t *testing.T) {
	for _, c := range []struct {
		later, earlier string
		want           []string
	}{
		{"The plan costs $45 a month", "The plan costs $40 a month", []string{"40"}},
		{"The release moved to 2026-05-09", "The release is on 2026-05-02 in Lisbon", []string{"2026", "05", "02"}},
		{"Deploy Atlas to /srv/b", "Deploy Atlas to /srv/a with Nginx", nil},
		// Only a value beside the old one: no update, nothing replaced.
		{"The plan costs $40 or $45 a month", "The plan costs $40 a month", nil},
	} {
		got := ledger.UpdatedSpecifics(c.later, c.earlier)
		keys := []string{}
		for k := range got {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		want := append([]string(nil), c.want...)
		sort.Strings(want)
		if strings.Join(keys, ",") != strings.Join(want, ",") {
			t.Errorf("UpdatedSpecifics(%q, %q) = %v, want %v", c.later, c.earlier, keys, want)
		}
	}
}
