package contracts

// The append-only files' crash test (docs/lisp-port-tool-layer.md, "Durable appends"): the Go
// engine and the Lisp image (lisp/append-parity.lisp) append in turn to every file they share
// (the layer's manifest, a model's usage ledger, the curated usage, the floor record and the
// neutral store), the test tears each file's last line between turns as a crash mid-write would,
// and both must read every file alike: a torn line is lost alone, never with the line appended
// after it, and no generation number is given to two lines either reads. Like the other Lisp
// parity tests it runs only with OM_LISP_PARITY=1 and sbcl on PATH.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/kernel"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/neutral"
)

const appendModel = "acme/append"

func appendFiles(dir, neutralPath string) []string {
	return []string{
		filepath.Join(dir, "harness", layer.ModelDir(appendModel), "manifest.jsonl"),
		filepath.Join(dir, "models", layer.ModelDir(appendModel)+".jsonl"),
		filepath.Join(dir, "usage.jsonl"),
		filepath.Join(dir, "harness", "floor.jsonl"),
		neutralPath,
	}
}

func goAppend(t *testing.T, s curated.Store, neutralPath, tag string) {
	t.Helper()
	if _, err := s.Layer().Mark(appendModel, layer.Generation{Op: layer.OpPropose, Session: tag, Reason: "level 1"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordModel(curated.ModelEvent{Model: appendModel, Kind: "detect", Outcome: tag, Tokens: 5, Engine: "v2"}); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordFeedback("m", curated.EventHelped, tag); err != nil {
		t.Fatal(err)
	}
	if err := s.Layer().RecordFloor(kernel.Lowering{RequestedBy: tag, Reason: "r", Time: "2026-09-21T00:00:00Z", Op: "lower"}); err != nil {
		t.Fatal(err)
	}
	if err := (neutral.Store{Path: neutralPath}).Append("", map[string]any{"type": "custom", "id": tag}); err != nil {
		t.Fatal(err)
	}
}

func goReads(t *testing.T, s curated.Store, neutralPath string) []string {
	t.Helper()
	gens, err := s.Layer().Generations(appendModel)
	if err != nil {
		t.Fatal(err)
	}
	manifest := "manifest:"
	for _, g := range gens {
		manifest += fmt.Sprintf(" %d:%s:%s", g.N, g.Op, g.Session)
	}
	events, err := s.ModelEvents(appendModel)
	if err != nil {
		t.Fatal(err)
	}
	ledger := "ledger:"
	for _, e := range events {
		ledger += " " + e.Outcome
	}
	usage, err := s.Usage()
	if err != nil {
		t.Fatal(err)
	}
	floor, err := s.Layer().FloorRecord()
	if err != nil {
		t.Fatal(err)
	}
	floorLine := "floor:"
	for _, l := range floor {
		floorLine += " " + l.RequestedBy
	}
	stored, err := (neutral.Store{Path: neutralPath}).Load()
	if err != nil {
		t.Fatal(err)
	}
	neutralLine := "neutral:"
	for _, st := range stored {
		neutralLine += fmt.Sprintf(" %v", st.Entry["id"])
	}
	return []string{manifest, ledger, fmt.Sprintf("usage: helped=%d", usage["m"].Helped), floorLine, neutralLine}
}

// tear cuts each file's last line in half and drops its newline, as a crash mid-write leaves it.
func tear(t *testing.T, paths []string) {
	t.Helper()
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		body := strings.TrimSuffix(string(data), "\n")
		start := strings.LastIndex(body, "\n") + 1
		cut := start + (len(body)-start)/2
		if err := os.WriteFile(p, []byte(body[:cut]), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestLispAppendsAfterATornTail(t *testing.T) {
	sbcl, _ := lispParity(t)
	script, err := filepath.Abs(filepath.Join("..", "..", "lisp", "append-parity.lisp"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	s := curated.New(dir)
	s.Now = func() time.Time { return time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC) }
	neutralPath := filepath.Join(t.TempDir(), "session.jsonl")
	files := appendFiles(dir, neutralPath)
	lispAppend := func(tag string) { runLisp(t, sbcl, script, "append", dir, neutralPath, appendModel, tag) }
	same := func(when string, want []string) {
		t.Helper()
		goGot := goReads(t, s, neutralPath)
		lispGot := runLisp(t, sbcl, script, "read", dir, neutralPath, appendModel)
		if strings.Join(goGot, "\n") != strings.Join(want, "\n") {
			t.Errorf("%s: Go reads\n  %s\nwant\n  %s", when, strings.Join(goGot, "\n  "), strings.Join(want, "\n  "))
		}
		if strings.Join(lispGot, "\n") != strings.Join(goGot, "\n") {
			t.Errorf("%s: Lisp reads\n  %s\nGo reads\n  %s", when, strings.Join(lispGot, "\n  "), strings.Join(goGot, "\n  "))
		}
	}
	reads := func(tags ...string) []string {
		var m []string
		for i, tag := range tags {
			m = append(m, fmt.Sprintf("%d:propose:%s", i+1, tag))
		}
		j := strings.Join(tags, " ")
		return []string{"manifest: " + strings.Join(m, " "), "ledger: " + j, fmt.Sprintf("usage: helped=%d", len(tags)), "floor: " + j, "neutral: " + j}
	}

	goAppend(t, s, neutralPath, "g1")
	goAppend(t, s, neutralPath, "g2")
	tear(t, files) // Go crashed writing g2
	same("after the Go crash", reads("g1"))
	lispAppend("l1")
	same("the image appended after Go's torn line", reads("g1", "l1"))
	tear(t, files) // the image crashed writing l1
	goAppend(t, s, neutralPath, "g3")
	same("Go appended after the image's torn line", reads("g1", "g3"))
	lispAppend("l2")
	goAppend(t, s, neutralPath, "g4")
	same("clean appends", reads("g1", "g3", "l2", "g4"))
	for _, p := range files {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		// Each torn fragment stays one line of its own, and nothing else was added.
		lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		if len(lines) != 6 || !strings.HasSuffix(string(data), "\n") || strings.Contains(string(data), "\n\n") {
			t.Errorf("%s: %d lines, want 6 (4 whole, 2 torn):\n%s", filepath.Base(p), len(lines), data)
		}
	}
}
