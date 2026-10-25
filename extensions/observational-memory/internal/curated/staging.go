package curated

import (
	"fmt"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/serialize"
)

// Check is memory v2's one validator, run on every write and every load of a memory
// (docs/memory-v2.md, "Curated memory"): a valid name and type, a one-line description, staging
// sessions only on a staged memory, and the whole memory as recall <name> shows it (Describe)
// within what one recall shows (serialize.RecallPageChars), which bounds the body. The fields a
// file may hold are checked where it is read (Store.Get); pinned is the user's alone because
// no write takes it (Store.Save clears it, Store.Update keeps the stored value).
func Check(m Memory) error {
	if err := ValidateName(m.Name); err != nil {
		return err
	}
	if !ValidType(m.Type) {
		return fmt.Errorf("memory type %q must be one of %s", m.Type, strings.Join(Types, ", "))
	}
	if strings.TrimSpace(m.Description) == "" || oneLine(m.Description) != m.Description {
		return fmt.Errorf("memory %q needs a one-line description", m.Name)
	}
	if !m.Staged && len(m.StagedBy) > 0 {
		return fmt.Errorf("memory %q names staging sessions but is not staged", m.Name)
	}
	if n := utf8.RuneCountInString(Describe(m)); n > serialize.RecallPageChars {
		return fmt.Errorf("memory %q is %d characters as recall shows it, and one recall shows at most %d: shorten the body", m.Name, n, serialize.RecallPageChars)
	}
	return nil
}

// sameDescription reports whether two descriptions are the same with whitespace and case
// normalized: the test for a save staged again in another session.
func sameDescription(a, b string) bool {
	return strings.EqualFold(oneLine(a), oneLine(b))
}

// Correction is a level-0 user correction on the session's branch, as Settle matches it
// against the memories: its record id, subject and time.
type Correction struct {
	ID, Subject string
	Time        time.Time
}

// SettleInput is what one Settle reads besides the store.
type SettleInput struct {
	// Project is the project the session runs in: corrections flag only the memories that
	// apply in it.
	Project string
	// Corrections are the level-0 user corrections on the session's branch.
	Corrections []Correction
}

// Settled is what one Settle changed.
type Settled struct {
	// Promoted are the staged memories made live, each with its evidence.
	Promoted []Promotion
	// Flagged are the memories a correction newly flagged.
	Flagged []string
}

// Promotion is one staged memory made live and the evidence for it.
type Promotion struct {
	Name, Evidence string
}

// The evidence a staged memory is promoted on (Promotion.Evidence).
const (
	EvidenceUserQuote = "a user's words quoted in feedback"
	EvidenceCitation  = "a compaction's citation"
	EvidenceSessions  = "the same save staged in another session"
)

// Settle is memory v2's evidence check (docs/memory-v2.md, "Curated memory"), made by code at
// agent_settled, session_start and after level 1, with no request. A staged memory is promoted
// on evidence recorded since it was saved: a helped feedback whose quote is a user's words
// (Call.UserEvidence), a level-1 citation (EventCited), or the same name and description staged
// in another session (Memory.StagedBy). A staged memory is never deleted. A correction whose
// subject matches a memory (SubjectMatches) and that was made after the memory was last saved
// or edited records one corrected event per correction, which ranks the memory after the
// others and flags it until it is edited; a pinned memory is never flagged, and nothing cools
// by time.
func (s Store) Settle(in SettleInput) (Settled, error) {
	var out Settled
	events, err := s.UsageEventsByName()
	if err != nil {
		return out, err
	}
	all, err := s.listAll()
	if err != nil {
		return out, err
	}
	for _, m := range all {
		if m.Staged {
			if evidence := promotionEvidence(m, events[m.Name]); evidence != "" {
				promoted, err := s.promote(m.Name, evidence)
				if err != nil {
					return out, err
				}
				if promoted {
					out.Promoted = append(out.Promoted, Promotion{Name: m.Name, Evidence: evidence})
				}
			}
		}
		if m.Pinned || !Visible(m, in.Project) {
			continue
		}
		recorded := map[string]bool{}
		for _, e := range events[m.Name] {
			if e.Event == EventCorrected {
				recorded[e.Record] = true
			}
		}
		flagged := false
		for _, c := range in.Corrections {
			if recorded[c.ID] || !c.Time.After(m.Updated) || !SubjectMatches(c.Subject, m.Name) {
				continue
			}
			recorded[c.ID] = true
			if err := s.appendUsage(usageLine{Name: m.Name, Event: EventCorrected, Record: c.ID}); err != nil {
				return out, err
			}
			flagged = true
		}
		if flagged {
			out.Flagged = append(out.Flagged, m.Name)
		}
	}
	return out, nil
}

// RecordCited records a level-1 citation of the memory name, resting on records (their ids):
// evidence for a staged memory that the next Settle reads.
func (s Store) RecordCited(name, records string) error {
	return s.appendUsage(usageLine{Name: name, Event: EventCited, Record: records})
}

// promotionEvidence is the evidence for promoting staged memory m, read from its usage events
// (in ledger order) since its content last changed: since it was saved, and after its last
// save or edit, so evidence given for one text never promotes another; "" when there is none.
func promotionEvidence(m Memory, events []UsageEvent) string {
	evidence := ""
	for _, e := range events {
		if e.Time.Before(m.Created) {
			continue
		}
		switch {
		case e.Event == EventSave || e.Event == EventEdit:
			evidence = ""
		case evidence != "":
		case e.Event == EventHelped && e.User:
			evidence = EvidenceUserQuote
		case e.Event == EventCited:
			evidence = EvidenceCitation
		}
	}
	if evidence == "" && len(m.StagedBy) >= 2 {
		return EvidenceSessions
	}
	return evidence
}

// promote makes the staged memory name live; false when it is no longer staged.
func (s Store) promote(name, evidence string) (bool, error) {
	promoted := false
	err := s.locked(func() error {
		m, err := s.Get(name)
		if err != nil || !m.Staged || !m.Retired.IsZero() {
			return err
		}
		m.Staged, m.StagedBy = false, nil
		promoted = true
		return s.writeChecked(m)
	})
	if err != nil || !promoted {
		return false, err
	}
	return true, s.appendUsage(usageLine{Name: name, Event: EventPromoted, Record: evidence})
}

// listAll is every readable, unretired memory in the store, whatever its project, in name
// order.
func (s Store) listAll() ([]Memory, error) {
	names, err := s.fileNames()
	if err != nil {
		return nil, err
	}
	out := []Memory{}
	for _, name := range names {
		if m, err := s.Get(name); err == nil && m.Retired.IsZero() {
			out = append(out, m)
		}
	}
	return out, nil
}

// SubjectMatches reports whether a correction's subject names the memory name: the subject's
// words, joined by hyphens, are the name or a whole hyphen-separated run of it, or the name is
// such a run of the subject. An empty subject matches nothing.
func SubjectMatches(subject, name string) bool {
	words := strings.FieldsFunc(strings.ToLower(subject), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	if len(words) == 0 {
		return false
	}
	parts := strings.Split(name, "-")
	return containsRun(parts, words) || containsRun(words, parts)
}

// containsRun reports whether run occurs in list as consecutive items.
func containsRun(list, run []string) bool {
	for i := 0; i+len(run) <= len(list); i++ {
		if slices.Equal(list[i:i+len(run)], run) {
			return true
		}
	}
	return false
}

// Flags are the memories a correction flagged since they were last saved or edited, from the
// usage ledger (Evidence.Corrected).
func (s Store) Flags() (map[string]bool, error) {
	usage, err := s.Usage()
	if err != nil {
		return nil, err
	}
	out := map[string]bool{}
	for name, e := range usage {
		if e.Corrected > 0 {
			out[name] = true
		}
	}
	return out, nil
}
