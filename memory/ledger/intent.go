package ledger

import "regexp"

// The types of an intention (Observation.Intent; contracts/truth.md, "Intentions"), each with how
// it closes. Every one closes only on a tool receipt (Closure), never on prose.
const (
	// IntentTodo is a step to do ("next we will", "we need to", "TODO"): it closes on a tool
	// receipt matching its keys.
	IntentTodo = "todo"
	// IntentPromise is the assistant's own commitment ("I'll", "I will", "let me"): it closes on a
	// tool receipt matching its keys, as a todo does.
	IntentPromise = "promise"
	// IntentConditionWait is a step waiting on a condition ("once the build passes", "as soon as CI
	// is green", "wait until"): it closes on a tool receipt matching its keys, as a todo does.
	IntentConditionWait = "condition-wait"
	// IntentDeferred is a step put off ("later", "next week", "eventually", "after the release"):
	// it closes only on a receipt made under a later request of the person's than its own, so the
	// work of the request it was deferred from never closes it.
	IntentDeferred = "deferred"
)

// IntentValues are the intention types, in contracts/truth.md's order.
var IntentValues = []string{IntentTodo, IntentPromise, IntentConditionWait, IntentDeferred}

// The fixed wording of the intention types (IntentOf), matched on ASCII word boundaries.
var (
	intentDeferred  = regexp.MustCompile(`(?i)\b(?:later|eventually|some\s?day|tomorrow|next\s+(?:week|month|sprint|quarter|session|time)|after\s+the\s+release|down\s+the\s+road|at\s+some\s+point|another\s+time)\b`)
	intentCondition = regexp.MustCompile(`(?i)(?:^(?:once|when|after)\b|\b(?:once|as\s+soon\s+as|wait(?:ing)?\s+(?:for|until))\b)`)
	intentPromise   = regexp.MustCompile(`(?i)^(?:i'll|i\s+will|i'm\s+going\s+to|i\s+am\s+going\s+to|let\s+me)\b`)
)

// IntentOf is a plan's type (status proposed), "" for any other record, the first that holds of
// its text: deferred, condition-wait, promise (the assistant's record, or one opening with "I'll",
// "I will", "let me"), todo.
func IntentOf(o Observation) string {
	if o.Status != StatusProposed {
		return ""
	}
	switch {
	case intentDeferred.MatchString(o.Content):
		return IntentDeferred
	case intentCondition.MatchString(o.Content):
		return IntentConditionWait
	case o.Kind == "assistant" || intentPromise.MatchString(o.Content):
		return IntentPromise
	}
	return IntentTodo
}
