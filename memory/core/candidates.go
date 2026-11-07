package core

import (
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/serialize"
)

// Session is what a session's records are offered to the core with: its project scope, its
// writer, the generation it read the core through, and the one-time test (extract.IsOneTime).
type Session struct {
	Scope   string
	Writer  Writer
	Base    int
	OneTime func(string) bool
}

// recordScope is the core scope of a record: its own when it is a core scope, else the session's;
// a level-1 merge takes the intersection of its sources' scopes (decision 8).
func recordScope(o ledger.Observation, byID map[string]ledger.Observation, session string) string {
	own := func(o ledger.Observation) string {
		if o.Scope == ScopeUser || o.Scope == ScopeGlobal || strings.HasPrefix(o.Scope, ScopeProjectPrefix) {
			return o.Scope
		}
		return session
	}
	if len(o.Replaces) < 2 {
		return own(o)
	}
	scopes := []string{}
	for _, id := range o.Replaces {
		if src, ok := byID[id]; ok {
			scopes = append(scopes, own(src))
		} else {
			scopes = append(scopes, session)
		}
	}
	return Intersect(scopes...)
}

// untrusted reports a record whose source is no person's words nor the assistant's: tool output,
// another session's message, external text.
func untrusted(o ledger.Observation) bool {
	switch o.Source {
	case ledger.SourceToolObserved, ledger.SourceOtherSession, ledger.SourceExternalUntrusted, ledger.SourceHostSummary:
		return true
	}
	return false
}

// Candidates are the records of a session's ledger fold offered to the core (decision 16: the
// user's constraints and the open intentions), in ledger order: a believed rule (a preference)
// and a believed plan no receipt closed. A rule the user said, or a plan the user said, is backed
// by the user's words; a plan the assistant made under a request the person typed by that
// request; tool output never backs one (ToolOnly), and a one-time permission is no standing rule.
func Candidates(records []ledger.Observation, labels map[string]string, closed map[string]bool, s Session) []Candidate {
	byID := map[string]ledger.Observation{}
	for _, o := range records {
		byID[o.ID] = o
	}
	var out []Candidate
	for _, o := range records {
		if labels[o.ID] != ledger.LabelBelieved || o.Kind == ledger.KindForget {
			continue
		}
		c := Candidate{Text: o.Content, Record: o.ID, Base: s.Base, Writer: s.Writer, ToolOnly: untrusted(o)}
		if o.Writer.Model != "" {
			c.Writer.Model = o.Writer.Model
		}
		if o.Writer.Session != "" {
			c.Writer.Session = o.Writer.Session
		}
		switch {
		case o.Kind == "preference" && o.Status == "":
			c.Kind, c.Key = KindRule, o.Subject
			if o.Source == ledger.SourceUserSaid || o.FromUser {
				c.Via = ViaQuote
			}
		case o.Status == ledger.StatusProposed && !closed[o.ID]:
			c.Kind = KindIntention
			switch {
			case o.Source == ledger.SourceUserSaid:
				c.Via = ViaQuote
			case o.Source == ledger.SourceAssistantClaim && o.DerivedFrom != "":
				c.Via = ViaRequest
			}
		default:
			continue
		}
		if c.Via == ViaQuote && len(o.SourceEntryIDs) > 0 {
			c.Roots = []string{UserRoot(o.SourceEntryIDs[0], o.Content)}
		}
		if c.Via == ViaQuote && Restating(o) {
			c.Said = o.Timestamp
		}
		c.Scope = recordScope(o, byID, s.Scope)
		if s.OneTime != nil && s.OneTime(o.Content) {
			c.OneTime = true
		}
		out = append(out, c)
	}
	return out
}

// Restating reports a record that may restate a forgotten core item (Decide, Restates): one
// level 0 wrote from the words the person typed (a user entry, never a custom message, tool
// output or another session's text), restating as the pushback contest reads a restatement
// (ledger.Restater: the user's own, not a retraction nor a concession; ledger.RestatesClaim of
// itself: it names keys or carries specifics). A level-1 record never does.
func Restating(o ledger.Observation) bool {
	return o.Origin == ledger.OriginCode && o.Source == ledger.SourceUserSaid && len(o.SourceEntryIDs) > 0 &&
		ledger.Restater(o) && ledger.RestatesClaim(o, o)
}

// forgetTime is a forget record's time (serialize.ParseTimestamp) as a core line carries it
// (RFC 3339, UTC, whole seconds), "" when unreadable (the commit's time is then used).
func forgetTime(stamp string) string {
	t, ok := serialize.ParseTimestamp(stamp)
	if !ok {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// ForgetLine is the core line a forget record carries across sessions (Phase 5's tombstones):
// for a forget of the project or the user, the forgotten records' ids and the ids of the items
// they would be in the core as (each kind, in the forget's scopes), never their text, at the time
// of the forget record (forgetTime), which a restatement must come after; ok is false
// for a forget of the session alone or one naming nothing.
func ForgetLine(forget ledger.Observation, forgotten []ledger.Observation, s Session) (Line, bool) {
	var scopes []string
	switch {
	case forget.Scope == ledger.ForgetScopeUser || forget.Scope == ScopeUser:
		scopes = []string{s.Scope, ScopeUser}
	case forget.Scope == ledger.ForgetScopeProject || strings.HasPrefix(forget.Scope, ScopeProjectPrefix):
		scopes = []string{s.Scope}
	default:
		return Line{}, false
	}
	var ids []string
	addID := func(id string) {
		if id == "" {
			return
		}
		for _, have := range ids {
			if have == id {
				return
			}
		}
		ids = append(ids, id)
	}
	for _, t := range forget.Forgets {
		addID(t.ID)
	}
	for _, o := range forgotten {
		addID(o.ID)
		for _, scope := range scopes {
			if scope == "" {
				continue
			}
			addID(ItemID(KindRule, scope, NormKey(keyOr(o.Subject, o.Content)), o.Content))
			addID(ItemID(KindIntention, scope, NormKey(o.Content), o.Content))
		}
	}
	if len(ids) == 0 {
		return Line{}, false
	}
	var writer *Writer
	if s.Writer != (Writer{}) {
		w := s.Writer
		writer = &w
	}
	return Line{Op: OpForget, ID: forget.ID, Scope: scopes[len(scopes)-1], Tombstones: ids, Base: s.Base, Writer: writer, Time: forgetTime(forget.Timestamp)}, true
}

func keyOr(key, text string) string {
	if strings.TrimSpace(key) != "" {
		return key
	}
	return text
}
