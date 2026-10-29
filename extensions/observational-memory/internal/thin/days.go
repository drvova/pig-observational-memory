package thin

import (
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
)

// DayHeaderPrefix opens the line DayHeaders puts before a user message.
const DayHeaderPrefix = "[Sent on "

// DayHeader is the line put before a user message sent at timestamp (a Pi timestamp), in the
// local time zone records are dated in (serialize.Location): "[Sent on Tuesday 2023-05-30]".
func DayHeader(timestamp any) (string, bool) {
	t, ok := serialize.ParseTimestamp(timestamp)
	if !ok {
		return "", false
	}
	t = t.In(serialize.Location)
	return DayHeaderPrefix + t.Format("Monday 2006-01-02") + "]", true
}

// DayHeaders puts a DayHeader text block first in each user message of a provider request
// whose day differs from the previous user message's in the request (the first one always
// gets one), so the model reads when the kept conversation, and the latest message, were
// said: the summary's records carry their dates, raw messages do not. It reads each message's
// own timestamp alone, so a message keeps the same bytes in every later request, and returns
// how many it marked. A message with no readable timestamp, or one already opening with a
// header, is left as it is and keeps the previous day.
func DayHeaders(messages []map[string]any) int {
	marked := 0
	previous := ""
	for _, message := range messages {
		if message == nil || message["role"] != "user" {
			continue
		}
		header, ok := DayHeader(message["timestamp"])
		if !ok || header == previous {
			continue
		}
		previous = header
		switch content := message["content"].(type) {
		case string:
			if strings.HasPrefix(content, DayHeaderPrefix) {
				continue
			}
			message["content"] = []any{map[string]any{"type": "text", "text": header}, map[string]any{"type": "text", "text": content}}
		case []any:
			if first, ok := firstText(content); ok && strings.HasPrefix(first, DayHeaderPrefix) {
				continue
			}
			message["content"] = append([]any{map[string]any{"type": "text", "text": header}}, content...)
		default:
			continue
		}
		marked++
	}
	return marked
}

func firstText(content []any) (string, bool) {
	if len(content) == 0 {
		return "", false
	}
	block, ok := content[0].(map[string]any)
	if !ok || block["type"] != "text" {
		return "", false
	}
	text, ok := block["text"].(string)
	return text, ok
}
