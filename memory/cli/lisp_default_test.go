package cli

import (
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/config"
	"github.com/drvova/pig-observational-memory/memory/lispimage"
	"github.com/drvova/pig-observational-memory/memory/lispimage/fakesbcl"
)

// engineLog is root's engine log.
func engineLog(t *testing.T, root string) string {
	t.Helper()
	raw, _ := os.ReadFile(lispimage.LogPath(root))
	return string(raw)
}

// The image is the default: with no sbcl to build it every event runs on the Go engine, each
// fallback one line in the engine log with its reason and nothing on om's stderr (the user did
// not ask for the image); with falling back off the event fails with the reason.
func TestLispDefaultWithoutSBCL(t *testing.T) {
	root, home := env(t)
	t.Setenv(EngineEnv, "")
	t.Setenv(LispImageEnv, "")
	t.Setenv(LispSocketEnv, "")
	t.Setenv(LispFallbackEnv, "")
	if c := newLispClient(root); c == nil || c.explicit || !c.launcher.Auto {
		t.Fatalf("default client %+v", c)
	}
	p := genericFixture(t, home)
	if _, errOut, code := omErr(t, p, "hook", "generic", "turn-end"); code != 0 || errOut != "" {
		t.Fatalf("exit %d: %q", code, errOut)
	}
	if summary := om(t, p, "hook", "generic", "compact"); !strings.Contains(summary, "9000") {
		t.Fatalf("summary on the Go engine %s", summary)
	}
	log := engineLog(t, root)
	if strings.Count(log, "\n") != 2 || strings.Count(log, "no sbcl on PATH") != 2 || !strings.Contains(log, "fallback: turn-end ran on the Go engine") {
		t.Fatalf("engine log %q", log)
	}
	// om status (itself on the Go engine) shows the count the engine log holds, its own
	// fallback included.
	if status := om(t, "", "status", "--harness", "mine", "--dir", home); !strings.Contains(status, "Engine fallbacks: 3 events ran on the Go engine") {
		t.Fatalf("status %s", status)
	}
	t.Setenv(EngineEnv, "go")
	if status := om(t, "", "status", "--harness", "mine", "--dir", home); strings.Contains(status, "Engine fallbacks") {
		t.Fatalf("status with the kill switch %s", status)
	}
	t.Setenv(EngineEnv, "")
	t.Setenv(LispFallbackEnv, "0")
	if _, errOut, code := omErr(t, p, "hook", "generic", "turn-end"); code != 1 || !strings.Contains(errOut, "no sbcl on PATH") {
		t.Fatalf("exit %d: %s", code, errOut)
	}
}

// The kill switch: "engine": "go" in the root's settings (as OM_ENGINE=go, TestLispModeOff)
// never reaches the socket; OM_ENGINE wins over the setting. Where the image has no transport
// (Windows) the default is Go.
func TestLispKillSwitch(t *testing.T) {
	root, home := env(t)
	t.Setenv(EngineEnv, "")
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(`{"observational-memory": {"engine": "go"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	socket, requests := fakeServer(t, root, defaultFakeAnswer)
	t.Setenv(LispSocketEnv, socket)
	p := genericFixture(t, home)
	om(t, p, "hook", "generic", "turn-end")
	if n := len(requests()); n != 0 {
		t.Fatalf("%d requests reached the socket", n)
	}
	t.Setenv(EngineEnv, "lisp")
	if newLispClient(root) == nil {
		t.Fatal("OM_ENGINE=lisp over the setting")
	}

	supported := config.LispSupported
	config.LispSupported = false
	t.Cleanup(func() { config.LispSupported = supported })
	t.Setenv(EngineEnv, "")
	if newLispClient(t.TempDir()) != nil {
		t.Fatal("the image by default without its transport")
	}
}

// First use: no image exists, so the first event starts a detached build (the fake sbcl) and runs
// on the Go engine; once the build is in place the next event starts the built image and runs
// there. The image reports the embedded sources' build id, so the client's build check passes.
func TestLispFirstUseBuild(t *testing.T) {
	root, home := env(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	calls, err := fakesbcl.Write(bin, fakesbcl.Options{
		Image:   "#!/bin/sh\nOM_TEST_FAKE_IMAGE=1 exec '" + exe + "' \"$@\"\n",
		BuildID: lispimage.BuildID(),
		Sleep:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv(EngineEnv, "")
	t.Setenv(LispImageEnv, "")
	t.Setenv(LispSocketEnv, "")
	t.Setenv(LispFallbackEnv, "")
	t.Setenv(LispIdleEnv, "5")
	p := genericFixture(t, home)

	start := time.Now()
	om(t, p, "hook", "generic", "turn-end")
	if time.Since(start) > 900*time.Millisecond {
		t.Fatalf("the event waited for the build: %s", time.Since(start))
	}
	om(t, p, "hook", "generic", "turn-end")
	log := engineLog(t, root)
	if !strings.Contains(log, "building the engine image") || !strings.Contains(log, "the engine image is being built") {
		t.Fatalf("engine log %q", log)
	}
	l := lispimage.FromEnv(root)
	deadline := time.Now().Add(20 * time.Second)
	for {
		if release, ok := lispimage.TryLock(filepath.Join(l.BuildDir(), "build.lock")); ok {
			release()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the build never finished")
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Cleanup(func() { stopFakeImage(t, l) })
	lines := strings.Count(engineLog(t, root), "fallback:")
	om(t, p, "hook", "generic", "turn-end")
	if starts, err := os.ReadFile(filepath.Join(root, "fake-starts")); err != nil || !strings.Contains(string(starts), "idle=5") {
		t.Fatalf("the built image never started: %v %q", err, starts)
	}
	if n := strings.Count(engineLog(t, root), "fallback:"); n != lines {
		t.Fatalf("the event after the build fell back: %q", engineLog(t, root))
	}
	if n := fakesbcl.Builds(calls); n != 1 {
		t.Fatalf("%d builds", n)
	}
}

// stopFakeImage stops a fake image the test started and waits for it to go.
func stopFakeImage(t *testing.T, l *lispimage.Launcher) {
	if conn, err := net.Dial("unix", l.Socket); err == nil {
		_, _ = conn.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"image/stop"}` + "\n"))
		buf := make([]byte, 256)
		_, _ = conn.Read(buf)
		conn.Close()
	}
	l.WaitGone(10 * time.Second)
}

// TestLispFirstUseBuildReal builds the real image from the embedded sources with the sbcl on
// PATH, as a user's first event does, and runs the next events on it with falling back off: the
// image's initialize reports lispimage.BuildID (the Go hash of the embedded copy equals the Lisp
// one), or the client would refuse it. Gated like the other image tests.
func TestLispFirstUseBuildReal(t *testing.T) {
	if os.Getenv("OM_LISP_PARITY") != "1" {
		t.Skip("set OM_LISP_PARITY=1 to build the SBCL engine image from the embedded sources")
	}
	sbcl, err := exec.LookPath("sbcl")
	if err != nil {
		t.Skip("sbcl is not on PATH")
	}
	root, home := env(t)
	t.Setenv("PATH", filepath.Dir(sbcl)+":/usr/bin:/bin")
	t.Setenv(EngineEnv, "")
	t.Setenv(LispImageEnv, "")
	t.Setenv(LispSocketEnv, "")
	t.Setenv(LispFallbackEnv, "")
	t.Setenv(LispIdleEnv, "30")
	p := genericFixture(t, home)
	om(t, p, "hook", "generic", "turn-end")
	l := lispimage.FromEnv(root)
	start := time.Now()
	deadline := start.Add(5 * time.Minute)
	for {
		if release, ok := lispimage.TryLock(filepath.Join(l.BuildDir(), "build.lock")); ok {
			release()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the build never finished")
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Cleanup(func() { stopFakeImage(t, l) })
	if err := l.Ensure(); err != nil {
		failed, _ := os.ReadFile(filepath.Join(l.BuildDir(), l.Build+".failed"))
		t.Fatalf("after the build: %v\n%s", err, failed)
	}
	info, _ := os.Stat(l.Image)
	t.Logf("built %s in %s, %d MB", l.Build, time.Since(start).Round(time.Second), info.Size()>>20)
	t.Setenv(LispFallbackEnv, "0")
	om(t, p, "hook", "generic", "turn-end")
	if summary := om(t, p, "hook", "generic", "compact"); !strings.Contains(summary, "9000") {
		t.Fatalf("summary on the image %s", summary)
	}
	if _, err := os.Stat(filepath.Join(root, "engine.pid")); err != nil {
		t.Fatalf("no image ran: %v", err)
	}
}

// An image reporting another build (om upgraded under a running image) is stopped before the
// event, and the current one started: here none can be built, so the event runs on Go.
func TestLispStaleImageStopped(t *testing.T) {
	root, home := env(t)
	t.Setenv(EngineEnv, "lisp")
	t.Setenv(LispImageEnv, "")
	t.Setenv(LispFallbackEnv, "")
	t.Setenv("OM_TEST_FAKE_BUILD", "stale")
	socket, requests := fakeServer(t, root, defaultFakeAnswer)
	t.Setenv(LispSocketEnv, socket)
	p := genericFixture(t, home)
	om(t, p, "hook", "generic", "turn-end")
	reqs := requests()
	if len(reqs) != 1 || reqs[0].Method != "image/stop" {
		t.Fatalf("requests %+v", reqs)
	}
	if !strings.Contains(engineLog(t, root), "no sbcl on PATH") {
		t.Fatalf("engine log %q", engineLog(t, root))
	}
}
