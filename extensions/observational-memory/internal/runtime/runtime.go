// Package runtime holds the extension's per-process state and resolves the
// memory-worker model.
package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/host"
)

// Phase names a consolidation stage.
type Phase string

// The consolidation stages.
const (
	PhaseObserver  Phase = "observer"
	PhaseReflector Phase = "reflector"
	PhaseDropper   Phase = "dropper"
)

// ResolveResult is a resolved memory-worker model, or why none resolved.
type ResolveResult struct {
	OK      bool
	Reason  string
	Model   host.Model
	APIKey  *string
	Headers map[string]any
	Env     map[string]string
	BaseURL string
	// FallbackUsed is true when this result came from config.fallbackModel after the primary failed.
	FallbackUsed bool
	// PrimaryFailure is the primary failure reason recorded when FallbackUsed is true.
	PrimaryFailure string
}

// ResolveCtx is what model resolution reads from the host.
type ResolveCtx struct {
	Model  host.Model
	Models host.Models
	HasUI  bool
	Notify func(message, level string)
}

func (c ResolveCtx) notify(message, level string) {
	if c.HasUI && c.Notify != nil {
		c.Notify(message, level)
	}
}

// ObserverEmptyBackoff records a deliberate-empty observer verdict so the
// observer does not re-fire over the same span every turn.
type ObserverEmptyBackoff struct {
	SessionIdentity string
	CoverageID      string
	TokensAtEmpty   int
}

// How long to wait for the availability re-check, and how long before the same
// provider may be re-checked again.
const (
	AvailabilityRecheckTimeout = 5 * time.Second
	AvailabilityRecheckRearm   = 60 * time.Second
)

// Runtime is the extension state shared by every hook, command and tool.
type Runtime struct {
	mu sync.Mutex

	config       config.Config
	configLoaded bool
	// LoadConfig reads the configuration for a working directory; tests replace it.
	LoadConfig func(cwd string) config.Config
	// Now is the clock; tests replace it.
	Now func() time.Time

	consolidationInFlight bool
	consolidationDone     chan struct{}
	consolidationPhase    Phase
	consolidationCancel   context.CancelFunc
	consolidationCtx      context.Context

	compactInFlight        bool
	compactHookInFlight    bool
	resolveFailureNotified bool

	lastObserverError  string
	lastReflectorError string
	lastDropperError   string

	availabilityRecheckedAt map[string]time.Time
	observerEmptyBackoff    *ObserverEmptyBackoff
}

// New creates a runtime with the default configuration.
func New() *Runtime {
	return &Runtime{
		config:                  config.Defaults,
		LoadConfig:              func(cwd string) config.Config { return config.Load(cwd, config.DefaultPaths(), nil) },
		Now:                     time.Now,
		availabilityRecheckedAt: map[string]time.Time{},
	}
}

// EnsureConfig loads the configuration once.
func (r *Runtime) EnsureConfig(cwd string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.configLoaded {
		return
	}
	r.config = r.LoadConfig(cwd)
	r.configLoaded = true
}

// Config is the current configuration.
func (r *Runtime) Config() config.Config {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.config
}

// SetConfig replaces the configuration and marks it loaded.
func (r *Runtime) SetConfig(cfg config.Config) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.config = cfg
	r.configLoaded = true
}

// ConfigLoaded reports whether EnsureConfig ran.
func (r *Runtime) ConfigLoaded() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.configLoaded
}

// State is a snapshot of the in-flight flags and last errors.
type State struct {
	ConsolidationInFlight  bool
	ConsolidationPhase     Phase
	CompactInFlight        bool
	CompactHookInFlight    bool
	ResolveFailureNotified bool
	LastObserverError      string
	LastReflectorError     string
	LastDropperError       string
	ObserverEmptyBackoff   *ObserverEmptyBackoff
}

// Snapshot reads the state.
func (r *Runtime) Snapshot() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return State{
		ConsolidationInFlight:  r.consolidationInFlight,
		ConsolidationPhase:     r.consolidationPhase,
		CompactInFlight:        r.compactInFlight,
		CompactHookInFlight:    r.compactHookInFlight,
		ResolveFailureNotified: r.resolveFailureNotified,
		LastObserverError:      r.lastObserverError,
		LastReflectorError:     r.lastReflectorError,
		LastDropperError:       r.lastDropperError,
		ObserverEmptyBackoff:   r.observerEmptyBackoff,
	}
}

// ConsolidationInFlight reports a running consolidation.
func (r *Runtime) ConsolidationInFlight() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.consolidationInFlight
}

// SetConsolidationPhase records the running stage.
func (r *Runtime) SetConsolidationPhase(phase Phase) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.consolidationPhase = phase
}

// CompactInFlight reports a proactive compaction in progress.
func (r *Runtime) CompactInFlight() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.compactInFlight
}

// SetCompactInFlight records a proactive compaction in progress.
func (r *Runtime) SetCompactInFlight(v bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.compactInFlight = v
}

// TryEnterCompactHook marks the compaction hook running; false when it already is.
func (r *Runtime) TryEnterCompactHook() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.compactHookInFlight {
		return false
	}
	r.compactHookInFlight = true
	return true
}

// LeaveCompactHook clears the compaction hook flag.
func (r *Runtime) LeaveCompactHook() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.compactHookInFlight = false
}

// SetCompactHookInFlight sets the compaction hook flag.
func (r *Runtime) SetCompactHookInFlight(v bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.compactHookInFlight = v
}

// ResolveFailureNotified reports whether the user was told about a resolution failure.
func (r *Runtime) ResolveFailureNotified() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resolveFailureNotified
}

// SetResolveFailureNotified records whether the user was told about a resolution failure.
func (r *Runtime) SetResolveFailureNotified(v bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolveFailureNotified = v
}

// ObserverEmptyBackoff is the current deliberate-empty backoff, or nil.
func (r *Runtime) ObserverEmptyBackoff() *ObserverEmptyBackoff {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.observerEmptyBackoff
}

// SetObserverEmptyBackoff records or clears the deliberate-empty backoff.
func (r *Runtime) SetObserverEmptyBackoff(b *ObserverEmptyBackoff) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.observerEmptyBackoff = b
}

// SetLastError records a stage's last error (tests use it directly).
func (r *Runtime) SetLastError(phase Phase, message string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch phase {
	case PhaseObserver:
		r.lastObserverError = message
	case PhaseReflector:
		r.lastReflectorError = message
	case PhaseDropper:
		r.lastDropperError = message
	}
}

// EffectiveProvider is the provider a configured model resolves against: its
// own, or the session model's when the setting names only an id.
func EffectiveProvider(configured *config.ConfiguredModel, sessionModel host.Model) string {
	if configured == nil {
		return ""
	}
	if configured.Provider != "" {
		return configured.Provider
	}
	return host.ModelProvider(sessionModel)
}

// modelLabel names a model in messages: provider/id, or the id alone when no
// provider is known.
func modelLabel(provider, id string) string {
	if provider == "" {
		return id
	}
	return provider + "/" + id
}

// ResolveModel resolves the model memory workers should use: config.model
// (falling back to the session model when it is absent from Pi's registry),
// then config.fallbackModel when the primary resolution fails. With no
// fallback configured the primary reason is returned unchanged; with a
// configured-but-broken fallback both reasons are reported.
func (r *Runtime) ResolveModel(ctx context.Context, rc ResolveCtx) ResolveResult {
	primary := r.resolvePrimaryModel(ctx, rc)
	if primary.OK {
		return primary
	}
	fallback := r.ResolveFallbackModel(ctx, rc)
	cfg := r.Config()
	if !fallback.OK {
		if cfg.FallbackModel != nil {
			return ResolveResult{Reason: primary.Reason + "; " + fallback.Reason}
		}
		return primary
	}
	provider := host.ModelProvider(fallback.Model)
	id := host.ModelID(fallback.Model)
	if cfg.FallbackModel != nil {
		if provider == "" {
			provider = EffectiveProvider(cfg.FallbackModel, rc.Model)
		}
		if id == "" {
			id = cfg.FallbackModel.ID
		}
	}
	if provider == "" {
		provider = "unknown"
	}
	if id == "" {
		id = "unknown"
	}
	rc.notify(fmt.Sprintf("Observational memory: primary model unavailable (%s); using fallback %s/%s", primary.Reason, provider, id), "warning")
	debuglog.Log(ctx, "resolve.fallback_used", map[string]any{"provider": provider, "id": id, "primaryFailure": primary.Reason})
	fallback.FallbackUsed = true
	fallback.PrimaryFailure = primary.Reason
	return fallback
}

// config.model when it resolves in Pi's registry, otherwise the session model.
func (r *Runtime) resolvePrimaryModel(ctx context.Context, rc ResolveCtx) ResolveResult {
	cfg := r.Config()
	model := rc.Model
	if cfg.Model != nil {
		provider := EffectiveProvider(cfg.Model, rc.Model)
		if configured := rc.Models.Find(provider, cfg.Model.ID); configured != nil {
			model = configured
		} else {
			rc.notify(fmt.Sprintf("Observational memory: configured model %s not found, using session model", modelLabel(provider, cfg.Model.ID)), "warning")
		}
	}
	if model == nil {
		return ResolveResult{Reason: "no model available (session has no model and no observational-memory model configured)"}
	}
	return r.resolveCandidate(ctx, rc, model)
}

// ResolveFallbackModel resolves config.fallbackModel with the same auth rules
// as the primary path. It fails when unset, identical to the effective
// primary, absent from the registry, or carrying no usable credentials.
func (r *Runtime) ResolveFallbackModel(ctx context.Context, rc ResolveCtx) ResolveResult {
	cfg := r.Config()
	target := cfg.FallbackModel
	if target == nil {
		return ResolveResult{Reason: "no fallback model configured"}
	}
	targetProvider := EffectiveProvider(target, rc.Model)
	var effectivePrimary host.Model
	if cfg.Model != nil {
		effectivePrimary = rc.Models.Find(EffectiveProvider(cfg.Model, rc.Model), cfg.Model.ID)
	}
	if effectivePrimary == nil {
		effectivePrimary = rc.Model
	}
	if effectivePrimary != nil && host.ModelProvider(effectivePrimary) == targetProvider && host.ModelID(effectivePrimary) == target.ID {
		return ResolveResult{Reason: fmt.Sprintf("fallback model %s is identical to the effective primary model", modelLabel(targetProvider, target.ID))}
	}
	model := rc.Models.Find(targetProvider, target.ID)
	if model == nil {
		return ResolveResult{Reason: fmt.Sprintf("fallback model %s not found", modelLabel(targetProvider, target.ID))}
	}
	return r.resolveCandidate(ctx, rc, model)
}

// hasUsableAuth mirrors Pi's request-auth acceptance rule: resolved auth is
// usable when it carries an apiKey OR at least one non-empty header value.
func hasUsableAuth(auth host.Auth) bool {
	if auth.APIKey != nil && *auth.APIKey != "" {
		return true
	}
	return countUsableHeaders(auth.Headers) > 0
}

func countHeaders(headers map[string]any) int { return len(headers) }

func countUsableHeaders(headers map[string]any) int {
	count := 0
	for _, value := range headers {
		if s, ok := value.(string); ok && s != "" {
			count++
		}
	}
	return count
}

// Apply Pi's request-auth acceptance rule to one already-selected model.
func (r *Runtime) resolveCandidate(ctx context.Context, rc ResolveCtx, model host.Model) ResolveResult {
	auth, err := rc.Models.GetApiKeyAndHeaders(model)
	if err != nil {
		auth = host.Auth{OK: false, Error: err.Error()}
	}
	provider := host.ModelProvider(model)
	if provider == "" {
		provider = "unknown"
	}
	isOAuth := rc.Models.IsUsingOAuth(model)
	// auth.OK false is the only unambiguous failure. OK with neither apiKey
	// nor headers, for a provider Pi reports a credential source for, is a
	// provider that signs at request time (Bedrock SigV4, Vertex ADC). OAuth
	// providers, an empty-string key and a provider without any credential
	// source keep failing.
	usable := hasUsableAuth(auth)
	resolvedEmptyAPIKey := auth.APIKey != nil && *auth.APIKey == ""
	providerCredentialConfigured := rc.Models.HasConfiguredAuth(model)
	if auth.OK && !usable && !isOAuth && !resolvedEmptyAPIKey && !providerCredentialConfigured {
		providerCredentialConfigured = r.recheckProviderCredential(ctx, rc.Models, model, provider)
	}
	signsAtRequestTime := auth.OK && !isOAuth && !resolvedEmptyAPIKey && providerCredentialConfigured
	if !auth.OK || (!usable && !signsAtRequestTime) {
		reason := fmt.Sprintf("no API key or auth headers for provider %q", provider)
		if isOAuth {
			reason = fmt.Sprintf("authentication failed for provider %q — OAuth credentials may have expired; run '/login %s' to re-authenticate", provider, provider)
		}
		debuglog.Log(ctx, "resolve.rejected", map[string]any{
			"provider":                     provider,
			"reason":                       reason,
			"authOk":                       auth.OK,
			"hasApiKey":                    auth.APIKey != nil && *auth.APIKey != "",
			"resolvedEmptyApiKey":          resolvedEmptyAPIKey,
			"headerCount":                  countHeaders(auth.Headers),
			"usableHeaderCount":            countUsableHeaders(auth.Headers),
			"isOAuth":                      isOAuth,
			"providerCredentialConfigured": providerCredentialConfigured,
			"signsAtRequestTime":           signsAtRequestTime,
		})
		return ResolveResult{Reason: reason}
	}
	if !usable {
		debuglog.Log(ctx, "resolve.request_time_signing", map[string]any{"provider": provider, "providerCredentialConfigured": providerCredentialConfigured})
	}
	// Match Pi's request model: OAuth may route to an account-specific
	// endpoint. Do not mutate the shared registry model.
	requestModel := model
	if auth.BaseURL != "" {
		requestModel = make(host.Model, len(model)+1)
		for k, v := range model {
			requestModel[k] = v
		}
		requestModel["baseUrl"] = auth.BaseURL
	}
	return ResolveResult{OK: true, Model: requestModel, APIKey: auth.APIKey, Headers: auth.Headers, Env: auth.Env, BaseURL: auth.BaseURL}
}

// recheckProviderCredential re-checks one provider's credential live, then
// re-reads Pi's snapshot. Bounded and rate-limited; never fails.
func (r *Runtime) recheckProviderCredential(ctx context.Context, models host.Models, model host.Model, provider string) bool {
	r.mu.Lock()
	now := r.Now()
	if last, ok := r.availabilityRecheckedAt[provider]; ok && now.Sub(last) < AvailabilityRecheckRearm {
		r.mu.Unlock()
		return false
	}
	r.availabilityRecheckedAt[provider] = now
	r.mu.Unlock()

	allowNetwork := false
	refreshCtx, cancel := context.WithTimeout(ctx, AvailabilityRecheckTimeout)
	defer cancel()
	refreshErr := ""
	timedOut := false
	done := make(chan error, 1)
	go func() {
		done <- models.Refresh(refreshCtx, host.RefreshOptions{AllowNetwork: &allowNetwork, Providers: []string{provider}})
	}()
	select {
	case err := <-done:
		if err != nil {
			refreshErr = err.Error()
		}
	case <-refreshCtx.Done():
		timedOut = true
	}
	// Re-read even after an error or timeout: a scoped pass can update the
	// snapshot for this provider and still fail elsewhere.
	recovered := models.HasConfiguredAuth(model)
	data := map[string]any{
		"provider":  provider,
		"refreshed": refreshErr == "" && !timedOut,
		"recovered": recovered,
		"elapsedMs": r.Now().Sub(now).Milliseconds(),
		"timedOut":  timedOut,
	}
	if refreshErr != "" {
		data["refreshError"] = refreshErr
	}
	debuglog.Log(ctx, "resolve.availability_recheck", data)
	return recovered
}

// LaunchConsolidationTask starts a consolidation run in the background. The
// returned channel closes when it finishes; a failure is notified and recorded.
func (r *Runtime) LaunchConsolidationTask(ui host.UI, work func(ctx context.Context) error) <-chan struct{} {
	r.mu.Lock()
	r.consolidationInFlight = true
	r.consolidationPhase = ""
	r.lastObserverError = ""
	r.lastReflectorError = ""
	r.lastDropperError = ""
	ctx, cancel := context.WithCancel(context.Background())
	r.consolidationCtx = ctx
	r.consolidationCancel = cancel
	done := make(chan struct{})
	r.consolidationDone = done
	r.mu.Unlock()
	go func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				if ui != nil && ui.HasUI() {
					ui.Notify(fmt.Sprintf("Observational memory: consolidation failed: %v", recovered), "warning")
				}
			}
			r.mu.Lock()
			r.consolidationInFlight = false
			r.consolidationPhase = ""
			if r.consolidationCancel != nil && r.consolidationCtx == ctx {
				r.consolidationCancel = nil
				r.consolidationCtx = nil
			}
			if r.consolidationDone == done {
				r.consolidationDone = nil
			}
			r.mu.Unlock()
			cancel()
			close(done)
		}()
		if err := work(ctx); err != nil && ui != nil && ui.HasUI() {
			ui.Notify("Observational memory: consolidation failed: "+err.Error(), "warning")
		}
	}()
	return done
}

// ConsolidationContext is the running consolidation's cancellation, or nil.
func (r *Runtime) ConsolidationContext() context.Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.consolidationCtx
}

// ConsolidationDone is the running consolidation's completion channel, or nil.
func (r *Runtime) ConsolidationDone() <-chan struct{} {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.consolidationDone == nil {
		return nil
	}
	return r.consolidationDone
}

// WasAborted reports whether the running consolidation was cancelled.
func (r *Runtime) WasAborted() bool {
	ctx := r.ConsolidationContext()
	return ctx != nil && ctx.Err() != nil
}

// AbortConsolidation cancels the in-flight consolidation run, if any.
func (r *Runtime) AbortConsolidation() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.consolidationCancel == nil || !r.consolidationInFlight {
		return false
	}
	r.consolidationCancel()
	return true
}

// RecordConsolidationStageError records a stage failure and notifies the user.
func (r *Runtime) RecordConsolidationStageError(ui host.UI, phase Phase, err error) string {
	message := err.Error()
	r.SetLastError(phase, message)
	if ui != nil && ui.HasUI() {
		ui.Notify(fmt.Sprintf("Observational memory: %s failed: %s", phase, message), "warning")
	}
	return message
}
