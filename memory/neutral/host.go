package neutral

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/session"
)

// Transcript is a harness's conversation as Pi-shaped entries (memory/transcript reads each
// harness's format).
type Transcript interface {
	Entries() ([]session.Entry, error)
}

// ErrNoCompact is what a request to compact answers: the harness compacts on its own
// schedule, and the engine only answers when it does (Host.Compaction).
var ErrNoCompact = errors.New("neutral: the harness owns compaction")

// Host is host.ToolHost over a harness transcript and the engine's Store. The harness hands
// the engine no model, no context usage and no UI, so the engine runs code only: level 0,
// selection, the memory tool; level 1 and the proactive compaction trigger are skipped.
type Host struct {
	Session string
	Dir     string
	// Root is the engine's config root (the curated store and the usage ledgers), "" for none.
	Root       string
	Transcript Transcript
	Store      Store
	// Log receives notifications (there is no UI); nil discards them.
	Log io.Writer
	// Now is the clock entries are stamped with; nil is time.Now.
	Now func() time.Time
}

var _ host.ToolHost = (*Host)(nil)
var _ host.ConfigHomer = (*Host)(nil)
var _ host.SessionFileReader = (*Host)(nil)

func (h *Host) now() time.Time {
	if h.Now != nil {
		return h.Now()
	}
	return time.Now()
}

// stored reads the transcript and the store.
func (h *Host) read() ([]session.Entry, error) {
	var transcript []session.Entry
	if h.Transcript != nil {
		t, err := h.Transcript.Entries()
		if err != nil {
			return nil, err
		}
		transcript = t
	}
	stored, err := h.Store.Load()
	if err != nil {
		return nil, err
	}
	return Branch(transcript, stored), nil
}

// GetBranch is the merged branch (Branch).
func (h *Host) GetBranch() ([]session.Entry, error) { return h.read() }

// GetEntries is the branch too: a harness transcript has no off-path entries.
func (h *Host) GetEntries() ([]session.Entry, error) { return h.read() }

// AppendEntry stores a custom entry after the branch's tip.
func (h *Host) AppendEntry(customType string, data any) error {
	_, err := h.appendEntry(map[string]any{"type": "custom", "customType": customType, "data": data})
	return err
}

// appendEntry stamps entry with an id and the time and stores it after the tip; it returns
// the id.
func (h *Host) appendEntry(entry map[string]any) (string, error) {
	branch, err := h.read()
	if err != nil {
		return "", err
	}
	tip := ""
	if len(branch) > 0 {
		tip = branch[len(branch)-1].ID
	}
	id, err := newID()
	if err != nil {
		return "", err
	}
	entry["id"], entry["parentId"] = id, tip
	entry["timestamp"] = h.now().UTC().Format("2006-01-02T15:04:05.000Z")
	return id, h.Store.Append(tip, entry)
}

// newID is an entry id the harness never uses ("om" and eight hex digits).
func newID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("neutral: entry id: %w", err)
	}
	return "om" + hex.EncodeToString(b), nil
}

func (h *Host) SessionID() string   { return h.Session }
func (h *Host) SessionFile() string { return h.Store.Path }
func (h *Host) Cwd() string         { return h.Dir }
func (h *Host) ConfigHome() string  { return h.Root }

// HasUI is false: a hook has no interface to show anything in.
func (h *Host) HasUI() bool { return false }

// Notify writes the message to Log.
func (h *Host) Notify(message, level string) {
	if h.Log != nil {
		fmt.Fprintf(h.Log, "om %s: %s\n", level, message)
	}
}

// IsIdle is true: hooks run between the harness's turns.
func (h *Host) IsIdle() (bool, error) { return true, nil }

// Compact fails: the harness owns compaction.
func (h *Host) Compact(callbacks host.CompactCallbacks) {
	if callbacks.OnError != nil {
		callbacks.OnError(ErrNoCompact)
	}
}

// GetContextUsage is unknown, so the engine's proactive trigger never runs.
func (h *Host) GetContextUsage() (*host.ContextUsage, error) { return nil, nil }

// ProjectTrusted is false: the engine reads only its global settings, never a project's.
func (h *Host) ProjectTrusted() (bool, error) { return false, nil }

// SessionModel is nil: the harness hands the engine no model, so level 1 is off.
func (h *Host) SessionModel() host.Model { return nil }

// Models is an empty registry.
func (h *Host) Models() host.Models { return noModels{} }

// The memory tool is served by the harness (MCP): the engine registers and activates nothing.
func (h *Host) AllTools() ([]host.ToolInfo, error)  { return nil, nil }
func (h *Host) ActiveTools() ([]string, error)      { return nil, nil }
func (h *Host) SetActiveTools(names []string) error { return nil }
func (h *Host) RegisterTool(spec host.ToolSpec) error {
	return nil
}

type noModels struct{}

func (noModels) Find(provider, id string) host.Model { return nil }
func (noModels) GetApiKeyAndHeaders(model host.Model) (host.Auth, error) {
	return host.Auth{Error: "no model registry"}, nil
}
func (noModels) IsUsingOAuth(model host.Model) bool                             { return false }
func (noModels) HasConfiguredAuth(model host.Model) bool                        { return false }
func (noModels) Refresh(ctx context.Context, options host.RefreshOptions) error { return nil }
func (noModels) Stream(model host.Model, request map[string]any) host.ModelStream {
	return nil
}
