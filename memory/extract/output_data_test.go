package extract

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// TestOutputGrammarsOnData runs the tool-output extractors over the tool results of a
// Compact-Bench retention file (OM_TOOLOUTPUT_DATA=.../e2/sessions.jsonl; memory/cmd/compactbench
// fetch) and prints, for each grammar's marker, the first results holding it with what the
// extractors read: the check of each grammar against real tool output. It only reads data.
func TestOutputGrammarsOnData(t *testing.T) {
	path := os.Getenv("OM_TOOLOUTPUT_DATA")
	if path == "" {
		t.Skip("set OM_TOOLOUTPUT_DATA to a Compact-Bench e2/sessions.jsonl")
	}
	markers := []string{"--- FAIL: ", "short test summary", "Tests:  ", "test result: FAILED", "Tests run: ", " FAILED\n", "error TS", ": error: ", "error[E", "  error  ",
		"Traceback (most recent call last):", "panic: ", "    at ", "Exception in thread", "' panicked at", "On branch ", "\ncommit ", "diff --git", "Saved working directory", "CONFLICT", "command not found"}
	want := 3
	if n := os.Getenv("OM_TOOLOUTPUT_EXAMPLES"); n == "1" {
		want = 1
	}
	shown := map[string]int{}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 512<<20)
	for sc.Scan() {
		var s struct {
			H []map[string]any `json:"H"`
		}
		if json.Unmarshal(sc.Bytes(), &s) != nil {
			continue
		}
		calls := map[string]toolCall{}
		for _, m := range s.H {
			if tcs, ok := m["tool_calls"].([]any); ok {
				for _, raw := range tcs {
					tc, _ := raw.(map[string]any)
					fn, _ := tc["function"].(map[string]any)
					args := map[string]any{}
					if a, ok := fn["arguments"].(string); ok {
						_ = json.Unmarshal([]byte(a), &args)
					}
					id, _ := tc["id"].(string)
					name, _ := fn["name"].(string)
					calls[id] = toolCall{id: id, name: name, args: args}
				}
			}
			if m["role"] != "tool" {
				continue
			}
			text, _ := m["content"].(string)
			id, _ := m["tool_call_id"].(string)
			for _, marker := range markers {
				if shown[marker] >= want || !strings.Contains(text, marker) {
					continue
				}
				shown[marker]++
				call := calls[id]
				facts := outputFactsOf(call, text, exitCode.MatchString(text) && !strings.Contains(text, "code 0"))
				command, _ := shellCommand(call.args)
				t.Logf("== %q %s %q\n  evidence: %q\n  values: %v", marker, call.name, clip(command, 100), facts.evidence, facts.values)
			}
		}
	}
	for _, marker := range markers {
		if shown[marker] == 0 {
			t.Logf("== %q: no result holds it", marker)
		}
	}
}
