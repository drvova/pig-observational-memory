package agents

import (
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
)

// The level-1 system prompt (docs/memory-interface.md, section 8; docs/memory-v2.md, "Level
// 1"): the kernel's fixed rules (kernel.Text), which open the prompt and carry every rule the
// code relies on, then the consolidation instructions, a seed carrying the guidance a model may
// restyle. The model's own instructions definition extends the seed (ComposeStage).

// The instructions names of memory v1's worker stages. Nothing runs them; the layer
// definitions v1 wrote under these names are read only by the migration
// (layer.Store.MigrateToV2), which folds their additions into the consolidation instructions.
const (
	InstructionsObserver  = "observer"
	InstructionsReflector = "reflector"
	InstructionsDropper   = "dropper"
)

// InstructionsConsolidation names memory v2's level-1 instructions (docs/memory-v2.md,
// "Self-built harness"): the seed ConsolidationInstructions, which a model may extend or
// replace, always after the kernel's level-1 rules (kernel.TextConsolidateRules).
const InstructionsConsolidation = "consolidation"

// ConsolidationInstructions are the level-1 request's seed instructions: guidance a model may
// restyle, never a rule code relies on (those are the kernel's, which open the prompt).
const ConsolidationInstructions = `What is worth doing:
- Label a candidate when its subject or kind is not what code guessed; reuse a listed subject before making a new one.
- Merge records that say one thing twice; keep records apart when they differ in any specific.
- Write a reflection only for a conclusion several records support that will still matter after this compaction.
- Settle a possible update only when the conversation shows which value stands.
- Cite a curated memory only when a shown record of the user's own words bears it out.
- Doing nothing is a valid answer: a record left alone stays exactly as code extracted it.
`

// The seed instructions ship to the layer, so an instructions definition records the digest of
// the seed it overrides and goes stale when an upgrade changes that seed.
func init() {
	layer.Ship(layer.KindInstructions, InstructionsConsolidation, ConsolidationInstructions)
}

// Compose is a system prompt: the kernel text textID names, then instructions. The
// kernel text always comes first and whole; instructions can only add after it.
func Compose(textID, instructions string) string {
	out := strings.TrimRight(kernel.Text(textID), "\n")
	if instructions = strings.TrimSpace(instructions); instructions != "" {
		out += "\n\n" + instructions
	}
	return out + "\n"
}

// AdditionsHeading opens a model's own instructions appended after the seed instructions (an
// instructions definition in layer.ModeExtend).
const AdditionsHeading = "Additional instructions for this model:"

// ComposeStage is a stage's system prompt (docs/memory-interface.md, section 8): the
// kernel text textID names, always first and whole, then the stage's instructions. With no own
// instructions they are the seed; an addition (the default) follows the seed under
// AdditionsHeading, so the kernel rules, the seed and the model's additions appear in that
// order; a replacement (replace, layer.ModeReplace) stands in the seed's place, never the
// kernel's.
func ComposeStage(textID, seed, own string, replace bool) string {
	own = strings.TrimSpace(own)
	switch {
	case own == "":
		return Compose(textID, seed)
	case replace:
		return Compose(textID, own)
	}
	return Compose(textID, strings.TrimRight(seed, "\n")+"\n\n"+AdditionsHeading+"\n"+own)
}
