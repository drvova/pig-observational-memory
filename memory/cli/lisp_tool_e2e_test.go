package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestLispMemoryToolTrial builds the engine image and runs the memory tool on it with
// OM_ENGINE=lisp and OM_LISP_FALLBACK=0, so nothing falls back to Go: the Phase 0 trial's memory
// (bench/lisp/trial, docs/lisp-measurements.md) is recorded with one generic turn end, and each
// of the 20 tasks' reference Lisp answers (lisp-tool.json) runs through om call, each in a child
// SBCL, printing the answer the trial expects (expected.json). This is the offline fluency check
// of the Lisp API: it makes no model request. The same memory then answers an MCP tools/call.
func TestLispMemoryToolTrial(t *testing.T) {
	if os.Getenv("OM_LISP_PARITY") != "1" {
		t.Skip("set OM_LISP_PARITY=1 to build the SBCL engine image and run the memory tool on it")
	}
	if _, err := exec.LookPath("sbcl"); err != nil {
		t.Skip("sbcl is not on PATH")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "lisp", "build-image.sh"))
	if err != nil {
		t.Fatal(err)
	}
	trial, err := filepath.Abs(filepath.Join("..", "..", "bench", "lisp", "trial"))
	if err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(t.TempDir(), "om-engine")
	if out, err := exec.Command(shell, script, image).CombinedOutput(); err != nil {
		t.Fatalf("build-image: %v\n%s", err, out)
	}
	root, home := env(t)
	t.Setenv(LispSocketEnv, "")
	t.Setenv(EngineEnv, "lisp")
	t.Setenv(LispImageEnv, image)
	t.Setenv(LispIdleEnv, "60")
	t.Setenv(LispFallbackEnv, "0")
	t.Cleanup(func() {
		c := newLispClient(root)
		if err := c.call("image/stop", map[string]any{}, nil); err != nil {
			t.Errorf("image/stop: %v", err)
		}
		c.close()
		for range 100 {
			if _, err := os.Stat(filepath.Join(root, "engine.pid")); err != nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Error("the image did not exit")
	})

	repo := filepath.Join(home, "repo")
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"harness": "trial", "session_id": "s1", "transcript_path": filepath.Join(trial, "memory.jsonl"), "format": "generic", "cwd": repo})
	om(t, string(payload), "hook", "generic", "turn-end")

	read := func(name string) map[string]string {
		data, err := os.ReadFile(filepath.Join(trial, name))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]string
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	answers, expected := read("lisp-tool.json"), read("expected.json")
	if len(expected) != 20 || len(answers) != len(expected) {
		t.Fatalf("%d answers for %d tasks", len(answers), len(expected))
	}
	for id, want := range expected {
		in, _ := json.Marshal(map[string]any{"code": answers[id]})
		var res struct {
			Text    string `json:"text"`
			IsError bool   `json:"isError"`
		}
		if err := json.Unmarshal([]byte(om(t, string(in), "call", "--harness", "trial", "--dir", repo)), &res); err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimRight(res.Text, "\n "); res.IsError || got != want {
			t.Errorf("%s: got %q (error %v), want %q", id, got, res.IsError, want)
		}
	}

	mcp := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memory","arguments":{"code":"(format t \"~a~%\" (length (notes :kind \"preference\")))"}}}`,
	}, "\n") + "\n"
	lines := strings.Split(strings.TrimSpace(om(t, mcp, "mcp", "--harness", "trial", "--dir", repo)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"instructions":"Runs Common Lisp code`) || !strings.Contains(lines[1], `"text":"2\n"`) || !strings.Contains(lines[1], `"isError":false`) {
		t.Fatalf("MCP:\n%s", strings.Join(lines, "\n"))
	}
}
