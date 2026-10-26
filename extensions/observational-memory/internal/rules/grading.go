package rules

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/layer"
)

// Grading stays with the kernel (docs/memory-interface.md, section 8): a rule is judged only by
// a kernel count, so the model never grades its own change.

// KernelKeys are the counts a rule may be judged by: the v2 detectors and the codec decode
// failures (CountCodecDecodeFailure), whose counting is kernel code.
func KernelKeys() []string {
	return append(append([]string{}, V2Detectors...), CountCodecDecodeFailure)
}

// JudgedKind reports whether memory v1's probation judged a written definition of kind named
// name: the definitions that changed what an observer pass did (format, schema, the observer's
// instructions, and a remedy catalogue). Memory v2 judges no written definition; the memory
// tool's harness listing still shows a v1-written one whose probation v1 left undecided. A defined tool
// (section 6) and a rule (section 7) have probations of their own; a detector changes nothing a
// pass does, it only counts; a summary changes no worker pass (the compaction that renders it
// is its proof); and the reflector's and dropper's instructions change no observer pass, so no
// kernel count could judge them: their proof is the recorded pass of their own stage that
// writes them (a held one that only ever runs empty passes is reverted).
func JudgedKind(kind, name string) bool {
	switch kind {
	case layer.KindFormat, layer.KindSchema:
		return true
	case layer.KindInstructions:
		return name == StageObserver
	case layer.KindRemedy:
		return containsString(Remedies, name)
	}
	return false
}

// eventTime is when e was recorded; false for an unreadable time.
func eventTime(e curated.ModelEvent) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, e.Time)
	return t, err == nil
}

// Evidence is a model's counted evidence as a level-1 offer shows it (V2EvidenceOf): the events
// each kernel check counted over its last MinEvidence compactions, and each Go detector it
// defined counted in those compactions' detector passes, with the source tokens level 0 read,
// and its rules with their states.
type Evidence struct {
	Model          string
	Passes, Tokens int
	Counts         map[string]int
	Rules          []string
	// Events is the length of the model's usage ledger it was read at.
	Events int
}

// Digest is what an offer compares (ProposalDue): which counts are non-zero and the rules'
// states, not the counts themselves, so an offer answers a change in what fails rather than
// every compaction's numbers.
func (e Evidence) Digest() string {
	keys := []string{}
	for k, n := range e.Counts {
		if n > 0 {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, ",") + "\x1f" + strings.Join(e.Rules, "\x1f")))
	return hex.EncodeToString(sum[:])[:16]
}

// Text is the evidence as the level-1 input carries it.
func (e Evidence) Text() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Counted over your last %d compactions (%d source tokens level 0 read):\n", e.Passes, e.Tokens)
	keys := make([]string, 0, len(e.Counts))
	for k := range e.Counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		b.WriteString("- no detector counted anything\n")
	}
	for _, k := range keys {
		fmt.Fprintf(&b, "- %s: %d\n", k, e.Counts[k])
	}
	if len(e.Rules) == 0 {
		b.WriteString("Rules applied to you: none\n")
	} else {
		b.WriteString("Rules applied to you:\n- " + strings.Join(e.Rules, "\n- ") + "\n")
	}
	return b.String()
}
