package runtime_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/testfix"
)

// Providers that authenticate at request time (Bedrock with AWS_PROFILE, Vertex
// with ADC) hand back an ok:true auth with nothing to carry, while Pi reports a
// credential source for them. Those shapes were measured against Pi's own
// registry; the fakes here reproduce them.

var bedrockModel = testfix.Model("amazon-bedrock", "eu.anthropic.claude-haiku-4-5-20251001-v1:0")

func ambientModels(configured bool, oauth bool) *testfix.FakeModels {
	models := testfix.NewFakeModels()
	models.Auth[host.ModelProvider(bedrockModel)] = host.Auth{OK: true}
	models.Configured[host.ModelProvider(bedrockModel)] = configured
	models.OAuth[host.ModelProvider(bedrockModel)] = oauth
	return models
}

func resolveBedrock(rt *runtime.Runtime, models host.Models) runtime.ResolveResult {
	return rt.ResolveModel(context.Background(), runtime.ResolveCtx{Model: bedrockModel, Models: models})
}

func TestResolvesWhenCredentialSourceExistsButNothingToAttach(t *testing.T) {
	result := resolveBedrock(withConfig(nil), ambientModels(true, false))

	if !result.OK || fmt.Sprintf("%p", result.Model) != fmt.Sprintf("%p", bedrockModel) || result.APIKey != nil || len(result.Headers) != 0 {
		t.Fatalf("result %+v", result)
	}
}

func TestStillFailsWithoutCredentialSource(t *testing.T) {
	result := resolveBedrock(withConfig(nil), ambientModels(false, false))

	if result.OK || !strings.Contains(result.Reason, fmt.Sprintf(authFailure, "amazon-bedrock")) {
		t.Fatalf("result %+v", result)
	}
}

func TestStillFailsForOAuthProvidersWhoseCredentialsNoLongerResolve(t *testing.T) {
	models := testfix.NewFakeModels()
	models.Auth["openai-codex"] = host.Auth{OK: true}
	models.Configured["openai-codex"] = true
	models.OAuth["openai-codex"] = true

	result := withConfig(nil).ResolveModel(context.Background(), runtime.ResolveCtx{Model: host.Model{"provider": "openai-codex", "id": "gpt-5-codex"}, Models: models})

	if result.OK || !strings.Contains(result.Reason, "/login openai-codex") {
		t.Fatalf("result %+v", result)
	}
}

func TestStillFailsWhenStoredCredentialResolvesToEmptyKey(t *testing.T) {
	models := testfix.NewFakeModels()
	models.Auth["xai"] = host.Auth{OK: true, APIKey: strp("")}
	models.Configured["xai"] = true

	result := withConfig(nil).ResolveModel(context.Background(), runtime.ResolveCtx{Model: host.Model{"provider": "xai", "id": "grok-4"}, Models: models})

	if result.OK || !strings.Contains(result.Reason, fmt.Sprintf(authFailure, "xai")) {
		t.Fatalf("result %+v", result)
	}
}

func TestDoesNotAcceptUnconfiguredProviderBecauseAuthOK(t *testing.T) {
	models := testfix.NewFakeModels()
	models.Auth["anthropic"] = host.Auth{OK: true}

	result := withConfig(nil).ResolveModel(context.Background(), runtime.ResolveCtx{Model: host.Model{"provider": "anthropic", "id": "claude"}, Models: models})

	if result.OK {
		t.Fatalf("result %+v", result)
	}
}

// A stale availability snapshot (Pi's refresh pass skipped, aborted or failed)
// reports the provider unconfigured although its credential works; a scoped,
// network-free re-check recovers it.
func staleSnapshotModels(recovers bool) *testfix.FakeModels {
	models := ambientModels(false, false)
	models.RefreshFn = func(ctx context.Context, options host.RefreshOptions) error {
		if recovers {
			models.Configured[host.ModelProvider(bedrockModel)] = true
		}
		return nil
	}
	return models
}

func TestRechecksLiveAndAcceptsWhenSnapshotStale(t *testing.T) {
	models := staleSnapshotModels(true)

	result := resolveBedrock(withConfig(nil), models)

	if !result.OK || len(models.RefreshCalls) != 1 {
		t.Fatalf("result %+v refresh calls %d", result, len(models.RefreshCalls))
	}
}

func TestAsksForScopedNetworkFreeRecheck(t *testing.T) {
	models := staleSnapshotModels(true)

	resolveBedrock(withConfig(nil), models)

	if len(models.RefreshCalls) != 1 {
		t.Fatalf("refresh calls %d", len(models.RefreshCalls))
	}
	call := models.RefreshCalls[0]
	if call.AllowNetwork == nil || *call.AllowNetwork || !reflect.DeepEqual(call.Providers, []string{"amazon-bedrock"}) {
		t.Fatalf("refresh options %+v", call)
	}
}

func TestStillRejectsWhenRecheckConfirmsUnconfigured(t *testing.T) {
	models := staleSnapshotModels(false)

	result := resolveBedrock(withConfig(nil), models)

	if result.OK || len(models.RefreshCalls) != 1 {
		t.Fatalf("result %+v refresh calls %d", result, len(models.RefreshCalls))
	}
}

func TestRateLimitsTheRecheck(t *testing.T) {
	rt := withConfig(nil)
	models := staleSnapshotModels(false)

	for i := 0; i < 3; i++ {
		resolveBedrock(rt, models)
	}

	if len(models.RefreshCalls) != 1 {
		t.Fatalf("refresh calls %d", len(models.RefreshCalls))
	}
}

func TestDoesNotRecheckWhenAuthUsable(t *testing.T) {
	models := staleSnapshotModels(true)
	models.Auth[host.ModelProvider(bedrockModel)] = testfix.KeyAuth("sk-live")

	result := resolveBedrock(withConfig(nil), models)

	if !result.OK || len(models.RefreshCalls) != 0 {
		t.Fatalf("result %+v refresh calls %d", result, len(models.RefreshCalls))
	}
}

func TestDoesNotRecheckOAuthProviders(t *testing.T) {
	models := staleSnapshotModels(true)
	models.OAuth[host.ModelProvider(bedrockModel)] = true

	result := resolveBedrock(withConfig(nil), models)

	if result.OK || len(models.RefreshCalls) != 0 {
		t.Fatalf("result %+v refresh calls %d", result, len(models.RefreshCalls))
	}
}

func TestSurvivesRefreshFailure(t *testing.T) {
	models := ambientModels(false, false)
	models.RefreshFn = func(ctx context.Context, options host.RefreshOptions) error {
		return errors.New("availability refresh failed")
	}

	result := resolveBedrock(withConfig(nil), models)

	if result.OK || len(models.RefreshCalls) != 1 {
		t.Fatalf("result %+v refresh calls %d", result, len(models.RefreshCalls))
	}
}
