package debuglog_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/debuglog"
)

func useAgentDir(t *testing.T) string {
	dir := t.TempDir()
	old := debuglog.AgentDir
	debuglog.AgentDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { debuglog.AgentDir = old })
	return dir
}

func readLines(t *testing.T, path string) []map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	rows := []map[string]any{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var row map[string]any
		if err := json.Unmarshal([]byte(line), &row); err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	return rows
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func TestWritesSessionScopedLog(t *testing.T) {
	dir := useAgentDir(t)
	ctx := debuglog.With(context.Background(), debuglog.Context{Enabled: true, Cwd: "/tmp/project", SessionID: "session-123", SessionFile: "/tmp/session.jsonl", RunID: "run-1"})
	debuglog.Log(ctx, "dropper.result", map[string]any{"reason": "no_tool_call"})
	path := filepath.Join(dir, "observational-memory", "debug", "session-123.ndjson")
	if !exists(path) || exists(filepath.Join(dir, debuglog.RelativePath)) {
		t.Fatal("log written to the wrong file")
	}
	rows := readLines(t, path)
	if len(rows) != 1 || rows[0]["event"] != "dropper.result" || rows[0]["cwd"] != "/tmp/project" || rows[0]["sessionId"] != "session-123" || rows[0]["sessionFile"] != "/tmp/session.jsonl" || rows[0]["runId"] != "run-1" || rows[0]["data"].(map[string]any)["reason"] != "no_tool_call" {
		t.Fatalf("row %+v", rows)
	}
}

func TestAppendsRunsAndSeparatesSessions(t *testing.T) {
	dir := useAgentDir(t)
	debuglog.Log(debuglog.With(context.Background(), debuglog.Context{Enabled: true, SessionID: "session-abc", RunID: "run-1"}), "one", nil)
	debuglog.Log(debuglog.With(context.Background(), debuglog.Context{Enabled: true, SessionID: "session-abc", RunID: "run-2"}), "two", nil)
	rows := readLines(t, filepath.Join(dir, "observational-memory", "debug", "session-abc.ndjson"))
	if len(rows) != 2 || rows[0]["runId"] != "run-1" || rows[1]["runId"] != "run-2" {
		t.Fatalf("rows %+v", rows)
	}
	debuglog.Log(debuglog.With(context.Background(), debuglog.Context{Enabled: true, SessionID: "session-a"}), "event", nil)
	debuglog.Log(debuglog.With(context.Background(), debuglog.Context{Enabled: true, SessionID: "session-b"}), "event", nil)
	if !exists(filepath.Join(dir, "observational-memory", "debug", "session-a.ndjson")) || !exists(filepath.Join(dir, "observational-memory", "debug", "session-b.ndjson")) {
		t.Fatal("per-session files missing")
	}
}

func TestLegacyFallback(t *testing.T) {
	dir := useAgentDir(t)
	debuglog.Log(debuglog.With(context.Background(), debuglog.Context{Enabled: true, Cwd: "/tmp/project", RunID: "run-1"}), "observer.start", nil)
	debuglog.Log(debuglog.With(context.Background(), debuglog.Context{Enabled: true, SessionID: "---"}), "observer.start", nil)
	rows := readLines(t, filepath.Join(dir, debuglog.RelativePath))
	if len(rows) != 2 || rows[0]["cwd"] != "/tmp/project" || rows[0]["runId"] != "run-1" || rows[1]["event"] != "observer.start" {
		t.Fatalf("rows %+v", rows)
	}
}

func TestSanitizesSessionIDs(t *testing.T) {
	if debuglog.SafeSessionID(" session/../id:value ") != "session_.._id_value" {
		t.Fatalf("sanitized %q", debuglog.SafeSessionID(" session/../id:value "))
	}
	if debuglog.RelativePathFor(debuglog.Context{SessionID: " session/../id:value "}) != filepath.Join("observational-memory", "debug", "session_.._id_value.ndjson") || debuglog.RelativePathFor(debuglog.Context{SessionID: "---"}) != debuglog.RelativePath {
		t.Fatal("relative paths wrong")
	}
}

func TestDisabledWritesNothing(t *testing.T) {
	dir := useAgentDir(t)
	debuglog.Log(debuglog.With(context.Background(), debuglog.Context{Enabled: false, SessionID: "session-123"}), "dropper.result", nil)
	if exists(filepath.Join(dir, "observational-memory")) {
		t.Fatal("disabled log wrote files")
	}
}
