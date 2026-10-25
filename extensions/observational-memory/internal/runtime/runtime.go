// Package runtime holds the extension's per-process state and resolves the
// memory-worker model.
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

// Phase names a consolidation stage.
type Phase string

// The consolidation stages.
const (
	PhaseObserver  Phase = "observer"
	PhaseReflector Phase = "reflector"
	PhaseDropper   Phase = "dropper"
	// PhaseHarness is the build or revision pass of the model's harness layer.
	PhaseHarness Phase = "harness pass"
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
	// TokensAtEmpty is the raw uncovered backlog when the verdict came; the
	// provider delta is not used here because a compaction rebases it.
	TokensAtEmpty int
	// ChunkEndID is the last source entry of the judged chunk. A branch that
	// no longer carries it (a tree jump) has content the observer never read,
	// so the backoff does not apply there.
	ChunkEndID string
}

// ReflectorEmptyBackoff records a reflector run that recorded nothing over a
// given memory input, so an identical prompt is not sent again every turn.
type ReflectorEmptyBackoff struct {
	SessionIdentity string
	// InputKey identifies the memory the reflector read (active observations
	// and reflections); it changes whenever that memory changes.
	InputKey string
	// PrimaryOwed marks the verdict of a fallback retry after the primary call
	// failed. The primary has not judged this memory, so the next pass gives it
	// one try, without the fallback retry; the backoff holds after that.
	PrimaryOwed bool
}

// CompactionBackoff records a proactive compaction the host refused or failed
// ("Nothing to compact", "Already compacted", a summarizer error), so the
// trigger does not ask again on every settle while nothing has changed.
type CompactionBackoff struct {
	SessionIdentity string
	// BoundaryID is the newest compaction entry at the refusal, "" before the first.
	BoundaryID string
	// Progress is the counted source tokens at the refusal; the trigger asks
	// again once another threshold's worth has arrived.
	Progress int
	// Reason is the host's error message.
	Reason string
	// TipID is the branch tip at a memory v2 refusal ("Nothing to compact"):
	// v2's auto-compaction-off trigger stays suspended until the tip changes.
	// "" for a v1 backoff.
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

	consolidationInFlight bool
	consolidationDone     chan struct{}
	consolidationPhase    Phase
	consolidationCancel   context.CancelFunc
	consolidationCtx      context.Context
	// abortedDone is the completion channel of the last run AbortConsolidation cancelled.
	abortedDone chan struct{}
	// resettled is the completion channel of the last aborted run a settle
	// claimed to settle again after (ClaimResettle).
	resettled <-chan struct{}
	// idleEnds counts events that end an idle window (an idle-mode
	// agent_start, a session end), so a settle can tell whether the time after
	// a run it launched or waited on is still idle.
	idleEnds uint64
	// afterHookOwed is set when an idle settle skipped its after-run check
	// (proactive compaction) because its idle window ended; the next idle
	// settle runs it before launching another run.
	afterHookOwed bool

	compactInFlight        bool
	compactHookInFlight    bool
	resolveFailureNotified bool

	lastObserverError  string
	lastReflectorError string
	lastDropperError   string

	availabilityRecheckedAt map[string]time.Time
	observerEmptyBackoff    *ObserverEmptyBackoff
	reflectorEmptyBackoff   *ReflectorEmptyBackoff
	compactionBackoff       *CompactionBackoff
	// thinKey and thinPlan are memory v2's thinning plan and the session and compaction it was
	// derived from (CachedThinPlan).
	thinKey  string
	thinPlan map[string]string
	// resolveNotices holds the resolution warnings already shown; a clean
	// resolution clears it, so a recurring condition warns once per episode.
	resolveNotices map[string]bool
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
// compaction mid-session, and it decides which token domain the proactive
// trigger counts, so a startup snapshot would leave a session with no
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

// TryStartCompact claims the proactive compaction slot; false when a
// compaction is already in flight. Checking and claiming in one step keeps two
// callers released together (two settles waiting on one run) from both
// compacting.
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

// CompactionBackoff is the current proactive-compaction backoff, or nil.
func (r *Runtime) CompactionBackoff() *CompactionBackoff {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.compactionBackoff
}

// SetCompactionBackoff records or clears the proactive-compaction backoff.
func (r *Runtime) SetCompactionBackoff(b *CompactionBackoff) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.compactionBackoff = b
}

// ReflectorEmptyBackoff is the current reflector backoff, or nil.
func (r *Runtime) ReflectorEmptyBackoff() *ReflectorEmptyBackoff {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reflectorEmptyBackoff
}

// SetReflectorEmptyBackoff records or clears the reflector backoff.
func (r *Runtime) SetReflectorEmptyBackoff(b *ReflectorEmptyBackoff) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.reflectorEmptyBackoff = b
}

// noticeOnce shows a resolution warning unless it was already shown in the
// current episode.
func (r *Runtime) noticeOnce(rc ResolveCtx, message, level string) {
	r.mu.Lock()
	if r.resolveNotices == nil {
		r.resolveNotices = map[string]bool{}
	}
	seen := r.resolveNotices[message]
	r.resolveNotices[message] = true
	r.mu.Unlock()
	if !seen {
		rc.notify(message, level)
	}
}

func (r *Runtime) clearNotices() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.resolveNotices = nil
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
//
// The "configured model not found" and "using fallback" warnings are shown
// once per episode: a resolution that needs neither clears them, so the user
// is told again only after the condition went away and came back.
func (r *Runtime) ResolveModel(ctx context.Context, rc ResolveCtx) ResolveResult {
	primary, notFound := r.resolvePrimaryModel(ctx, rc)
	if notFound != "" {
		r.noticeOnce(rc, notFound, "warning")
	}
	if primary.OK {
		if notFound == "" {
			r.clearNotices()
		}
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
	r.noticeOnce(rc, fmt.Sprintf("Observational memory: primary model unavailable (%s); using fallback %s/%s", primary.Reason, provider, id), "warning")
	debuglog.Log(ctx, "resolve.fallback_used", map[string]any{"provider": provider, "id": id, "primaryFailure": primary.Reason})
	fallback.FallbackUsed = true
	fallback.PrimaryFailure = primary.Reason
	return fallback
}

// config.model when it resolves in Pi's registry, otherwise the session
// model. notFound is the warning to show when config.model was not found.
func (r *Runtime) resolvePrimaryModel(ctx context.Context, rc ResolveCtx) (result ResolveResult, notFound string) {
	cfg := r.Config()
	model := rc.Model
	if cfg.Model != nil {
		provider := EffectiveProvider(cfg.Model, rc.Model)
		if configured := rc.Models.Find(provider, cfg.Model.ID); configured != nil {
			model = configured
		} else {
			notFound = fmt.Sprintf("Observational memory: configured model %s not found, using session model", modelLabel(provider, cfg.Model.ID))
		}
	}
	if model == nil {
		return ResolveResult{Reason: "no model available (session has no model and no observational-memory model configured)"}, notFound
	}
	return r.resolveCandidate(ctx, rc, model), notFound
}

// ResolveSessionModel resolves the session model alone, with the same auth rule as every other
// path (resolveCandidate): memory v2's level-1 request runs on the session model, with no worker
// model setting, and never on fallbackModel (a v1 setting v2 ignores), so it has exactly one
// candidate and never a second request. It shows no notice: a failure is reported by the caller.
func (r *Runtime) ResolveSessionModel(ctx context.Context, rc ResolveCtx) ResolveResult {
	if rc.Model == nil {
		return ResolveResult{Reason: "no session model"}
	}
	if rc.Models == nil {
		return ResolveResult{Reason: "no model registry"}
	}
	return r.resolveCandidate(ctx, rc, rc.Model)
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
// It launches nothing and returns nil while a run is in flight: checking and
// claiming the slot under one lock keeps two concurrent launchers from
// starting two runs, one of which nothing could abort.
func (r *Runtime) LaunchConsolidationTask(ui host.UI, work func(ctx context.Context) error) <-chan struct{} {
	r.mu.Lock()
	if r.consolidationInFlight {
		r.mu.Unlock()
		return nil
	}
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
				safeNotify(ui, fmt.Sprintf("Observational memory: consolidation failed: %v", recovered), "warning")
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
		if err := work(ctx); err != nil {
			safeNotify(ui, "Observational memory: consolidation failed: "+err.Error(), "warning")
		}
	}()
	return done
}

// safeNotify shows a notification and never panics, even when the host's
// context has gone stale (it is called from recover blocks).
func safeNotify(ui host.UI, message, level string) {
	if ui == nil {
		return
	}
	defer func() { _ = recover() }()
	if ui.HasUI() {
		ui.Notify(message, level)
	}
}

// RunAborted reports whether the run whose completion channel is done was
// cancelled by AbortConsolidation.
func (r *Runtime) RunAborted(done <-chan struct{}) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return done != nil && r.abortedDone != nil && (<-chan struct{})(r.abortedDone) == done
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

// NoteIdleEnd records an event that ends an idle window: an idle-mode
// agent_start or a session end.
func (r *Runtime) NoteIdleEnd() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.idleEnds++
}

// IdleEnds is how many events NoteIdleEnd recorded.
func (r *Runtime) IdleEnds() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.idleEnds
}

// SetAfterHookOwed records or clears an idle settle's skipped after-run check.
func (r *Runtime) SetAfterHookOwed(v bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.afterHookOwed = v
}

// ClaimResettle reports whether the caller is the first to settle again after
// the aborted run whose completion channel is done; every settle that adopted
// that run wakes when it ends, and one settle covers them all.
func (r *Runtime) ClaimResettle(done <-chan struct{}) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if done == nil || r.resettled == done {
		return false
	}
	r.resettled = done
	return true
}

// TakeAfterHookOwed reports and clears an owed after-run check.
func (r *Runtime) TakeAfterHookOwed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	owed := r.afterHookOwed
	r.afterHookOwed = false
	return owed
}

// AbortConsolidation cancels the in-flight consolidation run, if any.
func (r *Runtime) AbortConsolidation() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.consolidationCancel == nil || !r.consolidationInFlight {
		return false
	}
	r.abortedDone = r.consolidationDone
	r.consolidationCancel()
	return true
}

// RecordConsolidationStageError records a stage failure and notifies the user.
func (r *Runtime) RecordConsolidationStageError(ui host.UI, phase Phase, err error) string {
	message := err.Error()
	r.SetLastError(phase, message)
	safeNotify(ui, fmt.Sprintf("Observational memory: %s failed: %s", phase, message), "warning")
	return message
}

// CachedThinPlan is memory v2's thinning plan when it was derived for key (the session and its
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
