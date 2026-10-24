package curated

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestModelUsageLedger(t *testing.T) {
	s := testStore(t)
	// Writers in several goroutines (flock orders them, but not for the race detector's
	// clock): the real clock.
	s.Now = time.Now
	if events, err := s.ModelEvents("p/m"); err != nil || len(events) != 0 {
		t.Fatalf("missing ledger = %v, %v", events, err)
	}
	if err := s.RecordModel(ModelEvent{Kind: ModelKindMemory, Outcome: OutcomeOK}); err == nil {
		t.Fatal("an event without a model was recorded")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s.RecordModel(ModelEvent{Model: "p/m", Kind: ModelKindDecode, Outcome: OutcomeError, Codec: "rows"})
		}()
	}
	wg.Wait()
	if err := s.RecordModel(ModelEvent{Model: "p_2fm", Kind: ModelKindMemory, Outcome: OutcomeShown, Target: TargetSummary}); err != nil {
		t.Fatal(err)
	}
	events, err := s.ModelEvents("p/m")
	if err != nil || len(events) != 8 || events[0].Time == "" || events[0].Codec != "rows" {
		t.Fatalf("events = %+v, %v", events, err)
	}
	// Distinct ids never share a file, even when one spells the other's escape.
	other, _ := s.ModelEvents("p_2fm")
	if len(other) != 1 || other[0].Outcome != OutcomeShown {
		t.Fatalf("p_2fm events = %+v", other)
	}
	entries, _ := os.ReadDir(filepath.Join(s.Dir, "models"))
	if len(entries) != 2 {
		t.Fatalf("model files = %v", entries)
	}
}

func TestUsageEvents(t *testing.T) {
	s := testStore(t)
	if _, err := s.Define(Definition{Name: "bake-count", Query: "notes | count", Description: "Bakes."}, "s1"); err != nil {
		t.Fatal(err)
	}
	key := DefinitionUsageKey("bake-count")
	_ = s.Record(key, EventCallOK)
	_ = s.Record("other", EventView)
	_ = s.RecordFeedback(key, EventHarmed, "it was wrong about bakes")
	events, err := s.UsageEvents(key)
	if err != nil || len(events) != 3 || events[0].Event != EventSave || events[1].Event != EventCallOK || events[2].Quote == "" || events[2].Time.IsZero() {
		t.Fatalf("events = %+v, %v", events, err)
	}
	// Call events do not count as views in the index ranking.
	if usage, _ := s.Usage(); usage[key].Views != 0 || usage[key].Harmed != 1 {
		t.Fatalf("usage = %+v", usage[key])
	}
}

// A ledger read again parses only what was appended since, never a half-written line, and a
// ledger rewritten in place is read again whole.
func TestAppendCacheReadsOnlyNewCompleteLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.jsonl")
	parsed := 0
	parse := func(raw []byte) (string, bool) {
		parsed++
		return string(raw), len(raw) > 0
	}
	cache := &appendCache[string]{}
	read := func() []string {
		t.Helper()
		values, err := cache.read(path, parse)
		if err != nil {
			t.Fatal(err)
		}
		return values
	}
	if got := read(); len(got) != 0 {
		t.Fatalf("missing file = %v", got)
	}
	appendTo := func(text string) {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if _, err := f.WriteString(text); err != nil {
			t.Fatal(err)
		}
	}
	appendTo("a\nb\nc-half")
	if got := read(); len(got) != 2 || got[1] != "b" || parsed != 2 {
		t.Fatalf("first read %v (parsed %d)", got, parsed)
	}
	appendTo("-done\n")
	if got := read(); len(got) != 3 || got[2] != "c-half-done" || parsed != 3 {
		t.Fatalf("after the line was finished %v (parsed %d)", got, parsed)
	}
	if got := read(); len(got) != 3 || parsed != 3 {
		t.Fatalf("an unchanged file was parsed again: %v (parsed %d)", got, parsed)
	}
	// Rewritten with the same length and the same last line: the head no longer matches.
	if err := os.WriteFile(path, []byte("x\ny\nc-half-done\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := read(); len(got) != 3 || got[0] != "x" || got[1] != "y" {
		t.Fatalf("rewritten file %v", got)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if got := read(); len(got) != 0 {
		t.Fatalf("removed file %v", got)
	}
}
