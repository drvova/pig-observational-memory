package dreams

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/consolidate"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

var write = flag.Bool("write", false, "rewrite contracts/dreams.jsonl")

// casesPath holds the dream cases (docs/memory-v2.md, "Dreams"), which lisp/tests/delivery.lisp
// reads too: the section over a ledger, the tool with dreams on, and the hypotheses level 1 keeps.
var casesPath = filepath.Join("..", "..", "contracts", "dreams.jsonl")

func rec(id, kind, content string, f func(*ledger.Observation)) ledger.Observation {
	o := ledger.Observation{ID: id, Content: content, Kind: ledger.ObservationKind(kind), Timestamp: testfix.DefaultTimestamp, Relevance: "medium", SourceEntryIDs: []string{"u" + id[10:]}, TokenCount: 10, Origin: ledger.OriginCode}
	if f != nil {
		f(&o)
	}
	return o
}

func recorded(id string, records ...ledger.Observation) map[string]any {
	return map[string]any{"type": "custom", "id": id, "parentId": nil, "timestamp": testfix.DefaultTimestamp, "customType": ledger.OMObservationsRecorded,
		"data": map[string]any{"observations": records, "coversUpToId": "u" + id}}
}

func ledgerEntries() []map[string]any {
	plan := rec("0000000000d1", "plan", "Next we will add the 3 timings to `api/limit.go`.", func(o *ledger.Observation) { o.Status = "proposed" })
	failed := rec("0000000000d2", "attempt", "bash \"make ci\" failed: dial tcp 127.0.0.1:5432: connect: connection refused", func(o *ledger.Observation) {
		o.Tool, o.Outcome, o.Target = "bash", ledger.OutcomeFailed, "make ci"
		o.Keys.Add(ledger.KeyCommands, "make ci")
		o.Keys.Add(ledger.KeyErrors, "dial tcp 127.0.0.1:5432: connect: connection refused")
	})
	dead := rec("0000000000d3", "attempt", "bash \"go test ./api\" failed: --- FAIL: TestLimit", func(o *ledger.Observation) {
		o.Tool, o.Outcome, o.Target = "bash", ledger.OutcomeFailed, "go test ./api"
	})
	worked := rec("0000000000d4", "attempt", "bash \"go test ./api\" worked", func(o *ledger.Observation) {
		o.Tool, o.Outcome, o.Target, o.Supersedes = "bash", ledger.OutcomeWorked, "go test ./api", []string{"0000000000d3"}
	})
	decision := rec("0000000000d5", "decision", "We run make ci before every push.", func(o *ledger.Observation) { o.Keys.Add(ledger.KeyCommands, "make ci") })
	return []map[string]any{recorded("r1", plan, failed, dead, worked, decision)}
}

func hypothesesEntry() map[string]any {
	return map[string]any{"type": "custom", "id": "h1", "parentId": nil, "timestamp": testfix.DefaultTimestamp, "customType": ledger.OMHypothesesRecorded,
		"data": consolidate.HypothesesRecordedData{Hypotheses: []consolidate.Hypothesis{{ID: consolidate.HypothesisID("Does the database start after make ci?"), Content: "Does the database start after make ci?", From: []string{"0000000000d2"}}}, CoversUpToID: "ur1"}}
}

type dreamCase struct {
	Name    string           `json:"name"`
	Entries []map[string]any `json:"entries,omitempty"`
	Section *string          `json:"section,omitempty"`
	// Tool is the level-1 tool with dreams on: its description and parameters.
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	// Shown, Args and Hypotheses are a validation: the shown ids, the output's hypotheses, and
	// what level 1 keeps of them; Refused how many it refuses.
	Shown      []string                 `json:"shown,omitempty"`
	Args       []any                    `json:"args,omitempty"`
	Hypotheses []consolidate.Hypothesis `json:"hypotheses,omitempty"`
	Refused    int                      `json:"refused,omitempty"`
}

func roundTrip(v any) []map[string]any {
	raw, _ := json.Marshal(v)
	var out []map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}

func cases(t *testing.T) []dreamCase {
	var out []dreamCase
	for _, c := range []struct {
		name    string
		entries []map[string]any
	}{
		{"empty ledger", nil},
		{"code only", ledgerEntries()},
		{"with level 1's hypotheses", append(ledgerEntries(), hypothesesEntry())},
	} {
		entries := roundTrip(c.entries)
		section := Section(session.FromMaps(entries))
		out = append(out, dreamCase{Name: c.name, Entries: entries, Section: &section})
	}
	tool := consolidate.ToolWith(nil, false, true)
	params := roundTrip([]any{tool.Parameters})[0]
	out = append(out, dreamCase{Name: "tool with dreams", Description: tool.Description, Parameters: params})
	shown := []string{"0000000000d1", "0000000000d2"}
	long := strings.Repeat("why ", 61)
	for _, c := range []struct {
		name string
		args []any
	}{
		{"hypotheses kept", []any{map[string]any{"content": "Is the port 5432 in use by another service?", "supportingIds": []any{"0000000000d2"}}}},
		{"hypotheses refused", []any{
			map[string]any{"content": "", "supportingIds": []any{"0000000000d2"}},
			map[string]any{"content": long, "supportingIds": []any{"0000000000d2"}},
			map[string]any{"content": "Two\nlines", "supportingIds": []any{"0000000000d2"}},
			map[string]any{"content": "Cites nothing shown?", "supportingIds": []any{"0000000000ff"}},
			map[string]any{"content": "Cites nothing?", "supportingIds": []any{}},
		}},
		{"at most three", []any{
			map[string]any{"content": "One?", "supportingIds": []any{"0000000000d1"}},
			map[string]any{"content": "Two?", "supportingIds": []any{"0000000000d1"}},
			map[string]any{"content": "Three?", "supportingIds": []any{"0000000000d2"}},
			map[string]any{"content": "Four?", "supportingIds": []any{"0000000000d2"}},
		}},
	} {
		var candidates []ledger.Observation
		for _, id := range shown {
			candidates = append(candidates, rec(id, "fact", "Record "+id+".", nil))
		}
		args := map[string]any{"records": []any{map[string]any{"id": "0000000000d1", "relevance": "high"}}, "hypotheses": roundTripList(c.args)}
		r := consolidate.Validate(args, consolidate.Input{Candidates: candidates, Dreams: true})
		refused := 0
		for _, f := range r.Refusals {
			if f.What == "hypothesis" {
				refused++
			}
		}
		out = append(out, dreamCase{Name: c.name, Shown: shown, Args: roundTripList(c.args), Hypotheses: r.Hypotheses, Refused: refused})
		// Dreams off: the same output keeps none.
		if off := consolidate.Validate(args, consolidate.Input{Candidates: candidates}); len(off.Hypotheses) != 0 {
			t.Fatalf("%s: dreams off kept %v", c.name, off.Hypotheses)
		}
	}
	return out
}

func roundTripList(v []any) []any {
	raw, _ := json.Marshal(v)
	var out []any
	_ = json.Unmarshal(raw, &out)
	return out
}

// TestDreamCases writes or checks contracts/dreams.jsonl (go test ./dreams -args -write).
func TestDreamCases(t *testing.T) {
	var b bytes.Buffer
	for _, c := range cases(t) {
		line, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if *write {
		if err := os.WriteFile(casesPath, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(casesPath)
	if err != nil || !bytes.Equal(want, b.Bytes()) {
		t.Fatalf("contracts/dreams.jsonl is stale; rerun with -args -write and review the diff (%v)", err)
	}
}

// Scenario 20: every dream line is marked a hypothesis and cites records; none is a record, and
// the section is empty when nothing gives a dream.
func TestDreamLinesAreHypotheses(t *testing.T) {
	lines := Lines(session.FromMaps(roundTrip(append(ledgerEntries(), hypothesesEntry()))))
	if len(lines) < 5 {
		t.Fatalf("lines %q", lines)
	}
	for _, l := range lines {
		if !strings.HasPrefix(l, "- ") || !strings.HasSuffix(l, ")") {
			t.Errorf("line %q", l)
		}
	}
	if Section(nil) != "" {
		t.Fatal("a section with nothing to dream on")
	}
}
