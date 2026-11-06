package curated

import (
	"time"

	"github.com/drvova/pig-observational-memory/memory/insecure"
)

// EventReleased is a user's words in one session restating a held memory (HeldRule): the memory
// is shown in that session, and only there, until it is next edited (Released).
const EventReleased = "released"

// HeldRule is the rule of the security-weakening pattern list (insecure.Match;
// docs/brain-memory.md, decision 19) a memory's description or body matches, "" for none. A
// held memory is kept and listed in /om:status, but left out of the index, saved and the
// level-1 input, and never promoted, except in a session that released it (Released).
func HeldRule(m Memory) string {
	rule, _ := insecure.Match(m.Description + "\n" + m.Body)
	return rule
}

// Released reports whether session released the held memory m: a user's words of that session,
// quoted by a save or a helped feedback, restate a preference that weakens security
// (EventReleased) since m was last saved or edited. events are m's usage events.
func Released(m Memory, events []UsageEvent, session string) bool {
	if session == "" {
		return false
	}
	for _, e := range events {
		if e.Event == EventReleased && e.Session == session && !e.Time.Before(m.Updated.Truncate(time.Second)) {
			return true
		}
	}
	return false
}

// ShownAs are memories without the held ones session did not release (HeldRule, Released), in
// order; events are the usage events by name (Store.UsageEventsByName).
func ShownAs(memories []Memory, events map[string][]UsageEvent, session string) []Memory {
	out := make([]Memory, 0, len(memories))
	for _, m := range memories {
		if HeldRule(m) == "" || Released(m, events[m.Name], session) {
			out = append(out, m)
		}
	}
	return out
}

// Shown is ShownAs over the store's usage ledger; an unreadable ledger releases nothing.
func (s Store) Shown(memories []Memory, session string) []Memory {
	events, err := s.UsageEventsByName()
	if err != nil {
		events = nil
	}
	return ShownAs(memories, events, session)
}

// restates reports whether a user's quote restates a preference that weakens security: it
// matches the pattern list itself, so words that merely mention the memory release nothing.
func restates(quote string) bool {
	_, ok := insecure.Match(quote)
	return ok
}

// release records that session's user words (quote) restate the held memory name.
func (s Store) release(name, session, quote string) error {
	return s.appendUsage(usageLine{Name: name, Event: EventReleased, Quote: quote, User: true, Session: session})
}

// HeldMemory is a held memory as /om:status lists it.
type HeldMemory struct {
	Name, Type, Description, Rule string
}

// Held lists the held memories that apply in project, staged ones included, in name order.
func (s Store) Held(project string) ([]HeldMemory, error) {
	listed, err := s.List(project)
	if err != nil {
		return nil, err
	}
	out := []HeldMemory{}
	for _, m := range listed {
		if rule := HeldRule(m); rule != "" {
			out = append(out, HeldMemory{Name: m.Name, Type: m.Type, Description: m.Description, Rule: rule})
		}
	}
	return out, nil
}
