package memquery

import (
	"fmt"
	"strings"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omhost"
)

// runWrite applies a write. It returns the model's text and, for a save, edit, define or
// propose, the user's notification.
func runWrite(env Env, q *Query) (text, notify string, err error) {
	if q.Write == WriteHarness || q.Write == WriteRollback {
		// The layer tells the user of each change itself (held, claimed, reverted, rolled back).
		if env.Layer == nil {
			return "", "", errLayerOff
		}
		switch {
		case q.Write == WriteRollback:
			text, err = env.Layer.Rollback(q.N, q.Args["model"])
		case q.Forget:
			text, err = env.Layer.Forget(q.Args["kind"], q.Args["name"])
		default:
			text, err = env.Layer.Define(q.Args["kind"], q.Args["name"], q.Args["text"])
		}
		return text, "", err
	}
	if env.Saved == nil {
		return "", "", errCuratedOff
	}
	if q.Write == WriteDefine {
		return runDefine(env, q.Define)
	}
	if q.Write == WritePropose {
		if env.Proposer == nil {
			return "", "", &Error{Msg: "rules cannot be proposed here", Hint: "propose needs the per-model harness, which keeps rules in the curated store"}
		}
		text, err := env.Proposer.Propose(q.Args)
		if err != nil {
			return "", "", err
		}
		return text, text, nil
	}
	op := map[string]string{WriteSave: curated.OpSave, WriteEdit: curated.OpEdit, WriteForget: curated.OpForget, WriteFeedback: curated.OpFeedback}[q.Write]
	call := *env.Saved
	if call.Evidence == nil {
		call.Evidence = func(quote string) bool { return omhost.QuotedInConversation(env.Entries, quote, env.memoryTools()) }
	}
	if call.UserEvidence == nil {
		call.UserEvidence = func(quote string) bool { return omhost.QuotedByUser(env.Entries, quote, env.memoryTools()) }
	}
	text, err = call.Run(op, q.Args)
	if err != nil {
		return "", "", err
	}
	if q.Write == WriteSave || q.Write == WriteEdit {
		notify = text
	}
	return text, notify, nil
}

// runDefine checks a definition by running its query once, then stores it.
func runDefine(env Env, def *Definition) (string, string, error) {
	if def.Name == ToolName {
		return "", "", &Error{Msg: fmt.Sprintf("%q is the memory tool's own name", ToolName), Hint: "choose another name"}
	}
	if reason, reserved := reservedDefinitionName(def.Name); reserved {
		return "", "", &Error{Msg: fmt.Sprintf("%q is %s", def.Name, reason), Hint: "choose a more specific name, such as bake-count"}
	}
	if err := curated.ValidateName(def.Name); err != nil {
		return "", "", err
	}
	body, err := Parse(def.Query)
	if err != nil {
		return "", "", fmt.Errorf("in the defined query %q: %w", def.Query, err)
	}
	// recall's id may be the $input a defined tool fills in, so only a
	// table query is run to check its fields.
	if body.Source != SourceRecall {
		if _, err := runRead(env, body); err != nil {
			return "", "", fmt.Errorf("in the defined query %q: %w", def.Query, err)
		}
	}
	definer := env.Definer
	if definer == nil {
		definer = env.Saved.Store
	}
	saved, err := definer.Define(curated.Definition{Name: def.Name, Query: def.Query, Description: def.Description}, env.Saved.Source)
	if err != nil {
		return "", "", err
	}
	text := fmt.Sprintf("Defined query %s: %s (%s)", saved.Name, saved.Query, saved.Description)
	if env.DefinesTools {
		text += fmt.Sprintf(". It is registered as the tool %s, on probation until it returns results twice without error", saved.Name)
	}
	return text + ". It is stored and listed in saved; recall " + saved.Name + " shows it.", text, nil
}

// hostToolNames are the host's built-in tools. A defined query becomes a tool of its own
// name (docs/memory-interface.md section 6), so it may not take one of these.
var hostToolNames = []string{"read", "bash", "edit", "write", "grep", "find", "ls"}

// reservedDefinitionName reports whether name is kept from definitions, and why: a host
// tool's name, or a source, stage or write of the query language. Other extensions' tools
// are not known here; the harness's Definer checks the live tool list before it stores a
// definition (harness.Manager).
func reservedDefinitionName(name string) (string, bool) {
	for _, n := range hostToolNames {
		if name == n {
			return "a built-in tool's name", true
		}
	}
	for _, words := range [][]string{Sources, Stages, Writes} {
		for _, n := range words {
			if name == n {
				return "a word of the memory query language", true
			}
		}
	}
	return "", false
}

// InputParameter is the one optional string parameter of a tool the agent defined: its value
// replaces $input in the stored query (Substitute).
const InputParameter = "input"

// Substitute replaces every $input in query with input. Inside a quoted text the input is
// escaped so it stays one text; elsewhere it is written as one bare word when it is one (an
// id, a number, a single word), else as a quoted text, so input never adds stages or writes.
func Substitute(query, input string) string {
	const placeholder = "$" + InputParameter
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`).Replace(input)
	bare := input != "" && strings.IndexFunc(input, func(r rune) bool { return !isWordRune(r) }) < 0
	var b strings.Builder
	quoted := false
	for i := 0; i < len(query); {
		switch {
		case quoted && query[i] == '\\' && i+1 < len(query):
			b.WriteString(query[i : i+2])
			i += 2
		case query[i] == '"':
			quoted = !quoted
			b.WriteByte('"')
			i++
		case strings.HasPrefix(query[i:], placeholder):
			switch {
			case quoted:
				b.WriteString(escaped)
			case bare:
				b.WriteString(input)
			default:
				b.WriteString(`"` + escaped + `"`)
			}
			i += len(placeholder)
		default:
			b.WriteByte(query[i])
			i++
		}
	}
	return b.String()
}
