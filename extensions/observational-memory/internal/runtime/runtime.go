// Package runtime holds the extension's per-process state and resolves the
// session model for the level-1 request.
package runtime

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/debuglog"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
)

// ResolveResult is a resolved model with its request auth, or why none resolved.
type ResolveResult struct {
	OK      bool
	Reason  string
	Model   host.Model
	APIKey  *string
	Headers map[string]any
	Env     map[string]string
	BaseURL string
}

// ResolveCtx is what model resolution reads from the host.
type ResolveCtx struct {
	Model  host.Model
	Models host.Models
	HasUI  bool
	Notify func(message, level string)
}

// CompactionBackoff records a compaction the host refused before compacting ("Nothing to
// compact", "Already compacted", an invalid compaction setting), so the auto-compaction-off
// trigger does not ask again on every settle while nothing has changed.
type CompactionBackoff struct {
	SessionIdentity string
	// BoundaryID is the newest compaction entry at the refusal, "" before the first.
	BoundaryID string
	// Reason is the host's error message.
	Reason string
	// TipID is the branch tip at the refusal: the trigger stays suspended until the tip
	// changes.
	TipID string
}

// How long to wait for the availability re-check, and how long before the same
// provider may be re-checked again. Tests shorten them.
var (
	AvailabilityRecheckTimeout = 5 * time.Second
	AvailabilityRecheckRearm   = 60 * time.Second
)

// Runtime is the extension state shared by every hook, command and tool.
type Runtime struct {
	mu sync.Mutex

	config       config.Config
	configLoaded bool
	// LoadConfig reads the configuration for a working directory under the
	// host's project trust answer; tests replace it.
	LoadConfig func(cwd string, trust config.TrustState) config.Config
	// Now is the clock; tests replace it.
	Now func() time.Time
	// ReadPiAutoCompaction re-reads Pi's own compaction.enabled setting
	// (enabled, whether any settings file read sets it) under the trust the
	// configuration was loaded with; tests replace it.
	ReadPiAutoCompaction func(cwd string, trust config.TrustState) (bool, bool)
	// ReadPiCompactionTokens re-reads PiG's compaction.reserveTokens and its
	// modelOverrides under the trust the configuration was loaded with; tests
	// replace it.
	ReadPiCompactionTokens func(cwd string, trust config.TrustState) config.PiCompactionTokens
	// configFromDisk is true when EnsureConfig loaded the configuration,
	// false when a caller set it (tests).
	configFromDisk bool
	// configGeneration counts ReloadConfig calls, so an EnsureConfig that
	// read the host before one does not install what it read.
	configGeneration uint64

	compactInFlight     bool
	compactHookInFlight bool
	// compactHookRequest is the request context of the hook holding the compaction slot
	// (SetCompactHookRequest), nil when none recorded one.
	compactHookRequest context.Context

	availabilityRecheckedAt map[string]time.Time
	compactionBackoff       *CompactionBackoff
	// thinKey and thinPlan are the thinning plan and the session and compaction it was
	// derived from (CachedThinPlan).
	thinKey  string
	thinPlan map[string]string
	// definedTools lists the tools the agent defined that this process
	// registered (SetDefinedTools); nil means none.
	definedTools func() []string
	// shownOnce holds the notices ShowOnce already let through.
	shownOnce map[string]bool
}

// New creates a runtime with the default configuration.
func New() *Runtime {
	return &Runtime{
		config: config.Defaults,
		LoadConfig: func(cwd string, trust config.TrustState) config.Config {
			return config.Load(cwd, config.DefaultPaths(), trust, nil)
		},
		ReadPiAutoCompaction: func(cwd string, trust config.TrustState) (bool, bool) {
			return config.ReadPiAutoCompaction(cwd, config.DefaultPaths(), trust)
		},
		ReadPiCompactionTokens: func(cwd string, trust config.TrustState) config.PiCompactionTokens {
			return config.ReadPiCompactionTokens(cwd, config.DefaultPaths(), trust)
		},
		Now:                     time.Now,
		availabilityRecheckedAt: map[string]time.Time{},
	}
}

// ConfigHost is what loading the configuration reads from the host: the
// working directory and whether the project is trusted.
type ConfigHost interface {
	Cwd() string
	ProjectTrusted() (bool, error)
}

// EnsureConfig loads the configuration once per session (ReloadConfig drops
// it). Project settings are read only when the host reports the project
// trusted; a failed trust check reads the global settings alone and is not
// kept, so the next EnsureConfig asks the host again instead of running the
// rest of the session global-only while the memory tool, which checks trust on
// every call, reads the project's settings.
func (r *Runtime) EnsureConfig(h ConfigHost) {
	for {
		r.mu.Lock()
		loaded, generation := r.configLoaded, r.configGeneration
		r.mu.Unlock()
		if loaded {
			return
		}
		// The trust check is a host call: it is made without holding the lock.
		trust := config.TrustFrom(h.ProjectTrusted())
		cwd := h.Cwd()
		r.mu.Lock()
		if r.configLoaded {
			r.mu.Unlock()
			return
		}
		if r.configGeneration != generation {
			// A ReloadConfig (a session start) landed during the host calls:
			// their answers may describe the previous session, so ask again.
			r.mu.Unlock()
			continue
		}
		r.config = r.LoadConfig(cwd, trust)
		r.configLoaded = trust != config.ProjectTrustUnknown
		r.configFromDisk = true
		r.mu.Unlock()
		return
	}
}

// ReloadConfig drops a configuration EnsureConfig loaded, so the next
// EnsureConfig reads the settings and the project's trust again; an
// EnsureConfig still waiting on the host discards what it read. A
// configuration a caller set (SetConfig) stays.
func (r *Runtime) ReloadConfig() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.configGeneration++
	if r.configFromDisk {
		r.configLoaded = false
	}
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
	r.configFromDisk = false
}

// SetDefinedTools sets where DefinedTools reads the tools the agent defined
// with the memory tool that this process registered (harness.Manager.Registered).
func (r *Runtime) SetDefinedTools(list func() []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.definedTools = list
}

// DefinedTools lists the tools the agent defined with the memory tool that
// this process registered, as SetDefinedTools reads them; none when it was
// never set.
func (r *Runtime) DefinedTools() []string {
	r.mu.Lock()
	list := r.definedTools
	r.mu.Unlock()
	if list == nil {
		return nil
	}
	return list()
}

// LiveConfig is the configuration with Pi's own compaction.enabled (and
// PiG's compaction.reserveTokens) re-read. The user can toggle Pi's automatic
// compaction mid-session, and with it off the extension's own trigger is the
// only compaction, so a startup snapshot would leave a session with no
// compaction backstop at all.
func (r *Runtime) LiveConfig(cwd string) config.Config {
	r.mu.Lock()
	cfg, fromDisk, read, readTokens := r.config, r.configFromDisk, r.ReadPiAutoCompaction, r.ReadPiCompactionTokens
	r.mu.Unlock()
	if fromDisk && read != nil {
		if enabled, ok := read(cwd, cfg.ProjectTrust); ok {
			cfg.PiAutoCompactionEnabled = enabled
		} else {
			cfg.PiAutoCompactionEnabled = config.Defaults.PiAutoCompactionEnabled
		}
	}
	if fromDisk && readTokens != nil {
		cfg.PiCompaction = readTokens(cwd, cfg.ProjectTrust)
	}
	return cfg
}

// ShowOnce reports whether the notice named key has not been shown yet in this
// process, and marks it shown: a condition that cannot change while the
// process runs is reported once, not on every command.
func (r *Runtime) ShowOnce(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.shownOnce[key] {
		return false
	}
	if r.shownOnce == nil {
		r.shownOnce = map[string]bool{}
	}
	r.shownOnce[key] = true
	return true
}

// ConfigLoaded reports whether a configuration is loaded for the session: a
// caller set it, or EnsureConfig loaded it under a trust answer (one whose trust
// check failed leaves it false, so the next EnsureConfig asks again).
func (r *Runtime) ConfigLoaded() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.configLoaded
}

// State is a snapshot of the in-flight flags.
type State struct {
	CompactInFlight     bool
	CompactHookInFlight bool
}

// Snapshot reads the state.
func (r *Runtime) Snapshot() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	return State{CompactInFlight: r.compactInFlight, CompactHookInFlight: r.compactHookInFlight}
}

// CompactInFlight reports the extension's own compaction in progress (the
// auto-compaction-off trigger).
func (r *Runtime) CompactInFlight() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.compactInFlight
}

// SetCompactInFlight records the extension's own compaction in progress.
func (r *Runtime) SetCompactInFlight(v bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.compactInFlight = v
}

// TryStartCompact claims the extension's compaction slot; false when a
// compaction is already in flight. Checking and claiming in one step keeps two
// callers released together from both compacting.
func (r *Runtime) TryStartCompact() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.compactInFlight {
		return false
	}
	r.compactInFlight = true
	return true
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
	r.compactHookRequest = nil
}

// SetCompactHookRequest records the request context of the hook holding the compaction slot,
// so a concurrent hook can tell whether that one's compaction is still live
// (CompactHookOwnerLive). LeaveCompactHook clears it.
func (r *Runtime) SetCompactHookRequest(request context.Context) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.compactHookRequest = request
}

// CompactHookOwnerLive reports whether the hook holding the compaction slot recorded a request
// context that is not done: false with no slot holder, or one PiG cancelled.
func (r *Runtime) CompactHookOwnerLive() bool {
	r.mu.Lock()
	request := r.compactHookRequest
	held := r.compactHookInFlight
	r.mu.Unlock()
	return held && request != nil && request.Err() == nil
}

// SetCompactHookInFlight sets the compaction hook flag.
func (r *Runtime) SetCompactHookInFlight(v bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.compactHookInFlight = v
}

// CompactionBackoff is the current compaction trigger suspension, or nil.
func (r *Runtime) CompactionBackoff() *CompactionBackoff {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.compactionBackoff
}

// SetCompactionBackoff records or clears the compaction trigger suspension.
func (r *Runtime) SetCompactionBackoff(b *CompactionBackoff) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.compactionBackoff = b
}

// ResolveSessionModel resolves the session model under Pi's request-auth rule (resolveCandidate):
// the level-1 request runs on the session model, with no model setting and no fallback, so it
// has exactly one candidate and never a second request. It shows no notice: a failure is
// reported by the caller.
func (r *Runtime) ResolveSessionModel(ctx context.Context, rc ResolveCtx) ResolveResult {
	if rc.Model == nil {
		return ResolveResult{Reason: "no session model"}
	}
	if rc.Models == nil {
		return ResolveResult{Reason: "no model registry"}
	}
	return r.resolveCandidate(ctx, rc, rc.Model)
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

// CachedThinPlan is the thinning plan when it was derived for key (the session and its
// latest compaction entry, hooks.OnContext): the plan is computed once per compaction and
// reused for every request until the next one, so the request bytes depend only on the
// compaction, never on settings or tools that change between two compactions.
func (r *Runtime) CachedThinPlan(key string) (map[string]string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if key == "" || key != r.thinKey {
		return nil, false
	}
	return r.thinPlan, true
}

// SetThinPlan caches plan as the thinning plan for key, replacing the previous one.
func (r *Runtime) SetThinPlan(key string, plan map[string]string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.thinKey, r.thinPlan = key, plan
}
