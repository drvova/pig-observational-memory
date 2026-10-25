// Package consolidate is memory v2's level 1 (docs/memory-v2.md, "Level 1: one request at
// compaction"), as pure code: the input of the one request a compaction may make, the forced
// tool it answers with, and the validation and union rule that decide what of its answer is
// recorded. The hook (internal/hooks, consolidate_v2.go) makes the request and appends the
// result; nothing here calls a model or the host.
package consolidate

import (
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/agents"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
)

// ToolName is the level-1 request's one forced tool.
const ToolName = "consolidate_memory"

// Stage is the level-1 request's stage on its request event (agents.RequestEvent).
const Stage = "consolidate"

// SystemPrompt is the level-1 request's system prompt: the kernel's fixed rules
// (kernel.TextConsolidateRules), which no definition can change.
func SystemPrompt() string {
	return strings.TrimRight(kernel.Text(kernel.TextConsolidateRules), "\n") + "\n"
}

// stringArray is a JSON schema array of strings.
func stringArray(description string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": description}
}

// enumOf is a JSON schema string enum.
func enumOf[T ~string](values []T, description string) map[string]any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return map[string]any{"type": "string", "enum": out, "description": description}
}

// labelProperties are the fields a record and a merge both set.
func labelProperties() map[string]any {
	return map[string]any{
		"kind":       enumOf(ledger.ObservationKindValues, "What the record is. Ignored for attempt, file, commit and note records, whose kind code set, and for user corrections and preferences."),
		"relevance":  enumOf(ledger.RelevanceValues, "How long the record matters."),
		"subject":    map[string]any{"type": "string", "description": "Short lowercase noun phrase; a path a/b/c nests it under a broader subject. Reuse listed subjects."},
		"scope":      map[string]any{"type": "string", "description": "The qualifier that tells two figures apart; records with different scopes never replace each other."},
		"supersedes": stringArray("Ids of records this one replaces, only on an explicit correction; it must carry their specifics."),
	}
}

// Tool is the level-1 request's forced tool. It is never executed: RunSingleRequest reads its
// arguments from the one response.
func Tool() agents.Tool {
	record := labelProperties()
	record["id"] = map[string]any{"type": "string", "description": "The candidate's id."}
	record["outcome"] = enumOf([]string{ledger.OutcomeFailed, ledger.OutcomeWorked, ledger.OutcomeOpen}, "An attempt record's outcome.")
	merge := labelProperties()
	merge["ids"] = stringArray("The ids of the two or more records the merged line replaces: never attempt, file, commit or note records, nor records with different scopes.")
	merge["content"] = map[string]any{"type": "string", "description": "One line carrying every specific of the merged records."}
	return agents.Tool{
		Name:        ToolName,
		Label:       "Consolidate memory",
		Description: "Labels, merges, reflections and subject aliases for the memory of the compacted conversation. Every field is optional; cite only ids printed in the input.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"records": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": record, "required": []any{"id"}}},
				"merges":  map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": merge, "required": []any{"ids", "content"}}},
				"reflections": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
					"content":       map[string]any{"type": "string", "description": "One durable conclusion, carrying the specifics of the records it cites."},
					"supportingIds": stringArray("Ids of the records it distills."),
					"supersedes":    stringArray("Ids of earlier reflections it replaces; it must carry their specifics."),
				}, "required": []any{"content", "supportingIds"}}},
				"aliases": map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
					"from":   map[string]any{"type": "string"},
					"to":     map[string]any{"type": "string"},
					"reason": map[string]any{"type": "string"},
				}, "required": []any{"from", "to"}}},
			},
		},
	}
}
