package omtool_test

import (
	"strconv"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/om"
	"github.com/drvova/pig-observational-memory/memory/omtool"
)

// Both tools' fixed texts say that a change to shared memory needs the user's quoted words
// (docs/brain-memory.md, decision 2), name the quoted forms through placeholders, and give the
// gate's real bound.
func TestGuidelinesNameTheQuoteGate(t *testing.T) {
	bound := "at most " + strconv.Itoa(curated.MaxForgetsPerQuote) + " changes"
	for _, tool := range []struct {
		guidelines []string
		forms      []string
	}{
		{omtool.PromptGuidelines, []string{om.SnippetEditQuoted, om.SnippetForgetQuoted}},
		{omtool.LispPromptGuidelines, []string{omtool.LispSnippetEditQuoted, omtool.LispSnippetForgetQuoted}},
	} {
		text := strings.Join(tool.guidelines, "\n")
		for _, want := range append(tool.forms, bound, "roll back your layer", "/om:status") {
			if !strings.Contains(text, want) {
				t.Errorf("the guidelines lack %q", want)
			}
		}
	}
	if !om.IsPlaceholder(om.PlaceholderUserWords) || !strings.Contains(om.SnippetEditQuoted, om.PlaceholderUserWords) {
		t.Fatal("the quoted forms hold no placeholder")
	}
}
