package extract

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/secrets"
	"github.com/drvova/pig-observational-memory/memory/serialize"
)

// Cues (docs/brain-memory.md, decision 4; docs/memory-v2.md, "Cues"): between compactions, a
// harness whose hooks hand a tool's result back with added context (PostToolUse
// additionalContext) may get one short hint plus an om snippet after a tool call, never a set of
// records. A cue fires only on an exact structural match of the call against a standing record's
// keys: its shell command, its path (a record's directory key, ending in "/", holds the paths
// inside it), or a line of its output equal to a recorded error line. It abstains by default: no
// match is no cue, and a record is cued at most once per compaction, at most CueCap per
// compaction. Delivery state is the om.cue entries after the latest compaction on the branch, so
// nothing is kept anywhere else, and a cue is appended, never injected and then deleted. It is
// code only and makes no model request. The image decides byte for byte the same
// (lisp/src/cue.lisp, contracts/cue.jsonl).

// CueCap is the default cap on cues per compaction (config cueCap).
const CueCap = 3

// CueTextRunes bounds the record text a cue quotes.
const CueTextRunes = 120

// CueCall is the tool call a cue is decided for.
type CueCall struct {
	Tool   string
	Args   map[string]any
	Output string
}

// Cue is one cue: the record it points at, the key that matched (kind:value) and the text handed
// to the model.
type Cue struct {
	Record string `json:"record"`
	Key    string `json:"key"`
	Text   string `json:"text"`
}

// CueData is an om.cue entry's data: the cue's record and key, and the compaction it was given
// after ("" before the first).
type CueData struct {
	Record     string `json:"record"`
	Key        string `json:"key"`
	Compaction string `json:"compaction"`
}

// cueHint is what a cued record's kind says about the call.
func cueHint(o ledger.Observation) string {
	switch o.Kind {
	case ledger.KindAttempt:
		return "this failed before"
	case "preference":
		return "a standing rule names this"
	case "correction":
		return "a correction names this"
	case ledger.KindDecision:
		return "a decision names this"
	case ledger.KindConvention:
		return "a repository convention names this"
	}
	return ""
}

// cueable reports whether o may be cued: a failed attempt, a rule, a correction, a decision or a
// repository convention that is not held (ledger.HeldRule).
func cueable(o ledger.Observation) bool { return Cueable(o) }

// Cueable reports whether o may be cued: a failed attempt, a rule, a correction, a decision or a
// repository convention that is not held (ledger.HeldRule).
func Cueable(o ledger.Observation) bool {
	if cueHint(o) == "" || ledger.HeldRule(o) != "" {
		return false
	}
	return o.Kind != ledger.KindAttempt || o.Outcome == ledger.OutcomeFailed
}

// cueKey is one structural key of a call.
type cueKey struct{ kind, value string }

// cueKeys are call's keys, in order: its shell command, else its path (absolute, as level 0 keys
// it, and relative to cwd when inside it), then each distinct non-blank line of its output.
func cueKeys(call CueCall, cwd string) []cueKey {
	var out []cueKey
	x := &extraction{in: Input{Cwd: cwd}}
	tc := toolCall{name: call.Tool, args: call.Args}
	if command, ok := shellCommand(call.Args); ok && strings.TrimSpace(command) != "" {
		out = append(out, cueKey{ledger.KeyCommands, strings.TrimSpace(command)})
	} else if p, ok := callPath(tc); ok {
		out = append(out, cueKey{ledger.KeyPaths, x.pathKey(p)})
		if rel := x.pathDisplay(p); rel != x.pathKey(p) {
			out = append(out, cueKey{ledger.KeyPaths, rel})
		}
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(call.Output, "\n") {
		line = clip(strings.TrimSpace(line), serialize.BriefKeyArgRunes)
		if line != "" && !seen[line] {
			seen[line] = true
			out = append(out, cueKey{ledger.KeyErrors, line})
		}
	}
	return out
}

// keyMatches reports whether a record's key of kind holds the call's value: equal, or for a
// path, a directory key ("ends in /") the path is inside.
func keyMatches(kind, recorded, value string) bool {
	if recorded == value {
		return true
	}
	return kind == ledger.KeyPaths && strings.HasSuffix(recorded, "/") && len(value) > len(recorded) &&
		strings.HasPrefix(filepath.ToSlash(value), recorded)
}

// keysOf is o's keys of kind.
func keysOf(o ledger.Observation, kind string) []string {
	switch kind {
	case ledger.KeyPaths:
		return o.Keys.Paths
	case ledger.KeyCommands:
		return o.Keys.Commands
	case ledger.KeyErrors:
		return o.Keys.Errors
	}
	return nil
}

// CueFor decides the cue for call over the branch's entries: the newest standing cueable record
// one of the call's keys matches that no om.cue entry after the latest compaction names, while
// fewer than limit cues were given since that compaction; false when it abstains.
func CueFor(entries []ledger.Entry, call CueCall, cwd string, limit int) (Cue, bool) {
	return CueOver(entries, nil, call, cwd, limit)
}

// CueOver is CueFor with the standing records given (active, the fold of entries' records, in
// fold order), for a caller that already folded them; nil folds entries.
func CueOver(entries []ledger.Entry, active []ledger.Observation, call CueCall, cwd string, limit int) (Cue, bool) {
	if limit <= 0 {
		return Cue{}, false
	}
	from := ledger.FindLastCompactionIndex(entries) + 1
	given := map[string]bool{}
	n := 0
	for _, e := range entries[from:] {
		if e.Type != "custom" || e.CustomType != ledger.OMCue {
			continue
		}
		n++
		if data, ok := e.Data.(map[string]any); ok {
			if id, _ := data["record"].(string); id != "" {
				given[id] = true
			}
		}
	}
	if n >= limit {
		return Cue{}, false
	}
	keys := cueKeys(call, cwd)
	if len(keys) == 0 {
		return Cue{}, false
	}
	if active == nil {
		active = ledger.FoldLedger(entries, "").ActiveObservations
	}
	for i := len(active) - 1; i >= 0; i-- {
		o := active[i]
		if given[o.ID] || !cueable(o) {
			continue
		}
		for _, k := range keys {
			if slices.ContainsFunc(keysOf(o, k.kind), func(r string) bool { return keyMatches(k.kind, r, k.value) }) {
				return Cue{Record: o.ID, Key: k.kind + ":" + k.value, Text: CueText(o)}, true
			}
		}
	}
	return Cue{}, false
}

// CueText is a cue's text: framed unverified (decision 18), the hint, the record's text clipped
// and redacted, and the om snippet that reads the whole record.
func CueText(o ledger.Observation) string {
	return "memory cue (unverified): " + cueHint(o) + ": " + clip(secrets.Redact(o.Content), CueTextRunes) + " Read it: om.Recall(`" + o.ID + "`)"
}

// CueCompaction is the id of the latest compaction on entries, "" before the first: an om.cue
// entry's Compaction.
func CueCompaction(entries []ledger.Entry) string {
	if i := ledger.FindLastCompactionIndex(entries); i >= 0 {
		return entries[i].ID
	}
	return ""
}
