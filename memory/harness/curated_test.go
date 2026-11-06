package harness_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/config"
	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/harness"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	tf "github.com/drvova/pig-observational-memory/memory/testfix"
)

func v2Config() config.Config { return config.Defaults }

// The memory tool reads the session file when the branch has nothing, labelling the rows
// off-path. No model request is made.
func TestRunMemoryFallsBackToTheSessionFile(t *testing.T) {
	counter := tf.CountRequests(t)
	h, _, m := session(t)
	h.SessionEntries = append(append([]ledger.Entry{}, h.Entries...), tf.RawMessage("o1", "The kiln fires at 900C."))
	h.Entries = append(h.Entries, tf.RawMessage("u9", "kiln?"))
	if r := m.RunMemory(h, map[string]any{"code": `om.History(om.Search("900C"))`}); r.IsError || !strings.Contains(r.Text, "o1 (off-path)") {
		t.Fatalf("v2 history %q", r.Text)
	}
	if counter.Total() != 0 {
		t.Fatalf("the memory tool made %d requests", counter.Total())
	}
}

// The curated evidence check reads the branch's user corrections and the store's evidence: it
// flags a memory a later correction's subject matches, promotes a staged memory with a user's
// quote behind it, tells the user of both, and makes no request; /om:status lists them.
func TestSettleCuratedWithoutRequests(t *testing.T) {
	counter := tf.CountRequests(t)
	correction := tf.Observation("c0aaaaaaaaa1", func(o *ledger.Observation) {
		o.Content, o.Kind, o.Origin, o.Subject, o.SourceEntryIDs = "Actually I bake on Saturdays now.", "correction", ledger.OriginCode, "baking", []string{"u2"}
	})
	h, _, _ := session(t,
		tf.RawMessage("u2", "Actually I bake on Saturdays now.", map[string]any{"timestamp": "2026-10-20T10:00:00.000Z"}),
		tf.ObservationsRecordedEntry("om2", []ledger.Observation{correction}, "u2"),
	)
	cfg := v2Config()
	store, ok := harness.StoreOf(h, cfg)
	if !ok || store.Layer().Engine != curated.EngineV2 {
		t.Fatalf("a v2 store: %+v %v", store, ok)
	}
	store.Now = func() time.Time { return time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC) }
	call := curated.Call{Store: store, Source: "session-1"}
	if _, err := store.Save(curated.Memory{Name: "baking-day", Type: curated.TypeUser, Description: "The user bakes on Sundays."}, "session-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(curated.Memory{Name: "oven-first", Type: curated.TypeLesson, Description: "Preheat the oven.", Staged: true}, "session-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := call.Run(curated.OpFeedback, map[string]string{"name": "oven-first", "outcome": "helped", "quote": "I bake sourdough every Sunday."}); err != nil {
		t.Fatal(err)
	}
	// The feedback above had no UserEvidence, so it is no evidence; a user-quoted one is.
	call.UserEvidence = func(string) bool { return true }
	if _, err := call.Run(curated.OpFeedback, map[string]string{"name": "oven-first", "outcome": "helped", "quote": "I bake sourdough every Sunday."}); err != nil {
		t.Fatal(err)
	}
	settled, err := harness.SettleCurated(h, call)
	if err != nil || len(settled.Promoted) != 1 || settled.Promoted[0].Name != "oven-first" || strings.Join(settled.Flagged, ",") != "baking-day" {
		t.Fatalf("settled %+v, %v", settled, err)
	}
	if !hasMessage(h, "staged memory oven-first is now live") || !hasMessage(h, "a correction matches the memories baking-day") {
		t.Fatalf("notifications %v", h.Messages())
	}
	status := strings.Join(harness.CuratedStatusLines(h, cfg), "\n")
	if !strings.Contains(status, "2 live (0 pinned), 0 staged") || !strings.Contains(status, "ranked last): baking-day") {
		t.Fatalf("status:\n%s", status)
	}
	// A memory file the validator refuses is not loaded, but /om:status names it and why.
	if err := os.WriteFile(filepath.Join(store.Dir, "tagged.md"), []byte("---\ntype: user\ndescription: Tabs.\ntags: style\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if status := strings.Join(harness.CuratedStatusLines(h, cfg), "\n"); !strings.Contains(status, "Refused by the validator, so not loaded (1; edit within it or forget): tagged (") || !strings.Contains(status, "tags") {
		t.Fatalf("status:\n%s", status)
	}
	if counter.Total() != 0 {
		t.Fatalf("the evidence check made %d requests", counter.Total())
	}
}

// /om:status lists every change to shared memory staged until a user's words confirm it
// (docs/brain-memory.md, decision 2): a forget and an edit of a curated memory, each in the
// session that staged it, and a layer forget and rollback; it confirms none (slash commands stay
// read-only).
func TestStatusListsStagedChanges(t *testing.T) {
	h, _, _ := session(t)
	cfg := v2Config()
	store, _ := harness.StoreOf(h, cfg)
	owner := curated.Call{Store: store, Source: "owner", UserEvidence: func(string) bool { return true }}
	for _, name := range []string{"build-target", "deploy-day"} {
		if _, err := owner.Run(curated.OpSave, map[string]string{"name": name, "type": curated.TypeProject, "description": "Shared " + name + ".", "quote": "remember this for later"}); err != nil {
			t.Fatal(err)
		}
	}
	other := curated.Call{Store: store, Source: "s2"}
	if _, err := other.Run(curated.OpEdit, map[string]string{"name": "build-target", "description": "Build with make release-unsafe."}); err != nil {
		t.Fatal(err)
	}
	if _, err := other.Run(curated.OpForget, map[string]string{"name": "deploy-day"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordLayerChange(curated.EventLayerRollbackStaged, curated.LayerRollbackKey, "p/m", "s2", "", 0); err != nil {
		t.Fatal(err)
	}
	status := strings.Join(harness.CuratedStatusLines(h, cfg), "\n")
	want := harness.StagedChangesHeading + " (3; a staged forget or edit applies in its own session only, a staged layer change nowhere): edit build-target (s2): Build with make release-unsafe.; forget deploy-day (s2); layer rollback p/m to generation 0 (s2)"
	if !strings.Contains(status, want) {
		t.Fatalf("status:\n%s", status)
	}
	if m, err := store.GetAs("build-target", "", "s3"); err != nil || m.Description != "Shared build-target." {
		t.Fatalf("listing confirmed a staged edit: %+v %v", m, err)
	}
}
