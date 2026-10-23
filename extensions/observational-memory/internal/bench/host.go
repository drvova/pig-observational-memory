package bench

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
)

// Host is an in-memory, single-branch PiG host for one benchmark question:
// the branch is the haystack replayed as Pi message entries, and the memory
// workers append their ledger entries to it.
type Host struct {
	mu      sync.Mutex
	entries []session.Entry
	nextID  int
	models  host.Models
	model   host.Model
	id      string
}

// NewHost creates a host over models, whose model is the session model.
func NewHost(sessionID string, models host.Models, sessionModel host.Model, entries []session.Entry) *Host {
	h := &Host{models: models, model: sessionModel, id: sessionID}
	for _, e := range entries {
		h.entries = append(h.entries, session.FromMap(e.ToMap()))
	}
	h.nextID = len(h.entries)
	return h
}

func (h *Host) newID() string {
	h.nextID++
	return fmt.Sprintf("%08x", h.nextID)
}

func (h *Host) leafID() any {
	if len(h.entries) == 0 {
		return nil
	}
	return h.entries[len(h.entries)-1].ID
}

// AppendMessage appends a Pi message entry and returns its id. timestampMs is
// both the entry's and the message's timestamp.
func (h *Host) AppendMessage(role, text string, timestampMs int64) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.newID()
	message := map[string]any{
		"role":      role,
		"content":   []any{map[string]any{"type": "text", "text": text}},
		"timestamp": float64(timestampMs),
	}
	if role == "assistant" {
		message["stopReason"] = "stop"
	}
	h.entries = append(h.entries, session.FromMap(map[string]any{
		"type": "message", "id": id, "parentId": h.leafID(), "timestamp": isoMillis(timestampMs), "message": message,
	}))
	return id
}

// Entries copies the branch.
func (h *Host) Entries() []session.Entry {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]session.Entry{}, h.entries...)
}

// GetBranch is the whole branch.
func (h *Host) GetBranch() ([]session.Entry, error) { return h.Entries(), nil }

// AppendEntry appends a custom (ledger) entry, stamped with the latest
// message's time so the branch stays chronological.
func (h *Host) AppendEntry(customType string, data any) error {
	decoded, err := jsonValue(data)
	if err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	timestamp := ""
	if len(h.entries) > 0 {
		timestamp = h.entries[len(h.entries)-1].Timestamp
	}
	h.entries = append(h.entries, session.FromMap(map[string]any{
		"type": "custom", "id": h.newID(), "parentId": h.leafID(), "timestamp": timestamp, "customType": customType, "data": decoded,
	}))
	return nil
}

func (h *Host) SessionID() string             { return h.id }
func (h *Host) SessionFile() string           { return "" }
func (h *Host) HasUI() bool                   { return false }
func (h *Host) Notify(string, string)         {}
func (h *Host) IsIdle() (bool, error)         { return true, nil }
func (h *Host) Compact(host.CompactCallbacks) {}

// GetContextUsage reports no provider usage, so the workers schedule on the
// raw-entry estimates, as for a session whose provider reports none.
func (h *Host) GetContextUsage() (*host.ContextUsage, error) { return nil, nil }
func (h *Host) Cwd() string                                  { return "" }
func (h *Host) SessionModel() host.Model                     { return h.model }
func (h *Host) Models() host.Models                          { return h.models }

// isoMillis renders epoch milliseconds as Pi's entry timestamp string.
func isoMillis(ms int64) string {
	return time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
}

// jsonValue round-trips v through JSON, as data read back from a session is.
func jsonValue(v any) (any, error) {
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}
