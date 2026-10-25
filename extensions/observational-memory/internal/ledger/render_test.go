package ledger_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

func TestObservationToObserverLine(t *testing.T) {
	o := ledger.Observation{ID: "aaaaaaaaaaaa", Timestamp: "2026-05-02 10:30", Relevance: "high", Content: "User finished the game in 25 hours."}
	if got := ledger.ObservationToObserverLine(o); got != ledger.ObservationToSummaryLine(o) {
		t.Fatalf("unstructured %q", got)
	}
	o.Kind, o.Subject, o.Scope, o.Supersedes = "correction", "gaming hours", "normal difficulty", []string{"bbbbbbbbbbbb", "cccccccccccc"}
	want := "[aaaaaaaaaaaa] 2026-05-02 10:30 [high] User finished the game in 25 hours. {kind: correction; subject: gaming hours; scope: normal difficulty; supersedes: bbbbbbbbbbbb cccccccccccc}"
	if got := ledger.ObservationToObserverLine(o); got != want {
		t.Fatalf("structured\n got %q\nwant %q", got, want)
	}
	o.Kind, o.Scope, o.Supersedes = "", "", nil
	if got := ledger.ObservationToObserverLine(o); !strings.HasSuffix(got, " {subject: gaming hours}") {
		t.Fatalf("subject only %q", got)
	}
}

func TestReflectionDateRanges(t *testing.T) {
	at := func(ts string) func(*ledger.Observation) { return func(o *ledger.Observation) { o.Timestamp = ts } }
	o1 := tf.Observation(padID("a1", 'a'), at("2023-05-20 10:00"))
	o2 := tf.Observation(padID("a2", 'a'), at("2023-06-02 18:30"))
	o3 := tf.Observation(padID("a3", 'a'), at("2023-06-02 09:15"))
	folded := ledger.FoldedLedger{
		ObservationsByID: map[string]ledger.Observation{o1.ID: o1, o2.ID: o2, o3.ID: o3},
		Reflections: []ledger.Reflection{
			tf.Reflection(padID("e1", 'e'), []string{o1.ID, o2.ID}),
			tf.Reflection(padID("e2", 'e'), []string{o2.ID, o3.ID}),
			tf.Reflection(padID("e3", 'e'), []string{"unknownaaaaa"}),
		},
	}
	dates := ledger.ReflectionDateRanges(folded)
	want := map[string]string{padID("e1", 'e'): "2023-05-20 to 2023-06-02", padID("e2", 'e'): "2023-06-02"}
	if !reflect.DeepEqual(dates, want) {
		t.Fatalf("dates = %v, want %v", dates, want)
	}
}
