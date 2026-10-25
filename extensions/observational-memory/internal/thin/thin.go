// Package thin is memory v2's thinning of aged tool results (docs/memory-v2.md, "Thinning aged
// tool results"): a pure function of the branch that shortens, in each provider request's
// context, the tool results the model already answered before the latest compaction to their
// level-0 record line and the memory query that returns the original. It makes no request,
// and the session file keeps every original.
//
// What is thinned is decided from the branch up to the latest compaction entry alone, so the
// request bytes stay the same between requests and change only after a new compaction, which
// already broke the prompt cache prefix.
package thin

import (
	"slices"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/extract"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tokens"
)

// Input is what a thinning plan is derived from.
type Input struct {
	// Entries is the current branch.
	Entries []session.Entry
	// SkipTools are the memory tool and the tools the agent defined with it: their results
	// are memory reads, never thinned.
	SkipTools []string
	// Cwd shows a call's path relative to the working directory, as level 0 does.
	Cwd string
}

// Plan is the thinned text of each tool result to shorten, by its tool call id.
type Plan map[string]string

// Compute is the thinning plan for the branch (docs/memory-v2.md, "Thinning aged tool
// results"): every tool result in the latest compaction's retained tail (from its
// firstKeptEntryId up to the compaction entry) that an assistant message after it, still
// before the compaction, answered. Never the newest round of that tail (the results of its last
// assistant message that made tool calls, even when a final reply followed), never a result that
// landed after the compaction (a fresh one), never a user message (the split turn's opening
// request among them), never a memory tool result, never a result whose call is not on the
// branch, never one whose tool call id names another result in the tail, and never one whose
// thinned text would not be shorter. Without a compaction there is nothing to thin.
func Compute(in Input) Plan {
	entries := in.Entries
	c := ledger.FindLastCompactionIndex(entries)
	if c < 0 {
		return nil
	}
	kept := ledger.EntryIndexForID(entries[:c], entries[c].FirstKeptEntryID)
	if kept < 0 {
		return nil
	}
	plan := Plan{}
	for _, candidate := range candidatesIn(entries, kept, c, in) {
		plan[candidate.callID] = candidate.text
	}
	if len(plan) == 0 {
		return nil
	}
	return plan
}

// candidate is one tool result to thin.
type candidate struct {
	callID, text string
}

// candidatesIn are the thinnable tool results of entries[kept:c], oldest first.
func candidatesIn(entries []session.Entry, kept, c int, in Input) []candidate {
	// A tool call id that names more than one result in the tail cannot say which one a context
	// message is, so neither is thinned. Only the tail up to the compaction is read, so a result
	// that lands later never changes the plan (Apply skips an id the request repeats).
	resultsByCall := map[string]int{}
	for _, entry := range entries[kept:c] {
		if isToolResult(entry) {
			id, _ := entry.Message["toolCallId"].(string)
			resultsByCall[id]++
		}
	}
	// The results the model answered: those before the tail's last assistant message.
	lastAssistant := -1
	for i := c - 1; i >= kept; i-- {
		if isRole(entries[i], "assistant") {
			lastAssistant = i
			break
		}
	}
	// The newest round: the tool calls of the tail's last assistant message that made any. Its
	// results stay whole even when a final reply answered them, so the round the model's last
	// answer came from is still in front of it.
	newestRound := map[string]bool{}
	for i := c - 1; i >= kept; i-- {
		if ids := toolCallIDs(entries[i]); len(ids) > 0 {
			for _, id := range ids {
				newestRound[id] = true
			}
			break
		}
	}
	out := []candidate{}
	for i := kept; i < lastAssistant; i++ {
		entry := entries[i]
		if !isToolResult(entry) || entry.ID == "" {
			continue
		}
		callID, _ := entry.Message["toolCallId"].(string)
		name, _ := entry.Message["toolName"].(string)
		if callID == "" || name == "" || resultsByCall[callID] != 1 || newestRound[callID] || slices.Contains(in.SkipTools, name) {
			continue
		}
		args, found := callArguments(entries, kept, i, callID)
		if !found {
			continue
		}
		failed, _ := entry.Message["isError"].(bool)
		text := Text(extract.CallLine(in.Cwd, name, args, resultText(entry.Message["content"]), failed), entry.ID)
		if tokens.EstimateStringTokens(text) >= tokens.EstimateEntryTokens(entry) {
			continue
		}
		out = append(out, candidate{callID: callID, text: text})
	}
	return out
}

// toolCallIDs are the ids of an assistant message's tool calls, none for any other entry.
func toolCallIDs(entry session.Entry) []string {
	if !isRole(entry, "assistant") {
		return nil
	}
	blocks, _ := entry.Message["content"].([]any)
	ids := []string{}
	for _, raw := range blocks {
		block, _ := raw.(map[string]any)
		if block == nil || block["type"] != "toolCall" {
			continue
		}
		if id, _ := block["id"].(string); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// Text is a thinned tool result: its level-0 record line and the memory query that returns the
// original result entry.
func Text(line, entryID string) string {
	return "[thinned] " + line + `; the full result: memory "recall ` + entryID + `"`
}

// callArguments are the arguments of the tool call callID, looked up in the assistant messages
// of entries[kept:result]; found is false when the call is not there (its assistant message was
// compacted away), and the result is then left whole.
func callArguments(entries []session.Entry, kept, result int, callID string) (map[string]any, bool) {
	for i := result - 1; i >= kept; i-- {
		if !isRole(entries[i], "assistant") {
			continue
		}
		blocks, _ := entries[i].Message["content"].([]any)
		for _, raw := range blocks {
			block, _ := raw.(map[string]any)
			if block == nil || block["type"] != "toolCall" || block["id"] != callID {
				continue
			}
			args, _ := block["arguments"].(map[string]any)
			return args, true
		}
	}
	return nil, false
}

// Apply shortens, in place, the tool result messages of a request's context that plan names,
// and returns how many it changed. Every other message, and every field but a thinned
// result's content, is left as it is, so PiG keeps the list's system messages where they are.
func Apply(messages []map[string]any, plan Plan) int {
	if len(plan) == 0 {
		return 0
	}
	// An id the request holds more than once (a provider reusing tool call ids) cannot say which
	// result the plan meant, so none of them is thinned.
	seen := map[string]int{}
	for _, message := range messages {
		if message != nil && message["role"] == "toolResult" {
			id, _ := message["toolCallId"].(string)
			seen[id]++
		}
	}
	changed := 0
	for _, message := range messages {
		if message == nil || message["role"] != "toolResult" {
			continue
		}
		id, _ := message["toolCallId"].(string)
		text, ok := plan[id]
		if !ok || seen[id] != 1 {
			continue
		}
		message["content"] = []any{map[string]any{"type": "text", "text": text}}
		changed++
	}
	return changed
}

func isRole(entry session.Entry, role string) bool {
	return entry.Type == "message" && entry.Message != nil && entry.Message["role"] == role
}

func isToolResult(entry session.Entry) bool { return isRole(entry, "toolResult") }

// resultText is the text blocks of a tool result's content.
func resultText(content any) string {
	switch c := content.(type) {
	case string:
		return c
	case []any:
		text := ""
		for _, raw := range c {
			block, _ := raw.(map[string]any)
			if block == nil || block["type"] != "text" {
				continue
			}
			if s, ok := block["text"].(string); ok {
				if text != "" {
					text += "\n"
				}
				text += s
			}
		}
		return text
	}
	return ""
}
