// Package tokens holds the local token estimates the memory ledger uses.
package tokens

import (
	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"

	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/jsfmt"
	"github.com/drvova/pi-observational-memory/extensions/observational-memory/internal/session"
)

// EstimateStringTokens is the ~4 characters per token heuristic over a
// JavaScript string length (UTF-16 units), rounded up.
func EstimateStringTokens(text string) int {
	return (jsfmt.UTF16Len(text) + 3) / 4
}

// ObservationLine is what estimates an observation's rendered footprint.
type ObservationLine struct {
	ID, Timestamp, Relevance, Content string
}

// ObservationLineTokenCount estimates the rendered footprint of an observation
// line as it appears in summaries and pool listings:
// "[id] YYYY-MM-DD HH:MM [relevance] content". Pool budgets that only count
// bare content undercount every line's metadata overhead.
func ObservationLineTokenCount(o ObservationLine) int {
	return EstimateStringTokens("[" + o.ID + "] " + o.Timestamp + " [" + o.Relevance + "] " + o.Content)
}

// EstimateMessageTokens is Pi's estimateTokens over one message object.
var EstimateMessageTokens = func(message map[string]any) int {
	return sdk.EstimateTokens(message)
}

// EstimateEntryTokens estimates one source entry's context footprint.
func EstimateEntryTokens(entry session.Entry) int {
	switch entry.Type {
	case "message":
		if entry.Message != nil {
			return EstimateMessageTokens(entry.Message)
		}
	case "custom_message":
		switch content := entry.Content.(type) {
		case string:
			if content != "" {
				return EstimateStringTokens(content)
			}
		case []any:
			total := 0
			for _, raw := range content {
				block, _ := raw.(map[string]any)
				if block == nil {
					continue
				}
				if block["type"] == "text" {
					if text, _ := block["text"].(string); text != "" {
						total += EstimateStringTokens(text)
					}
				}
			}
			return total
		}
	case "branch_summary":
		if summary, ok := entry.Summary.(string); ok {
			return EstimateStringTokens(summary)
		}
	}
	return 0
}
