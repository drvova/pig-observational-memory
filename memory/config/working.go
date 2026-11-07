package config

// Working memory's caps, the raw floor and the quality trigger (docs/memory-v2.md, "Working
// memory" and "The raw floor and the quality trigger"; docs/brain-memory.md, decisions 15 and
// 16). The caps apply always; the floor and the trigger ship off until the engine's own
// effective-length measurements show a gain for a model. Each limit is set globally and per
// model under modelLimits, keyed "<provider>/<id>" as PiG's compaction.modelOverrides are.

// The defaults: about 10 constraints, 10 scope exclusions and the last 5 actions; a raw floor of
// 32,000 tokens and a quality threshold of 64,000 raw tokens for a model with no measurement.
const (
	DefaultWorkingMemoryConstraints     = 10
	DefaultWorkingMemoryScopeExclusions = 10
	DefaultWorkingMemoryActions         = 5
	DefaultRawFloorTokens               = 32_000
	DefaultQualityTriggerTokens         = 64_000
)

// ModelLimits are the per-model limits; 0 is unset (the next source's value, else the default).
type ModelLimits struct {
	Constraints, ScopeExclusions, Actions int
	RawFloorTokens, QualityTriggerTokens  int
}

// limitKeys are the settings each limit is read from, globally and inside a modelLimits entry.
var limitKeys = []string{
	"workingMemoryConstraints", "workingMemoryScopeExclusions", "workingMemoryActions",
	"rawFloorTokens", "qualityTriggerTokens",
}

func (m *ModelLimits) field(key string) *int {
	switch key {
	case "workingMemoryConstraints":
		return &m.Constraints
	case "workingMemoryScopeExclusions":
		return &m.ScopeExclusions
	case "workingMemoryActions":
		return &m.Actions
	case "rawFloorTokens":
		return &m.RawFloorTokens
	case "qualityTriggerTokens":
		return &m.QualityTriggerTokens
	}
	return nil
}

// readLimits reads the limit keys of one settings object.
func readLimits(value map[string]any) (ModelLimits, bool) {
	var out ModelLimits
	set := false
	for _, key := range limitKeys {
		if n, ok := positiveInteger(value[key]); ok {
			*out.field(key) = n
			set = true
		}
	}
	return out, set
}

// over lays o's set limits over m.
func (m ModelLimits) over(o ModelLimits) ModelLimits {
	for _, key := range limitKeys {
		if n := *o.field(key); n > 0 {
			*m.field(key) = n
		}
	}
	return m
}

// LimitsFor are the session model's limits: its modelLimits entry, else the global setting,
// else the default, field by field.
func (c Config) LimitsFor(provider, id string) ModelLimits {
	out := ModelLimits{
		Constraints:          DefaultWorkingMemoryConstraints,
		ScopeExclusions:      DefaultWorkingMemoryScopeExclusions,
		Actions:              DefaultWorkingMemoryActions,
		RawFloorTokens:       DefaultRawFloorTokens,
		QualityTriggerTokens: DefaultQualityTriggerTokens,
	}.over(c.Limits)
	if provider != "" || id != "" {
		out = out.over(c.ModelLimits[provider+"/"+id])
	}
	return out
}
