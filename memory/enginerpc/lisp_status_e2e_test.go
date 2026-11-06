package enginerpc_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/commands"
	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/enginerpc"
	"github.com/drvova/pig-observational-memory/memory/hooks"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/rules"
	"github.com/drvova/pig-observational-memory/memory/runtime"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/tables"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// statusTime is when the status stores were written.
var statusTime = time.Date(2026, 11, 2, 9, 0, 0, 0, time.UTC)

// statusStore writes, with the Go engine, a curated store the whole /om:status reads: live,
// staged and flagged memories and one the validator refuses, a rule for the session model and a
// default switch-codec rule, usage ledgers whose decode and summary votes switch the session
// model's profile, a detector pass, another model's code calls, a written instructions
// definition, and legacy defined queries (one migrated, one with no snippet).
func statusStore(t *testing.T, home string) {
	t.Helper()
	s := curated.New(curated.DefaultDir(home))
	s.Now = func() time.Time { return statusTime }
	for _, m := range []curated.Memory{
		{Name: "deploy-port", Type: "project", Description: "The service deploys on port 8080."},
		{Name: "tabs", Type: "user", Description: "The user wants tabs.", Staged: true, StagedBy: []string{"s0"}},
	} {
		if _, err := s.Save(m, "s0"); err != nil {
			t.Fatal(err)
		}
	}
	must(t, s.Record("deploy-port", curated.EventCorrected))
	must(t, os.WriteFile(filepath.Join(s.Dir, "bad-one.md"), []byte("---\nname: bad-one\nfrob: x\n---\n"), 0o600))
	model := "test-provider/session"
	for _, r := range []rules.Rule{
		{Detector: rules.DetectUncitedAssistantSpecifics, Remedy: rules.RemedyKeepHead, Models: []string{model}, State: rules.StateEstablished, Engine: curated.EngineV2, Reason: "test"},
		{Detector: rules.DetectDecodeFailure, Remedy: rules.RemedySwitchCodec, Models: []string{rules.AllModels}, State: rules.StateProbation, Engine: curated.EngineV2, Reason: "test"},
	} {
		if _, err := rules.Add(s, r); err != nil {
			t.Fatal(err)
		}
	}
	at := func(i int) string { return statusTime.Add(time.Duration(i) * time.Minute).Format(time.RFC3339) }
	n := 0
	record := func(e curated.ModelEvent) {
		n++
		e.Time, e.Engine = at(n), curated.EngineV2
		must(t, s.RecordModel(e))
	}
	for range 4 {
		record(curated.ModelEvent{Model: model, Kind: curated.ModelKindDecode, Outcome: curated.OutcomeError, Codec: "rows"})
		record(curated.ModelEvent{Model: model, Kind: curated.ModelKindDecode, Outcome: curated.OutcomeColumns, Codec: "rows"})
		record(curated.ModelEvent{Model: model, Kind: curated.ModelKindCode, Outcome: curated.OutcomeShown, Target: curated.TargetSummary})
		record(curated.ModelEvent{Model: model, Kind: "pass", Stage: "compaction", Tokens: 1200, Counts: map[string]int{rules.DetectUncitedAssistantSpecifics: 2}})
	}
	record(curated.ModelEvent{Model: model, Kind: curated.ModelKindDetectorPass, Cut: "h7", Counts: map[string]int{rules.DefinedCountKey("ports"): 3}, Errors: map[string]string{"slow": "killed"}})
	record(curated.ModelEvent{Model: "p/other", Kind: curated.ModelKindCode, Outcome: curated.OutcomeError, Legacy: true})
	record(curated.ModelEvent{Model: "p/other", Kind: curated.ModelKindCode, Outcome: "ok"})
	if _, err := s.Layer().Put(layer.Definition{Kind: layer.KindInstructions, Name: "consolidation", Model: model, Text: "Mode: extend\nKeep every port.", Session: "s0"}); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(s.Dir, "definitions")
	must(t, os.MkdirAll(legacy, 0o700))
	writeLegacyDefinition(t, legacy, "deploys", `notes | search "deploy" | limit 5`)
	writeLegacyDefinition(t, legacy, "by-kind", `notes | count by kind`)
}

// writeLegacyDefinition writes a definitions/<name>.rows file as the store wrote it before the
// layer.
func writeLegacyDefinition(t *testing.T, dir, name, query string) {
	t.Helper()
	text := tables.Rows.Encode(tables.Table{Name: "definition", Fields: []string{"name", "query", "description", "created", "sources"},
		Rows: [][]string{{name, query, "What it returns.", statusTime.Format(time.RFC3339Nano), "s-legacy"}}})
	must(t, os.WriteFile(filepath.Join(dir, name+".rows"), []byte(text+"\n"), 0o600))
}

// statusSettings are global settings with a removed setting and a kernel floor lowering.
const statusSettings = `{"observational-memory": {"consolidate": false, "observeAfterTokens": 5000,
 "kernelFloor": {"lower": true, "requestedBy": "the owner", "reason": "testing the floor"}}}`

// statusBranch is a coding turn and a compaction memory v2 wrote, with level 1's report.
func statusBranch() []session.Entry {
	// Level 0's records, appended by the Go engine's turn end.
	h := newHost(codingTurn("p", "fix the loader"))
	hooks.OnTurnEnd(runtime.New(), h)
	entries := h.Entries
	return append(entries, session.Entry{Type: "compaction", ID: "cmp1", ParentID: entries[len(entries)-1].ID, Timestamp: "2026-11-02T09:30:00Z",
		Summary: "summary", FirstKeptEntryID: entries[len(entries)-1].ID,
		Details: map[string]any{"type": "om.folded", "version": float64(1), "fullFold": true, "observations": []any{}, "reflections": []any{}, "engine": "v2",
			"consolidation": map[string]any{"requested": true, "applied": true, "records": float64(2), "merges": float64(1), "confirmed": float64(1),
				"cited": float64(1), "promoted": float64(1), "harnessOffered": true, "defined": float64(1), "refused": float64(2)}}},
		testfix.RawMessage("x1", "next"))
}

var floorTime = regexp.MustCompile(` at \d{4}-\d\d-\d\dT[0-9:.]+Z \(`)

// TestLispStatusMatchesInProcess runs /om:status on the image and in process over copies of the
// same curated store, settings and branch: every line before the image's own code section is
// the same (the floor's lowering time aside), and nothing falls back.
func TestLispStatusMatchesInProcess(t *testing.T) {
	image := buildLispImage(t)
	agentDir := settingsDir(t)
	must(t, os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(statusSettings), 0o600))
	log := &lockedLog{}
	client := startLispEngine(t, image, agentDir, log)
	home := t.TempDir()
	statusStore(t, home)
	run := func(status func(h *testfix.FakeHost) string) string {
		copied := t.TempDir()
		copyDir(t, home, copied)
		h := newHost(statusBranch())
		h.Home = copied
		h.WorkDir = copied
		// A project settings file an untrusted project's status names as ignored.
		must(t, os.MkdirAll(filepath.Join(copied, ".pig"), 0o700))
		must(t, os.WriteFile(filepath.Join(copied, ".pig", "settings.json"), []byte(`{}`), 0o600))
		return floorTime.ReplaceAllString(status(h), " at TIME (")
	}
	want := run(func(h *testfix.FakeHost) string {
		lines, err := commands.StatusLines(runtime.New(), h)
		must(t, err)
		return strings.Join(lines, "\n")
	})
	got := run(func(h *testfix.FakeHost) string {
		must(t, client.Command(h, enginerpc.CommandStatus, ""))
		if len(h.Messages()) != 1 {
			t.Fatalf("messages %q", h.Messages())
		}
		return withoutCodeSection(h.Messages()[0])
	})
	if got != want {
		gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
		for i := 0; i < len(gl) || i < len(wl); i++ {
			var g, w string
			if i < len(gl) {
				g = gl[i]
			}
			if i < len(wl) {
				w = wl[i]
			}
			if g != w {
				t.Fatalf("line %d differs:\nlisp: %s\ngo:   %s\n\nlisp report:\n%s", i+1, g, w, got)
			}
		}
	}
	for _, line := range []string{"Kernel floor: LOWERED by the owner", "Removed settings, ignored: observeAfterTokens", "Rules: 2 active",
		"codec → jsonl", "layout → lines", "Project settings ignored", "Records: 6 recorded", "summary → 1/2 of the room", "Retired defined queries (2;", "Code calls: 2 (1 errors, 1 queries"} {
		if !strings.Contains(want, line) {
			t.Errorf("the report lacks %q:\n%s", line, want)
		}
	}
	if lines := log.lines(); len(lines) != 0 {
		t.Fatalf("fallbacks:\n%s", strings.Join(lines, "\n"))
	}
	t.Logf("%d lines compared:\n%s", strings.Count(want, "\n")+1, want)
}

func copyDir(t *testing.T, from, to string) {
	t.Helper()
	must(t, filepath.Walk(from, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(from, path)
		target := filepath.Join(to, rel)
		if info.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	}))
}
