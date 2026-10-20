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

// ThinkingLevel is a Pi model thinking level.
type ThinkingLevel string

// ThinkingLevelValues are Pi's thinking levels.
var ThinkingLevelValues = []ThinkingLevel{"off", "minimal", "low", "medium", "high", "xhigh", "max"}

// ConfiguredModel names a memory-worker model.
type ConfiguredModel struct {
	Provider string        `json:"provider"`
	ID       string        `json:"id"`
	Thinking ThinkingLevel `json:"thinking,omitempty"`
}

// CompactAfterTokensMode is how compactAfterTokens is interpreted.
//
//   - "calibrated" (default): use the static compactAfterTokens value directly.
//   - "ratio": floor(model.contextWindow * compactAfterTokensRatio), falling
//     back to compactAfterTokens when the window is unknown.
type CompactAfterTokensMode string

// The compactAfterTokens modes.
const (
	ModeCalibrated CompactAfterTokensMode = "calibrated"
	ModeRatio      CompactAfterTokensMode = "ratio"
)

// CompactAfterTokensModeValues are the valid modes.
var CompactAfterTokensModeValues = []CompactAfterTokensMode{ModeCalibrated, ModeRatio}

// Config is the resolved extension configuration. Optional token budgets are 0
// when unset; the resolve* functions derive them from a context window then.
type Config struct {
	ObserveAfterTokens int
	ReflectAfterTokens int
	// ObserverChunkMaxTokens caps the estimated source tokens serialized into
	// one observer chunk. 0 derives the cap from the memory model's window.
	ObserverChunkMaxTokens  int
	CompactAfterTokens      int
	CompactAfterTokensMode  CompactAfterTokensMode
	CompactAfterTokensRatio float64
	// CompactionMaxRetainedTokens caps what the compaction hook keeps in
	// context when it moves Pi's retention boundary back to the observer
	// frontier. 0 derives it from the session model's window.
	CompactionMaxRetainedTokens int
	// CompactionSummaryMaxTokens budgets the rendered memory summary. 0
	// derives it from the session model's window.
	CompactionSummaryMaxTokens int
	// CompactionCatchUpMaxChunks caps the observer chunks the compaction hook
	// runs synchronously before Pi's cut. 0 disables catch-up.
	CompactionCatchUpMaxChunks int
	// WorkerMemoryMaxTokens budgets the prior memory each worker request
	// carries. 0 derives it from the memory model's window.
	WorkerMemoryMaxTokens        int
	ObservationsPoolMaxTokens    int
	ObservationsPoolTargetTokens int
	AgentMaxTurns                int
	// AgentMaxTokens is the output budget of the memory-agent loops, clamped
	// to the model's own maxTokens.
	AgentMaxTokens int
	Model          *ConfiguredModel
	// ConsolidateWhenIdle launches the workers from agent_settled and aborts
	// an in-flight run when a new agent run starts.
	ConsolidateWhenIdle bool
	// FallbackModel is a second memory-worker model, tried when the primary
	// cannot be resolved and once more when a worker stage fails its call.
	FallbackModel           *ConfiguredModel
	ShowWorkerNotifications bool
	Passive                 bool
	DebugLog                bool
	// PiAutoCompactionEnabled is Pi's own compaction.enabled setting.
	PiAutoCompactionEnabled bool
}

// Defaults are the built-in settings.
var Defaults = Config{
	ObserveAfterTokens:           10_000,
	ReflectAfterTokens:           20_000,
	CompactAfterTokens:           81_000,
	CompactAfterTokensMode:       ModeCalibrated,
	CompactAfterTokensRatio:      0.68,
	ObservationsPoolMaxTokens:    20_000,
	ObservationsPoolTargetTokens: 10_000,
	AgentMaxTurns:                16,
	AgentMaxTokens:               32_000,
	CompactionCatchUpMaxChunks:   2,
	ConsolidateWhenIdle:          false,
	ShowWorkerNotifications:      true,
	Passive:                      false,
	DebugLog:                     false,
	PiAutoCompactionEnabled:      true,
}

// ResolveCompactAfterTokens is the effective proactive-compaction threshold.
// In ratio mode it is floor(contextWindow * ratio) (at least 1) when the
// window is known, else compactAfterTokens.
func ResolveCompactAfterTokens(cfg Config, contextWindow int) int {
	if cfg.CompactAfterTokensMode == ModeRatio && contextWindow > 0 {
		return max(1, int(math.Floor(float64(contextWindow)*cfg.CompactAfterTokensRatio)))
	}
	return cfg.CompactAfterTokens
}

// CompactionRetainedFallbackMaxTokens is the retained-tail budget when unset and the window is unknown.
const CompactionRetainedFallbackMaxTokens = 60_000

// CompactionRetainedContextRatio is the window share the hook may keep when retaining unobserved source.
const CompactionRetainedContextRatio = 0.5

// ResolveCompactionMaxRetainedTokens is the retained-tail budget.
func ResolveCompactionMaxRetainedTokens(cfg Config, contextWindow int) int {
	if cfg.CompactionMaxRetainedTokens > 0 {
		return cfg.CompactionMaxRetainedTokens
	}
	if contextWindow > 0 {
		return max(1, int(math.Floor(float64(contextWindow)*CompactionRetainedContextRatio)))
	}
	return CompactionRetainedFallbackMaxTokens
}

// CompactionSummaryFallbackMaxTokens is the summary budget when unset and the window is unknown.
const CompactionSummaryFallbackMaxTokens = 8_000

// CompactionSummaryContextRatio is the window share the rendered summary may occupy by default.
const CompactionSummaryContextRatio = 0.125

// ResolveCompactionSummaryMaxTokens is the rendered summary budget.
func ResolveCompactionSummaryMaxTokens(cfg Config, contextWindow int) int {
	if cfg.CompactionSummaryMaxTokens > 0 {
		return cfg.CompactionSummaryMaxTokens
	}
	if contextWindow > 0 {
		return max(1, int(math.Floor(float64(contextWindow)*CompactionSummaryContextRatio)))
	}
	return CompactionSummaryFallbackMaxTokens
}

// WorkerMemoryFallbackMaxTokens is the worker prior-memory budget when unset and the window is unknown.
const WorkerMemoryFallbackMaxTokens = 16_000

// WorkerMemoryContextRatio is the memory model window share worker prompts may spend on prior memory.
const WorkerMemoryContextRatio = 0.25

// ResolveWorkerMemoryMaxTokens is the prior-memory budget for worker prompts.
func ResolveWorkerMemoryMaxTokens(cfg Config, contextWindow int) int {
	if cfg.WorkerMemoryMaxTokens > 0 {
		return cfg.WorkerMemoryMaxTokens
	}
	if contextWindow > 0 {
		return max(1, int(math.Floor(float64(contextWindow)*WorkerMemoryContextRatio)))
	}
	return WorkerMemoryFallbackMaxTokens
}

// ObserverChunkFallbackMaxTokens is the chunk cap when unset and the window is unknown.
const ObserverChunkFallbackMaxTokens = 60_000

// ObserverChunkMinTokens is the smallest useful observer chunk.
const ObserverChunkMinTokens = 256

// ObserverChunkContextRatio is the memory model window share used for the derived chunk cap.
const ObserverChunkContextRatio = 0.2

// ResolveObserverChunkMaxTokens is the maximum estimated tokens the observer
// serializes into one chunk.
func ResolveObserverChunkMaxTokens(cfg Config, contextWindow int) int {
	if cfg.ObserverChunkMaxTokens > 0 {
		return max(ObserverChunkMinTokens, cfg.ObserverChunkMaxTokens)
	}
	if contextWindow > 0 {
		return max(ObserverChunkMinTokens, int(math.Floor(float64(contextWindow)*ObserverChunkContextRatio)))
	}
	return ObserverChunkFallbackMaxTokens
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

func isThinkingLevel(v any) (ThinkingLevel, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	for _, level := range ThinkingLevelValues {
		if string(level) == s {
			return level, true
		}
	}
	return "", false
}

func isCompactAfterTokensMode(v any) (CompactAfterTokensMode, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	for _, mode := range CompactAfterTokensModeValues {
		if string(mode) == s {
			return mode, true
		}
	}
	return "", false
}

// A valid ratio is a finite number strictly between 0 and 1.
func validRatio(v any) (float64, bool) {
	f, ok := v.(float64)
	if !ok || math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 || f >= 1 {
		return 0, false
	}
	return f, true
}

func normalizeModel(v any) *ConfiguredModel {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	provider, _ := m["provider"].(string)
	id, _ := m["id"].(string)
	if provider == "" || id == "" {
		return nil
	}
	model := &ConfiguredModel{Provider: provider, ID: id}
	if level, ok := isThinkingLevel(m["thinking"]); ok {
		model.Thinking = level
	}
	return model
}

// partial is one settings source's valid overrides.
type partial struct {
	ints     map[string]int
	catchUp  *int
	mode     *CompactAfterTokensMode
	ratio    *float64
	bools    map[string]bool
	model    *ConfiguredModel
	fallback *ConfiguredModel
}

var numberKeys = []string{
	"observeAfterTokens",
	"reflectAfterTokens",
	"observerChunkMaxTokens",
	"compactAfterTokens",
	"compactionMaxRetainedTokens",
	"compactionSummaryMaxTokens",
	"workerMemoryMaxTokens",
	"observationsPoolMaxTokens",
	"observationsPoolTargetTokens",
	"agentMaxTurns",
	"agentMaxTokens",
}

func normalizeSettings(value map[string]any) partial {
	p := partial{ints: map[string]int{}, bools: map[string]bool{}}
	for _, key := range numberKeys {
		if n, ok := positiveInteger(value[key]); ok {
			p.ints[key] = n
		}
	}
	if f, ok := value["compactionCatchUpMaxChunks"].(float64); ok && !math.IsNaN(f) && !math.IsInf(f, 0) && f == math.Trunc(f) && f >= 0 {
		n := int(f)
		p.catchUp = &n
	}
	if mode, ok := isCompactAfterTokensMode(value["compactAfterTokensMode"]); ok {
		p.mode = &mode
	}
	if ratio, ok := validRatio(value["compactAfterTokensRatio"]); ok {
		p.ratio = &ratio
	}
	for _, key := range []string{"consolidateWhenIdle", "showWorkerNotifications", "passive", "debugLog"} {
		if b, ok := value[key].(bool); ok {
			p.bools[key] = b
		}
	}
	p.model = normalizeModel(value["model"])
	p.fallback = normalizeModel(value["fallbackModel"])
	return p
}

func (p partial) apply(cfg *Config, target *int, targetSet *bool) {
	for key, n := range p.ints {
		switch key {
		case "observeAfterTokens":
			cfg.ObserveAfterTokens = n
		case "reflectAfterTokens":
			cfg.ReflectAfterTokens = n
		case "observerChunkMaxTokens":
			cfg.ObserverChunkMaxTokens = n
		case "compactAfterTokens":
			cfg.CompactAfterTokens = n
		case "compactionMaxRetainedTokens":
			cfg.CompactionMaxRetainedTokens = n
		case "compactionSummaryMaxTokens":
			cfg.CompactionSummaryMaxTokens = n
		case "workerMemoryMaxTokens":
			cfg.WorkerMemoryMaxTokens = n
		case "observationsPoolMaxTokens":
			cfg.ObservationsPoolMaxTokens = n
		case "observationsPoolTargetTokens":
			*target = n
			*targetSet = true
		case "agentMaxTurns":
			cfg.AgentMaxTurns = n
		case "agentMaxTokens":
			cfg.AgentMaxTokens = n
		}
	}
	if p.catchUp != nil {
		cfg.CompactionCatchUpMaxChunks = *p.catchUp
	}
	if p.mode != nil {
		cfg.CompactAfterTokensMode = *p.mode
	}
	if p.ratio != nil {
		cfg.CompactAfterTokensRatio = *p.ratio
	}
	for key, b := range p.bools {
		switch key {
		case "consolidateWhenIdle":
			cfg.ConsolidateWhenIdle = b
		case "showWorkerNotifications":
			cfg.ShowWorkerNotifications = b
		case "passive":
			cfg.Passive = b
		case "debugLog":
			cfg.DebugLog = b
		}
	}
	if p.model != nil {
		cfg.Model = p.model
	}
	if p.fallback != nil {
		cfg.FallbackModel = p.fallback
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

// Load reads the global and project settings and the environment.
func Load(cwd string, paths Paths, lookupEnv func(string) (string, bool)) Config {
	if lookupEnv == nil {
		lookupEnv = os.LookupEnv
	}
	globalPath := filepath.Join(paths.AgentDir, "settings.json")
	projectPath := filepath.Join(cwd, paths.ProjectDirName, "settings.json")
	cfg := Defaults
	cfg.ObservationsPoolTargetTokens = 0
	target, targetSet := 0, false
	readNamespaced(globalPath).apply(&cfg, &target, &targetSet)
	readNamespaced(projectPath).apply(&cfg, &target, &targetSet)
	if passive, set := ReadEnvPassive(lookupEnv); set {
		cfg.Passive = passive
	}
	if targetSet && target < cfg.ObservationsPoolMaxTokens {
		cfg.ObservationsPoolTargetTokens = target
	} else {
		cfg.ObservationsPoolTargetTokens = cfg.ObservationsPoolMaxTokens / 2
	}
	if enabled, ok := readPiCompactionEnabled(projectPath); ok {
		cfg.PiAutoCompactionEnabled = enabled
	} else if enabled, ok := readPiCompactionEnabled(globalPath); ok {
		cfg.PiAutoCompactionEnabled = enabled
	} else {
		cfg.PiAutoCompactionEnabled = Defaults.PiAutoCompactionEnabled
	}
	return cfg
}
