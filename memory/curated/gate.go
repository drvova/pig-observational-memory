package curated

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/drvova/pig-observational-memory/memory/secrets"
)

// The quote gate on changes to shared memory (docs/brain-memory.md, decision 2): a forget or an
// edit of a curated memory other sessions see, and a forget or rollback of a model's layer,
// reaches every session only on a user's words in this session asking for it, quoted with the
// change, and one quote confirms at most MaxForgetsPerQuote changes. Without them a curated
// change applies in its own session only (hidden or overridden there at once) and is staged,
// and a layer change is staged and changes nothing; /om:status lists every staged change
// (StagedChanges). Code evidence (Store.Forget, Store.Update) is not gated.

// The usage ledger's events of the gate.
const (
	// EventEditStaged is an edit with no user's words of a memory other sessions see: it
	// overrides the memory in the session that made it (PendingRevision) and leaves it shared.
	EventEditStaged = "edit-staged"
	// EventLayerForget and EventLayerRollback are a layer forget or rollback a user's words
	// confirmed (their quote kept), recorded under LayerKey or LayerRollbackKey with the model.
	EventLayerForget   = "layer-forget"
	EventLayerRollback = "layer-rollback"
	// EventLayerForgetStaged and EventLayerRollbackStaged are a layer forget or rollback with no
	// user's words: staged, the layer unchanged.
	EventLayerForgetStaged   = "layer-forget-staged"
	EventLayerRollbackStaged = "layer-rollback-staged"
)

// quotedChangeEvents are the events whose user quote counts toward MaxForgetsPerQuote.
var quotedChangeEvents = map[string]bool{EventForget: true, EventEdit: true, EventLayerForget: true, EventLayerRollback: true}

// LayerKey is the usage ledger name a layer change of kind/name is recorded under, apart from
// any memory's (a memory name holds no colon).
func LayerKey(kind, name string) string { return "layer:" + kind + "/" + name }

// LayerRollbackKey is the usage ledger name a layer rollback is recorded under; its record is
// the target generation.
const LayerRollbackKey = "layer:rollback"

// Revision is a staged edit's content: the memory's type, description and body as the edit made
// them in its session.
type Revision struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Body        string `json:"body,omitempty"`
}

// PendingRevision is the edit session staged of m and still stands for it: the last
// EventEditStaged of session since m was saved, unless a save or an edit of m for every session
// came after it. events are m's usage events, in ledger order.
func PendingRevision(m Memory, events []UsageEvent, session string) (Revision, bool) {
	var rev *Revision
	if session == "" {
		return Revision{}, false
	}
	for _, e := range events {
		if e.Time.Before(m.Created) {
			continue
		}
		switch e.Event {
		case EventSave, EventEdit:
			rev = nil
		case EventEditStaged:
			if e.Session == session && e.Revision != nil {
				rev = e.Revision
			}
		}
	}
	if rev == nil {
		return Revision{}, false
	}
	return *rev, true
}

// withRevision is m as session sees it: with the edit it staged (PendingRevision) applied.
func withRevision(m Memory, events []UsageEvent, session string) Memory {
	if rev, ok := PendingRevision(m, events, session); ok {
		m.Type, m.Description, m.Body = rev.Type, rev.Description, rev.Body
	}
	return m
}

// StageEdit records an edit of the memory name that has no user's words: session sees the
// memory as the edit leaves it at once (GetAs, ListAs), every other session as it was, and a
// later edit with a user's words changes it for every session. The edit applies to what session
// sees, its earlier staged edit included, and the result passes Check.
func (s Store) StageEdit(name, project, session string, edit Edit) (Memory, error) {
	m, err := s.GetAs(name, project, session)
	if err != nil {
		return Memory{}, err
	}
	if m, err = applyEdit(m, edit, session); err != nil {
		return Memory{}, err
	}
	m = redactMemory(m)
	if err := Check(m); err != nil {
		return Memory{}, err
	}
	rev := Revision{Type: m.Type, Description: m.Description, Body: m.Body}
	return m, s.appendUsage(usageLine{Name: name, Event: EventEditStaged, Session: session, Revision: &rev})
}

// RecordLayerChange records a layer change under key (LayerKey, LayerRollbackKey) for model:
// one a user's words confirmed (EventLayerForget, EventLayerRollback, with quote) or one staged
// by session (EventLayerForgetStaged, EventLayerRollbackStaged). target is a rollback's target
// generation, -1 for a forget.
func (s Store) RecordLayerChange(event, key, model, session, quote string, target int) error {
	line := usageLine{Name: key, Event: event, Model: model, Session: session, Quote: quote, User: quote != ""}
	if target >= 0 {
		line.Record = strconv.Itoa(target)
	}
	return s.appendUsage(line)
}

// Covers reports whether quote (normalized; quoted false when none was given) is a user's words
// that may confirm one more change to shared memory: a user's words in this conversation
// (UserEvidence) that have confirmed fewer than MaxForgetsPerQuote changes (QuotedChanges).
// When it does not, reason says why, for the staged change's text.
func (c Call) Covers(quote string, quoted bool) (bool, string, error) {
	if !quoted {
		return false, "no user's words ask for it", nil
	}
	if !c.userQuoted(quote) {
		return false, "the quote is not a user's words in this conversation", nil
	}
	n, err := c.Store.QuotedChanges(quote)
	if err != nil {
		return false, "", err
	}
	if n >= MaxForgetsPerQuote {
		return false, fmt.Sprintf("the quote already confirmed %d changes, the most one quote confirms; more changes need the user's words for each", MaxForgetsPerQuote), nil
	}
	return true, "", nil
}

// GateQuote is a change's quote as the gate reads it: normalized (NormalizeQuote), with what
// names the change in the error of one too short; quoted is false when none was given.
func GateQuote(args map[string]string, what string) (quote string, quoted bool, err error) {
	quote, quoted = args["quote"]
	if !quoted {
		return "", false, nil
	}
	normalized, err := NormalizeQuote(quote)
	if err != nil {
		return "", false, fmt.Errorf("%s", strings.Replace(err.Error(), "feedback needs", what+"'s quote needs", 1))
	}
	return normalized, true, nil
}

// StagedChange is one change to shared memory staged until a user's words confirm it, as
// /om:status lists it.
type StagedChange struct {
	// Op is forget, edit, layer forget or layer rollback.
	Op string
	// Name is the memory's name, a layer definition's kind/name, or "" for a rollback.
	Name    string
	Session string
	// Model is a layer change's model; Detail is a staged edit's description or a rollback's
	// target generation.
	Model, Detail string
}

// String is the change as /om:status lists it.
func (c StagedChange) String() string {
	switch c.Op {
	case "edit":
		return "edit " + c.Name + " (" + c.Session + "): " + c.Detail
	case "layer forget":
		return "layer forget " + c.Model + " " + c.Name + " (" + c.Session + ")"
	case "layer rollback":
		return "layer rollback " + c.Model + " to generation " + c.Detail + " (" + c.Session + ")"
	}
	return c.Op + " " + c.Name + " (" + c.Session + ")"
}

// StagedChanges lists the staged changes still standing: the staged forgets (HiddenIn) and edits
// (PendingRevision) of the memories that apply in project, by memory name and then in ledger
// order, and then the layer's staged forgets and rollbacks that no confirmed one of the same
// definition (or, for a rollback, of the same model) came after, in ledger order, each once.
func (s Store) StagedChanges(project string) ([]StagedChange, error) {
	memories, err := s.List(project)
	if err != nil {
		return nil, err
	}
	events, err := s.UsageEventsByName()
	if err != nil {
		return nil, err
	}
	out := []StagedChange{}
	for _, m := range memories {
		seen := map[string]bool{}
		for _, e := range events[m.Name] {
			if e.Session == "" || e.Time.Before(m.Created) {
				continue
			}
			switch e.Event {
			case EventForgetStaged:
				if !seen["forget\x00"+e.Session] {
					seen["forget\x00"+e.Session] = true
					out = append(out, StagedChange{Op: "forget", Name: m.Name, Session: e.Session})
				}
			case EventEditStaged:
				if seen["edit\x00"+e.Session] {
					continue
				}
				seen["edit\x00"+e.Session] = true
				if rev, ok := PendingRevision(m, events[m.Name], e.Session); ok {
					out = append(out, StagedChange{Op: "edit", Name: m.Name, Session: e.Session, Detail: secrets.Redact(rev.Description)})
				}
			}
		}
	}
	lines, err := s.readUsageLines()
	if err != nil {
		return nil, err
	}
	var layer []StagedChange
	for _, line := range lines {
		switch line.Event {
		case EventLayerForgetStaged, EventLayerRollbackStaged:
			c := StagedChange{Op: "layer forget", Name: strings.TrimPrefix(line.Name, "layer:"), Session: line.Session, Model: line.Model}
			if line.Event == EventLayerRollbackStaged {
				c = StagedChange{Op: "layer rollback", Session: line.Session, Model: line.Model, Detail: line.Record}
			}
			dup := false
			for _, have := range layer {
				dup = dup || have == c
			}
			if !dup {
				layer = append(layer, c)
			}
		case EventLayerForget, EventLayerRollback:
			kept := layer[:0]
			for _, have := range layer {
				confirmed := have.Model == line.Model && (line.Event == EventLayerRollback && have.Op == "layer rollback" ||
					line.Event == EventLayerForget && have.Op == "layer forget" && "layer:"+have.Name == line.Name)
				if !confirmed {
					kept = append(kept, have)
				}
			}
			layer = kept
		}
	}
	return append(out, layer...), nil
}
