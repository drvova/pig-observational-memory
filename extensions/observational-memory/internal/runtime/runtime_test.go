package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

func strp(s string) *string { return &s }

func headersAuth(headers map[string]any) host.Auth { return host.Auth{OK: true, Headers: headers} }

type notifier struct{ calls []testfix.Notification }

func (n *notifier) notify(message, level string) {
	n.calls = append(n.calls, testfix.Notification{Message: message, Level: level})
}

func resolveCtx(model host.Model, models host.Models, n *notifier) runtime.ResolveCtx {
	rc := runtime.ResolveCtx{Model: model, Models: models}
	if n != nil {
		rc.HasUI = true
		rc.Notify = n.notify
	}
	return rc
}

func withConfig(mutate func(cfg *config.Config)) *runtime.Runtime {
	rt := runtime.New()
	cfg := config.Defaults
	if mutate != nil {
		mutate(&cfg)
	}
	rt.SetConfig(cfg)
	return rt
}

func mapsEqual(a, b map[string]any) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	return reflect.DeepEqual(a, b)
}

const authFailure = `no API key or auth headers for provider "%s"`

func TestReturnsModelResolutionFailures(t *testing.T) {
	rt := withConfig(nil)
	result := rt.ResolveSessionModel(context.Background(), resolveCtx(nil, testfix.NewFakeModels(), nil))
	if result.OK || result.Reason != "no session model" {
		t.Fatalf("result %+v", result)
	}

	models := testfix.NewFakeModels()
	models.Auth["anthropic"] = host.Auth{OK: false}
	result = rt.ResolveSessionModel(context.Background(), resolveCtx(host.Model{"provider": "anthropic"}, models, nil))
	if result.OK || result.Reason != fmt.Sprintf(authFailure, "anthropic") {
		t.Fatalf("result %+v", result)
	}
}

func TestAcceptsOAuthShapedAuth(t *testing.T) {
	rt := withConfig(nil)
	model := host.Model{"provider": "kimi-coding", "id": "kimi-for-coding"}
	models := testfix.NewFakeModels()
	models.Auth["kimi-coding"] = headersAuth(map[string]any{"Authorization": "Bearer oauth-token"})

	result := rt.ResolveSessionModel(context.Background(), resolveCtx(model, models, nil))

	if !result.OK || result.APIKey != nil || !mapsEqual(result.Headers, map[string]any{"Authorization": "Bearer oauth-token"}) || fmt.Sprintf("%p", result.Model) != fmt.Sprintf("%p", model) {
		t.Fatalf("result %+v", result)
	}
}

func TestAcceptsAPIKeyAuthUnchanged(t *testing.T) {
	rt := withConfig(nil)
	model := host.Model{"provider": "anthropic", "id": "claude"}
	models := testfix.NewFakeModels()
	models.Auth["anthropic"] = testfix.KeyAuth("sk-ant-key")

	result := rt.ResolveSessionModel(context.Background(), resolveCtx(model, models, nil))

	if !result.OK || result.APIKey == nil || *result.APIKey != "sk-ant-key" || len(result.Headers) != 0 {
		t.Fatalf("result %+v", result)
	}
}

func TestAppliesAuthBaseURLWithoutMutatingModel(t *testing.T) {
	{
		for _, headersOnly := range []bool{false, true} {
			name := fmt.Sprintf("headersOnly=%v", headersOnly)
			model := testfix.Model("github-copilot", "gpt-4.1", map[string]any{"baseUrl": "https://api.individual.githubcopilot.com", "api": "openai-completions", "contextWindow": 128000})
			baseURL := "https://api.business.githubcopilot.com"
			auth := host.Auth{OK: true, Headers: map[string]any{"Authorization": "Bearer test-token"}, BaseURL: baseURL}
			if !headersOnly {
				auth.APIKey = strp("test-key")
			}
			models := testfix.NewFakeModels()
			models.Add(model, auth)
			rt := withConfig(nil)
			result := rt.ResolveSessionModel(context.Background(), resolveCtx(model, models, nil))

			if !result.OK {
				t.Fatalf("%s: %+v", name, result)
			}
			if result.Model["baseUrl"] != baseURL || result.Model["id"] != "gpt-4.1" || model["baseUrl"] != "https://api.individual.githubcopilot.com" {
				t.Fatalf("%s: model %v original %v", name, result.Model, model)
			}
			if fmt.Sprintf("%p", result.Model) == fmt.Sprintf("%p", model) {
				t.Fatalf("%s: model was mutated in place", name)
			}
			if headersOnly != (result.APIKey == nil) || !mapsEqual(result.Headers, auth.Headers) {
				t.Fatalf("%s: auth %+v", name, result)
			}
		}
	}
}

func TestKeepsOriginalModelWhenAuthBaseURLEmpty(t *testing.T) {
	rt := withConfig(nil)
	model := host.Model{"provider": "openai", "id": "test-model", "baseUrl": "https://example.com/v1"}
	models := testfix.NewFakeModels()
	models.Auth["openai"] = host.Auth{OK: true, APIKey: strp("test-key"), BaseURL: ""}

	result := rt.ResolveSessionModel(context.Background(), resolveCtx(model, models, nil))

	if !result.OK || fmt.Sprintf("%p", result.Model) != fmt.Sprintf("%p", model) {
		t.Fatalf("result %+v", result)
	}
}

func TestRejectsAuthWithoutUsableCredentials(t *testing.T) {
	rt := withConfig(nil)
	model := host.Model{"provider": "xai"}
	for _, auth := range []host.Auth{
		{OK: true},
		{OK: true, APIKey: strp("")},
		{OK: true, Headers: map[string]any{}},
		{OK: true, Headers: map[string]any{"Authorization": ""}},
	} {
		models := testfix.NewFakeModels()
		models.Auth["xai"] = auth
		result := rt.ResolveSessionModel(context.Background(), resolveCtx(model, models, nil))
		if result.OK || result.Reason != fmt.Sprintf(authFailure, "xai") {
			t.Fatalf("auth %+v: result %+v", auth, result)
		}
	}
}

func TestPointsOAuthProvidersAtLogin(t *testing.T) {
	rt := withConfig(nil)
	model := host.Model{"provider": "openai-codex", "id": "gpt-5-codex"}
	models := testfix.NewFakeModels()
	models.Auth["openai-codex"] = host.Auth{OK: false, Error: "refresh failed"}
	models.OAuth["openai-codex"] = true

	result := rt.ResolveSessionModel(context.Background(), resolveCtx(model, models, nil))

	want := `authentication failed for provider "openai-codex" — OAuth credentials may have expired; run '/login openai-codex' to re-authenticate`
	if result.OK || result.Reason != want {
		t.Fatalf("result %+v", result)
	}
}

func TestKeepsCompactionFlagsIndependent(t *testing.T) {
	rt := runtime.New()
	rt.SetCompactInFlight(true)
	rt.SetCompactHookInFlight(true)
	state := rt.Snapshot()
	if !state.CompactInFlight || !state.CompactHookInFlight {
		t.Fatalf("state %+v", state)
	}
}

func TestForwardsEnvAndBaseURLFromRegistry(t *testing.T) {
	rt := withConfig(nil)
	model := host.Model{"provider": "cloudflare-workers-ai", "id": "@cf/mistralai/mistral-small-3.1-24b-instruct"}
	baseURL := "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/v1"
	models := testfix.NewFakeModels()
	models.Auth["cloudflare-workers-ai"] = host.Auth{OK: true, APIKey: strp("test-key"), Headers: map[string]any{"Authorization": "Bearer test"}, Env: map[string]string{"CLOUDFLARE_ACCOUNT_ID": "abc123"}, BaseURL: baseURL}

	result := rt.ResolveSessionModel(context.Background(), resolveCtx(model, models, nil))

	if !result.OK || result.Model["baseUrl"] != baseURL || result.BaseURL != baseURL || *result.APIKey != "test-key" ||
		!mapsEqual(result.Headers, map[string]any{"Authorization": "Bearer test"}) || !reflect.DeepEqual(result.Env, map[string]string{"CLOUDFLARE_ACCOUNT_ID": "abc123"}) {
		t.Fatalf("result %+v", result)
	}
}

func TestLiveConfigRereadsPiAutoCompaction(t *testing.T) {
	rt := runtime.New()
	rt.LoadConfig = func(string, config.TrustState) config.Config { return config.Defaults }
	enabled, set := true, true
	rt.ReadPiAutoCompaction = func(string, config.TrustState) (bool, bool) { return enabled, set }
	h := testfix.NewFakeHost()
	h.WorkDir = "/project"
	rt.EnsureConfig(h)

	if !rt.LiveConfig("/project").PiAutoCompactionEnabled {
		t.Fatal("enabled not read")
	}
	enabled = false
	if rt.LiveConfig("/project").PiAutoCompactionEnabled {
		t.Fatal("toggle not picked up")
	}
	set = false
	if rt.LiveConfig("/project").PiAutoCompactionEnabled != config.Defaults.PiAutoCompactionEnabled {
		t.Fatal("unset setting should fall back to the default")
	}

	// An explicitly set configuration is left as set.
	cfg := config.Defaults
	cfg.PiAutoCompactionEnabled = false
	rt.SetConfig(cfg)
	enabled, set = true, true
	if rt.LiveConfig("/project").PiAutoCompactionEnabled {
		t.Fatal("explicit configuration overridden")
	}
}

// EnsureConfig asks the host for the project's trust and loads under it once
// per session; ReloadConfig (a session start) asks again, and leaves a
// configuration a caller set alone.
func TestEnsureConfigLoadsUnderProjectTrust(t *testing.T) {
	rt := runtime.New()
	var seen []config.TrustState
	rt.LoadConfig = func(_ string, trust config.TrustState) config.Config {
		seen = append(seen, trust)
		cfg := config.Defaults
		cfg.ProjectTrust = trust
		return cfg
	}
	h := testfix.NewFakeHost()
	rt.EnsureConfig(h)
	rt.EnsureConfig(h)
	if !reflect.DeepEqual(seen, []config.TrustState{config.ProjectUntrusted}) || h.TrustCalls != 1 {
		t.Fatalf("loads %v, trust checks %d", seen, h.TrustCalls)
	}
	h.TrustErr = errors.New("host unavailable")
	rt.ReloadConfig()
	rt.EnsureConfig(h)
	h.TrustErr = nil
	h.Trusted = true
	rt.ReloadConfig()
	rt.EnsureConfig(h)
	if want := []config.TrustState{config.ProjectUntrusted, config.ProjectTrustUnknown, config.ProjectTrusted}; !reflect.DeepEqual(seen, want) {
		t.Fatalf("loads %v, want %v", seen, want)
	}
	if rt.Config().ProjectTrust != config.ProjectTrusted {
		t.Fatalf("config trust %q", rt.Config().ProjectTrust)
	}
	// Pi's compaction.enabled is re-read under the same trust.
	var readTrust config.TrustState
	rt.ReadPiAutoCompaction = func(_ string, trust config.TrustState) (bool, bool) {
		readTrust = trust
		return false, false
	}
	rt.LiveConfig("")
	if readTrust != config.ProjectTrusted {
		t.Fatalf("compaction.enabled read under %q", readTrust)
	}
	set := config.Defaults
	set.CompactionSummaryMaxTokens = 7
	rt.SetConfig(set)
	rt.ReloadConfig()
	rt.EnsureConfig(h)
	if rt.Config().CompactionSummaryMaxTokens != 7 || len(seen) != 3 {
		t.Fatalf("reload replaced a set configuration: %+v", rt.Config())
	}
}

// A failed trust check is not kept for the session: the next EnsureConfig asks
// the host again, so the workers and commands do not stay global-only while
// the memory tool, which checks trust on every call, reads project settings.
func TestEnsureConfigRetriesFailedTrustCheck(t *testing.T) {
	rt := runtime.New()
	var seen []config.TrustState
	rt.LoadConfig = func(_ string, trust config.TrustState) config.Config {
		seen = append(seen, trust)
		cfg := config.Defaults
		cfg.ProjectTrust = trust
		return cfg
	}
	h := testfix.NewFakeHost()
	h.TrustErr = errors.New("transient")
	rt.EnsureConfig(h)
	if rt.Config().ProjectTrust != config.ProjectTrustUnknown || rt.ConfigLoaded() {
		t.Fatalf("after a failed check: trust %q, loaded %v", rt.Config().ProjectTrust, rt.ConfigLoaded())
	}
	h.TrustErr = nil
	h.Trusted = true
	rt.EnsureConfig(h)
	rt.EnsureConfig(h)
	if want := []config.TrustState{config.ProjectTrustUnknown, config.ProjectTrusted}; !reflect.DeepEqual(seen, want) || h.TrustCalls != 2 {
		t.Fatalf("loads %v, trust checks %d", seen, h.TrustCalls)
	}
	if !rt.ConfigLoaded() || rt.Config().ProjectTrust != config.ProjectTrusted {
		t.Fatalf("retry not kept: trust %q", rt.Config().ProjectTrust)
	}
}

// reloadingHost reloads the runtime's configuration during its first trust
// check, as a session start landing while an EnsureConfig waits on the host.
type reloadingHost struct {
	rt       *runtime.Runtime
	answers  []bool
	cwds     []string
	calls    int
	reloaded bool
}

func (h *reloadingHost) ProjectTrusted() (bool, error) {
	answer := h.answers[h.calls]
	h.calls++
	if !h.reloaded {
		h.reloaded = true
		h.rt.ReloadConfig()
	}
	return answer, nil
}

func (h *reloadingHost) Cwd() string { return h.cwds[h.calls-1] }

// An EnsureConfig whose host calls straddle a ReloadConfig does not install
// what it read for the outgoing session; it asks again.
func TestEnsureConfigDiscardsReadsBeforeReload(t *testing.T) {
	rt := runtime.New()
	var loads []string
	rt.LoadConfig = func(cwd string, trust config.TrustState) config.Config {
		loads = append(loads, cwd+":"+string(trust))
		cfg := config.Defaults
		cfg.ProjectTrust = trust
		return cfg
	}
	h := &reloadingHost{rt: rt, answers: []bool{false, true}, cwds: []string{"/old", "/new"}}
	rt.EnsureConfig(h)
	if want := []string{"/new:" + string(config.ProjectTrusted)}; !reflect.DeepEqual(loads, want) || h.calls != 2 {
		t.Fatalf("loads %v after %d trust checks, want %v", loads, h.calls, want)
	}
	if !rt.ConfigLoaded() || rt.Config().ProjectTrust != config.ProjectTrusted {
		t.Fatalf("config trust %q", rt.Config().ProjectTrust)
	}
}
