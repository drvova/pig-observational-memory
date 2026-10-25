package sdkhost_test

import (
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/sdkhost"
)

// The context event's messages are the payload's own objects, so an edit in place reaches the
// list the SDK sends back; a message that is not an object is left out.
func TestContextMessagesAreThePayloadsObjects(t *testing.T) {
	result := map[string]any{"role": "toolResult", "toolCallId": "c1", "content": []any{}}
	data := map[string]any{"messages": []any{map[string]any{"role": "user", "content": "hi"}, "junk", result}}
	messages := sdkhost.ContextMessages(data)
	if len(messages) != 2 {
		t.Fatalf("messages %v", messages)
	}
	messages[1]["content"] = "thinned"
	if result["content"] != "thinned" {
		t.Fatal("the edit did not reach the payload")
	}
	if got := sdkhost.ContextMessages(map[string]any{}); len(got) != 0 {
		t.Fatalf("no messages: %v", got)
	}
}
