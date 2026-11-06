package enginerpc_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/layer"
	"github.com/drvova/pig-observational-memory/memory/rules"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// harnessTurns are two exchanges whose assistant replies add specifics the user never gave (a
// list, figures and names), so the harness detectors count at a turn end and the keep-head rule
// adds notes. (A memory tool call is left out: the image's level 0 refuses its notes, which are
// not ported; dropped-then-queried is checked by memory/contracts' detector parity.)
func harnessTurns() []session.Entry {
	text := func(s string) []any { return []any{map[string]any{"type": "text", "text": s}} }
	reply := func(id, s string) session.Entry {
		return message(id, map[string]any{"role": "assistant", "stopReason": "stop", "content": text(s)})
	}
	return []session.Entry{
		testfix.RawMessage("h1", "what should the migration plan look like?"),
		reply("h2", "Here is the plan.\n\nMove Billing, Ledger and Payroll first; Billing needs 3 replicas on Postgres 16 and a 40 GB volume.\n\nThen the rest."),
		testfix.RawMessage("h3", "and the budget?"),
		reply("h6", "Budget it at 12,500 USD a month across Kraken and Atlas, reviewed every 6 weeks."),
	}
}

// harnessStore is a config home whose store holds a keep-head rule for the scenario's session
// model, written by the Go engine at a fixed time.
func harnessStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	store := curated.New(curated.DefaultDir(home))
	store.Now = func() time.Time { return time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC) }
	if _, err := rules.Add(store, rules.Rule{Detector: rules.DetectUncitedAssistantSpecifics, Remedy: rules.RemedyKeepHead,
		Models: []string{"test-provider/session"}, State: rules.StateEstablished, Engine: curated.EngineV2, Reason: "test"}); err != nil {
		t.Fatal(err)
	}
	return home
}

// harnessRun drives two turn ends of the harness scenario with a store and a session model,
// and returns what was appended and the model's usage ledger, its times left out.
func harnessRun(t *testing.T, d driver) (any, []string) {
	t.Helper()
	entries := harnessTurns()
	h := newHost(entries[:2])
	h.Home = harnessStore(t)
	d.turnEnd(h)
	h.SetBranch(append(h.Entries, entries[2:]...))
	d.turnEnd(h)
	data, err := os.ReadFile(filepath.Join(curated.DefaultDir(h.Home), "models", layer.ModelDir("test-provider/session")+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := []string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		var e map[string]any
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatal(err)
		}
		delete(e, "time")
		b, _ := json.Marshal(e)
		lines = append(lines, string(b))
	}
	return testfix.ToJSONValue(h.Appended), lines
}

// TestLispHarnessDetectMatchesInProcess runs two turn ends with a curated store and a session
// model on the image and on the in-process path: the image runs the harness detectors itself
// (no fallback), and its keep-head notes, every appended entry and the detect events it records
// (but for their times) equal Go's.
func TestLispHarnessDetectMatchesInProcess(t *testing.T) {
	image := buildLispImage(t)
	agentDir := settingsDir(t)
	wantAppended, wantLedger := harnessRun(t, newInProcess())
	log := &lockedLog{}
	gotAppended, gotLedger := harnessRun(t, overRPC{t: t, c: startLispEngine(t, image, agentDir, log)})
	encoded, _ := json.Marshal(wantAppended)
	if !strings.Contains(string(encoded), `"origin":"code"`) || len(wantLedger) != 2 || !strings.Contains(strings.Join(wantLedger, ""), rules.DetectUncitedAssistantSpecifics) {
		t.Fatalf("the scenario tests nothing:\n%s\n%s", encoded, strings.Join(wantLedger, "\n"))
	}
	if !reflect.DeepEqual(wantAppended, gotAppended) {
		a, _ := json.MarshalIndent(wantAppended, "", " ")
		b, _ := json.MarshalIndent(gotAppended, "", " ")
		t.Errorf("appended differ:\nin-process %s\nlisp       %s", clip(a), clip(b))
	}
	if !reflect.DeepEqual(wantLedger, gotLedger) {
		t.Errorf("ledgers differ:\nin-process %s\nlisp       %s", strings.Join(wantLedger, "\n"), strings.Join(gotLedger, "\n"))
	}
	if lines := log.lines(); len(lines) != 0 {
		t.Errorf("fallbacks:\n%s", strings.Join(lines, "\n"))
	}
}
