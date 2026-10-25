// Package debuglog writes the optional NDJSON diagnostics log.
package debuglog

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	sdk "github.com/MichaelKinsy/PiG/extensions/sdk"
)

// MaxBytes rotates the log once it reaches this size.
const MaxBytes = 10 * 1024 * 1024

// RelativePath is the shared log under the agent directory.
var RelativePath = filepath.Join("observational-memory", "debug.ndjson")

// SessionDirRelativePath holds per-session logs under the agent directory.
var SessionDirRelativePath = filepath.Join("observational-memory", "debug")

// Context is the logging scope carried on a context.Context.
type Context struct {
	Enabled     bool
	Cwd         string
	SessionID   string
	SessionFile string
	RunID       string
}

type key struct{}

// With derives a context whose logging scope is parent's updated by c (empty
// fields keep the parent's value; Enabled is always c's).
func With(parent context.Context, c Context) context.Context {
	if parent == nil {
		parent = context.Background()
	}
	merged := c
	if existing, ok := parent.Value(key{}).(Context); ok {
		if merged.Cwd == "" {
			merged.Cwd = existing.Cwd
		}
		if merged.SessionID == "" {
			merged.SessionID = existing.SessionID
		}
		if merged.SessionFile == "" {
			merged.SessionFile = existing.SessionFile
		}
		if merged.RunID == "" {
			merged.RunID = existing.RunID
		}
	}
	return context.WithValue(parent, key{}, merged)
}

// From reads the logging scope of ctx.
func From(ctx context.Context) (Context, bool) {
	if ctx == nil {
		return Context{}, false
	}
	c, ok := ctx.Value(key{}).(Context)
	return c, ok
}

// AgentDir resolves the directory the log lives under; tests replace it.
var AgentDir = func() (string, error) {
	if dir, err := sdk.AgentDir(); err == nil && dir != "" {
		return dir, nil
	}
	if env := os.Getenv("PI_CODING_AGENT_DIR"); env != "" {
		return env, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".pi", "agent"), nil
}

// Now is the clock; tests replace it.
var Now = time.Now

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)
var alnum = regexp.MustCompile(`[A-Za-z0-9]`)

// SafeSessionID is a session id as a file name, or "" when it has no usable
// characters.
func SafeSessionID(sessionID string) string {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return ""
	}
	sanitized := unsafeChars.ReplaceAllString(trimmed, "_")
	sanitized = strings.Trim(sanitized, "_")
	if len(sanitized) > 128 {
		sanitized = sanitized[:128]
	}
	if !alnum.MatchString(sanitized) {
		return ""
	}
	return sanitized
}

// RelativePathFor is the log path under the agent directory for a session.
func RelativePathFor(c Context) string {
	if safe := SafeSessionID(c.SessionID); safe != "" {
		return filepath.Join(SessionDirRelativePath, safe+".ndjson")
	}
	return RelativePath
}

type record struct {
	TS          string         `json:"ts"`
	Event       string         `json:"event"`
	Cwd         string         `json:"cwd,omitempty"`
	SessionID   string         `json:"sessionId,omitempty"`
	SessionFile string         `json:"sessionFile,omitempty"`
	RunID       string         `json:"runId,omitempty"`
	Data        map[string]any `json:"data"`
}

// RequestEvent is the event logged for every model request the extension makes, at its single
// stream call site (agents.Request): data carries the stage, the model, and the input and
// output tokens when the provider reported them. A request whose response failed logs its
// stage's stream_error event instead, with the same data; every request's event carries
// "request": true (IsRequest).
const RequestEvent = "model.request"

// IsRequest reports whether a logged event's data is a model request's (RequestEvent, or a
// failed request's stream_error event).
func IsRequest(data map[string]any) bool {
	request, _ := data["request"].(bool)
	return request
}

// Observer receives every event Log is given, whether or not the log is
// enabled, with its own copy of the event's data.
type Observer func(event string, data map[string]any)

var (
	observersMu  sync.Mutex
	observers    = map[int]Observer{}
	nextObserver int
)

// Subscribe adds an observer of every logged event, enabled or not, and
// returns the function that removes it. Tests use it to count what the
// extension does (testfix.CountRequests); production code adds none.
//
// Observers are process-wide, not scoped to a context: an observer sees the
// events of every goroutine, the background runs a test starts without a
// context of its own included, which is what makes a count of zero meaningful.
// The price is that a test observing events must not run in parallel with
// another test that logs (no t.Parallel) and must wait for the work it started.
func Subscribe(observer Observer) (unsubscribe func()) {
	observersMu.Lock()
	defer observersMu.Unlock()
	id := nextObserver
	nextObserver++
	observers[id] = observer
	return func() {
		observersMu.Lock()
		defer observersMu.Unlock()
		delete(observers, id)
	}
}

func notifyObservers(event string, data map[string]any) {
	observersMu.Lock()
	if len(observers) == 0 {
		observersMu.Unlock()
		return
	}
	current := make([]Observer, 0, len(observers))
	for _, observer := range observers {
		current = append(current, observer)
	}
	observersMu.Unlock()
	for _, observer := range current {
		func() {
			defer func() { _ = recover() }()
			observer(event, maps.Clone(data))
		}()
	}
}

// Log appends one event when the scope on ctx is enabled, and passes it to the
// observers (Subscribe) either way. It never fails.
func Log(ctx context.Context, event string, data map[string]any) {
	notifyObservers(event, data)
	c, ok := From(ctx)
	if !ok || !c.Enabled {
		return
	}
	defer func() { _ = recover() }()
	dir, err := AgentDir()
	if err != nil {
		return
	}
	path := filepath.Join(dir, RelativePathFor(c))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	rotateIfNeeded(path)
	if data == nil {
		data = map[string]any{}
	}
	line, err := json.Marshal(record{
		TS:          Now().UTC().Format("2006-01-02T15:04:05.000Z07:00"),
		Event:       event,
		Cwd:         c.Cwd,
		SessionID:   c.SessionID,
		SessionFile: c.SessionFile,
		RunID:       c.RunID,
		Data:        data,
	})
	if err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(line, '\n'))
}

func rotateIfNeeded(path string) {
	info, err := os.Stat(path)
	if err != nil || info.Size() < MaxBytes {
		return
	}
	backup := path + ".1"
	// Rename over the old backup where the platform allows it, so a second
	// writer cannot slip between an unlink and the rename; fall back to
	// unlink-then-rename where it does not (Windows).
	if err := os.Rename(path, backup); err != nil {
		_ = os.Remove(backup)
		_ = os.Rename(path, backup)
	}
}
