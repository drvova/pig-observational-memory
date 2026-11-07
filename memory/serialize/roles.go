package serialize

import (
	"regexp"
	"sort"
	"strings"
)

// RoleQuote is what EscapeRoleLabels puts before a role-like label.
const RoleQuote = "quoted "

// The role-like labels tool text may forge (contracts/secrets.md, "Role labels"), matched over
// the ASCII-lowercased text; each match's group 1 ends where RoleQuote goes in. A bracketed label
// ([User]:, [User @ ...]:, the serializer's own [Tool result for ...], [Bash @ ...] and
// [Custom (...) @ ...], never a tool call's [bash(...)] placeholder) and a tag (<user>,
// </user>, <|im_start|>) are escaped anywhere; a bare label (User:, Human:, **User:**) and a
// heading that is only a role (### User) only at a line's start, after its indentation and quote markers.
var roleLabels = []*regexp.Regexp{
	regexp.MustCompile(`(\[[ \t]*)(?:user|human|assistant|system|developer|source entry id|branch summary|tool result)\b`),
	regexp.MustCompile(`(\[[ \t]*)(?:bash|custom)[ \t]+[@(]`),
	regexp.MustCompile(`(</?\|?)(?:user|human|assistant|system|developer|im_start|im_end)\b`),
	regexp.MustCompile(`(?m)^([ \t]*(?:>[ \t]*)*)(?:#{1,6}[ \t]*(?:\*\*)?(?:user|human|assistant|system|developer)(?:\*\*)?[ \t]*(?::|$)|(?:\*\*)?(?:user|human|assistant|system|developer)(?:\*\*)?[ \t]*:)`),
}

// EscapeRoleLabels puts RoleQuote before every role-like label in text, so text a tool returned
// can never read as a turn of the conversation when the level-1 request flattens it (scenario 11
// of docs/brain-memory.md). The labels' own letters are kept; the result is deterministic and
// the Lisp core's (lisp/src/roles.lisp) byte for byte.
func EscapeRoleLabels(text string) string {
	lower := asciiLowerRoles(text)
	if !strings.ContainsAny(lower, "[<:#") {
		return text
	}
	points := []int{}
	seen := map[int]bool{}
	for _, re := range roleLabels {
		for _, m := range re.FindAllStringSubmatchIndex(lower, -1) {
			if p := m[3]; !seen[p] {
				seen[p] = true
				points = append(points, p)
			}
		}
	}
	if len(points) == 0 {
		return text
	}
	sort.Ints(points)
	var b strings.Builder
	last := 0
	for _, p := range points {
		b.WriteString(text[last:p])
		b.WriteString(RoleQuote)
		last = p
	}
	b.WriteString(text[last:])
	return b.String()
}

// asciiLowerRoles lowercases ASCII letters only, so every byte keeps its offset.
func asciiLowerRoles(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}
