package runtime_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// The decision inputs behind a rejected resolve go into the debug log, because
// the reason string reads the same for auth.ok false and for ok true with
// nothing to carry. Credential values never do.

func useAgentDir(t *testing.T) string {
	dir := t.TempDir()
	old := debuglog.AgentDir
	debuglog.AgentDir = func() (string, error) { return dir, nil }
	t.Cleanup(func() { debuglog.AgentDir = old })
	return dir
}

func logEvents(t *testing.T, dir, event string) []map[string]any {
	data, err := os.ReadFile(filepath.Join(dir, debuglog.RelativePath))
	if err != nil {
		return nil
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
		if event == "" || row["event"] == event {
			rows = append(rows, row)
		}
	}
	return rows
}

func resolveLogged(models host.Models, model host.Model) runtime.ResolveResult {
	ctx := debuglog.With(context.Background(), debuglog.Context{Enabled: true})
	return withConfig(nil).ResolveSessionModel(ctx, runtime.ResolveCtx{Model: model, Models: models})
}

func expectData(t *testing.T, data map[string]any, want map[string]any) {
	for key, value := range want {
		if data[key] != value {
			t.Fatalf("data[%s] = %v, want %v (data %v)", key, data[key], value, data)
		}
	}
}

func TestRecordsWhyResolveWasRejected(t *testing.T) {
	dir := useAgentDir(t)
	models := testfix.NewFakeModels()
	models.Auth["anthropic"] = host.Auth{OK: true, Headers: map[string]any{"Authorization": ""}}

	result := resolveLogged(models, host.Model{"provider": "anthropic", "id": "claude"})

	if result.OK {
		t.Fatalf("result %+v", result)
	}
	rejected := logEvents(t, dir, "resolve.rejected")
	if len(rejected) != 1 {
		t.Fatalf("rejected events %v", rejected)
	}
	expectData(t, rejected[0]["data"].(map[string]any), map[string]any{
		"provider": "anthropic", "authOk": true, "hasApiKey": false, "resolvedEmptyApiKey": false,
		"headerCount": float64(1), "usableHeaderCount": float64(0), "isOAuth": false,
		"providerCredentialConfigured": false, "signsAtRequestTime": false,
	})
}

func TestDistinguishesEmptyKeyFromUnconfiguredProvider(t *testing.T) {
	dir := useAgentDir(t)
	models := testfix.NewFakeModels()
	models.Auth["xai"] = host.Auth{OK: true, APIKey: strp("")}
	models.Configured["xai"] = true

	resolveLogged(models, host.Model{"provider": "xai", "id": "grok-4"})

	rejected := logEvents(t, dir, "resolve.rejected")
	if len(rejected) != 1 {
		t.Fatalf("rejected events %v", rejected)
	}
	expectData(t, rejected[0]["data"].(map[string]any), map[string]any{
		"provider": "xai", "resolvedEmptyApiKey": true, "providerCredentialConfigured": true, "signsAtRequestTime": false,
	})
}

func TestRecordsRequestTimeSigningAcceptance(t *testing.T) {
	dir := useAgentDir(t)
	models := testfix.NewFakeModels()
	models.Auth["amazon-bedrock"] = host.Auth{OK: true}
	models.Configured["amazon-bedrock"] = true

	result := resolveLogged(models, host.Model{"provider": "amazon-bedrock", "id": "eu.anthropic.claude-haiku-4-5-20251001-v1:0"})

	if !result.OK {
		t.Fatalf("result %+v", result)
	}
	accepted := logEvents(t, dir, "resolve.request_time_signing")
	if len(accepted) != 1 {
		t.Fatalf("accepted events %v", accepted)
	}
	expectData(t, accepted[0]["data"].(map[string]any), map[string]any{"provider": "amazon-bedrock", "providerCredentialConfigured": true})
	if len(logEvents(t, dir, "resolve.rejected")) != 0 {
		t.Fatal("unexpected rejection logged")
	}
}

func TestNeverLogsCredentialMaterial(t *testing.T) {
	dir := useAgentDir(t)
	models := testfix.NewFakeModels()
	models.Auth["anthropic"] = host.Auth{OK: true, APIKey: strp("sk-secret-key"), Headers: map[string]any{"Authorization": "Bearer secret-token"}}
	models.Configured["anthropic"] = true

	resolveLogged(models, host.Model{"provider": "anthropic", "id": "claude"})

	contents, _ := os.ReadFile(filepath.Join(dir, debuglog.RelativePath))
	if strings.Contains(string(contents), "sk-secret-key") || strings.Contains(string(contents), "secret-token") {
		t.Fatalf("credential material logged: %s", contents)
	}
}
