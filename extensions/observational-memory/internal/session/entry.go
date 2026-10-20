// Package session models Pi session entries as this extension reads them.
//
// Entries arrive from the PiG host as decoded JSON objects (Pi's SessionEntry
// shape). Entry keeps the members the memory ledger reads and the raw object,
// so cut-point logic can inspect the exact Pi shape.
package session

import "encoding/json"

// Entry is one session entry on the current branch.
type Entry struct {
	Type      string
	ID        string
	ParentID  string
	Timestamp string
	// Message is the Pi message of a "message" entry (role, content, usage, …).
	Message map[string]any
	// Content is a custom_message's content: a string or a block array.
	Content    any
	CustomType string
	// Summary is a branch_summary's or compaction's summary.
	Summary any
	FromID  string
	// Data is a custom entry's payload as decoded JSON.
	Data any
	// Details is a compaction entry's details as decoded JSON.
	Details          any
	FirstKeptEntryID string
	// Raw is the decoded entry object this Entry was read from, or nil for an
	// entry built in code.
	Raw map[string]any
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

// FromMap reads one decoded entry object.
func FromMap(m map[string]any) Entry {
	e := Entry{
		Type:             str(m["type"]),
		ID:               str(m["id"]),
		ParentID:         str(m["parentId"]),
		Timestamp:        str(m["timestamp"]),
		CustomType:       str(m["customType"]),
		FromID:           str(m["fromId"]),
		FirstKeptEntryID: str(m["firstKeptEntryId"]),
		Raw:              m,
	}
	if msg, ok := m["message"].(map[string]any); ok {
		e.Message = msg
	}
	if v, ok := m["content"]; ok {
		e.Content = v
	}
	if v, ok := m["summary"]; ok {
		e.Summary = v
	}
	if v, ok := m["data"]; ok {
		e.Data = v
	}
	if v, ok := m["details"]; ok {
		e.Details = v
	}
	return e
}

// FromMaps reads a branch of decoded entry objects.
func FromMaps(maps []map[string]any) []Entry {
	entries := make([]Entry, 0, len(maps))
	for _, m := range maps {
		if m == nil {
			continue
		}
		entries = append(entries, FromMap(m))
	}
	return entries
}

// FromJSON decodes raw entry JSON objects.
func FromJSON(raw []json.RawMessage) ([]Entry, error) {
	entries := make([]Entry, 0, len(raw))
	for _, r := range raw {
		var m map[string]any
		if err := json.Unmarshal(r, &m); err != nil {
			return nil, err
		}
		if m == nil {
			continue
		}
		entries = append(entries, FromMap(m))
	}
	return entries, nil
}

// FromAny reads an entry list from a decoded JSON value ([]any of objects),
// as an event payload carries it.
func FromAny(v any) []Entry {
	switch list := v.(type) {
	case []map[string]any:
		return FromMaps(list)
	case []any:
		entries := make([]Entry, 0, len(list))
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				entries = append(entries, FromMap(m))
			}
		}
		return entries
	}
	return nil
}

// ToMap returns the entry as a decoded JSON object: Raw when the entry was
// read from one, else an object built from its fields.
func (e Entry) ToMap() map[string]any {
	if e.Raw != nil {
		return e.Raw
	}
	m := map[string]any{"type": e.Type, "id": e.ID}
	if e.ParentID != "" {
		m["parentId"] = e.ParentID
	}
	if e.Timestamp != "" {
		m["timestamp"] = e.Timestamp
	}
	if e.Message != nil {
		m["message"] = e.Message
	}
	if e.Content != nil {
		m["content"] = e.Content
	}
	if e.CustomType != "" {
		m["customType"] = e.CustomType
	}
	if e.Summary != nil {
		m["summary"] = e.Summary
	}
	if e.FromID != "" {
		m["fromId"] = e.FromID
	}
	if e.Data != nil {
		m["data"] = e.Data
	}
	if e.Details != nil {
		m["details"] = e.Details
	}
	if e.FirstKeptEntryID != "" {
		m["firstKeptEntryId"] = e.FirstKeptEntryID
	}
	return m
}

// SummaryString is the entry's summary when it is a string.
func (e Entry) SummaryString() (string, bool) {
	s, ok := e.Summary.(string)
	return s, ok
}

// MessageRole is the role of a "message" entry's message, or "".
func (e Entry) MessageRole() string {
	if e.Message == nil {
		return ""
	}
	return str(e.Message["role"])
}

// IndexByID maps entry ids to their branch index.
func IndexByID(entries []Entry) map[string]int {
	index := make(map[string]int, len(entries))
	for i, entry := range entries {
		index[entry.ID] = i
	}
	return index
}

// IndexOf is the branch index of id, or -1.
func IndexOf(entries []Entry, id string) int {
	if id == "" {
		return -1
	}
	for i, entry := range entries {
		if entry.ID == id {
			return i
		}
	}
	return -1
}
