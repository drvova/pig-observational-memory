// Package lispimage reaches the user's SBCL engine image (docs/lisp-port-image-client.md,
// docs/lisp-port-peer.md): one image per store root, listening on a unix socket, started on
// demand. The om CLI's Lisp mode (memory/cli) and the engine-rpc socket dialer
// (enginerpc.SocketDialer, the PiG adapter's "engine": "lisp") share it, so both start, wait
// for and find the image the same way. The image takes its own lock beside the socket (exit
// code 3 when another holds it), so two clients starting it at once end with one image.
package lispimage

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/memory/config"
)

// The environment the launcher reads.
const (
	// RootEnv moves the image's store root (default ~/.om).
	RootEnv = "OM_HOME"
	// ImageEnv is the image's executable (else the lispImage setting).
	ImageEnv = "OM_LISP_IMAGE"
	// SocketEnv moves the socket (default <root>/engine.sock).
	SocketEnv = "OM_ENGINE_SOCKET"
	// IdleEnv is how many seconds the image stays up with no client (else the lispIdleSeconds
	// setting, else 900; 0 never exits).
	IdleEnv = "OM_ENGINE_IDLE_SECONDS"
	// FallbackEnv set to 0 turns falling back to the Go engine off: an image that cannot be
	// started or reached, or a method it has not ported, is then an error.
	FallbackEnv = "OM_LISP_FALLBACK"
)

// DefaultIdleSeconds is how long an image the launcher starts stays up with no client.
const DefaultIdleSeconds = 900

// Launcher finds and starts the image for one store root.
type Launcher struct {
	// Root is the image's store root; Socket its socket.
	Root, Socket string
	// Image is the executable to start, "" when none is configured.
	Image string
	// Idle is the idle exit passed to an image the launcher starts.
	Idle int
	// StartTimeout is how long Start waits for a started image to listen.
	StartTimeout time.Duration
	// Auto is true when no image is configured: Image is then the build of the embedded sources
	// (<root>/engine/<Build>/om-engine), built on first use (Ensure), and Build its id.
	Auto  bool
	Build string
	// pruned is set once Ensure pruned old builds.
	pruned bool
}

// DefaultRoot is $OM_HOME, else ~/.om.
func DefaultRoot() (string, error) {
	if dir := os.Getenv(RootEnv); dir != "" {
		return dir, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("om: no home directory: %w", err)
	}
	return filepath.Join(home, ".om"), nil
}

// FromEnv is the launcher for root as the environment and root's settings.json set it: the
// image from OM_LISP_IMAGE, else the absolute lispImage setting, else the build of the embedded
// sources (Auto, built on first use; UseImage sets another); the socket from
// OM_ENGINE_SOCKET, else <root>/engine.sock; the idle exit from OM_ENGINE_IDLE_SECONDS, else
// lispIdleSeconds, else DefaultIdleSeconds.
func FromEnv(root string) *Launcher {
	l := &Launcher{Root: root, Socket: os.Getenv(SocketEnv), Image: os.Getenv(ImageEnv), Idle: -1, StartTimeout: 10 * time.Second}
	if l.Socket == "" {
		l.Socket = filepath.Join(root, "engine.sock")
	}
	if n, err := strconv.Atoi(os.Getenv(IdleEnv)); err == nil && n >= 0 {
		l.Idle = n
	}
	if raw, err := os.ReadFile(filepath.Join(root, "settings.json")); err == nil {
		var s map[string]map[string]any
		if json.Unmarshal(raw, &s) == nil {
			nested := s[config.SettingsKey]
			if p, _ := nested["lispImage"].(string); l.Image == "" && filepath.IsAbs(p) {
				l.Image = p
			}
			if n, ok := nested["lispIdleSeconds"].(float64); ok && l.Idle < 0 && n >= 0 {
				l.Idle = int(n)
			}
		}
	}
	if l.Idle < 0 {
		l.Idle = DefaultIdleSeconds
	}
	if l.Image == "" {
		l.Auto, l.Build = true, BuildID()
		l.Image = filepath.Join(l.BuildDir(), l.Build, ImageName)
	}
	return l
}

// UseImage makes path the image to start instead of the embedded build ("" keeps the launcher's).
func (l *Launcher) UseImage(path string) {
	if path == "" {
		return
	}
	l.Image, l.Auto, l.Build = path, false, ""
}

// FallbackOn reports whether falling back to the Go engine is on (OM_LISP_FALLBACK is not 0).
func FallbackOn() bool { return os.Getenv(FallbackEnv) != "0" }

// Dial connects to a listening image.
func (l *Launcher) Dial() (net.Conn, error) {
	return net.DialTimeout("unix", l.Socket, 2*time.Second)
}

// Connect dials the image, starting it when the socket is absent or refuses.
func (l *Launcher) Connect() (net.Conn, error) {
	if conn, err := l.Dial(); err == nil {
		return conn, nil
	}
	return l.Start()
}

// Start runs the image detached (its output appended to <root>/engine.log) and waits for it to
// listen, returning the connection. Another client may have started one at the same time: the
// loser exits on the image lock (code 3), and the socket the winner listens on is what this
// waits for.
func (l *Launcher) Start() (net.Conn, error) {
	if err := l.Ensure(); err != nil {
		return nil, err
	}
	if l.Image == "" {
		return nil, fmt.Errorf("om: no engine image listens on %s; set %s (or lispImage in %s)", l.Socket, ImageEnv, filepath.Join(l.Root, "settings.json"))
	}
	if err := os.MkdirAll(l.Root, 0o700); err != nil {
		return nil, err
	}
	log, err := os.OpenFile(filepath.Join(l.Root, "engine.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	defer log.Close()
	cmd := exec.Command(l.Image, "--root", l.Root, "--socket", l.Socket, "--idle-seconds", strconv.Itoa(l.Idle))
	cmd.Stdout, cmd.Stderr = log, log
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("om: starting the engine image %s: %w", l.Image, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	timeout := l.StartTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if conn, err := l.Dial(); err == nil {
			return conn, nil
		}
		select {
		case err := <-exited:
			// Exit 3 is the image lock: another image is starting; keep waiting for its socket.
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 3 {
				return nil, fmt.Errorf("om: the engine image exited before it listened (%v); see %s", err, filepath.Join(l.Root, "engine.log"))
			}
		case <-time.After(10 * time.Millisecond):
		}
	}
	return nil, fmt.Errorf("om: the engine image did not listen on %s within %s", l.Socket, timeout)
}

// LockPath is the image's lock beside its socket (engine.sock -> engine.lock), the file the image
// holds an exclusive flock on while it runs.
func (l *Launcher) LockPath() string {
	return strings.TrimSuffix(l.Socket, filepath.Ext(l.Socket)) + ".lock"
}

// WaitGone waits, at most timeout, until no image holds the lock: after a connection to the
// image failed mid-request, a redial could still reach the dying image's listen backlog, which
// then resets, so a retry waits for it to be gone first and reaches a fresh image. It reports
// whether the lock was free in time.
func (l *Launcher) WaitGone(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if release, ok := TryLock(l.LockPath()); ok {
			release()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// ExpectedBuild is the build id the image must report: the embedded sources' for the embedded
// build (Auto), else the one lisp/build-image.sh wrote beside the image (<image>.build), ""
// when there is none: a client that knows it refuses an image built from other sources.
func (l *Launcher) ExpectedBuild() string {
	if l.Auto {
		return l.Build
	}
	if l.Image == "" {
		return ""
	}
	raw, err := os.ReadFile(l.Image + ".build")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
