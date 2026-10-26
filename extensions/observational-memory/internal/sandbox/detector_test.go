package sandbox

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
)

// detectorFake answers Notes with rows records and records every method it is asked.
func detectorFake(rows int, asked *[]string) fake {
	return fake{
		"Notes": func(ctx context.Context, args []json.RawMessage) (any, error) {
			*asked = append(*asked, "Notes")
			l := om.NoteList{Rows: []om.Note{}}
			for range rows {
				l.Rows = append(l.Rows, om.Note{ID: "o1", Kind: "plan"})
			}
			l.Matched, l.Showing = rows, rows
			return l, nil
		},
		"Recall": func(ctx context.Context, args []json.RawMessage) (any, error) {
			*asked = append(*asked, "Recall")
			return om.Text{}, nil
		},
		"Save": func(ctx context.Context, args []json.RawMessage) (any, error) {
			*asked = append(*asked, "Save")
			return "saved", nil
		},
	}
}

func TestDetectorRunReturnsTheCount(t *testing.T) {
	var asked []string
	src := "func plans(l om.NoteList) int { return l.Len() }\nfunc Count() int { return plans(om.Notes(om.Kind(`plan`))) }"
	r := RunDetector(context.Background(), src, detectorFake(3, &asked))
	if r.Outcome != OutcomeOK || r.Count != 3 {
		t.Fatalf("run %+v", r)
	}
	if strings.Join(asked, ",") != "Notes" {
		t.Fatalf("asked %v", asked)
	}
}

func TestDetectorOMHasNoWrites(t *testing.T) {
	var asked []string
	for _, src := range []string{
		"func Count() int { om.Save(`n`, `lesson`, `d`); return 1 }",
		"func Count() int { _, _ = om.Recall(`e1`); return 1 }",
		"func Count() int { return len(om.Saved().Rows) }",
		"func Count() int { return len(kept()) }",
	} {
		r := RunDetector(context.Background(), src, detectorFake(1, &asked))
		if r.Outcome != OutcomeCompile {
			t.Fatalf("%s: outcome %+v, want a compile failure", src, r)
		}
	}
	if len(asked) != 0 {
		t.Fatalf("a detector that did not compile was answered: %v", asked)
	}
}

func TestDetectorDisallowedMethodKillsTheChild(t *testing.T) {
	var asked []string
	// A record's Recall method exists on the type; the host refuses it from a detector child.
	src := "func Count() int { n := om.Notes().Rows[0]; n.Recall(); return 1 }"
	r := RunDetector(context.Background(), src, detectorFake(1, &asked))
	if r.Outcome != OutcomeCrashed || !strings.Contains(r.Err, `"Recall"`) {
		t.Fatalf("run %+v", r)
	}
	if strings.Join(asked, ",") != "Notes" {
		t.Fatalf("the host answered %v", asked)
	}
	// A forged write from a fixture child is refused before the handler sees it.
	asked = nil
	r = runDetector(context.Background(), Config{Mode: "fixture-forge-save"}, "func Count() int { return 0 }", detectorFake(1, &asked))
	if r.Outcome != OutcomeCrashed || !strings.Contains(r.Err, `"Save"`) || len(asked) != 0 {
		t.Fatalf("forged save: %+v, asked %v", r, asked)
	}
}

func TestDetectorLoopIsKilledByItsContext(t *testing.T) {
	for _, src := range []string{
		"func Count() int { for {} }",
		"func Count() int { <-make(chan int); return 0 }",
	} {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan DetectorResult, 1)
		go func() { done <- RunDetector(ctx, src, fake{}) }()
		time.Sleep(200 * time.Millisecond)
		start := time.Now()
		cancel()
		select {
		case r := <-done:
			if r.Outcome != OutcomeCancelled || r.Queued {
				t.Fatalf("%s: %+v, want a started run cancelled", src, r)
			}
			if waited := time.Since(start); waited > 5*time.Second {
				t.Fatalf("the kill took %v", waited)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("%s: the run did not end on its context", src)
		}
	}
}

// A run whose context ended before its child was spawned never started: it is cancelled and
// queued, so its caller gives it no evidence.
func TestDetectorCancelledBeforeSpawnIsQueued(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if r := RunDetector(ctx, "func Count() int { return 1 }", fake{}); r.Outcome != OutcomeCancelled || !r.Queued {
		t.Fatalf("run %+v, want cancelled and queued", r)
	}
}

func TestDetectorCrashIsTheRunsOwn(t *testing.T) {
	// The policy refuses a go statement; RunDetector's caller checks it, so this source reaches
	// the child only here, to crash it.
	crash := "func Count() int { go func() { panic(`boom`) }(); for {} }"
	r := RunDetector(context.Background(), crash, fake{})
	if r.Outcome != OutcomeCrashed || !strings.Contains(r.Err, "boom") {
		t.Fatalf("crash: %+v", r)
	}
	// The next run is a fresh child and counts.
	var asked []string
	if r := RunDetector(context.Background(), "func Count() int { return om.Notes().Len() }", detectorFake(2, &asked)); r.Outcome != OutcomeOK || r.Count != 2 {
		t.Fatalf("after a crash: %+v", r)
	}
	// A panic is a runtime error, and a non-int value is refused.
	if r := RunDetector(context.Background(), "func Count() int { var m map[string]int; m[`x`] = 1; return 1 }", fake{}); r.Outcome != OutcomeRuntime {
		t.Fatalf("panic: %+v", r)
	}
}
