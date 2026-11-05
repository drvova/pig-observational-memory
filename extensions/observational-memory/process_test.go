package observationalmemory

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/enginerpc"
)

// useProcessEngine turns the engine setting to "process" in a temporary agent directory and
// starts each engine over in-memory pipes (enginerpc.PipeDialer, a Server in this process),
// so the factory registers the process-mode handlers and the lifecycle tests drive them
// through the registered extension as PiG does.
func useProcessEngine(t *testing.T) *int {
	t.Helper()
	agentDir := t.TempDir()
	settings := `{"observational-memory": {"engine": "process", "enginePath": "/nonexistent/om"}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	var mu sync.Mutex
	var stops []func() error
	started := 0
	previous := engineDialer
	engineDialer = func(path string) enginerpc.Dialer {
		if path != "/nonexistent/om" {
			t.Errorf("engine path %q", path)
		}
		dial := enginerpc.PipeDialer(func(r io.Reader, w io.Writer) error { return enginerpc.NewServer().Serve(r, w) })
		return func() (io.Reader, io.Writer, func() error, error) {
			r, w, stop, err := dial()
			mu.Lock()
			stops = append(stops, stop)
			started++
			mu.Unlock()
			return r, w, stop, err
		}
	}
	t.Cleanup(func() {
		engineDialer = previous
		mu.Lock()
		defer mu.Unlock()
		for _, stop := range stops {
			_ = stop()
		}
	})
	return &started
}

// The lifecycle tests pass with the engine behind the RPC.
func TestProcessEngineLifecycle(t *testing.T) {
	for name, test := range map[string]func(*testing.T){
		"namespace resets":    TestSessionBoundariesResetTheNamespace,
		"retired tools":       TestRetiredDefinedToolsAreNeverRegistered,
		"status over the rpc": testStatusOverTheRPC,
	} {
		t.Run(name, func(t *testing.T) {
			started := useProcessEngine(t)
			test(t)
			if *started != 1 {
				t.Fatalf("started %d engines", *started)
			}
		})
	}
}

// testStatusOverTheRPC runs /om:status: the engine's Notify reaches PiG's notify call.
func testStatusOverTheRPC(t *testing.T) {
	w := connect(t)
	w.event("session_start")
	var notified []string
	w.answer = func(method string, args json.RawMessage) any {
		if method == "ui.notify" {
			notified = append(notified, string(args))
		}
		return nil
	}
	w.request(map[string]any{"method": "command", "tool": "om:status", "args": ""})
	if len(notified) != 1 || !strings.Contains(notified[0], "emory") {
		t.Fatalf("notify calls %v (all calls %v)", notified, w.calls)
	}
}

// Without the setting the factory never starts an engine.
func TestInProcessEngineByDefault(t *testing.T) {
	t.Setenv("PI_CODING_AGENT_DIR", t.TempDir())
	previous := engineDialer
	engineDialer = func(string) enginerpc.Dialer {
		t.Fatal("an engine process was started")
		return nil
	}
	t.Cleanup(func() { engineDialer = previous })
	if processEngine() != nil {
		t.Fatal("process engine without the setting")
	}
}
