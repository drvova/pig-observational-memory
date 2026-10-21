// Package debuglog writes the optional NDJSON diagnostics log.
package debuglog

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
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

// Log appends one event when the scope on ctx is enabled. It never fails.
func Log(ctx context.Context, event string, data map[string]any) {
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
