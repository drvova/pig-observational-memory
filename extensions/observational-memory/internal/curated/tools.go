package curated

import (
	"errors"
	"fmt"
	"strings"
)

// The writes the memory tool applies to the store (docs/memory-interface.md,
// section 5). Each takes the write's key=value arguments and returns the text
// the agent sees; errors come back as text too, so the agent can correct the
// query.
const (
	OpSave     = "save"
	OpEdit     = "edit"
	OpForget   = "forget"
	OpFeedback = "feedback"
)

// Call is one write's context: the store, the project it is seen from and the
// session that writes.
type Call struct {
	Store   Store
	Project string
	Source  string
	// Evidence reports whether a feedback quote (normalized) appears in the conversation;
	// a feedback whose quote it rejects is refused. Nil accepts any quote.
	Evidence func(quote string) bool
}

// The feedback quote's bounds, in characters after whitespace is collapsed.
const (
	MinQuoteChars = 8
	MaxQuoteChars = 400
)

// NormalizeQuote is a feedback quote as the ledger stores it: whitespace
// collapsed, at least MinQuoteChars characters, clipped to MaxQuoteChars. The
// quote is the evidence for the outcome, so a feedback without one is refused.
func NormalizeQuote(quote string) (string, error) {
	quote = oneLine(quote)
	runes := []rune(quote)
	if len(runes) < MinQuoteChars {
		return "", fmt.Errorf("feedback needs quote=\"...\": at least %d characters from this conversation that show the memory helped or misled you", MinQuoteChars)
	}
	if len(runes) > MaxQuoteChars {
		quote = string(runes[:MaxQuoteChars])
	}
	return quote, nil
}

// Run applies the write op with its arguments and returns the agent-facing
// text.
func (c Call) Run(op string, args map[string]string) (string, error) {
	name := args["name"]
	switch op {
	case OpSave:
		m := Memory{Name: name, Type: args["type"], Description: args["description"], Body: strings.TrimSpace(args["body"])}
		switch scope := args["scope"]; scope {
		case "", "global":
		case "project":
			if c.Project == "" {
				return "", errors.New("no project directory is known; save it as global instead")
			}
			m.Project = c.Project
		default:
			return "", fmt.Errorf("scope %q must be global or project", scope)
		}
		saved, err := c.Store.Save(m, c.Source)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Saved memory %s [%s]%s: %s", saved.Name, saved.Type, scopeNote(saved), saved.Description), nil
	case OpEdit:
		edit := Edit{}
		// Models often send every optional field, blank: only a body may be
		// cleared with an empty string.
		if v, ok := args["type"]; ok && strings.TrimSpace(v) != "" {
			edit.Type = &v
		}
		if v, ok := args["description"]; ok && strings.TrimSpace(v) != "" {
			edit.Description = &v
		}
		if v, ok := args["body"]; ok {
			edit.Body = &v
		}
		if edit.Type == nil && edit.Description == nil && edit.Body == nil {
			return "", errors.New("nothing to change: give a new type, description or body")
		}
		m, err := c.Store.Update(name, c.Project, edit, c.Source)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Updated memory %s [%s]: %s", m.Name, m.Type, m.Description), nil
	case OpForget:
		err := c.Store.Forget(name, c.Project)
		if errors.Is(err, ErrNotFound) {
			// A defined query is forgotten by the same write when no memory
			// holds the name.
			// ForgetDefinition removes one GetDefinition refuses to read too.
			if defErr := c.Store.ForgetDefinition(name); defErr == nil {
				return "Forgot defined query " + name + ".", nil
			} else if !errors.Is(defErr, ErrNotFound) {
				return "", defErr
			}
		}
		if err != nil {
			return "", err
		}
		return "Forgot memory " + name + ".", nil
	case OpFeedback:
		outcome := args["outcome"]
		if outcome != EventHelped && outcome != EventHarmed {
			return "", errors.New(`outcome must be "helped" or "harmed"`)
		}
		quote, err := NormalizeQuote(args["quote"])
		if err != nil {
			return "", err
		}
		if c.Evidence != nil && !c.Evidence(quote) {
			return "", errors.New("the quote is not in this conversation: copy words verbatim from a user or assistant message that show the memory helped or misled you")
		}
		key, what := name, "memory"
		if _, err := c.Store.GetIn(name, c.Project); err != nil {
			if _, defErr := c.Store.GetDefinition(name); !errors.Is(err, ErrNotFound) || defErr != nil {
				return "", err
			}
			key, what = DefinitionUsageKey(name), "defined query"
		}
		if err := c.Store.RecordFeedback(key, outcome, quote); err != nil {
			return "", err
		}
		return "Recorded that " + what + " " + name + " " + outcome + ".", nil
	}
	return "", fmt.Errorf("unknown memory write %q", op)
}

func scopeNote(m Memory) string {
	if m.Project != "" {
		return " (this project)"
	}
	return ""
}

// Describe renders a memory in full for the memory tool's recall <name>.
func Describe(m Memory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s]%s: %s\n", m.Name, m.Type, scopeNote(m), m.Description)
	if m.Body != "" {
		b.WriteString("\n" + m.Body + "\n")
	}
	fmt.Fprintf(&b, "\nsaved %s, updated %s", m.Created.Format("2006-01-02"), m.Updated.Format("2006-01-02"))
	if len(m.Sources) > 0 {
		fmt.Fprintf(&b, ", sessions: %s", strings.Join(m.Sources, ", "))
	}
	return b.String()
}
