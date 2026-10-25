// Package config loads the observational-memory settings.
package config

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// The memoryFormat values: how the compaction summary lays out the memory.
// They match ledger.FormatValues.
const (
	MemoryFormatTables = "tables"
	MemoryFormatLines  = "lines"
)

// MemoryFormatValues are the valid memoryFormat values; the first is the
// default.
var MemoryFormatValues = []string{MemoryFormatTables, MemoryFormatLines}

// EngineV2 tags what memory v2 writes (docs/memory-v2.md): its compactions' details
// (ledger.MemoryDetails.Engine), its usage-ledger events and its held definitions
// (curated.EngineV2), so what memory v1 wrote, untagged, stays apart. v2 is the only engine;
// the memoryEngine setting that chose between them is removed (RemovedSettings).
const EngineV2 = "v2"

// Config is the resolved extension configuration.
type Config struct {
	// CompactionSummaryMaxTokens is an extra cap on the summary budget
	// (ResolveV2SummaryBudget); 0 when unset.
	CompactionSummaryMaxTokens int
	// Passive turns off turn-end extraction, level 1, thinning and the
	// compaction trigger.
	Passive bool
	// DebugLog writes the debug log (internal/debuglog).
	DebugLog bool
	// PiAutoCompactionEnabled is Pi's own compaction.enabled setting.
	PiAutoCompactionEnabled bool
	// PiCompaction is PiG's own compaction.reserveTokens with its
	// compaction.modelOverrides, read under the same trust (ReadPiCompactionTokens).
	PiCompaction PiCompactionTokens
	// CuratedMemory gives the agent its own long-lived memory: the memory
	// tool's saved source and writes, and an evidence-ranked index at the head
	// of each conversation.
	CuratedMemory bool
	// CuratedMemoryIndexTokens bounds the index's memory lines.
	CuratedMemoryIndexTokens int
	// MemoryFormat is the compaction summary's layout, one of
	// MemoryFormatValues (ledger.FormatValues).
	MemoryFormat string
	// KernelFloor is the user's request to lower the kernel floor
	// (docs/memory-interface.md, section 8), nil when the floor stays engaged.
	// Configuration is the only way to lower it: the model cannot.
	KernelFloor *FloorRequest
	// ProjectTrust is the host's trust answer the configuration was loaded
	// under; only a trusted project's settings are read.
	ProjectTrust TrustState
	// ProjectSettingsIgnored is true when the project has a settings file
	// that was not read because the project is not trusted.
	ProjectSettingsIgnored bool
	// ConsolidateOff is set by consolidate: false, which turns the level-1
	// consolidation request off. Read it through Consolidates.
	ConsolidateOff bool
	// RemovedSet are the RemovedSettings a settings file that was read sets to
	// a valid value, in RemovedSettings order: nothing reads them and /om:status
	// lists them while they are set (docs/memory-v2.md, "Migration > Settings").
	RemovedSet []string
	// RemovedWorkerModel is the removed `model` setting's value while a settings
	// file that was read still sets it: memory v1's memory-worker model, whose
	// layer held every harness definition but a summary, read only to migrate
	// that layer (harness.MigrateLayerV2); nil when unset.
	RemovedWorkerModel *WorkerModelRef
}

// WorkerModelRef names memory v1's memory-worker model as the removed `model`
// setting did: an id, on Provider or, when it is empty, the session model's.
type WorkerModelRef struct {
	Provider, ID string
}

// RemovedSettings are the settings memory v2 removed (docs/memory-v2.md, "Migration >
// Settings"): v1's worker scheduling, worker models, pools, moved cut, catch-up and proactive
// trigger, and memoryEngine, which chose between v1 and v2 until v1 was deleted. They are
// ignored; a settings file that still sets one to a value v1 applied is listed in /om:status.
var RemovedSettings = []string{
	"observeAfterTokens",
	"reflectAfterTokens",
	"observerChunkMaxTokens",
	"compactionCatchUpMaxChunks",
	"consolidateWhenIdle",
	"model",
	"fallbackModel",
	"observationsPoolTargetTokens",
	"observationsPoolMaxTokens",
	"compactionMaxRetainedTokens",
	"compactAfterTokens",
	"compactAfterTokensMode",
	"compactAfterTokensRatio",
	"workerMemoryMaxTokens",
	"agentMaxTurns",
	"agentMaxTokens",
	"showWorkerNotifications",
	"memoryEngine",
}

// Consolidates reports whether the extension makes its level-1 consolidation
// request at a compaction: consolidate, true unless set to false.
func (c Config) Consolidates() bool { return !c.ConsolidateOff }

// TrustState is the host's answer to whether the current project is trusted.
// Project settings are read only when it is ProjectTrusted; every other value,
// the zero value included, means global settings alone, as PiG does.
type TrustState string

// The trust states.
const (
	// ProjectTrusted: the host trusts the project.
	ProjectTrusted TrustState = "trusted"
	// ProjectUntrusted: the host does not trust the project.
	ProjectUntrusted TrustState = "untrusted"
	// ProjectTrustUnknown: the trust check failed, so the project is treated
	// as untrusted (fail closed).
	ProjectTrustUnknown TrustState = "unknown"
)

// TrustFrom reads a host trust check (Context.IsProjectTrusted): an error
// means the trust is unknown, never trusted.
func TrustFrom(trusted bool, err error) TrustState {
	switch {
	case err != nil:
		return ProjectTrustUnknown
	case trusted:
		return ProjectTrusted
	}
	return ProjectUntrusted
}

// FloorRequest is the configured request to lower the kernel floor: who asked
// and why. Both are recorded before the lowering takes effect, and a request
// missing either is refused (kernel.Floor.Lower).
type FloorRequest struct {
	RequestedBy string `json:"requestedBy"`
	Reason      string `json:"reason"`
}

// Defaults are the built-in settings.
var Defaults = Config{
	Passive:                  false,
	DebugLog:                 false,
	PiAutoCompactionEnabled:  true,
	CuratedMemory:            true,
	CuratedMemoryIndexTokens: 2_000,
	MemoryFormat:             MemoryFormatTables,
}

// CompactionSummaryFallbackMaxTokens is the summary size the harness profile never halves
// below (harness.Profile.ApplySummary): memory v1's summary budget for an unknown window.
const CompactionSummaryFallbackMaxTokens = 8_000

// V2SummaryBudgetInputs are what the memory v2 summary budget is derived from
// (docs/memory-v2.md, "The summary budget"). Every value comes from PiG or the
// branch; 0 means unknown, except for RemovedTokens and NextRunHeadroom.
type V2SummaryBudgetInputs struct {
	// ContextWindow is the session model's context window.
	ContextWindow int
	// ReserveTokens is PiG's compaction.reserveTokens from the preparation's
	// settings (model overrides applied by PiG). 0 or less is unknown, and
	// PiGDefaultReserveTokens stands in for it.
	ReserveTokens int
	// TokensBefore is PiG's estimate of the context before the compaction.
	// Without a usage anchor PiG v0.5.1 counts the current system message (the
	// prompt, its sections and the tool schemas) in it
	// (compaction.EstimateProjectedContextTokens), so no overhead is added here.
	TokensBefore int
	// RemovedTokens is the estimate of what the compaction removes: the cut
	// range plus the previous summary.
	RemovedTokens int
	// MaxOutputTokens is the session model's output limit.
	MaxOutputTokens int
	// NextRunHeadroom is the largest per-run growth of the context since the
	// previous compaction (ledger.NextRunHeadroom); 0 without usage.
	NextRunHeadroom int
}

// PiGDefaultReserveTokens is PiG's default compaction.reserveTokens (PiG v0.5.1,
// compaction.DefaultCompactionSettings). ResolveV2SummaryBudget uses it when the
// preparation carried no reserve: assuming no reserve would raise the threshold
// bound by the whole reserve and let the next context land above PiG's
// threshold, so an unknown reserve fails closed to PiG's default. A configured
// reserve of 0 is read as unknown too, which only makes the budget smaller.
const PiGDefaultReserveTokens = 16_384

// PiGContextSafetyTokens is PiG's context safety margin (PiG v0.5.1, ai/simple_options.go
// contextSafetyTokens, which ClampMaxTokensToContext keeps free on every request). Memory v2's
// level-1 request is made only when est(prompt + input) + output cap + this margin fits the
// window, so PiG never has to clamp its output below the cap.
const PiGContextSafetyTokens = 4096

// ResolveV2SummaryBudget is the memory v2 summary budget:
//
//	threshold = contextWindow - reserveTokens - tokensBefore + removed - nextRunHeadroom
//	            (when the window is known; reserveTokens is PiGDefaultReserveTokens when unknown)
//	budget    = min(threshold, maxOutputTokens when known, removed)
//	budget    = min(budget, compactionSummaryMaxTokens when set)
//
// The threshold bound keeps the next context, and one more run like the
// largest since the previous compaction, under PiG's own compaction
// threshold; the headroom belongs to that bound alone. The output bound keeps
// the summary no larger than one summary output of PiG's, and the removed
// bound keeps it no larger than what it replaces. A result of 0 or less means
// the minimal brief.
func ResolveV2SummaryBudget(cfg Config, in V2SummaryBudgetInputs) int {
	removed := max(0, in.RemovedTokens)
	budget := removed
	if in.ContextWindow > 0 {
		reserve := in.ReserveTokens
		if reserve <= 0 {
			reserve = PiGDefaultReserveTokens
		}
		budget = min(budget, in.ContextWindow-reserve-max(0, in.TokensBefore)+removed-max(0, in.NextRunHeadroom))
	}
	if in.MaxOutputTokens > 0 {
		budget = min(budget, in.MaxOutputTokens)
	}
	if cfg.CompactionSummaryMaxTokens > 0 {
		budget = min(budget, cfg.CompactionSummaryMaxTokens)
	}
	return budget
}

// SettingsKey is the settings.json namespace.
const SettingsKey = "observational-memory"

// PassiveEnv is the environment variable that forces passive mode.
const PassiveEnv = "PI_OBSERVATIONAL_MEMORY_PASSIVE"

func positiveInteger(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f != math.Trunc(f) || f <= 0 {
		return 0, false
	}
	return int(f), true
}

func isMemoryFormat(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	for _, format := range MemoryFormatValues {
		if format == s {
			return format, true
		}
	}
	return "", false
}

// isOneOf reports whether v is a string among values.
func isOneOf(v any, values ...string) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}
	for _, value := range values {
		if value == s {
			return true
		}
	}
	return false
}

// removedValueValid reports whether a removed setting's value is one memory v1 applied (an
// invalid one was ignored by v1 as well, so it is not listed as set).
func removedValueValid(key string, v any) bool {
	switch key {
	case "compactionCatchUpMaxChunks":
		f, ok := v.(float64)
		return ok && !math.IsNaN(f) && !math.IsInf(f, 0) && f == math.Trunc(f) && f >= 0
	case "compactAfterTokensMode":
		return isOneOf(v, "calibrated", "ratio")
	case "compactAfterTokensRatio":
		f, ok := v.(float64)
		return ok && !math.IsNaN(f) && !math.IsInf(f, 0) && f > 0 && f < 1
	case "model", "fallbackModel":
		// provider is optional: an id alone named a model on the session's provider.
		m, ok := v.(map[string]any)
		id, _ := m["id"].(string)
		return ok && id != ""
	case "memoryEngine":
		return isOneOf(v, "v1", "v2")
	case "consolidateWhenIdle", "showWorkerNotifications":
		_, ok := v.(bool)
		return ok
	}
	_, ok := positiveInteger(v)
	return ok
}

// partial is one settings source's valid overrides.
type partial struct {
	ints   map[string]int
	format *string
	bools  map[string]bool
	// floor is kernelFloor's request when floorSet; nil with floorSet is an
	// explicit lower: false, which overrides a lower-priority source's request.
	floor    *FloorRequest
	floorSet bool
	// removed are the RemovedSettings the source sets to a valid value.
	removed []string
	// worker is the removed `model` setting's value when it is one of them.
	worker *WorkerModelRef
}

var numberKeys = []string{
	"compactionSummaryMaxTokens",
	"curatedMemoryIndexTokens",
}

func normalizeSettings(value map[string]any) partial {
	p := partial{ints: map[string]int{}, bools: map[string]bool{}}
	for _, key := range numberKeys {
		if n, ok := positiveInteger(value[key]); ok {
			p.ints[key] = n
		}
	}
	if format, ok := isMemoryFormat(value["memoryFormat"]); ok {
		p.format = &format
	}
	for _, key := range []string{"passive", "debugLog", "curatedMemory", "consolidate"} {
		if b, ok := value[key].(bool); ok {
			p.bools[key] = b
		}
	}
	p.floor, p.floorSet = normalizeFloor(value["kernelFloor"])
	for _, key := range RemovedSettings {
		if raw, present := value[key]; present && removedValueValid(key, raw) {
			p.removed = append(p.removed, key)
			if key == "model" {
				m := raw.(map[string]any)
				ref := WorkerModelRef{}
				ref.Provider, _ = m["provider"].(string)
				ref.ID, _ = m["id"].(string)
				p.worker = &ref
			}
		}
	}
	return p
}

// normalizeFloor reads kernelFloor: {"lower": true, "requestedBy": "...",
// "reason": "..."}. A request with lower true is kept even without who or why,
// so lowering refuses it and says so; lower false is an explicit engage.
func normalizeFloor(v any) (*FloorRequest, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return nil, false
	}
	lower, ok := m["lower"].(bool)
	if !ok {
		return nil, false
	}
	if !lower {
		return nil, true
	}
	requestedBy, _ := m["requestedBy"].(string)
	reason, _ := m["reason"].(string)
	return &FloorRequest{RequestedBy: requestedBy, Reason: reason}, true
}

func (p partial) apply(cfg *Config) {
	for key, n := range p.ints {
		switch key {
		case "compactionSummaryMaxTokens":
			cfg.CompactionSummaryMaxTokens = n
		case "curatedMemoryIndexTokens":
			cfg.CuratedMemoryIndexTokens = n
		}
	}
	if p.format != nil {
		cfg.MemoryFormat = *p.format
	}
	for key, b := range p.bools {
		switch key {
		case "passive":
			cfg.Passive = b
		case "debugLog":
			cfg.DebugLog = b
		case "curatedMemory":
			cfg.CuratedMemory = b
		case "consolidate":
			cfg.ConsolidateOff = !b
		}
	}
	if p.floorSet {
		cfg.KernelFloor = p.floor
	}
	if p.worker != nil {
		cfg.RemovedWorkerModel = p.worker
	}
	if len(p.removed) > 0 {
		set := map[string]bool{}
		for _, key := range append(append([]string{}, cfg.RemovedSet...), p.removed...) {
			set[key] = true
		}
		cfg.RemovedSet = nil
		for _, key := range RemovedSettings {
			if set[key] {
				cfg.RemovedSet = append(cfg.RemovedSet, key)
			}
		}
	}
}

// ReadEnvPassive reads PI_OBSERVATIONAL_MEMORY_PASSIVE: true for 1/true/yes/on,
// false for 0/false/no/off, unset otherwise.
func ReadEnvPassive(lookup func(string) (string, bool)) (passive bool, set bool) {
	raw, ok := lookup(PassiveEnv)
	if !ok {
		return false, false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	return false, false
}

func readJSONObject(path string) (map[string]any, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		return nil, false
	}
	return raw, true
}

// Pi's compaction.enabled from one settings file, if present and boolean.
func readPiCompactionEnabled(path string) (bool, bool) {
	raw, ok := readJSONObject(path)
	if !ok {
		return false, false
	}
	compaction, ok := raw["compaction"].(map[string]any)
	if !ok {
		return false, false
	}
	enabled, ok := compaction["enabled"].(bool)
	return enabled, ok
}

func readNamespaced(path string) partial {
	raw, ok := readJSONObject(path)
	if !ok {
		return partial{ints: map[string]int{}, bools: map[string]bool{}}
	}
	nested, ok := raw[SettingsKey].(map[string]any)
	if !ok {
		return partial{ints: map[string]int{}, bools: map[string]bool{}}
	}
	return normalizeSettings(nested)
}

// Paths are where settings are read from.
type Paths struct {
	// AgentDir is Pi's agent directory (global settings.json lives there).
	AgentDir string
	// ProjectDirName is the per-project configuration directory (".pi" or ".pig").
	ProjectDirName string
}

// DefaultPaths asks the PiG host for its directories, falling back to Pi's
// conventions (PI_CODING_AGENT_DIR or ~/.pi/agent, and .pi).
func DefaultPaths() Paths {
	paths := Paths{ProjectDirName: ".pi"}
	if dir, err := sdk.AgentDir(); err == nil && dir != "" {
		paths.AgentDir = dir
	} else if env := os.Getenv("PI_CODING_AGENT_DIR"); env != "" {
		paths.AgentDir = env
	} else if home, err := os.UserHomeDir(); err == nil {
		paths.AgentDir = filepath.Join(home, ".pi", "agent")
	}
	if name, err := sdk.ConfigDirName(); err == nil && name != "" {
		paths.ProjectDirName = name
	}
	return paths
}

// PiCompactionTokens is PiG's compaction.reserveTokens as its settings files set it: the
// ordinary value and the compaction.modelOverrides entries ("provider/modelId") that take
// precedence for one model. The zero value sets nothing, and PiG's default applies.
type PiCompactionTokens struct {
	// ReserveTokens is the ordinary compaction.reserveTokens, nil when no file sets it.
	ReserveTokens *int
	// ReserveOverrides are the modelOverrides reserveTokens values by "provider/modelId".
	ReserveOverrides map[string]int
}

// ReserveTokensFor is PiG's reserveTokens for the model provider/id, as PiG's
// SettingsManager.GetCompactionSettings resolves it: the model's override, else the ordinary
// value, else PiGDefaultReserveTokens. A configured 0 is kept: it is PiG's own value.
func (p PiCompactionTokens) ReserveTokensFor(provider, id string) int {
	if provider != "" || id != "" {
		if n, ok := p.ReserveOverrides[provider+"/"+id]; ok {
			return n
		}
	}
	if p.ReserveTokens != nil {
		return *p.ReserveTokens
	}
	return PiGDefaultReserveTokens
}

// piCompactionToken reads one compaction token setting the way PiG accepts it: a non-negative
// whole number. Anything else is unset here (PiG reports it as an invalid setting).
func piCompactionToken(v any) (int, bool) {
	n, ok := v.(float64)
	if !ok || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 || n != math.Trunc(n) || n > 1<<53-1 {
		return 0, false
	}
	return int(n), true
}

// readPiCompactionTokens lays the compaction token settings of one settings file over into:
// a value the file sets replaces the one below, field by field and override by override, as
// PiG merges its project settings over its global ones.
func readPiCompactionTokens(path string, into *PiCompactionTokens) {
	raw, ok := readJSONObject(path)
	if !ok {
		return
	}
	compaction, ok := raw["compaction"].(map[string]any)
	if !ok {
		return
	}
	if n, ok := piCompactionToken(compaction["reserveTokens"]); ok {
		into.ReserveTokens = &n
	}
	overrides, _ := compaction["modelOverrides"].(map[string]any)
	for key, value := range overrides {
		entry, _ := value.(map[string]any)
		if n, ok := piCompactionToken(entry["reserveTokens"]); ok {
			if into.ReserveOverrides == nil {
				into.ReserveOverrides = map[string]int{}
			}
			into.ReserveOverrides[key] = n
		}
	}
}

// ReadPiCompactionTokens reads PiG's compaction.reserveTokens and its modelOverrides the way
// ReadPiAutoCompaction reads compaction.enabled: the global settings, then the project's over
// them, and the project's only when it is trusted.
func ReadPiCompactionTokens(cwd string, paths Paths, trust TrustState) PiCompactionTokens {
	out := PiCompactionTokens{}
	readPiCompactionTokens(filepath.Join(paths.AgentDir, "settings.json"), &out)
	if trust == ProjectTrusted {
		readPiCompactionTokens(filepath.Join(cwd, paths.ProjectDirName, "settings.json"), &out)
	}
	return out
}

// ReadPiAutoCompaction reads Pi's own compaction.enabled the way Load does:
// the project setting wins over the global one, and is read only when the
// project is trusted. ok is false when no file read sets it.
func ReadPiAutoCompaction(cwd string, paths Paths, trust TrustState) (enabled bool, ok bool) {
	if trust == ProjectTrusted {
		if enabled, ok := readPiCompactionEnabled(filepath.Join(cwd, paths.ProjectDirName, "settings.json")); ok {
			return enabled, true
		}
	}
	return readPiCompactionEnabled(filepath.Join(paths.AgentDir, "settings.json"))
}

// Load reads the global settings, the project settings when trust is
// ProjectTrusted, and the environment. PiG uses the global configuration alone
// until the user trusts a project, so an untrusted project's settings, or
// ones whose trust could not be checked, are never read.
func Load(cwd string, paths Paths, trust TrustState, lookupEnv func(string) (string, bool)) Config {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	globalPath := filepath.Join(paths.AgentDir, "settings.json")
	projectPath := filepath.Join(cwd, paths.ProjectDirName, "settings.json")
	cfg := Defaults
	cfg.ProjectTrust = trust
	readNamespaced(globalPath).apply(&cfg)
	if trust == ProjectTrusted {
		project := readNamespaced(projectPath)
		if project.floor != nil {
			// A project's settings travel with its repository, so they can keep the
			// kernel floor engaged but never lower it: only the user's own settings do.
			project.floor, project.floorSet = nil, false
		}
		project.apply(&cfg)
	} else if info, err := os.Stat(projectPath); err == nil && !info.IsDir() {
		cfg.ProjectSettingsIgnored = true
	}
	if passive, set := ReadEnvPassive(lookupEnv); set {
		cfg.Passive = passive
	}
	if enabled, ok := ReadPiAutoCompaction(cwd, paths, trust); ok {
		cfg.PiAutoCompactionEnabled = enabled
	} else {
		cfg.PiAutoCompactionEnabled = Defaults.PiAutoCompactionEnabled
	}
	cfg.PiCompaction = ReadPiCompactionTokens(cwd, paths, trust)
	return cfg
}
