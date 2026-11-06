package observationalmemory

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/config"
	"github.com/drvova/pig-observational-memory/memory/enginerpc"
	"github.com/drvova/pig-observational-memory/memory/lispimage"
	"github.com/drvova/pig-observational-memory/memory/lispimage/fakesbcl"
)

// useProcessEngine turns the engine setting to "process" in a temporary agent directory and
// starts each engine over in-memory pipes (enginerpc.PipeDialer, a Server in this process),
// so the factory registers the process-mode handlers and the lifecycle tests drive them
// through the registered extension as PiG does.
func useProcessEngine(t *testing.T) *int {
	t.Helper()
	agentDir := t.TempDir()
	settings := `{"observational-memory": {"engine": "process", "enginePath": "/nonexistent/om"}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv(config.EngineEnv, "")
	var mu sync.Mutex
	var stops []func() error
	started := 0
	previous := engineDialer
	engineDialer = func(path string) enginerpc.Dialer {
		if path != "/nonexistent/om" {
			t.Errorf("engine path %q", path)
		}
		dial := enginerpc.PipeDialer(func(r io.Reader, w io.Writer) error { return enginerpc.NewServer().Serve(r, w) })
		return func() (io.Reader, io.Writer, func() error, error) {
			r, w, stop, err := dial()
			mu.Lock()
			stops = append(stops, stop)
			started++
			mu.Unlock()
			return r, w, stop, err
		}
	}
	t.Cleanup(func() {
		engineDialer = previous
		mu.Lock()
		defer mu.Unlock()
		for _, stop := range stops {
			_ = stop()
		}
	})
	return &started
}

// The lifecycle tests pass with the engine behind the RPC.
func TestProcessEngineLifecycle(t *testing.T) {
	for name, test := range map[string]func(*testing.T){
		"namespace resets":    TestSessionBoundariesResetTheNamespace,
		"retired tools":       TestRetiredDefinedToolsAreNeverRegistered,
		"status over the rpc": testStatusOverTheRPC,
	} {
		t.Run(name, func(t *testing.T) {
			started := useProcessEngine(t)
			test(t)
			if *started != 1 {
				t.Fatalf("started %d engines", *started)
			}
		})
	}
}

// testStatusOverTheRPC runs /om:status: the engine's Notify reaches PiG's notify call.
func testStatusOverTheRPC(t *testing.T) {
	w := connect(t)
	w.event("session_start")
	var notified []string
	w.answer = func(method string, args json.RawMessage) any {
		if method == "ui.notify" {
			notified = append(notified, string(args))
		}
		return nil
	}
	w.request(map[string]any{"method": "command", "tool": "om:status", "args": ""})
	if len(notified) != 1 || !strings.Contains(notified[0], "emory") {
		t.Fatalf("notify calls %v (all calls %v)", notified, w.calls)
	}
}

// "engine": "go" (and OM_ENGINE=go over any setting) is the kill switch: the factory never
// starts an engine. Without a setting the image is the default, and where the image has no
// transport (Windows) the in-process engine is.
func TestEngineSelection(t *testing.T) {
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv(lispimage.RootEnv, t.TempDir())
	t.Setenv(lispimage.ImageEnv, "")
	t.Setenv(lispimage.FallbackEnv, "")
	previous, previousLisp, previousLog := engineDialer, lispDialer, engineLog
	engineLog = &lockedBuilder{}
	engineDialer = func(string) enginerpc.Dialer {
		t.Fatal("an engine process was started")
		return nil
	}
	lispDials := 0
	lispDialer = func(l *lispimage.Launcher) enginerpc.Dialer {
		lispDials++
		return func() (io.Reader, io.Writer, func() error, error) { return nil, nil, nil, errors.New("no image here") }
	}
	t.Cleanup(func() { engineDialer, lispDialer, engineLog = previous, previousLisp, previousLog })
	write := func(engine string) {
		if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"observational-memory": {"engine": "`+engine+`"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv(config.EngineEnv, "")
	if setting := config.ReadEngine(config.DefaultPaths()); !setting.Lisp() || !setting.Default {
		t.Fatalf("default %+v", setting)
	}
	processEngine()
	if lispDials != 1 {
		t.Fatalf("the default did not reach the image: %d", lispDials)
	}
	for _, engine := range []string{"go", "in-process"} {
		write(engine)
		if processEngine() != nil {
			t.Fatalf("engine %q started one", engine)
		}
	}
	write("lisp")
	t.Setenv(config.EngineEnv, "go")
	if processEngine() != nil {
		t.Fatal("OM_ENGINE=go over the setting")
	}
	if lispDials != 1 {
		t.Fatalf("the kill switch reached the image: %d", lispDials)
	}

	supported := config.LispSupported
	config.LispSupported = false
	t.Cleanup(func() { config.LispSupported = supported })
	t.Setenv(config.EngineEnv, "")
	write("")
	if setting := config.ReadEngine(config.DefaultPaths()); setting.Mode != config.EngineInProcess || processEngine() != nil {
		t.Fatalf("windows default %+v", setting)
	}
}

// While the image is being built from the embedded sources (here a fake sbcl that takes its
// time) the extension keeps the image's client, and every event runs on the Go fallback.
func TestLispEngineWhileBuilding(t *testing.T) {
	agentDir, root, bin := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv(config.EngineEnv, "")
	t.Setenv(lispimage.RootEnv, root)
	t.Setenv(lispimage.ImageEnv, "")
	t.Setenv(lispimage.SocketEnv, "")
	t.Setenv(lispimage.FallbackEnv, "")
	if _, err := fakesbcl.Write(bin, fakesbcl.Options{Image: "#!/bin/sh\nexit 1\n", BuildID: lispimage.BuildID(), Sleep: 1}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	previous := engineLog
	log := &lockedBuilder{}
	engineLog = log
	t.Cleanup(func() {
		engineLog = previous
		l := lispimage.FromEnv(root)
		for i := 0; i < 1000; i++ {
			if release, ok := lispimage.TryLock(filepath.Join(l.BuildDir(), "build.lock")); ok {
				release()
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	client := processEngine()
	if client == nil {
		t.Fatalf("no client while building: %q", log.String())
	}
	defer client.Close()
	if text := log.String(); !strings.Contains(text, "building the engine image") || !strings.Contains(text, "until it is built") {
		t.Fatalf("log %q", text)
	}
}

// useLispEngine turns the engine setting to "lisp" with an "image" (over pipes) that answers
// initialize and refuses every event as not ported, before acting, so each runs on the Go
// fallback. It returns the fallback log.
func useLispEngine(t *testing.T) *lockedBuilder {
	t.Helper()
	agentDir := t.TempDir()
	settings := `{"observational-memory": {"engine": "lisp", "lispImage": "/nonexistent/om-engine"}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv(config.EngineEnv, "")
	t.Setenv(lispimage.RootEnv, t.TempDir())
	t.Setenv(lispimage.ImageEnv, "")
	t.Setenv(lispimage.FallbackEnv, "")
	var mu sync.Mutex
	var stops []func() error
	previous, previousLog := lispDialer, engineLog
	log := &lockedBuilder{}
	engineLog = log
	lispDialer = func(l *lispimage.Launcher) enginerpc.Dialer {
		if l.Image != "/nonexistent/om-engine" {
			t.Errorf("image %q", l.Image)
		}
		dial := enginerpc.PipeDialer(func(r io.Reader, w io.Writer) error {
			peer := enginerpc.NewPeer(r, w, func(_ context.Context, method string, _ json.RawMessage) (any, error) {
				switch method {
				case enginerpc.MethodInitialize:
					return enginerpc.InitializeResult{ProtocolVersion: 1, Name: "om", Tool: enginerpc.MemoryToolSpec()}, nil
				case enginerpc.MethodShutdown:
					return struct{}{}, nil
				}
				return nil, &enginerpc.Error{Code: enginerpc.CodeNotPorted, Message: method + " is not ported"}
			})
			return peer.Run()
		})
		return func() (io.Reader, io.Writer, func() error, error) {
			r, w, stop, err := dial()
			mu.Lock()
			stops = append(stops, stop)
			mu.Unlock()
			return r, w, stop, err
		}
	}
	t.Cleanup(func() {
		lispDialer, engineLog = previous, previousLog
		mu.Lock()
		defer mu.Unlock()
		for _, stop := range stops {
			_ = stop()
		}
	})
	return log
}

// lockedBuilder is a log written from several goroutines.
type lockedBuilder struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedBuilder) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuilder) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// The lifecycle tests pass with the "lisp" engine refusing every event: each runs on the Go
// fallback, and each fallback is logged.
func TestLispEngineLifecycle(t *testing.T) {
	for name, test := range map[string]func(*testing.T){
		"namespace resets":    TestSessionBoundariesResetTheNamespace,
		"retired tools":       TestRetiredDefinedToolsAreNeverRegistered,
		"status over the rpc": testStatusOverTheRPC,
	} {
		t.Run(name, func(t *testing.T) {
			log := useLispEngine(t)
			test(t)
			if !strings.Contains(log.String(), "event/sessionStart: not ported") || !strings.Contains(log.String(), "running it on the Go engine") {
				t.Fatalf("fallback log %q", log.String())
			}
		})
	}
}

// With "engine": "lisp" and no image that starts, the extension runs in-process and says why
// once; with falling back off it keeps the client.
func TestLispEngineWithoutAnImage(t *testing.T) {
	agentDir := t.TempDir()
	settings := `{"observational-memory": {"engine": "lisp", "lispImage": "/nonexistent/om-engine"}}`
	if err := os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv(config.EngineEnv, "")
	t.Setenv(lispimage.RootEnv, t.TempDir())
	t.Setenv(lispimage.ImageEnv, "")
	t.Setenv(lispimage.SocketEnv, "")
	t.Setenv(lispimage.FallbackEnv, "")
	previous := engineLog
	log := &lockedBuilder{}
	engineLog = log
	t.Cleanup(func() { engineLog = previous })
	if processEngine() != nil {
		t.Fatal("a lisp client without an image")
	}
	if text := log.String(); strings.Count(text, "\n") != 1 || !strings.Contains(text, `engine "lisp"`) || !strings.Contains(text, "using the in-process engine") {
		t.Fatalf("log %q", text)
	}
	t.Setenv(lispimage.FallbackEnv, "0")
	log = &lockedBuilder{}
	engineLog = log
	if processEngine() == nil {
		t.Fatal("falling back off dropped the client")
	}
	if !strings.Contains(log.String(), "falling back is off") {
		t.Fatalf("log %q", log.String())
	}
}
