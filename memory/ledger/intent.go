package ledger

// The types of an intention (Observation.Intent; contracts/truth.md, "Intentions"), each with how
// it closes. Every one closes only on a tool receipt (Closure), never on prose.
const (
	// IntentTodo is a step to do ("next we will", "we need to", "TODO"): it closes on a tool
	// receipt matching its keys.
	IntentTodo = "todo"
	// IntentPromise is the assistant's own commitment ("I'll", "I will", "let me"): it closes on a
	// tool receipt matching its keys, as a todo does.
	IntentPromise = "promise"
	// IntentConditionWait is a step waiting on a condition ("once the build passes", "when CI is
	// green", "as soon as"): it closes on a tool receipt matching its keys, as a todo does.
	IntentConditionWait = "condition-wait"
	// IntentDeferred is a step put off ("later", "next week", "eventually", "after the release"):
	// it closes only on a receipt from a later episode than its own, so the work of the request it
	// was deferred from never closes it.
	IntentDeferred = "deferred"
)

// IntentValues are the intention types, in contracts/truth.md's order.
var IntentValues = []string{IntentTodo, IntentPromise, IntentConditionWait, IntentDeferred}
