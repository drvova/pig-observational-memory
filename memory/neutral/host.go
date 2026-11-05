package neutral

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
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

	// The parsed transcript and store, kept while their files are unchanged (Versioned), so a
	// hook that appends many entries reads each file once rather than once per append.
	mu                sync.Mutex
	transcript        []session.Entry
	transcriptVersion string
	stored            []Stored
	storeVersion      string
}

// Versioned is a transcript that can say when it changed: a value that differs whenever its
// content may have (a file's size and modification time).
type Versioned interface {
	Version() string
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

// read is the merged branch of the transcript and the store, each parsed again only when its
// file changed.
func (h *Host) read() ([]session.Entry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var transcript []session.Entry
	if h.Transcript != nil {
		v, versioned := h.Transcript.(Versioned)
		version := ""
		if versioned {
			version = v.Version()
		}
		if versioned && version != "" && version == h.transcriptVersion {
			transcript = h.transcript
		} else {
			t, err := h.Transcript.Entries()
			if err != nil {
				return nil, err
			}
			transcript, h.transcript, h.transcriptVersion = t, t, version
		}
	}
	if version := fileVersion(h.Store.Path); version == "" || version != h.storeVersion {
		stored, err := h.Store.Load()
		if err != nil {
			return nil, err
		}
		h.stored, h.storeVersion = stored, version
	}
	return Branch(transcript, h.stored), nil
}

// fileVersion is a file's size and modification time, "" when it cannot be read.
func fileVersion(path string) string {
	st, err := os.Stat(path)
	if err != nil {
		return ""
	}
	return fmt.Sprintf("%d:%d", st.Size(), st.ModTime().UnixNano())
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
	h.mu.Lock()
	defer h.mu.Unlock()
	before := fileVersion(h.Store.Path)
	if err := h.Store.Append(tip, entry); err != nil {
		return "", err
	}
	// The store grew by exactly this line unless another writer got in between: keep the cache
	// only when it was current before the append.
	if before == h.storeVersion {
		h.stored = append(h.stored, Stored{After: tip, Entry: entry})
		h.storeVersion = fileVersion(h.Store.Path)
	} else {
		h.storeVersion = ""
	}
	return id, nil
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

// Notify writes the message to Log: an info message as it is (a status the user asked for), any
// other level after "om <level>: ".
func (h *Host) Notify(message, level string) {
	if h.Log == nil {
		return
	}
	if level == "info" {
		fmt.Fprintln(h.Log, message)
		return
	}
	fmt.Fprintf(h.Log, "om %s: %s\n", level, message)
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
