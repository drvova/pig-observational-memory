package serialize

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// The kinds of minimal brief (MinimalBrief).
const (
	// BriefFull is the opening request and one line per tool call.
	BriefFull = "brief"
	// BriefPointer is the opening request line and an omitted pointer to the whole range.
	BriefPointer = "pointer"
)

// BriefHeading opens a full minimal brief.
const BriefHeading = "Brief of the compacted conversation: its opening request and one line per tool call. The memory tool searches the full text (history | search \"words\")."

// BriefKeyArgRunes bounds a tool call's key argument on its brief line, as a clipped
// observation's content is bounded.
const BriefKeyArgRunes = 80

// briefKeyArgs are the arguments that name what a tool call acted on, most telling first.
var briefKeyArgs = []string{"command", "path", "file_path", "filePath", "pattern", "query", "url", "name"}

// BriefSmallest is the smallest minimal brief, the last resort when no other form fits.
const BriefSmallest = "omitted."

// MinimalBrief renders the cut range of a compaction that no memory record fits
// (docs/memory-v2.md, "Owning every compaction PiG prepares"): the opening user request, then
// one line per tool call (`* tool "key arg" (entry id)`), thinking, text and tool bodies
// dropped, and calls to the tools named in skipTools (the memory tools) left out. entries are
// the range's source entries (ledger.IsSourceEntry), so the pointer counts and names the
// conversation removed, not bookkeeping entries.
//
// The result is at most limit tokens, where limit is the smaller of maxTokens and
// ceilingTokens that is above 0 (the caller passes what the range itself carries as
// ceilingTokens, so the brief is never larger than what it replaces). The forms, largest
// first: the full brief (only when maxTokens > 0); the opening request line, clipped to fit,
// and an omitted pointer to the whole range; the pointer alone; a short pointer with the count
// and the first and last ids; the count alone; and BriefSmallest, which is returned even when
// it exceeds the limit (a range of a token or two), so the result is never empty. With
// maxTokens <= 0 the full brief is skipped and the request is clipped to BriefKeyArgRunes at
// most; with no limit at all, that first pointer form is the result.
func MinimalBrief(entries []session.Entry, skipTools []string, maxTokens, ceilingTokens int) (text, kind string) {
	limit := ceilingTokens
	if maxTokens > 0 && (limit <= 0 || maxTokens < limit) {
		limit = maxTokens
	}
	fits := func(text string) bool { return limit <= 0 || tokens.EstimateStringTokens(text) <= limit }
	opening, openingID := openingRequest(entries)
	if maxTokens > 0 {
		lines := []string{BriefHeading}
		if opening != "" {
			lines = append(lines, requestLine(opening, openingID))
		}
		lines = append(lines, toolCallLines(entries, skipTools)...)
		if full := strings.Join(lines, "\n"); fits(full) {
			return full, BriefFull
		}
	}
	pointer := omittedPointer(entries)
	if opening != "" {
		// The largest clip of the request whose line and the pointer fit the limit.
		clip := BriefKeyArgRunes
		if maxTokens > 0 {
			clip = len([]rune(oneLine(opening)))
		}
		for clip > 0 && !fits(requestLine(clipRunes(opening, clip), openingID)+"\n"+pointer) {
			clip = clip * 3 / 4
		}
		if clip > 0 {
			return requestLine(clipRunes(opening, clip), openingID) + "\n" + pointer, BriefPointer
		}
	}
	for _, form := range []string{pointer, shortPointer(entries), countPointer(entries)} {
		if fits(form) {
			return form, BriefPointer
		}
	}
	return BriefSmallest, BriefPointer
}

// openingRequest is the text of the first user message in entries, and its entry id.
func openingRequest(entries []session.Entry) (string, string) {
	for _, entry := range entries {
		if entry.Type != "message" || entry.Message == nil || entry.Message["role"] != "user" {
			continue
		}
		if text := strings.TrimSpace(textOnly(entry.Message["content"])); text != "" {
			return text, entry.ID
		}
	}
	return "", ""
}

func requestLine(text, id string) string {
	line := "user: " + oneLine(text)
	if id != "" {
		line += " (" + id + ")"
	}
	return line
}

// toolCallLines are one line per tool call in the assistant messages of entries.
func toolCallLines(entries []session.Entry, skipTools []string) []string {
	lines := []string{}
	for _, entry := range entries {
		if entry.Type != "message" || entry.Message == nil || entry.Message["role"] != "assistant" {
			continue
		}
		blocks, _ := entry.Message["content"].([]any)
		for _, raw := range blocks {
			block, _ := raw.(map[string]any)
			if block == nil || block["type"] != "toolCall" {
				continue
			}
			name, _ := block["name"].(string)
			if name == "" || slices.Contains(skipTools, name) {
				continue
			}
			line := "* " + name
			if arg := keyArg(block["arguments"]); arg != "" {
				line += fmt.Sprintf(" %q", clipRunes(arg, BriefKeyArgRunes))
			}
			if entry.ID != "" {
				line += " (" + entry.ID + ")"
			}
			lines = append(lines, line)
		}
	}
	return lines
}

// keyArg is the argument that names what a call acted on: the first of briefKeyArgs present,
// else the first string argument by name; "" when there is none.
func keyArg(arguments any) string {
	args, _ := arguments.(map[string]any)
	if args == nil {
		return ""
	}
	for _, key := range briefKeyArgs {
		if s, ok := args[key].(string); ok && strings.TrimSpace(s) != "" {
			return oneLine(s)
		}
	}
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if s, ok := args[key].(string); ok && strings.TrimSpace(s) != "" {
			return oneLine(s)
		}
	}
	return ""
}

// omittedPointer points at the whole range: its entry count, dates and first and last ids.
func omittedPointer(entries []session.Entry) string {
	if len(entries) == 0 {
		return "omitted: nothing; the compacted range held no entries."
	}
	first, last := entries[0], entries[len(entries)-1]
	pointer := "omitted: " + entryCount(len(entries))
	if from, to := entryDate(first), entryDate(last); from != "" && to != "" {
		pointer += " from " + from + " to " + to
	}
	if first.ID != "" && last.ID != "" {
		pointer += " (" + first.ID + " to " + last.ID + ")"
	}
	return pointer + `; the memory tool searches them (history | search "words").`
}

// shortPointer is the omitted pointer without dates or the memory query: the count and the
// first and last ids.
func shortPointer(entries []session.Entry) string {
	if len(entries) == 0 {
		return "omitted: nothing."
	}
	pointer := "omitted: " + entryCount(len(entries))
	if first, last := entries[0].ID, entries[len(entries)-1].ID; first != "" && last != "" {
		pointer += " (" + first + " to " + last + ")"
	}
	return pointer + "."
}

// countPointer is the omitted pointer reduced to the entry count.
func countPointer(entries []session.Entry) string {
	if len(entries) == 0 {
		return "omitted: nothing."
	}
	return "omitted: " + entryCount(len(entries)) + "."
}

func entryCount(n int) string {
	if n == 1 {
		return "1 entry"
	}
	return fmt.Sprintf("%d entries", n)
}

func entryDate(entry session.Entry) string {
	if len(entry.Timestamp) >= len("2006-01-02") {
		return entry.Timestamp[:len("2006-01-02")]
	}
	return ""
}

func oneLine(text string) string { return strings.Join(strings.Fields(text), " ") }

// clipRunes is text on one line, cut to n runes with an ellipsis when longer.
func clipRunes(text string, n int) string {
	line := []rune(oneLine(text))
	if len(line) <= n {
		return string(line)
	}
	if n <= 1 {
		return "…"
	}
	return string(line[:n-1]) + "…"
}
