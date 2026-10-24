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

// ConfiguredModel names a memory-worker model. An empty Provider means the
// session model's provider (upstream issue #31): workers then follow model
// switches within that provider without a restart.
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

// The memoryFormat values: how the compaction summary lays out the memory.
// They match ledger.FormatValues.
const (
	MemoryFormatTables = "tables"
	MemoryFormatLines  = "lines"
)

// MemoryFormatValues are the valid memoryFormat values; the first is the
// default.
var MemoryFormatValues = []string{MemoryFormatTables, MemoryFormatLines}

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
	// CompactionSummaryMaxTokens caps the rendered memory summary. 0 leaves
	// the budget to the room the next context has (ResolveSummaryBudget).
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
	// CuratedMemory gives the agent its own long-lived memory: the memory
	// tool's saved source and writes, and an evidence-ranked index at the head
	// of each conversation.
	CuratedMemory bool
	// CuratedMemoryIndexTokens bounds the index's memory lines.
	CuratedMemoryIndexTokens int
	// MemoryFormat is the compaction summary's layout, one of
	// MemoryFormatValues (ledger.RenderOptions.Format).
	MemoryFormat string
	// KernelFloor is the user's request to lower the kernel floor
	// (docs/memory-interface.md, section 8), nil when the floor stays engaged.
	// Configuration is the only way to lower it: the model cannot.
	KernelFloor *FloorRequest
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
	CuratedMemory:                true,
	CuratedMemoryIndexTokens:     2_000,
	MemoryFormat:                 MemoryFormatTables,
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

// CompactionSummaryFallbackMaxTokens is the summary budget when the window is
// unknown, and the most the floor raises a known window's budget to.
const CompactionSummaryFallbackMaxTokens = 8_000

// ResolveSummaryBudget is the rendered summary budget: the room the next
// context has once Pi's cut lands,
//
//	room  = contextWindow - overheadTokens - max(retainedTokens, compactAfterTokens)
//	free  = contextWindow - overheadTokens - retainedTokens
//	floor = min(CompactionSummaryFallbackMaxTokens, free/2)
//	budget = max(room, floor, 1), capped by CompactionSummaryMaxTokens
//
// where overheadTokens is what the request carries besides the branch (system
// prompt, tools), retainedTokens the source Pi keeps after the cut and
// compactAfterTokens the proactive threshold. The threshold measures source
// from the cut's first kept entry, so the retained tail is already part of it
// and is not subtracted twice. With an unknown window there is no room to
// measure, so the budget is the cap when set and
// CompactionSummaryFallbackMaxTokens otherwise.
//
// The floor covers windows the threshold does not fit (the default 81k on a
// 64k model): there the proactive trigger never fires and Pi's own threshold
// governs the next compaction, so room would be negative for space the
// context can never reach. The summary then takes the fallback budget, but
// never more than half of what the window has left after the retained tail,
// so the conversation keeps at least as much room as the summary. The budget
// is never below 1: the renderer reads a non-positive budget as unlimited, and
// a budget at or below its fixed header renders no record, which the
// compaction hook answers by declining to Pi's native summarizer.
func ResolveSummaryBudget(cfg Config, contextWindow, overheadTokens, retainedTokens int) int {
	if contextWindow <= 0 {
		if cfg.CompactionSummaryMaxTokens > 0 {
			return cfg.CompactionSummaryMaxTokens
		}
		return CompactionSummaryFallbackMaxTokens
	}
	retained := max(0, retainedTokens)
	free := contextWindow - max(0, overheadTokens) - retained
	room := free + retained - max(retained, ResolveCompactAfterTokens(cfg, contextWindow))
	budget := max(1, room, min(CompactionSummaryFallbackMaxTokens, free/2))
	if cfg.CompactionSummaryMaxTokens > 0 {
		budget = min(budget, cfg.CompactionSummaryMaxTokens)
	}
	return budget
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
	// provider is optional: an id alone names a model on the session's
	// provider. id is required.
	provider, _ := m["provider"].(string)
	id, _ := m["id"].(string)
	if id == "" {
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
	format   *string
	ratio    *float64
	bools    map[string]bool
	model    *ConfiguredModel
	fallback *ConfiguredModel
	// floor is kernelFloor's request when floorSet; nil with floorSet is an
	// explicit lower: false, which overrides a lower-priority source's request.
	floor    *FloorRequest
	floorSet bool
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
	"curatedMemoryIndexTokens",
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
	if format, ok := isMemoryFormat(value["memoryFormat"]); ok {
		p.format = &format
	}
	for _, key := range []string{"consolidateWhenIdle", "showWorkerNotifications", "passive", "debugLog", "curatedMemory"} {
		if b, ok := value[key].(bool); ok {
			p.bools[key] = b
		}
	}
	p.model = normalizeModel(value["model"])
	p.fallback = normalizeModel(value["fallbackModel"])
	p.floor, p.floorSet = normalizeFloor(value["kernelFloor"])
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
		case "curatedMemoryIndexTokens":
			cfg.CuratedMemoryIndexTokens = n
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
	if p.format != nil {
		cfg.MemoryFormat = *p.format
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
		case "curatedMemory":
			cfg.CuratedMemory = b
		}
	}
	if p.model != nil {
		cfg.Model = p.model
	}
	if p.fallback != nil {
		cfg.FallbackModel = p.fallback
	}
	if p.floorSet {
		cfg.KernelFloor = p.floor
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

// ReadPiAutoCompaction reads Pi's own compaction.enabled the way Load does:
// the project setting wins over the global one. ok is false when neither file
// sets it.
func ReadPiAutoCompaction(cwd string, paths Paths) (enabled bool, ok bool) {
	if enabled, ok := readPiCompactionEnabled(filepath.Join(cwd, paths.ProjectDirName, "settings.json")); ok {
		return enabled, true
	}
	return readPiCompactionEnabled(filepath.Join(paths.AgentDir, "settings.json"))
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
	project := readNamespaced(projectPath)
	if project.floor != nil {
		// A project's settings travel with its repository, so they can keep the
		// kernel floor engaged but never lower it: only the user's own settings do.
		project.floor, project.floorSet = nil, false
	}
	project.apply(&cfg, &target, &targetSet)
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
