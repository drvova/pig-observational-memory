package serialize

import (
	"regexp"
	"strings"
)

// skillBlock is PiG's expanded skill invocation (internal/codingagent/skills.go
// skillBlockRe): the skill's body, then the user's own words after a blank line.
var skillBlock = regexp.MustCompile(`^<skill name="[^"]+" location="[^"]+">\n[\s\S]*?\n</skill>(?:\n\n([\s\S]+))?$`)

// markupTags are the wrappers a harness puts around text it injects into a user message
// (after pi-vcc's filter-noise.ts; level 0's input filter and memory v2's history read user text through it): never the user's words. "file" is PiG's @file payload
// (codingagent.ProcessCLIFileArguments: <file name="/abs/path">content</file>, image notes and
// read failures included): a file's lines are not the user's statements.
var markupTags = []string{"file", "system-reminder", "ide_opened_file", "command-message", "command-name", "command-args", "local-command-stdout", "context-window-usage"}

var markupWrappers = func() []*regexp.Regexp {
	out := make([]*regexp.Regexp, 0, len(markupTags))
	for _, tag := range markupTags {
		out = append(out, regexp.MustCompile(`<`+tag+`(?:\s[^>]*)?>[\s\S]*?</`+tag+`>`))
	}
	return out
}()

// WithoutMarkup is a user message's text without harness markup: an expanded skill keeps only
// the words the user typed after it, and injected wrappers are removed.
func WithoutMarkup(text string) string {
	if m := skillBlock.FindStringSubmatch(strings.TrimSpace(text)); m != nil {
		text = m[1]
	}
	for _, wrapper := range markupWrappers {
		text = wrapper.ReplaceAllString(text, "")
	}
	return strings.TrimSpace(text)
}
