package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

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

func (n *notifier) has(message, level string) bool {
	for _, c := range n.calls {
		if c.Message == message && c.Level == level {
			return true
		}
	}
	return false
}

func (n *notifier) hasContaining(fragment, level string) bool {
	for _, c := range n.calls {
		if strings.Contains(c.Message, fragment) && c.Level == level {
			return true
		}
	}
	return false
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

func TestUsesConfiguredModelWhenPresent(t *testing.T) {
	rt := withConfig(func(cfg *config.Config) { cfg.Model = &config.ConfiguredModel{Provider: "anthropic", ID: "configured"} })
	models := testfix.NewFakeModels()
	models.Add(testfix.Model("anthropic", "configured"), host.Auth{OK: true, APIKey: strp("key"), Headers: map[string]any{"test": "yes"}})

	result := rt.ResolveModel(context.Background(), resolveCtx(host.Model{"provider": "openai"}, models, nil))

	if !reflect.DeepEqual(models.FindCalls, []string{"anthropic/configured"}) {
		t.Fatalf("find calls %v", models.FindCalls)
	}
	if !result.OK || host.ModelID(result.Model) != "configured" || result.APIKey == nil || *result.APIKey != "key" || result.Headers["test"] != "yes" {
		t.Fatalf("result %+v", result)
	}
}

func TestFallsBackToSessionModelAndNotifies(t *testing.T) {
	rt := withConfig(func(cfg *config.Config) { cfg.Model = &config.ConfiguredModel{Provider: "anthropic", ID: "missing"} })
	models := testfix.NewFakeModels()
	models.Auth["openai"] = host.Auth{OK: true, APIKey: strp("key"), Headers: map[string]any{"test": "yes"}}
	n := &notifier{}

	result := rt.ResolveModel(context.Background(), resolveCtx(host.Model{"provider": "openai"}, models, n))

	if !result.OK || host.ModelProvider(result.Model) != "openai" {
		t.Fatalf("result %+v", result)
	}
	if !n.has("Observational memory: configured model anthropic/missing not found, using session model", "warning") {
		t.Fatalf("notifications %+v", n.calls)
	}
}

func TestReturnsModelResolutionFailures(t *testing.T) {
	rt := withConfig(nil)
	result := rt.ResolveModel(context.Background(), resolveCtx(nil, testfix.NewFakeModels(), nil))
	if result.OK || result.Reason != "no model available (session has no model and no observational-memory model configured)" {
		t.Fatalf("result %+v", result)
	}

	models := testfix.NewFakeModels()
	models.Auth["anthropic"] = host.Auth{OK: false}
	result = rt.ResolveModel(context.Background(), resolveCtx(host.Model{"provider": "anthropic"}, models, nil))
	if result.OK || result.Reason != fmt.Sprintf(authFailure, "anthropic") {
		t.Fatalf("result %+v", result)
	}
}

func TestAcceptsOAuthShapedAuth(t *testing.T) {
	rt := withConfig(nil)
	model := host.Model{"provider": "kimi-coding", "id": "kimi-for-coding"}
	models := testfix.NewFakeModels()
	models.Auth["kimi-coding"] = headersAuth(map[string]any{"Authorization": "Bearer oauth-token"})

	result := rt.ResolveModel(context.Background(), resolveCtx(model, models, nil))

	if !result.OK || result.APIKey != nil || !mapsEqual(result.Headers, map[string]any{"Authorization": "Bearer oauth-token"}) || fmt.Sprintf("%p", result.Model) != fmt.Sprintf("%p", model) {
		t.Fatalf("result %+v", result)
	}
}

func TestAcceptsAPIKeyAuthUnchanged(t *testing.T) {
	rt := withConfig(nil)
	model := host.Model{"provider": "anthropic", "id": "claude"}
	models := testfix.NewFakeModels()
	models.Auth["anthropic"] = testfix.KeyAuth("sk-ant-key")

	result := rt.ResolveModel(context.Background(), resolveCtx(model, models, nil))

	if !result.OK || result.APIKey == nil || *result.APIKey != "sk-ant-key" || len(result.Headers) != 0 {
		t.Fatalf("result %+v", result)
	}
}

func TestAppliesAuthBaseURLWithoutMutatingModel(t *testing.T) {
	for _, configured := range []bool{false, true} {
		for _, headersOnly := range []bool{false, true} {
			name := fmt.Sprintf("configured=%v headersOnly=%v", configured, headersOnly)
			model := testfix.Model("github-copilot", "gpt-4.1", map[string]any{"baseUrl": "https://api.individual.githubcopilot.com", "api": "openai-completions", "contextWindow": 128000})
			baseURL := "https://api.business.githubcopilot.com"
			auth := host.Auth{OK: true, Headers: map[string]any{"Authorization": "Bearer test-token"}, BaseURL: baseURL}
			if !headersOnly {
				auth.APIKey = strp("test-key")
			}
			models := testfix.NewFakeModels()
			models.Add(model, auth)
			rt := withConfig(func(cfg *config.Config) {
				if configured {
					cfg.Model = &config.ConfiguredModel{Provider: "github-copilot", ID: "gpt-4.1"}
				}
			})
			sessionModel := model
			if configured {
				sessionModel = host.Model{"provider": "openai", "id": "session-model"}
			}

			result := rt.ResolveModel(context.Background(), resolveCtx(sessionModel, models, nil))

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

	result := rt.ResolveModel(context.Background(), resolveCtx(model, models, nil))

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
		result := rt.ResolveModel(context.Background(), resolveCtx(model, models, nil))
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

	result := rt.ResolveModel(context.Background(), resolveCtx(model, models, nil))

	want := `authentication failed for provider "openai-codex" — OAuth credentials may have expired; run '/login openai-codex' to re-authenticate`
	if result.OK || result.Reason != want {
		t.Fatalf("result %+v", result)
	}
}

func TestTracksConsolidationTaskState(t *testing.T) {
	rt := runtime.New()
	release := make(chan struct{})
	started := make(chan struct{})

	done := rt.LaunchConsolidationTask(nil, func(ctx context.Context) error {
		rt.SetConsolidationPhase(runtime.PhaseObserver)
		close(started)
		<-release
		return nil
	})
	<-started

	state := rt.Snapshot()
	if !state.ConsolidationInFlight || state.ConsolidationPhase != runtime.PhaseObserver || rt.ConsolidationDone() != done {
		t.Fatalf("state %+v", state)
	}
	close(release)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("consolidation did not finish")
	}
	state = rt.Snapshot()
	if state.ConsolidationInFlight || state.ConsolidationPhase != "" || rt.ConsolidationDone() != nil {
		t.Fatalf("state %+v", state)
	}
}

func TestRecordsStageSpecificConsolidationErrors(t *testing.T) {
	rt := runtime.New()
	h := testfix.NewFakeHost()

	if rt.RecordConsolidationStageError(h, runtime.PhaseObserver, errors.New("observe failed")) != "observe failed" ||
		rt.RecordConsolidationStageError(h, runtime.PhaseReflector, errors.New("reflect failed")) != "reflect failed" ||
		rt.RecordConsolidationStageError(h, runtime.PhaseDropper, errors.New("drop failed")) != "drop failed" {
		t.Fatal("stage error messages not returned")
	}
	state := rt.Snapshot()
	if state.LastObserverError != "observe failed" || state.LastReflectorError != "reflect failed" || state.LastDropperError != "drop failed" {
		t.Fatalf("state %+v", state)
	}
	want := []testfix.Notification{
		{Message: "Observational memory: observer failed: observe failed", Level: "warning"},
		{Message: "Observational memory: reflector failed: reflect failed", Level: "warning"},
		{Message: "Observational memory: dropper failed: drop failed", Level: "warning"},
	}
	if !reflect.DeepEqual(h.Notifications, want) {
		t.Fatalf("notifications %+v", h.Notifications)
	}
}

func TestKeepsCompactionFlagsIndependent(t *testing.T) {
	rt := runtime.New()
	rt.SetCompactInFlight(true)
	rt.SetCompactHookInFlight(true)
	state := rt.Snapshot()
	if state.ConsolidationInFlight || state.ConsolidationPhase != "" || !state.CompactInFlight || !state.CompactHookInFlight {
		t.Fatalf("state %+v", state)
	}
}

func TestForwardsEnvAndBaseURLFromRegistry(t *testing.T) {
	rt := withConfig(nil)
	model := host.Model{"provider": "cloudflare-workers-ai", "id": "@cf/mistralai/mistral-small-3.1-24b-instruct"}
	baseURL := "https://api.cloudflare.com/client/v4/accounts/{CLOUDFLARE_ACCOUNT_ID}/ai/v1"
	models := testfix.NewFakeModels()
	models.Auth["cloudflare-workers-ai"] = host.Auth{OK: true, APIKey: strp("test-key"), Headers: map[string]any{"Authorization": "Bearer test"}, Env: map[string]string{"CLOUDFLARE_ACCOUNT_ID": "abc123"}, BaseURL: baseURL}

	result := rt.ResolveModel(context.Background(), resolveCtx(model, models, nil))

	if !result.OK || result.Model["baseUrl"] != baseURL || result.BaseURL != baseURL || *result.APIKey != "test-key" ||
		!mapsEqual(result.Headers, map[string]any{"Authorization": "Bearer test"}) || !reflect.DeepEqual(result.Env, map[string]string{"CLOUDFLARE_ACCOUNT_ID": "abc123"}) {
		t.Fatalf("result %+v", result)
	}
}

var fallbackTarget = config.ConfiguredModel{Provider: "opencode-go", ID: "deepseek-v4.1-flash"}

func fallbackModels(primaryProvider string, primaryAuth, fallbackAuth host.Auth) *testfix.FakeModels {
	models := testfix.NewFakeModels()
	models.Add(testfix.Model(fallbackTarget.Provider, fallbackTarget.ID), fallbackAuth)
	models.Auth[primaryProvider] = primaryAuth
	return models
}

func isFallback(model host.Model) bool {
	return host.ModelProvider(model) == fallbackTarget.Provider && host.ModelID(model) == fallbackTarget.ID
}

func TestResolvesFallbackWhenPrimaryHasNoUsableAuth(t *testing.T) {
	rt := withConfig(func(cfg *config.Config) {
		cfg.Model = &config.ConfiguredModel{Provider: "anthropic", ID: "haiku"}
		target := fallbackTarget
		cfg.FallbackModel = &target
	})
	models := fallbackModels("session", host.Auth{OK: false, Error: "expired"}, testfix.KeyAuth("go-key"))
	n := &notifier{}

	result := rt.ResolveModel(context.Background(), resolveCtx(host.Model{"provider": "session"}, models, n))

	if !result.OK || !isFallback(result.Model) || *result.APIKey != "go-key" || !result.FallbackUsed {
		t.Fatalf("result %+v", result)
	}
	if !strings.Contains(result.PrimaryFailure, "no API key or auth headers") {
		t.Fatalf("primary failure %q", result.PrimaryFailure)
	}
	if !n.hasContaining("using fallback opencode-go/deepseek-v4.1-flash", "warning") {
		t.Fatalf("notifications %+v", n.calls)
	}
}

func TestDoesNotConsultFallbackWhenPrimaryResolves(t *testing.T) {
	rt := withConfig(func(cfg *config.Config) { target := fallbackTarget; cfg.FallbackModel = &target })
	primary := host.Model{"provider": "anthropic", "id": "haiku"}
	models := fallbackModels("anthropic", testfix.KeyAuth("primary-key"), testfix.KeyAuth("go-key"))

	result := rt.ResolveModel(context.Background(), resolveCtx(primary, models, nil))

	if !result.OK || fmt.Sprintf("%p", result.Model) != fmt.Sprintf("%p", primary) || *result.APIKey != "primary-key" || len(result.Headers) != 0 || result.FallbackUsed {
		t.Fatalf("result %+v", result)
	}
	if len(models.FindCalls) != 0 {
		t.Fatalf("find calls %v", models.FindCalls)
	}
}

func TestReportsBothReasonsWhenPrimaryAndFallbackFail(t *testing.T) {
	rt := withConfig(func(cfg *config.Config) { target := fallbackTarget; cfg.FallbackModel = &target })
	models := fallbackModels("anthropic", host.Auth{OK: false}, host.Auth{OK: false})

	result := rt.ResolveModel(context.Background(), resolveCtx(host.Model{"provider": "anthropic"}, models, nil))

	if result.OK || !strings.Contains(result.Reason, fmt.Sprintf(authFailure, "anthropic")) || !strings.Contains(result.Reason, fmt.Sprintf(authFailure, "opencode-go")) {
		t.Fatalf("result %+v", result)
	}
}

func TestResolveFallbackModelReportsUnsetIdenticalAndMissing(t *testing.T) {
	models := testfix.NewFakeModels()
	models.Add(testfix.Model("anthropic", "haiku"), testfix.KeyAuth("k"))
	ctx := context.Background()

	rt := withConfig(nil)
	if result := rt.ResolveFallbackModel(ctx, resolveCtx(nil, models, nil)); result.OK || result.Reason != "no fallback model configured" {
		t.Fatalf("result %+v", result)
	}

	rt = withConfig(func(cfg *config.Config) {
		cfg.Model = &config.ConfiguredModel{Provider: "anthropic", ID: "haiku"}
		cfg.FallbackModel = &config.ConfiguredModel{Provider: "anthropic", ID: "haiku"}
	})
	if result := rt.ResolveFallbackModel(ctx, resolveCtx(nil, models, nil)); result.OK || result.Reason != "fallback model anthropic/haiku is identical to the effective primary model" {
		t.Fatalf("result %+v", result)
	}

	rt = withConfig(func(cfg *config.Config) {
		cfg.FallbackModel = &config.ConfiguredModel{Provider: "anthropic", ID: "haiku"}
	})
	if result := rt.ResolveFallbackModel(ctx, resolveCtx(host.Model{"provider": "anthropic", "id": "haiku"}, models, nil)); result.OK || result.Reason != "fallback model anthropic/haiku is identical to the effective primary model" {
		t.Fatalf("result %+v", result)
	}

	rt = withConfig(func(cfg *config.Config) { target := fallbackTarget; cfg.FallbackModel = &target })
	if result := rt.ResolveFallbackModel(ctx, resolveCtx(nil, models, nil)); result.OK || result.Reason != "fallback model opencode-go/deepseek-v4.1-flash not found" {
		t.Fatalf("result %+v", result)
	}
}

func TestResolveFallbackModelHandlesExpiredOAuth(t *testing.T) {
	rt := withConfig(func(cfg *config.Config) {
		cfg.FallbackModel = &config.ConfiguredModel{Provider: "openai-codex", ID: "gpt-5-codex"}
	})
	models := testfix.NewFakeModels()
	models.Add(testfix.Model("openai-codex", "gpt-5-codex"), host.Auth{OK: false, Error: "refresh failed"})
	models.OAuth["openai-codex"] = true

	result := rt.ResolveFallbackModel(context.Background(), resolveCtx(host.Model{"provider": "anthropic", "id": "haiku"}, models, nil))

	want := `authentication failed for provider "openai-codex" — OAuth credentials may have expired; run '/login openai-codex' to re-authenticate`
	if result.OK || result.Reason != want {
		t.Fatalf("result %+v", result)
	}
}

func TestResolveFallbackModelHandlesRequestTimeSigning(t *testing.T) {
	rt := withConfig(func(cfg *config.Config) {
		cfg.FallbackModel = &config.ConfiguredModel{Provider: "amazon-bedrock", ID: "us.anthropic.claude-sonnet-4-5"}
	})
	models := testfix.NewFakeModels()
	fallback := models.Add(testfix.Model("amazon-bedrock", "us.anthropic.claude-sonnet-4-5"), host.Auth{OK: true})
	models.Configured["amazon-bedrock"] = true

	result := rt.ResolveFallbackModel(context.Background(), resolveCtx(host.Model{"provider": "anthropic", "id": "haiku"}, models, nil))

	if !result.OK || fmt.Sprintf("%p", result.Model) != fmt.Sprintf("%p", fallback) || result.APIKey != nil || len(result.Headers) != 0 || len(result.Env) != 0 || result.BaseURL != "" {
		t.Fatalf("result %+v", result)
	}
}

func TestResolveFallbackModelAppliesSameAuthRules(t *testing.T) {
	rt := withConfig(func(cfg *config.Config) {
		cfg.Model = &config.ConfiguredModel{Provider: "anthropic", ID: "haiku"}
		target := fallbackTarget
		cfg.FallbackModel = &target
	})
	models := fallbackModels("anthropic", testfix.KeyAuth("primary"), headersAuth(map[string]any{"Authorization": "Bearer go"}))

	result := rt.ResolveFallbackModel(context.Background(), resolveCtx(nil, models, nil))

	if !result.OK || !isFallback(result.Model) || result.APIKey != nil || !mapsEqual(result.Headers, map[string]any{"Authorization": "Bearer go"}) {
		t.Fatalf("result %+v", result)
	}
}

// An id-only model setting resolves against the session model's provider
// (upstream issue #31).
func TestIDOnlyConfiguredModelUsesSessionProvider(t *testing.T) {
	rt := withConfig(func(cfg *config.Config) { cfg.Model = &config.ConfiguredModel{ID: "memory"} })
	models := testfix.NewFakeModels()
	models.Add(testfix.Model("anthropic", "memory"), testfix.KeyAuth("key"))

	result := rt.ResolveModel(context.Background(), resolveCtx(host.Model{"provider": "anthropic", "id": "big"}, models, nil))

	if !result.OK || host.ModelProvider(result.Model) != "anthropic" || host.ModelID(result.Model) != "memory" || !reflect.DeepEqual(models.FindCalls, []string{"anthropic/memory"}) {
		t.Fatalf("result %+v finds %v", result, models.FindCalls)
	}

	// The same id on another session provider follows the switch.
	models.Add(testfix.Model("openai", "memory"), testfix.KeyAuth("key"))
	result = rt.ResolveModel(context.Background(), resolveCtx(host.Model{"provider": "openai", "id": "big"}, models, nil))
	if !result.OK || host.ModelProvider(result.Model) != "openai" {
		t.Fatalf("result %+v", result)
	}

	// Not found on the session provider: the notice names the id alone when no provider is known.
	n := &notifier{}
	rt.ResolveModel(context.Background(), resolveCtx(host.Model{"provider": "xai", "id": "big"}, models, n))
	if !n.has("Observational memory: configured model xai/memory not found, using session model", "warning") {
		t.Fatalf("notifications %+v", n.calls)
	}
}

func TestIDOnlyFallbackModelIdentityCheck(t *testing.T) {
	rt := withConfig(func(cfg *config.Config) { cfg.FallbackModel = &config.ConfiguredModel{ID: "haiku"} })
	models := testfix.NewFakeModels()
	models.Add(testfix.Model("anthropic", "haiku"), testfix.KeyAuth("k"))

	result := rt.ResolveFallbackModel(context.Background(), resolveCtx(host.Model{"provider": "anthropic", "id": "haiku"}, models, nil))
	if result.OK || result.Reason != "fallback model anthropic/haiku is identical to the effective primary model" {
		t.Fatalf("result %+v", result)
	}

	result = rt.ResolveFallbackModel(context.Background(), resolveCtx(host.Model{"provider": "anthropic", "id": "sonnet"}, models, nil))
	if !result.OK || host.ModelID(result.Model) != "haiku" {
		t.Fatalf("result %+v", result)
	}

	result = rt.ResolveFallbackModel(context.Background(), resolveCtx(nil, models, nil))
	if result.OK || result.Reason != "fallback model haiku not found" {
		t.Fatalf("result %+v", result)
	}
}

// Resolution warnings are shown once per episode: repeats are quiet, and a
// clean resolution re-arms them.
func TestResolveWarningsShownOncePerEpisode(t *testing.T) {
	rt := withConfig(func(cfg *config.Config) { cfg.Model = &config.ConfiguredModel{Provider: "anthropic", ID: "missing"} })
	models := testfix.NewFakeModels()
	models.Auth["openai"] = testfix.KeyAuth("key")
	n := &notifier{}
	session := host.Model{"provider": "openai", "id": "gpt"}

	for i := 0; i < 3; i++ {
		if result := rt.ResolveModel(context.Background(), resolveCtx(session, models, n)); !result.OK {
			t.Fatalf("result %+v", result)
		}
	}
	if len(n.calls) != 1 {
		t.Fatalf("notifications %+v", n.calls)
	}

	// The configured model appears: a clean resolution clears the episode.
	models.Add(testfix.Model("anthropic", "missing"), testfix.KeyAuth("key"))
	rt.ResolveModel(context.Background(), resolveCtx(session, models, n))
	delete(models.Registry, testfix.Key("anthropic", "missing"))
	rt.ResolveModel(context.Background(), resolveCtx(session, models, n))
	if len(n.calls) != 2 {
		t.Fatalf("notifications %+v", n.calls)
	}
}

// RunAborted tells an aborted run from one that finished.
func TestRunAbortedTracksTheAbortedRun(t *testing.T) {
	rt := runtime.New()
	release := make(chan struct{})
	done := rt.LaunchConsolidationTask(nil, func(ctx context.Context) error { <-ctx.Done(); return nil })
	if !rt.AbortConsolidation() {
		t.Fatal("nothing to abort")
	}
	<-done
	if !rt.RunAborted(done) {
		t.Fatal("aborted run not reported")
	}
	finished := rt.LaunchConsolidationTask(nil, func(ctx context.Context) error { <-release; return nil })
	close(release)
	<-finished
	if rt.RunAborted(finished) || rt.RunAborted(nil) {
		t.Fatal("finished run reported as aborted")
	}
}

// Pi's own compaction.enabled is re-read for a configuration loaded from disk.
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
	set.ObserveAfterTokens = 7
	rt.SetConfig(set)
	rt.ReloadConfig()
	rt.EnsureConfig(h)
	if rt.Config().ObserveAfterTokens != 7 || len(seen) != 3 {
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
