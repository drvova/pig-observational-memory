// Package serialize renders session entries as the memory workers and the
// recall tool read them.
package serialize

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/jsfmt"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/session"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/tokens"
)

// Now is the clock; tests replace it.
var Now = time.Now

// Location is the local time zone rendered timestamps use; tests replace it.
var Location = time.Local

const unknownTimestamp = "????-??-?? ??:??"

func fmtLocal(t time.Time) string {
	return t.In(Location).Format("2006-01-02 15:04")
}

// ParseTimestamp reads a Pi timestamp: epoch milliseconds (a JSON number) or a
// date string as JavaScript's Date constructor accepts the common forms.
func ParseTimestamp(v any) (time.Time, bool) {
	switch value := v.(type) {
	case nil:
		return time.Time{}, false
	case float64:
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return time.Time{}, false
		}
		return time.UnixMilli(int64(value)), true
	case int:
		return time.UnixMilli(int64(value)), true
	case int64:
		return time.UnixMilli(value), true
	case json.Number:
		f, err := value.Float64()
		if err != nil {
			return time.Time{}, false
		}
		return time.UnixMilli(int64(f)), true
	case string:
		return parseDateString(value)
	}
	return time.Time{}, false
}

func parseDateString(s string) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}, false
	}
	utcLayouts := []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z0700", "2006-01-02T15:04:05Z0700"}
	for _, layout := range utcLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	// Date-only ISO forms are UTC in JavaScript.
	for _, layout := range []string{"2006-01-02", "2006-01", "2006"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	// Date-time forms without an offset are local time in JavaScript.
	localLayouts := []string{"2006-01-02T15:04:05.000", "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02 15:04:05", "2006-01-02 15:04"}
	for _, layout := range localLayouts {
		if t, err := time.ParseInLocation(layout, s, Location); err == nil {
			return t, true
		}
	}
	if t, err := time.Parse(time.RFC1123Z, s); err == nil {
		return t, true
	}
	if t, err := time.Parse(time.RFC1123, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}

// FormatTimestamp renders a timestamp as "YYYY-MM-DD HH:MM" local time, or
// "????-??-?? ??:??" when it is absent or unreadable.
func FormatTimestamp(v any) string {
	if v == nil {
		return unknownTimestamp
	}
	t, ok := ParseTimestamp(v)
	if !ok {
		return unknownTimestamp
	}
	return fmtLocal(t)
}

// FormatRecallTimestamp renders the first readable timestamp, else "Unknown time".
func FormatRecallTimestamp(values ...any) string {
	for _, v := range values {
		if v == nil {
			continue
		}
		if t, ok := ParseTimestamp(v); ok {
			return fmtLocal(t)
		}
	}
	return "Unknown time"
}

// NowTimestamp is the current local time as "YYYY-MM-DD HH:MM".
func NowTimestamp() string { return fmtLocal(Now()) }

type placeholderOptions struct {
	omitRedactedThinking bool
	includeThinking      bool
}

const nonText = "[non-text content omitted]"

// JSONStringify is JSON.stringify for a decoded value (object keys sorted).
func JSONStringify(v any) string {
	if v == nil {
		return "{}"
	}
	// JSON.stringify does not HTML-escape <, > and &; encoding/json does by
	// default, which would inflate shell commands in the observer chunk. Key
	// order is sorted (the arguments arrive as a decoded map), where
	// JSON.stringify keeps insertion order.
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(v); err != nil {
		return "{}"
	}
	return strings.TrimSuffix(buffer.String(), "\n")
}

func textAndPlaceholders(content any, options placeholderOptions) string {
	if text, ok := content.(string); ok {
		return text
	}
	blocks, ok := content.([]any)
	if !ok {
		return nonText
	}
	parts := []string{}
	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok || block == nil {
			parts = append(parts, nonText)
			continue
		}
		switch block["type"] {
		case "text":
			if text, ok := block["text"].(string); ok {
				parts = append(parts, text)
				continue
			}
		case "thinking":
			// Thinking is rendered only where the caller asked for it; left
			// out, it leaves no placeholder behind.
			if !options.includeThinking || (options.omitRedactedThinking && block["redacted"] == true) {
				continue
			}
			if thinking, ok := block["thinking"].(string); ok {
				parts = append(parts, "[thinking: "+thinking+"]")
				continue
			}
			parts = append(parts, nonText)
			continue
		case "toolCall":
			if name, ok := block["name"].(string); ok {
				args := block["arguments"]
				if args == nil {
					args = map[string]any{}
				}
				parts = append(parts, "["+name+"("+JSONStringify(args)+")]")
				continue
			}
		}
		parts = append(parts, nonText)
	}
	return strings.Join(parts, "\n")
}

func textOnly(content any) string {
	switch c := content.(type) {
	case nil:
		return ""
	case string:
		return c
	case []any:
		parts := []string{}
		for _, raw := range c {
			block, _ := raw.(map[string]any)
			if block == nil || block["type"] != "text" {
				continue
			}
			if text, ok := block["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// bashExecutionText renders a `!command` run the way the agent saw it: the
// command, its output, and a note when it was cancelled or truncated. Such
// messages carry command/output fields and no content, so the generic
// tool-result rendering would show them as empty.
func bashExecutionText(msg map[string]any) string {
	command, _ := msg["command"].(string)
	output, _ := msg["output"].(string)
	text := "$ " + command
	if strings.TrimSpace(output) != "" {
		text += "\n" + output
	}
	notes := []string{}
	switch code := msg["exitCode"].(type) {
	case float64:
		if code != 0 {
			notes = append(notes, "exit code "+strconv.Itoa(int(code)))
		}
	case int:
		if code != 0 {
			notes = append(notes, "exit code "+strconv.Itoa(code))
		}
	}
	if cancelled, _ := msg["cancelled"].(bool); cancelled {
		notes = append(notes, "cancelled")
	}
	if truncated, _ := msg["truncated"].(bool); truncated {
		notes = append(notes, "output truncated")
	}
	if len(notes) > 0 {
		text += "\n(" + strings.Join(notes, "; ") + ")"
	}
	return text
}

func nonEmptyLines(text string) string {
	kept := []string{}
	for _, line := range strings.Split(text, "\n") {
		if line != "" {
			kept = append(kept, line)
		}
	}
	return strings.Join(kept, "\n")
}

func jsString(v any) string {
	switch value := v.(type) {
	case nil:
		return "undefined"
	case string:
		return value
	}
	return JSONStringify(v)
}

// SerializeConversation renders Pi messages for the observer: user text, the
// assistant's text and tool calls, and tool results. Thinking blocks are left
// out: they are the model's private reasoning, and feeding them to a prompt
// that condenses the conversation has drawn provider refusals under
// anti-distillation terms (upstream issue #97). Recall keeps them, since it
// shows evidence to the same session rather than to a worker.
func SerializeConversation(messages []map[string]any) string {
	blocks := []string{}
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		timestamp := FormatTimestamp(msg["timestamp"])
		switch msg["role"] {
		case "user":
			blocks = append(blocks, "[User @ "+timestamp+"]: "+textOnly(msg["content"]))
		case "assistant":
			body := nonEmptyLines(textAndPlaceholders(msg["content"], placeholderOptions{}))
			if body == "" {
				continue
			}
			blocks = append(blocks, "[Assistant @ "+timestamp+"]: "+body)
		case "bashExecution":
			blocks = append(blocks, "[Bash @ "+timestamp+"]: "+bashExecutionText(msg))
		default:
			blocks = append(blocks, "[Tool result for "+jsString(msg["toolName"])+" @ "+timestamp+"]: "+textOnly(msg["content"]))
		}
	}
	return strings.Join(blocks, "\n\n")
}

// MaxRecordContentChars bounds a recorded observation or reflection.
const MaxRecordContentChars = 10_000

// TruncateRecordContent keeps the first MaxRecordContentChars characters.
func TruncateRecordContent(content string) string {
	length := jsfmt.UTF16Len(content)
	if length <= MaxRecordContentChars {
		return content
	}
	head := jsfmt.Head(content, MaxRecordContentChars)
	return head + " … [truncated " + itoa(length-MaxRecordContentChars) + " chars]"
}

// itoa is the plain decimal the TypeScript version interpolates: the
// truncation note is stored record content and feeds the record's id.
func itoa(n int) string { return strconv.Itoa(n) }

func renderCustomMessage(entry session.Entry, recallFormat bool) string {
	var timestamp string
	if recallFormat {
		timestamp = FormatRecallTimestamp(entryTimestamp(entry))
	} else {
		timestamp = FormatTimestamp(entryTimestamp(entry))
	}
	var text string
	if recallFormat {
		text = textAndPlaceholders(entry.Content, placeholderOptions{})
	} else {
		switch content := entry.Content.(type) {
		case string:
			text = content
		case []any:
			parts := []string{}
			for _, raw := range content {
				block, _ := raw.(map[string]any)
				if block == nil || block["type"] != "text" {
					continue
				}
				if t, ok := block["text"].(string); ok {
					parts = append(parts, t)
				}
			}
			text = strings.Join(parts, "\n")
		}
	}
	if recallFormat {
		origin := "Custom message"
		if entry.CustomType != "" {
			origin = "Custom message (" + entry.CustomType + ")"
		}
		return "[" + origin + " @ " + timestamp + "]: " + text
	}
	tag := "Custom"
	if entry.CustomType != "" {
		tag = "Custom (" + entry.CustomType + ")"
	}
	return "[" + tag + " @ " + timestamp + "]: " + text
}

func entryTimestamp(entry session.Entry) any {
	if entry.Timestamp == "" {
		return nil
	}
	return entry.Timestamp
}

// SerializeBranchEntries renders message, custom message and branch summary
// entries, separated by blank lines.
func SerializeBranchEntries(entries []session.Entry) string {
	blocks := []string{}
	for _, entry := range entries {
		switch entry.Type {
		case "message":
			if entry.Message != nil {
				if part := SerializeConversation([]map[string]any{entry.Message}); part != "" {
					blocks = append(blocks, part)
				}
			}
		case "custom_message":
			blocks = append(blocks, renderCustomMessage(entry, false))
		case "branch_summary":
			if summary, ok := entry.Summary.(string); ok {
				blocks = append(blocks, "[Branch summary @ "+FormatTimestamp(entryTimestamp(entry))+"]: "+summary)
			}
		}
	}
	return strings.Join(blocks, "\n\n")
}

// SourceAddressedSerialization is an observer chunk: labelled source blocks and
// the ids they cover.
type SourceAddressedSerialization struct {
	Text                    string
	SourceEntryIDs          []string
	EstimatedTokens         int
	TruncatedSourceEntryIDs []string
}

// SourceAddressedOptions bounds a serialization; MaxTokens 0 means unbounded.
type SourceAddressedOptions struct {
	MaxTokens int
}

const sourceOmissionMarker = "\n\n[… middle omitted: source exceeds observer input budget; original source remains in the session ledger …]\n\n"

func truncateSourceBlockToTokenBudget(label, rendered string, maxTokens int) (string, bool) {
	required := label + "\n" + sourceOmissionMarker
	if tokens.EstimateStringTokens(required) > maxTokens {
		return "", false
	}
	full := label + "\n" + rendered
	if tokens.EstimateStringTokens(full) <= maxTokens {
		return full, true
	}
	maxChars := maxTokens * 4
	if maxChars < 1 {
		maxChars = 1
	}
	retainedChars := maxChars - jsfmt.UTF16Len(required)
	headChars := int(math.Ceil(float64(retainedChars) / 2))
	tailChars := retainedChars - headChars
	tail := ""
	if tailChars > 0 {
		tail = jsfmt.Tail(rendered, tailChars)
	}
	return label + "\n" + jsfmt.Head(rendered, headChars) + sourceOmissionMarker + tail, true
}

func isSourceRenderableEntry(entry session.Entry) bool {
	return entry.Type == "message" || entry.Type == "custom_message" || entry.Type == "branch_summary"
}

// ObserverRenders reports whether the observer chunk would carry anything for
// entry: a source entry with renderable content. Entries that render nothing
// (an aborted assistant turn with no content) are skipped by the chunk
// serializer and therefore never need observation coverage.
func ObserverRenders(entry session.Entry) bool {
	if entry.ID == "" || !isSourceRenderableEntry(entry) {
		return false
	}
	return strings.TrimSpace(SerializeBranchEntries([]session.Entry{entry})) != ""
}

// SerializeSourceAddressedBranchEntries serializes complete source entries up
// to the token budget. If the first entry alone exceeds the budget, a clearly
// marked head/tail excerpt is included so one pathological tool result cannot
// permanently block observation coverage. The ledger entry itself is never
// modified and remains recallable by id.
func SerializeSourceAddressedBranchEntries(entries []session.Entry, options SourceAddressedOptions) SourceAddressedSerialization {
	blocks := []string{}
	sourceEntryIDs := []string{}
	truncated := []string{}
	estimated := 0
	for _, entry := range entries {
		if entry.ID == "" || !isSourceRenderableEntry(entry) {
			continue
		}
		rendered := SerializeBranchEntries([]session.Entry{entry})
		if strings.TrimSpace(rendered) == "" {
			continue
		}
		label := "[Source entry id: " + entry.ID + "]"
		block := label + "\n" + rendered
		separator := ""
		if len(blocks) > 0 {
			separator = "\n\n"
		}
		blockTokens := tokens.EstimateStringTokens(separator + block)
		if options.MaxTokens > 0 && estimated+blockTokens > options.MaxTokens {
			if len(blocks) > 0 {
				break
			}
			excerpt, ok := truncateSourceBlockToTokenBudget(label, rendered, options.MaxTokens)
			if !ok {
				break
			}
			blocks = append(blocks, excerpt)
			sourceEntryIDs = append(sourceEntryIDs, entry.ID)
			truncated = append(truncated, entry.ID)
			estimated = tokens.EstimateStringTokens(excerpt)
			break
		}
		blocks = append(blocks, block)
		sourceEntryIDs = append(sourceEntryIDs, entry.ID)
		estimated += blockTokens
	}
	text := strings.Join(blocks, "\n\n")
	return SourceAddressedSerialization{
		Text:                    text,
		SourceEntryIDs:          sourceEntryIDs,
		EstimatedTokens:         tokens.EstimateStringTokens(text),
		TruncatedSourceEntryIDs: truncated,
	}
}

func renderRecallMessage(entry session.Entry) (string, bool) {
	msg := entry.Message
	if msg == nil {
		return "", false
	}
	timestamp := FormatRecallTimestamp(msg["timestamp"], entryTimestamp(entry))
	switch msg["role"] {
	case "user":
		return "[User @ " + timestamp + "]: " + textAndPlaceholders(msg["content"], placeholderOptions{}), true
	case "assistant":
		body := nonEmptyLines(textAndPlaceholders(msg["content"], placeholderOptions{includeThinking: true, omitRedactedThinking: true}))
		if body == "" {
			return "", false
		}
		return "[Assistant @ " + timestamp + "]: " + body, true
	case "bashExecution":
		return "[Bash @ " + timestamp + "]: " + bashExecutionText(msg), true
	}
	return "[Tool result: " + jsString(msg["toolName"]) + " @ " + timestamp + "]: " + textAndPlaceholders(msg["content"], placeholderOptions{}), true
}

// RenderRecallSourceEntry renders one source entry for the recall tool.
func RenderRecallSourceEntry(entry session.Entry) (string, bool) {
	switch entry.Type {
	case "message":
		return renderRecallMessage(entry)
	case "custom_message":
		return renderCustomMessage(entry, true), true
	case "branch_summary":
		if summary, ok := entry.Summary.(string); ok {
			return "[Branch summary @ " + FormatRecallTimestamp(entryTimestamp(entry)) + "]: " + summary, true
		}
	}
	return "", false
}

// RenderRecallSourceEntries renders source entries separated by blank lines.
func RenderRecallSourceEntries(entries []session.Entry) string {
	blocks := []string{}
	for _, entry := range entries {
		rendered, ok := RenderRecallSourceEntry(entry)
		if !ok || strings.TrimSpace(rendered) == "" {
			continue
		}
		blocks = append(blocks, rendered)
	}
	return strings.Join(blocks, "\n\n")
}
