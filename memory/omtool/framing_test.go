package omtool_test

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/omtool"
)

// docs/brain-memory.md, decision 18: the Go and the Lisp tool text, and the curated index's
// header, frame remembered items as unverified once each, in the same words.
func TestToolTextFramesMemoryAsUnverifiedOnce(t *testing.T) {
	for name, text := range map[string]string{
		"go":     strings.Join(omtool.PromptGuidelines, "\n"),
		"lisp":   strings.Join(omtool.LispPromptGuidelines, "\n"),
		"header": curated.IndexHeader,
	} {
		if n := strings.Count(text, ledger.UnverifiedNotice); n != 1 {
			t.Errorf("%s: the framing appears %d times", name, n)
		}
		if strings.Contains(strings.ToLower(text), "are you sure") {
			t.Errorf("%s asks for a re-check", name)
		}
	}
	if !strings.HasPrefix(omtool.UnverifiedGuideline, "Use memory") {
		t.Errorf("the guideline does not name the tool: %q", omtool.UnverifiedGuideline)
	}
}
