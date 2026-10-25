// Package serialize renders session entries as the memory workers and the
// memory tool read them.
package serialize

import (
	"bytes"
	"encoding/json"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/jsfmt"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
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

// bashExcludedFromContext reports whether msg is a `!!command` run. PiG keeps
// such runs out of the agent's context (agent/messages.go customUserMessage),
// so memory keeps them out too: they render nothing for the observer or recall.
func bashExcludedFromContext(msg map[string]any) bool {
	excluded, _ := msg["excludeFromContext"].(bool)
	return excluded
}

// BashExecutionText renders a `!command` run that reached the agent's context:
// the command, its output, and a note when it exited non-zero, was cancelled or
// was truncated. Such messages carry command/output fields and no content, so
// the generic tool-result rendering would show them as empty. Callers skip
// `!!command` runs (bashExcludedFromContext) first.
func BashExecutionText(msg map[string]any) string {
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
// shows evidence to the same session rather than to a worker. Calls to the
// tools named in skipTools (the memory tools) and the results answering them
// are left out too, as HistoryText leaves them out: memory plumbing is not
// conversation to remember, and observing it would feed memory back into
// itself. An assistant message holding nothing else renders nothing.
func SerializeConversation(messages []map[string]any, skipTools ...string) string {
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
			body := nonEmptyLines(textAndPlaceholders(withoutToolCalls(msg["content"], skipTools), placeholderOptions{}))
			if body == "" {
				continue
			}
			blocks = append(blocks, "[Assistant @ "+timestamp+"]: "+body)
		case "bashExecution":
			if bashExcludedFromContext(msg) {
				continue
			}
			blocks = append(blocks, "[Bash @ "+timestamp+"]: "+BashExecutionText(msg))
		default:
			if skippedToolResult(msg, skipTools) {
				continue
			}
			blocks = append(blocks, "[Tool result for "+jsString(msg["toolName"])+" @ "+timestamp+"]: "+textOnly(msg["content"]))
		}
	}
	return strings.Join(blocks, "\n\n")
}

// MaxRecordContentChars bounds a recorded observation or reflection.
const MaxRecordContentChars = 10_000

// RecallPageChars is the most one recall shows (docs/memory-v2.md, "Memory tool"): a record's
// content bound, the largest text recall <id> returns for one record. Under memory v2,
// recall <entry id> pages a longer entry at this size, and a curated memory must fit it whole.
const RecallPageChars = MaxRecordContentChars

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
// entries, separated by blank lines; calls to the tools in skipTools and their
// results are left out (SerializeConversation).
func SerializeBranchEntries(entries []session.Entry, skipTools ...string) string {
	blocks := []string{}
	for _, entry := range entries {
		switch entry.Type {
		case "message":
			if entry.Message != nil {
				if part := SerializeConversation([]map[string]any{entry.Message}, skipTools...); part != "" {
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
	Text string
	// SourceEntryIDs are the labelled entries, the ones the observer may cite.
	SourceEntryIDs  []string
	EstimatedTokens int
	// TruncatedSourceEntryIDs are the labelled entries shown as a head/tail excerpt.
	TruncatedSourceEntryIDs []string
	// EntryIDs are every entry the chunk read, in branch order: the labelled ones
	// and, among them or after the last, those that held only skipped memory-tool
	// content (SourceAddressedOptions.SkipTools). Empty when nothing was labelled.
	EntryIDs []string
	// CoversUpToID is the last entry the chunk read (the last of EntryIDs), what
	// its coverage marker covers when the observer reads it all: coverage stays
	// positional, so a trailing entry whose content was skipped is covered too.
	// "" when nothing was labelled.
	CoversUpToID string
}

// SourceAddressedOptions bounds a serialization; MaxTokens 0 means unbounded.
type SourceAddressedOptions struct {
	MaxTokens int
	// SkipTools names the memory tools (the memory tool and the tools the agent
	// defined with it): their calls and the results answering them are left out
	// of the chunk (SerializeConversation).
	SkipTools []string
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
// (an aborted assistant turn with no content, a `!!command` run, or, with
// skipTools, a message holding only memory-tool calls or a memory-tool result)
// are skipped by the chunk serializer and therefore never need observation
// coverage.
func ObserverRenders(entry session.Entry, skipTools ...string) bool {
	if entry.ID == "" || !isSourceRenderableEntry(entry) {
		return false
	}
	return strings.TrimSpace(SerializeBranchEntries([]session.Entry{entry}, skipTools...)) != ""
}

// ObserverRendersFunc is ObserverRenders with skipTools bound, as the ledger's
// needs-coverage predicates take it.
func ObserverRendersFunc(skipTools []string) func(session.Entry) bool {
	return func(entry session.Entry) bool { return ObserverRenders(entry, skipTools...) }
}

// SerializeSourceAddressedBranchEntries serializes complete source entries up
// to the token budget. If the first entry alone exceeds the budget, a clearly
// marked head/tail excerpt is included so one pathological tool result cannot
// permanently block observation coverage. The ledger entry itself is never
// modified and remains recallable by id. An entry holding only content skipped
// for options.SkipTools gets no block, but is read (EntryIDs, CoversUpToID).
func SerializeSourceAddressedBranchEntries(entries []session.Entry, options SourceAddressedOptions) SourceAddressedSerialization {
	blocks := []string{}
	sourceEntryIDs := []string{}
	entryIDs := []string{}
	skipped := []string{}
	truncated := []string{}
	estimated := 0
	for _, entry := range entries {
		if entry.ID == "" || !isSourceRenderableEntry(entry) {
			continue
		}
		rendered := SerializeBranchEntries([]session.Entry{entry}, options.SkipTools...)
		if strings.TrimSpace(rendered) == "" {
			if len(options.SkipTools) > 0 && ObserverRenders(entry) {
				skipped = append(skipped, entry.ID)
			}
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
			entryIDs = append(append(entryIDs, skipped...), entry.ID)
			skipped = nil
			truncated = append(truncated, entry.ID)
			estimated = tokens.EstimateStringTokens(excerpt)
			break
		}
		blocks = append(blocks, block)
		sourceEntryIDs = append(sourceEntryIDs, entry.ID)
		entryIDs = append(append(entryIDs, skipped...), entry.ID)
		skipped = nil
		estimated += blockTokens
	}
	coversUpToID := ""
	if len(blocks) > 0 {
		// Skipped entries after the last block (before the entry the budget
		// stopped at, or at the end) were read too.
		entryIDs = append(entryIDs, skipped...)
		coversUpToID = entryIDs[len(entryIDs)-1]
	}
	text := strings.Join(blocks, "\n\n")
	return SourceAddressedSerialization{
		Text:                    text,
		SourceEntryIDs:          sourceEntryIDs,
		EstimatedTokens:         tokens.EstimateStringTokens(text),
		TruncatedSourceEntryIDs: truncated,
		EntryIDs:                entryIDs,
		CoversUpToID:            coversUpToID,
	}
}

func renderRecallMessage(entry session.Entry, thinking bool) (string, bool) {
	msg := entry.Message
	if msg == nil {
		return "", false
	}
	timestamp := FormatRecallTimestamp(msg["timestamp"], entryTimestamp(entry))
	switch msg["role"] {
	case "user":
		return "[User @ " + timestamp + "]: " + textAndPlaceholders(msg["content"], placeholderOptions{}), true
	case "assistant":
		body := nonEmptyLines(textAndPlaceholders(msg["content"], placeholderOptions{includeThinking: thinking, omitRedactedThinking: true}))
		if body == "" {
			return "", false
		}
		return "[Assistant @ " + timestamp + "]: " + body, true
	case "bashExecution":
		if bashExcludedFromContext(msg) {
			return "", false
		}
		return "[Bash @ " + timestamp + "]: " + BashExecutionText(msg), true
	}
	return "[Tool result: " + jsString(msg["toolName"]) + " @ " + timestamp + "]: " + textAndPlaceholders(msg["content"], placeholderOptions{}), true
}

// RenderRecallSourceEntry renders one source entry for recall <id>.
func RenderRecallSourceEntry(entry session.Entry) (string, bool) {
	return renderRecallSourceEntry(entry, true)
}

// RenderRecallSourceEntryText renders one source entry for memory v2's recall <entry id>: as
// RenderRecallSourceEntry, with thinking left out, as extraction and history leave it out
// (docs/memory-v2.md, "Memory tool").
func RenderRecallSourceEntryText(entry session.Entry) (string, bool) {
	return renderRecallSourceEntry(entry, false)
}

// WithoutThinking is entries with every thinking block taken out of their messages' content,
// as memory v2's recall reads them (docs/memory-v2.md, "Memory tool": thinking is never matched,
// counted or returned). Entries without thinking are returned as they are; the others are
// copied, never changed in place.
func WithoutThinking(entries []session.Entry) []session.Entry {
	var out []session.Entry
	for i, entry := range entries {
		blocks, ok := entry.Message["content"].([]any)
		if entry.Type != "message" || !ok || !slices.ContainsFunc(blocks, isThinkingBlock) {
			if out != nil {
				out = append(out, entry)
			}
			continue
		}
		if out == nil {
			out = append(make([]session.Entry, 0, len(entries)), entries[:i]...)
		}
		kept := make([]any, 0, len(blocks))
		for _, block := range blocks {
			if !isThinkingBlock(block) {
				kept = append(kept, block)
			}
		}
		message := make(map[string]any, len(entry.Message))
		for k, v := range entry.Message {
			message[k] = v
		}
		message["content"] = kept
		entry.Message = message
		out = append(out, entry)
	}
	if out == nil {
		return entries
	}
	return out
}

func isThinkingBlock(raw any) bool {
	block, ok := raw.(map[string]any)
	return ok && block["type"] == "thinking"
}

func renderRecallSourceEntry(entry session.Entry, thinking bool) (string, bool) {
	switch entry.Type {
	case "message":
		return renderRecallMessage(entry, thinking)
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

// History roles: who wrote a history entry (HistoryText).
const (
	HistoryUser      = "user"
	HistoryAssistant = "assistant"
	HistoryTool      = "tool"
)

// HistoryText is a branch entry's text as the memory tool's history source
// reads it: user text, the assistant's text and tool calls (thinking left
// out, as for the observer), and tool results and `!command` runs as text.
// Custom entries, custom messages, summaries and `!!command` runs are not
// history; ok is false for them and for a message with no text. Calls to the
// tools named in skipTools and their results are left out too, so the memory
// tool's own queries and answers (and those of the tools the agent defined
// with it) never echo back as history; an assistant message holding nothing
// else is not history.
func HistoryText(entry session.Entry, skipTools ...string) (role, text string, ok bool) {
	if entry.Type != "message" || entry.Message == nil {
		return "", "", false
	}
	msg := entry.Message
	switch msg["role"] {
	case "user":
		role, text = HistoryUser, textOnly(msg["content"])
	case "assistant":
		role, text = HistoryAssistant, nonEmptyLines(textAndPlaceholders(withoutToolCalls(msg["content"], skipTools), placeholderOptions{}))
	case "toolResult":
		if skippedToolResult(msg, skipTools) {
			return "", "", false
		}
		role, text = HistoryTool, textOnly(msg["content"])
	case "bashExecution":
		if bashExcludedFromContext(msg) {
			return "", "", false
		}
		role, text = HistoryTool, BashExecutionText(msg)
	default:
		role, text = HistoryTool, textOnly(msg["content"])
	}
	if strings.TrimSpace(text) == "" {
		return "", "", false
	}
	return role, text, true
}

// skippedToolResult reports whether msg is a tool result answering one of tools.
func skippedToolResult(msg map[string]any, tools []string) bool {
	if msg["role"] != "toolResult" {
		return false
	}
	name, _ := msg["toolName"].(string)
	return name != "" && slices.Contains(tools, name)
}

// withoutToolCalls is content minus its toolCall blocks naming one of tools (none keeps them all).
func withoutToolCalls(content any, tools []string) any {
	blocks, ok := content.([]any)
	if !ok || len(tools) == 0 {
		return content
	}
	kept := make([]any, 0, len(blocks))
	for _, raw := range blocks {
		if block, ok := raw.(map[string]any); ok && block["type"] == "toolCall" {
			if name, _ := block["name"].(string); name != "" && slices.Contains(tools, name) {
				continue
			}
		}
		kept = append(kept, raw)
	}
	return kept
}
