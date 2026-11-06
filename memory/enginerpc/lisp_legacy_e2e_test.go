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
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/tables"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// legacyTurn is an exchange with every kind of memory call level 0 and the detectors read: a
// save in the removed query language (a note), a code call whose details carry a save and an
// edit (notes), a read in the removed language (what it asked for), a call of a retired defined
// query with its input, and a failed legacy save (no note).
func legacyTurn() []session.Entry {
	call := func(id, name string, args map[string]any) session.Entry {
		return message("l"+id, map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []any{
			map[string]any{"type": "toolCall", "id": "lc" + id, "name": name, "arguments": args},
		}})
	}
	result := func(id, name, text string, isError bool, details any) session.Entry {
		m := map[string]any{"role": "toolResult", "toolCallId": "lc" + id, "toolName": name, "content": []any{map[string]any{"type": "text", "text": text}}, "isError": isError}
		if details != nil {
			m["details"] = details
		}
		return message("lt"+id, m)
	}
	return []session.Entry{
		testfix.RawMessage("lu", "the service listens on port 8080; remember it and how billing deploys"),
		call("1", "memory", map[string]any{"query": `save name=port type=project description="The service listens on port 8080."`}),
		result("1", "memory", "Saved port.", false, nil),
		call("2", "memory", map[string]any{"code": "om.Save(`tabs`, `user`, `The user wants tabs.`)"}),
		result("2", "memory", "saved", false, map[string]any{"code": "om.Save(`tabs`, `user`, `The user wants tabs.`)", "outcome": "ok",
			"writes": []any{map[string]any{"op": "save", "name": "tabs", "type": "user", "description": "The user wants tabs."},
				map[string]any{"op": "edit", "name": "port", "description": "The service listens on port 9090 now."}}}),
		call("3", "memory", map[string]any{"query": `notes | search "billing replicas"`}),
		result("3", "memory", "no rows", false, nil),
		call("4", "deploys", map[string]any{"input": "billing"}),
		result("4", "deploys", "no rows", false, nil),
		call("5", "memory", map[string]any{"query": `save name=gone type=project description="Never saved."`}),
		result("5", "memory", "refused", true, nil),
		message("la", map[string]any{"role": "assistant", "stopReason": "stop", "content": []any{map[string]any{"type": "text",
			"text": "Saved: port 8080. Billing runs 3 replicas on Postgres 16 with a 40 GB volume."}}}),
	}
}

// legacyStore is a config home whose store holds a retired defined query and the keep-head rule.
func legacyStore(t *testing.T) string {
	t.Helper()
	home := harnessStore(t)
	dir := filepath.Join(curated.DefaultDir(home), "definitions")
	must(t, os.MkdirAll(dir, 0o700))
	text := tables.Rows.Encode(tables.Table{Name: "definition", Fields: []string{"name", "query", "description", "created", "sources"},
		Rows: [][]string{{"deploys", `notes | search "$input deploy"`, "How a service deploys.", time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339Nano), "s-old"}}})
	must(t, os.WriteFile(filepath.Join(dir, "deploys.rows"), []byte(text+"\n"), 0o600))
	return home
}

func legacyRun(t *testing.T, d driver) (any, []string) {
	t.Helper()
	h := newHost(legacyTurn())
	h.Home = legacyStore(t)
	d.sessionStart(h)
	d.turnEnd(h)
	data, err := os.ReadFile(filepath.Join(curated.DefaultDir(h.Home), "models", layer.ModelDir("test-provider/session")+".jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := []string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(data), "\n"), "\n") {
		var e map[string]any
		must(t, json.Unmarshal([]byte(line), &e))
		delete(e, "time")
		b, _ := json.Marshal(e)
		lines = append(lines, string(b))
	}
	return testfix.ToJSONValue(h.Appended), lines
}

// TestLispLegacyMemoryCallsMatchInProcess runs a session start and a turn end whose branch holds
// memory calls in the removed query language, a code call's writes and a retired defined query's
// call, on the image and in process: the image reads them itself (no fallback), and its notes,
// every appended entry and the detect event equal Go's.
func TestLispLegacyMemoryCallsMatchInProcess(t *testing.T) {
	image := buildLispImage(t)
	agentDir := settingsDir(t)
	wantAppended, wantLedger := legacyRun(t, newInProcess())
	log := &lockedLog{}
	gotAppended, gotLedger := legacyRun(t, overRPC{t: t, c: startLispEngine(t, image, agentDir, log)})
	encoded, _ := json.Marshal(wantAppended)
	for _, want := range []string{`"kind":"note"`, `"target":"port"`, `"target":"tabs"`, `9090`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("the scenario lacks %s:\n%s", want, encoded)
		}
	}
	if strings.Contains(string(encoded), "Never saved") {
		t.Fatalf("a failed legacy save became a note:\n%s", encoded)
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
