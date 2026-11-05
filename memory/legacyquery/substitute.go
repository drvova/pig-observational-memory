package legacyquery

import "strings"

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
