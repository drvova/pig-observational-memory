// Package cli is the om command (docs/universal.md): the memory engine as one binary that any
// coding-agent harness drives through its hooks, its MCP client or a plugin, with one store
// under the engine's config root.
package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/drvova/pig-observational-memory/memory/gitrepo"
	"github.com/drvova/pig-observational-memory/memory/hostinfo"
	"github.com/drvova/pig-observational-memory/memory/neutral"
	"github.com/drvova/pig-observational-memory/memory/transcript"
)

// RootEnv names the environment variable that moves the config root.
const RootEnv = "OM_HOME"

// Root is the engine's config root: $OM_HOME, else ~/.om. Its settings.json holds the
// engine's settings (docs/configuration.md), as Pi's agent directory does for the PiG extension.
func Root() (string, error) {
	if dir := os.Getenv(RootEnv); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("om: no home directory: %w", err)
	}
	return filepath.Join(home, ".om"), nil
}

// useRoot points the engine's settings and debug log at root.
func useRoot(root string) {
	hostinfo.AgentDir = func() (string, error) { return root, nil }
	hostinfo.ConfigDirName = func() (string, error) { return ".om", nil }
}

// Session is one harness session the engine works on: which harness, its id, where its
// transcript is and in what format, and its working directory.
type Session struct {
	Harness    string `json:"harness"`
	ID         string `json:"sessionId"`
	Transcript string `json:"transcriptPath"`
	Format     string `json:"format"`
	Cwd        string `json:"cwd"`
	Updated    string `json:"updated,omitempty"`
}

// Host is the neutral host over s under root, notifications going to log.
func (s Session) Host(root string, log io.Writer) (*neutral.Host, error) {
	if s.ID == "" {
		return nil, errors.New("om: no session id")
	}
	var source neutral.Transcript
	if kind, ok := transcript.DBFormats[s.Format]; ok {
		source = transcript.DB{Path: s.Transcript, Session: s.ID, Kind: kind}
	} else if format, ok := transcript.Formats[s.Format]; ok {
		source = transcript.File{Path: s.Transcript, Format: format}
	} else {
		return nil, fmt.Errorf("om: unknown transcript format %q", s.Format)
	}
	return &neutral.Host{
		Session:    s.ID,
		Dir:        s.Cwd,
		Root:       root,
		Transcript: source,
		Store:      neutral.StoreFor(root, s.Harness, s.ID),
		Log:        log,
		// The adapter side reads the repository, so the engine never runs git itself.
		HarnessName:   s.Harness,
		ReadRepo:      gitrepo.Reader{Store: root}.Read,
		ReadStructure: gitrepo.Structure,
	}, nil
}

// pointerPath is where the current session of a harness in a directory is recorded: the MCP
// server the harness starts per project learns from it which session a memory call reads.
func pointerPath(root, harness, cwd string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(cwd)))
	return filepath.Join(root, "current", harness, hex.EncodeToString(sum[:8])+".json")
}

// remember records s as its harness's current session in its directory.
func remember(root string, s Session) error {
	s.Updated = time.Now().UTC().Format(time.RFC3339)
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	p := pointerPath(root, s.Harness, s.Cwd)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// current is the harness's current session in cwd (remember), false when none is recorded.
func current(root, harness, cwd string) (Session, bool) {
	data, err := os.ReadFile(pointerPath(root, harness, cwd))
	if err != nil {
		return Session{}, false
	}
	var s Session
	if json.Unmarshal(data, &s) != nil || s.ID == "" {
		return Session{}, false
	}
	return s, true
}
