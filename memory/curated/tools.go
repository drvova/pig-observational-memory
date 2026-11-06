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
	// UserEvidence reports whether a quote (normalized) is a user's words in the conversation:
	// a save quoting one is live at once, any other save is staged,
	// and a helped feedback quoting one is evidence for a staged memory (Store.Settle). Nil
	// finds no user quote. A forget quoting one retires a memory for every session.
	UserEvidence func(quote string) bool
	// SourceOf is the SourceDigest of the tool output holding a quote (normalized) that is no
	// user's words, "" when none does: a save quoting it is staged citing that content, the
	// evidence of independence between sessions (Store.Settle). Nil finds none, and such a save
	// is refused.
	SourceOf func(quote string) string
}

// MaxForgetsPerQuote is how many memories one quote of the user's words forgets for every
// session (docs/brain-memory.md, decision 2): a forget of more items needs the user's words for
// each, and a further forget with the same quote is staged.
const MaxForgetsPerQuote = 3

// userQuoted reports whether quote is a user's words (UserEvidence).
func (c Call) userQuoted(quote string) bool {
	return c.UserEvidence != nil && c.UserEvidence(quote)
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
		digest := ""
		if quote, given := args["quote"]; given {
			normalized, err := NormalizeQuote(quote)
			if err != nil {
				return "", errors.New(strings.Replace(err.Error(), "feedback needs", "save's quote needs", 1))
			}
			if !c.userQuoted(normalized) {
				if c.SourceOf != nil {
					digest = c.SourceOf(normalized)
				}
				if digest == "" {
					return "", errors.New("the quote is not a user's words in this conversation: copy words verbatim from a user message that confirm the memory, or save without quote to stage it")
				}
				// Words from tool output never confirm a memory: the save is staged, citing
				// the content they come from.
				m.Staged = true
			}
		} else {
			m.Staged = true
		}
		saved, err := c.Store.SaveCiting(m, c.Source, digest)
		if err != nil {
			return "", err
		}
		if saved.Staged {
			text := fmt.Sprintf("Staged memory %s [%s]%s: %s. It shows in saved in this session and stays out of the memory index until evidence confirms it: a user's words (feedback name=%s outcome=helped quote=\"...\"), a compaction citing it, or the same save in another session quoting other tool output it rests on.", saved.Name, saved.Type, scopeNote(saved), saved.Description, saved.Name)
			if len(saved.StagedBy) > 1 {
				events, err := c.Store.UsageEvents(saved.Name)
				if err != nil {
					return "", err
				}
				if independentStagings(saved, events) {
					text = fmt.Sprintf("Staged memory %s [%s]%s: %s. Another session staged the same memory from other source content, which is the evidence to make it live: it enters the memory index at its next render.", saved.Name, saved.Type, scopeNote(saved), saved.Description)
				} else {
					text = fmt.Sprintf("Staged memory %s [%s]%s: %s. Another session staged the same memory, but not from other source content it quotes, so it is no independent evidence: it stays staged until a user's words (feedback name=%s outcome=helped quote=\"...\"), a compaction citing it, or a session quoting other tool output confirms it.", saved.Name, saved.Type, scopeNote(saved), saved.Description, saved.Name)
				}
			}
			return text, nil
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
		quote, quoted := args["quote"]
		if quoted {
			normalized, err := NormalizeQuote(quote)
			if err != nil {
				return "", errors.New(strings.Replace(err.Error(), "feedback needs", "forget's quote needs", 1))
			}
			quote = normalized
		}
		m, err := c.Store.readIn(name, c.Project)
		switch {
		case err == nil && !SeenBy(m, c.Source):
			// A memory another session staged is not this session's to forget.
			err = ErrNotFound
		case err == nil && m.Staged && len(m.StagedBy) == 1:
			// Only this session sees a memory it alone staged: forgetting it reaches no
			// other session.
			err = c.Store.Forget(name, c.Project)
		case err == nil:
			return c.forgetShared(m, quote, quoted)
		}
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
		if _, err := c.Store.GetAs(name, c.Project, c.Source); err != nil {
			if _, defErr := c.Store.GetDefinition(name); !errors.Is(err, ErrNotFound) || defErr != nil {
				return "", err
			}
			key, what = DefinitionUsageKey(name), "defined query"
		}
		user := outcome == EventHelped && what == "memory" && c.userQuoted(quote)
		if err := c.Store.appendUsage(usageLine{Name: key, Event: outcome, Quote: quote, User: user}); err != nil {
			return "", err
		}
		text := "Recorded that " + what + " " + name + " " + outcome + "."
		if m, err := c.Store.GetAs(name, c.Project, c.Source); user && err == nil && m.Staged {
			text += " The quote is a user's words, the evidence a staged memory needs: it goes live at the next check and enters the memory index at its next render."
		}
		return text, nil
	}
	return "", fmt.Errorf("unknown memory write %q", op)
}

// forgetShared is the forget of memory m that other sessions see (docs/brain-memory.md, decision
// 2): retired for every session on a user's words asking for it (quote, normalized), at most
// MaxForgetsPerQuote memories per quote; any other forget, a quote-less one and one quoting tool
// output or the model's own words among them, hides m in this session at once and is staged
// (Store.StageForget), which a later forget with the user's words confirms.
func (c Call) forgetShared(m Memory, quote string, quoted bool) (string, error) {
	reason := "no user's words ask for it"
	if quoted {
		reason = "the quote is not a user's words in this conversation"
		if c.userQuoted(quote) {
			n, err := c.Store.QuotedForgets(quote)
			if err != nil {
				return "", err
			}
			if n < MaxForgetsPerQuote {
				if err := c.Store.ForgetQuoted(m.Name, c.Project, quote); err != nil {
					return "", err
				}
				return "Forgot memory " + m.Name + ".", nil
			}
			reason = fmt.Sprintf("the quote already forgot %d memories, the most one quote forgets; a forget of more needs the user's words for each", MaxForgetsPerQuote)
		}
	}
	if err := c.Store.StageForget(m.Name, c.Project, c.Source); err != nil {
		return "", err
	}
	return fmt.Sprintf("Hid memory %s in this session and staged its forget: %s, so other sessions still see it. Forgetting it for every session needs words copied verbatim from a user message asking for it, quoted with the forget.", m.Name, reason), nil
}

func scopeNote(m Memory) string {
	if m.Project != "" {
		return " (this project)"
	}
	return ""
}

// stateNote marks a staged or pinned memory in Describe.
func stateNote(m Memory) string {
	switch {
	case m.Staged:
		return " (staged)"
	case m.Pinned:
		return " (pinned)"
	}
	return ""
}

// Describe renders a memory in full for the memory tool's om.Recall(name).
func Describe(m Memory) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%s]%s%s: %s\n", m.Name, m.Type, scopeNote(m), stateNote(m), m.Description)
	if m.Body != "" {
		b.WriteString("\n" + m.Body + "\n")
	}
	fmt.Fprintf(&b, "\nsaved %s, updated %s", m.Created.Format("2006-01-02"), m.Updated.Format("2006-01-02"))
	if len(m.Sources) > 0 {
		fmt.Fprintf(&b, ", sessions: %s", strings.Join(m.Sources, ", "))
	}
	return b.String()
}
