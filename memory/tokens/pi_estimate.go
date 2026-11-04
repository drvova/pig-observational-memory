package tokens

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/jsfmt"
)

// estimatedImageChars is Pi's ESTIMATED_IMAGE_CHARS: what an image block counts as.
const estimatedImageChars = 4800

// EstimatePiMessage is Pi's estimateTokens over one agent message: the UTF-16 length of the
// text a message carries (a system message's sections and added tools, an assistant's text,
// thinking and tool calls with their JSON arguments, a bash execution's command and output, a
// summary), over four, rounded up; a role it does not know counts nothing.
func EstimatePiMessage(message map[string]any) int {
	chars := 0
	switch role, _ := message["role"].(string); role {
	case "system":
		chars = contentChars(message["content"])
		if sections, ok := message["sections"].(map[string]any); ok {
			for _, section := range sections {
				if text, ok := section.(string); ok {
					chars += jsfmt.UTF16Len(text)
				}
			}
		}
		if added := message["toolsAdded"]; truthy(added) {
			chars += jsonChars(added)
		}
	case "user", "custom", "toolResult":
		chars = contentChars(message["content"])
	case "assistant":
		blocks, _ := message["content"].([]any)
		for _, raw := range blocks {
			block, _ := raw.(map[string]any)
			switch kind, _ := block["type"].(string); kind {
			case "text":
				text, _ := block["text"].(string)
				chars += jsfmt.UTF16Len(text)
			case "thinking":
				thinking, _ := block["thinking"].(string)
				chars += jsfmt.UTF16Len(thinking)
			case "toolCall":
				name, _ := block["name"].(string)
				chars += jsfmt.UTF16Len(name) + jsonChars(block["arguments"])
			}
		}
	case "bashExecution":
		command, _ := message["command"].(string)
		output, _ := message["output"].(string)
		chars = jsfmt.UTF16Len(command) + jsfmt.UTF16Len(output)
	case "branchSummary", "compactionSummary":
		summary, _ := message["summary"].(string)
		chars = jsfmt.UTF16Len(summary)
	default:
		return 0
	}
	return int(math.Ceil(float64(chars) / 4))
}

func contentChars(content any) int {
	switch content := content.(type) {
	case string:
		return jsfmt.UTF16Len(content)
	case []any:
		chars := 0
		for _, raw := range content {
			block, _ := raw.(map[string]any)
			switch kind, _ := block["type"].(string); kind {
			case "text":
				text, _ := block["text"].(string)
				chars += jsfmt.UTF16Len(text)
			case "image":
				chars += estimatedImageChars
			}
		}
		return chars
	}
	return 0
}

// jsonChars is the UTF-16 length of value's JSON text; JSON.stringify writes no HTML escapes,
// so neither does this.
func jsonChars(value any) int {
	if value == nil {
		return len("null")
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return 0
	}
	return jsfmt.UTF16Len(strings.TrimSuffix(b.String(), "\n"))
}

func truthy(value any) bool {
	switch value := value.(type) {
	case nil:
		return false
	case bool:
		return value
	case string:
		return value != ""
	case float64:
		return value != 0 && !math.IsNaN(value)
	}
	return true
}
