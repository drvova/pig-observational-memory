package ledger_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

func padID(prefix string, fill byte) string {
	return prefix + strings.Repeat(string(fill), 12-len(prefix))
}

func workerFixtures() ([]ledger.Reflection, []ledger.Observation) {
	reflections := []ledger.Reflection{}
	observations := []ledger.Observation{}
	for i := 1; i <= 4; i++ {
		n := string(rune('0' + i))
		reflections = append(reflections, tf.Reflection(padID("e"+n, 'e'), []string{"aaaaaaaaaaaa"}, func(r *ledger.Reflection) { r.Content = "Reflection " + n + " " + strings.Repeat("r", 120) }))
		observations = append(observations, tf.Observation(padID("a"+n, 'a'), withContent("Observation "+n+" "+strings.Repeat("o", 120))))
	}
	return reflections, observations
}

func TestBoundWorkerMemoryNoBudget(t *testing.T) {
	reflections, observations := workerFixtures()
	slice := ledger.BoundWorkerMemory(reflections, observations, 0, false)
	if len(slice.Reflections) != 4 || len(slice.Observations) != 4 || slice.OmittedReflections+slice.OmittedObservations != 0 {
		t.Fatal("budget-less slice omitted records")
	}
}

func TestBoundWorkerMemoryNewest(t *testing.T) {
	reflections, observations := workerFixtures()
	slice := ledger.BoundWorkerMemory(reflections, observations, 200, false)
	if !reflect.DeepEqual(tf.IDs(slice.Observations), []string{"a3aaaaaaaaaa", "a4aaaaaaaaaa"}) {
		t.Fatalf("observations %v", tf.IDs(slice.Observations))
	}
	if !reflect.DeepEqual(tf.RefIDs(slice.Reflections), []string{"e3eeeeeeeeee", "e4eeeeeeeeee"}) {
		t.Fatalf("reflections %v", tf.RefIDs(slice.Reflections))
	}
	if slice.OmittedObservations != 2 || slice.OmittedReflections != 2 {
		t.Fatal("omitted counts wrong")
	}
}

func TestBoundWorkerMemoryOldest(t *testing.T) {
	reflections, observations := workerFixtures()
	slice := ledger.BoundWorkerMemory(reflections, observations, 200, true)
	if !reflect.DeepEqual(tf.IDs(slice.Observations), []string{"a1aaaaaaaaaa", "a2aaaaaaaaaa"}) {
		t.Fatalf("observations %v", tf.IDs(slice.Observations))
	}
	if !reflect.DeepEqual(tf.RefIDs(slice.Reflections), []string{"e3eeeeeeeeee", "e4eeeeeeeeee"}) {
		t.Fatalf("reflections %v", tf.RefIDs(slice.Reflections))
	}
}

func TestBoundWorkerMemoryReclaim(t *testing.T) {
	reflections, observations := workerFixtures()
	slice := ledger.BoundWorkerMemory(reflections, observations[:1], 200, false)
	if len(slice.Observations) != 1 || len(slice.Reflections) != 4 {
		t.Fatalf("reclaim failed: %d observations, %d reflections", len(slice.Observations), len(slice.Reflections))
	}
}

func TestBoundObserverMemoryCostsObserverLines(t *testing.T) {
	observations := []ledger.Observation{}
	for i := 0; i < 40; i++ {
		id := padID(fmt.Sprintf("b%02d", i), 'b')
		observations = append(observations, tf.Observation(id, withContent(fmt.Sprintf("User baked loaf %d at home", i)), func(o *ledger.Observation) {
			o.Kind, o.Subject, o.Scope = ledger.ObservationKind("event"), "baking", "sourdough at home"
			o.Supersedes = []string{"aaaaaaaaaaaa", "cccccccccccc"}
		}))
	}
	const budget = 300
	lineTotal := func(slice ledger.WorkerMemorySlice, line func(ledger.Observation) string) int {
		total := 0
		for _, observation := range slice.Observations {
			total += (len(line(observation))+3)/4 + 1
		}
		return total
	}
	observer := ledger.BoundObserverMemory(nil, observations, budget)
	if got := lineTotal(observer, ledger.ObservationToObserverLine); got > budget || len(observer.Observations) == 0 {
		t.Fatalf("observer lines cost %d tokens for %d records, budget %d", got, len(observer.Observations), budget)
	}
	summary := ledger.BoundWorkerMemory(nil, observations, budget, false)
	if lineTotal(summary, ledger.ObservationToObserverLine) <= budget || len(summary.Observations) <= len(observer.Observations) {
		t.Fatal("fixture does not exercise the longer observer line")
	}
}
