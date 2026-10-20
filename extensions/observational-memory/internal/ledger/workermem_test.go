package ledger_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/ledger"
	tf "github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/testfix"
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
