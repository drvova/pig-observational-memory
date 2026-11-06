package extract

import (
	"strings"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
)

// Receipts (docs/memory-v2.md, "Working memory"; docs/brain-memory.md, decision 16): a plan
// closes only on a tool receipt matching its keys (ledger.PlanKeys, ledger.KeyMatches), recorded
// as a closing link on the receipt record (ledger.Closure). A shell command that exited 0, a
// successful file write or edit and a commit are receipts; a "read file X" plan
// (ledger.IsReadPlan) closes only on a worked call on X whose output holds a line no other tool
// output of the extraction holds. Assistant prose is never a receipt.

// receipt is what a record's tool call or file state can close.
type receipt struct {
	kind string
	keys []string
	// target and output are a read receipt's path key and the call's output.
	target, output string
}

// callOutput is one tool call's output in the extraction, for the read receipt's unique line.
type callOutput struct {
	target, text string
}

// uniqueLineMin is the length a line needs to tell one file from another.
const uniqueLineMin = 8

// callReceipt is the receipt a worked call gives, nil for none: "exit 0" for a shell command
// (keys: the command and its call key), "read" for a call with a path whose tool reads a file
// (keys: the path key). A file write's receipt is its file record's (emitFiles).
func (x *extraction) callReceipt(call toolCall, key, resultText string) *receipt {
	if command, ok := shellCommand(call.args); ok {
		keys := []string{command}
		if key != command {
			keys = append(keys, key)
		}
		return &receipt{kind: ledger.ReceiptExit0, keys: keys}
	}
	if fileToolState(call) == stateRead {
		return &receipt{kind: ledger.ReceiptRead, keys: []string{key}, target: key, output: resultText}
	}
	return nil
}

// standingPlans are the plans a receipt may close: the ledger's and this extraction's records
// stated as plans (status proposed) that nothing supersedes and no closing link names, the
// ledger's first; an extraction plan carries its record for its anchor.
func (x *extraction) standingPlans() ([]ledger.Observation, []*record) {
	superseded := map[string]bool{}
	for id := range x.superseded {
		superseded[id] = true
	}
	for _, r := range x.records {
		for _, id := range r.obs.Supersedes {
			superseded[id] = true
		}
	}
	closed := ledger.ClosedBy(x.in.Ledger.Observations)
	var plans []ledger.Observation
	for _, o := range x.in.Ledger.ActiveObservations {
		if _, done := closed[o.ID]; o.Status == ledger.StatusProposed && !superseded[o.ID] && !done {
			plans = append(plans, o)
		}
	}
	var own []*record
	for _, r := range x.records {
		if r.obs.Status == ledger.StatusProposed && !superseded[r.obs.ID] {
			own = append(own, r)
		}
	}
	return plans, own
}

// closesPlan reports whether rc closes plan: a read receipt only a read plan, on its path and a
// unique line; any other receipt only a plan that is not a read plan, on any of its keys.
func (x *extraction) closesPlan(plan ledger.Observation, rc *receipt) bool {
	if ledger.IsReadPlan(plan) != (rc.kind == ledger.ReceiptRead) {
		return false
	}
	matched := false
	for _, k := range ledger.PlanKeys(plan) {
		for _, r := range rc.keys {
			matched = matched || ledger.KeyMatches(k, r)
		}
	}
	if !matched || rc.kind != ledger.ReceiptRead {
		return matched
	}
	return x.uniqueLine(rc)
}

// wouldClose reports whether a receipt closes any standing plan before anchor: a worked call
// that makes no attempt record of its own makes one when it does, so the closing link has a
// receipt record to sit on.
func (x *extraction) wouldClose(rc *receipt, anchor int) bool {
	if rc == nil {
		return false
	}
	plans, own := x.standingPlans()
	for _, p := range plans {
		if x.closesPlan(p, rc) {
			return true
		}
	}
	for _, r := range own {
		if r.anchor < anchor && x.closesPlan(r.obs, rc) {
			return true
		}
	}
	return false
}

// uniqueLine reports whether a read receipt's output holds a line, trimmed and at least
// uniqueLineMin bytes long with a letter in it, that no output of a call on another target in
// this extraction holds.
func (x *extraction) uniqueLine(rc *receipt) bool {
	others := map[string]bool{}
	for _, o := range x.outputs {
		if o.target == rc.target {
			continue
		}
		for _, line := range strings.Split(o.text, "\n") {
			others[strings.TrimSpace(line)] = true
		}
	}
	for _, line := range strings.Split(rc.output, "\n") {
		line = strings.TrimSpace(line)
		if len(line) >= uniqueLineMin && strings.ContainsAny(strings.ToLower(line), "abcdefghijklmnopqrstuvwxyz") && !others[line] {
			return true
		}
	}
	return false
}

// resolveClosures puts on each receipt record, in branch order, a closing link to every
// standing plan it closes (closesPlan): the ledger's plans and this extraction's plans anchored
// before it, each closed once, by its first receipt. It runs once the ids are assigned.
func (x *extraction) resolveClosures() {
	plans, own := x.standingPlans()
	closed := map[string]bool{}
	for _, r := range x.records {
		if r.receipt == nil {
			continue
		}
		try := func(plan ledger.Observation) {
			if !closed[plan.ID] && plan.ID != r.obs.ID && x.closesPlan(plan, r.receipt) {
				closed[plan.ID] = true
				r.obs.Closes = append(r.obs.Closes, ledger.Closure{ID: plan.ID, ClosedBy: r.obs.ID, Receipt: r.receipt.kind})
			}
		}
		for _, p := range plans {
			try(p)
		}
		for _, p := range own {
			if p.anchor < r.anchor {
				try(p.obs)
			}
		}
	}
}

// Requests are, by index of entries[:end], the index of the newest request the person typed at
// or before it (isRequest), -1 before the first.
func Requests(entries []session.Entry, end int) []int {
	end = min(end, len(entries))
	out := make([]int, end)
	current := -1
	for i := 0; i < end; i++ {
		if isRequest(entries[i]) {
			current = i
		}
		out[i] = current
	}
	return out
}

// IsRequest reports a user message that opens an episode (contracts/encoding.md, "Episode"): the
// person's own request, which a plan goes stale against.
func IsRequest(entry session.Entry) bool { return isRequest(entry) }

// IsQuestionText reports text that ends with a question mark (isQuestion): a request that asks
// is not a requirement a plan goes stale against.
func IsQuestionText(text string) bool { return isQuestion(strings.TrimSpace(text)) }

// IsScopeExclusion reports a rule that is a scope exclusion ("don't touch X", "leave X alone",
// "only modify X", "stay out of X"; scopePatterns): the registry caps those apart from the other
// rules.
func IsScopeExclusion(text string) bool {
	text = strings.ReplaceAll(text, "’", "'")
	for _, p := range scopePatterns {
		if p.MatchString(text) {
			return true
		}
	}
	return false
}

// RequestText is a user message's text, harness markup removed.
func RequestText(entry session.Entry) string {
	if entry.Message == nil {
		return ""
	}
	return withoutMarkup(textBlocks(entry.Message["content"]))
}
