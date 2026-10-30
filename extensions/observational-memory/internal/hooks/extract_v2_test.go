package hooks

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// codingTurn is one user request and the assistant's run on it: words before a failing test
// run, an edit, and a passing rerun.
func codingTurn(prefix, request string) []ledger.Entry {
	call := func(id, name string, args map[string]any, text string) ledger.Entry {
		return testfix.MessageEntry(prefix+"a"+id, map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []any{
			map[string]any{"type": "thinking", "thinking": "private"},
			map[string]any{"type": "text", "text": text},
			map[string]any{"type": "toolCall", "id": prefix + "c" + id, "name": name, "arguments": args},
		}})
	}
	result := func(id, name, text string, isError bool) ledger.Entry {
		return testfix.MessageEntry(prefix+"t"+id, map[string]any{"role": "toolResult", "toolCallId": prefix + "c" + id, "toolName": name, "content": []any{map[string]any{"type": "text", "text": text}}, "isError": isError})
	}
	return []ledger.Entry{
		userMessage(prefix+"u", request),
		call("1", "bash", map[string]any{"command": "go test ./..."}, "Running the suite to find the failing case."),
		// A long failing log, so the removed range leaves the summary room for its records.
		result("1", "bash", "--- FAIL: TestLoad"+strings.Repeat("\n    loader_test.go:12: unexpected key", 400)+"\nCommand exited with code 1", true),
		call("2", "edit", map[string]any{"path": "loader.go"}, ""),
		result("2", "edit", "ok", false),
		call("3", "bash", map[string]any{"command": "go test ./..."}, ""),
		result("3", "bash", "ok", false),
	}
}

func appendedOfType(h *testfix.FakeHost, customType string) []testfix.Appended {
	out := []testfix.Appended{}
	for _, a := range h.Appended {
		if a.CustomType == customType {
			out = append(out, a)
		}
	}
	return out
}

func recordedContents(h *testfix.FakeHost) []string {
	out := []string{}
	for _, a := range appendedOfType(h, ledger.OMObservationsRecorded) {
		data, ok := ledger.ObservationsRecordedDataFrom(a.Data)
		if !ok {
			continue
		}
		for _, o := range data.Observations {
			out = append(out, o.Content)
		}
	}
	return out
}

// Under memory v2 every turn end extracts the entries since the last extraction, appends the
// records and a marker, and makes no request; the next turn end reads only what is new.
func TestV2TurnEndExtractsWithoutRequests(t *testing.T) {
	counter := testfix.CountRequests(t)
	turn := codingTurn("x", "Fix the loader for config version 3.")
	rt, h := v2Setup(t, turn)

	OnTurnEnd(rt, h)

	contents := strings.Join(recordedContents(h), "\n")
	for _, want := range []string{
		"Fix the loader for config version 3.",
		`Running the suite to find the failing case. [bash "go test ./..." failed: --- FAIL: TestLoad]`,
		`bash "go test ./..." worked`,
		"loader.go edited",
	} {
		if !strings.Contains(contents, want) {
			t.Fatalf("missing %q in:\n%s", want, contents)
		}
	}
	if strings.Contains(contents, "private") {
		t.Fatal("thinking was extracted")
	}
	if progress := ledger.ExtractionProgressIndex(h.Entries); progress != len(turn)-1 {
		t.Fatalf("extraction reaches %d, want %d", progress, len(turn)-1)
	}

	// A turn with nothing to record still advances the marker; a repeat records nothing twice.
	before := len(h.Appended)
	h.SetBranch(append(h.Entries, userMessage("yu", "thanks")))
	OnTurnEnd(rt, h)
	OnTurnEnd(rt, h)
	if got := h.Appended[before:]; len(got) != 1 || got[0].CustomType != ledger.OMExtracted {
		t.Fatalf("appended %+v", got)
	}
	if counter.Total() != 0 || h.ModelsFake.Streams.Calls() != 0 {
		t.Fatalf("requests %v", counter.ByStage())
	}
}

// Passive mode turns turn-end extraction off.
func TestTurnEndExtractionOffWhenPassive(t *testing.T) {
	turn := codingTurn("x", "Fix the loader for config version 3.")
	rt, h := v2Setup(t, turn)
	cfg := rt.Config()
	cfg.Passive = true
	rt.SetConfig(cfg)
	OnTurnEnd(rt, h)
	if len(h.Appended) != 0 {
		t.Fatalf("passive appended %+v", h.Appended)
	}
}

// v2CompactionRun extracts at turn ends, then compacts at firstKept.
func v2CompactionRun(t *testing.T, rt *runtime.Runtime, h *testfix.FakeHost, firstKept string) CompactionResult {
	t.Helper()
	return expectV2Compaction(t, runV2Hook(rt, h, v2Event(h.Entries, firstKept, 20_000, "threshold")), firstKept, h)
}

// A compaction extracts whatever of the cut range no turn end read, appends it with a marker
// through the last entry PiG removes, renders it, and makes no request.
func TestV2CompactionExtractsTheCutRange(t *testing.T) {
	counter := testfix.CountRequests(t)
	entries := append(codingTurn("x", "Fix the loader for config version 3."), userMessage("yu", "Now bump the Helm chart to 2.4."))
	rt, h := v2Setup(t, entries)
	event := v2Event(entries, "yu", 20_000, "threshold")
	event.Preparation.FileOps = CompactionFileOps{Read: []string{"go.mod"}, Edited: []string{"loader.go"}}

	c := expectV2Compaction(t, runV2Hook(rt, h, event), "yu", h)

	for _, want := range []string{"Fix the loader for config version 3.", "loader.go edited", "go.mod read", `bash \"go test ./...\" worked`} {
		if !strings.Contains(c.Summary, want) {
			t.Fatalf("summary lacks %q:\n%s", want, c.Summary)
		}
	}
	if strings.Contains(c.Summary, "Helm") {
		t.Fatalf("the kept tail was extracted into the summary:\n%s", c.Summary)
	}
	if progress := ledger.ExtractionProgressIndex(h.Entries); progress != len(entries)-2 {
		t.Fatalf("extraction marker at %d, want the last removed entry %d", progress, len(entries)-2)
	}
	// The failed run is superseded by the rendered worked one, so it is dropped by rule.
	dropped := appendedOfType(h, ledger.OMObservationsDropped)
	if len(dropped) != 1 || strings.Contains(c.Summary, "failed: --- FAIL") {
		t.Fatalf("dropped %+v summary:\n%s", dropped, c.Summary)
	}
	for _, o := range c.Details.Observations {
		if strings.Contains(o.Content, "failed: --- FAIL") {
			t.Fatalf("details keep the dropped record %+v", o)
		}
	}
	if counter.Total() != 0 {
		t.Fatalf("requests %v", counter.ByStage())
	}
}

// Records a turn end extracted fold at the compaction that removes their entries; a batch
// whose segment PiG keeps does not fold. The turn end batches by cut-point segment, so a cut
// at the assistant message keeps the user's record in the summary.
func TestV2TurnEndRecordsFoldAtTheCut(t *testing.T) {
	counter := testfix.CountRequests(t)
	turn := codingTurn("x", "Fix the loader for config version 3.")
	rt, h := v2Setup(t, turn)
	OnTurnEnd(rt, h)
	appendedBefore := len(h.Appended)

	c := v2CompactionRun(t, rt, h, "xa3")

	// The records, after the instructions (which name the outcomes).
	_, records, _ := strings.Cut(c.Summary, ledger.TablesRecallParagraph)
	if !strings.Contains(records, "Fix the loader for config version 3.") || !strings.Contains(records, "failed: --- FAIL") || strings.Contains(records, "worked") || !strings.Contains(records, "loader.go edited") {
		t.Fatalf("summary:\n%s", c.Summary)
	}
	// Everything before the cut was extracted at the turn end: nothing new is appended.
	if got := h.Appended[appendedBefore:]; len(got) != 0 {
		t.Fatalf("appended again %+v", got)
	}
	if counter.Total() != 0 {
		t.Fatalf("requests %v", counter.ByStage())
	}
}

// A second hook while one holds the compaction slot appends what no hook appended yet, so the
// compaction PiG persists finds its range's records on the session; a hook after it, on a
// stale snapshot, appends nothing again.
func TestV2ConcurrentHookAppendsOnce(t *testing.T) {
	entries := append(codingTurn("x", "Fix the loader for config version 3."), userMessage("yu", "next"))
	rt, h := v2Setup(t, entries)
	rt.SetCompactHookInFlight(true)
	c := v2CompactionRun(t, rt, h, "yu")
	recorded := appendedOfType(h, ledger.OMObservationsRecorded)
	if len(recorded) == 0 || !strings.Contains(c.Summary, "Fix the loader for config version 3.") {
		t.Fatalf("appended %+v summary:\n%s", h.Appended, c.Summary)
	}
	// The second hook gets the same stale event snapshot; the fresh branch shows the records.
	before := len(appendedOfType(h, ledger.OMObservationsRecorded))
	again := expectV2Compaction(t, runV2Hook(rt, h, v2Event(entries, "yu", 20_000, "threshold")), "yu", h)
	if got := len(appendedOfType(h, ledger.OMObservationsRecorded)); got != before || !strings.Contains(again.Summary, "Fix the loader for config version 3.") {
		t.Fatalf("records appended twice (%d, then %d):\n%s", before, got, again.Summary)
	}
}
