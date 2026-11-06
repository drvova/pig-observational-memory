package lispimage_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/config"
	"github.com/drvova/pig-observational-memory/memory/lispimage"
	"github.com/drvova/pig-observational-memory/memory/lispimage/fakesbcl"
)

// buildEnv is a root with no configured image and a fake sbcl first on PATH.
func buildEnv(t *testing.T, o fakesbcl.Options) (root, calls string) {
	t.Helper()
	root, bin := t.TempDir(), t.TempDir()
	if o.BuildID == "" {
		o.BuildID = lispimage.BuildID()
	}
	if o.Image == "" {
		o.Image = "#!/bin/sh\nexit 0\n"
	}
	calls, err := fakesbcl.Write(bin, o)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	t.Setenv(lispimage.ImageEnv, "")
	t.Setenv(lispimage.SocketEnv, "")
	t.Setenv(lispimage.RootEnv, root)
	return root, calls
}

// waitIdle waits until no build holds root's build lock, so a test ends with its builder gone.
func waitIdle(t *testing.T, l *lispimage.Launcher) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if release, ok := lispimage.TryLock(filepath.Join(l.BuildDir(), "build.lock")); ok {
			release()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the build never finished")
}

func building(t *testing.T, err error, want string) {
	t.Helper()
	ue, ok := lispimage.IsUnavailable(err)
	if !ok || !ue.Building || !strings.Contains(ue.Reason, want) {
		t.Fatalf("want a build (%q), got %v", want, err)
	}
}

// With no image configured the launcher's image is the embedded sources' build: the first
// Ensure starts it detached and says so without waiting; once it is in place Ensure is nil, the
// id beside it is the embedded sources', and nothing temporary is left.
func TestFirstUseBuild(t *testing.T) {
	root, calls := buildEnv(t, fakesbcl.Options{})
	l := lispimage.FromEnv(root)
	if !l.Auto || l.Build != lispimage.BuildID() || l.Image != filepath.Join(root, "engine", lispimage.BuildID(), lispimage.ImageName) || l.ExpectedBuild() != lispimage.BuildID() {
		t.Fatalf("launcher %+v", l)
	}
	building(t, l.Ensure(), "started now")
	waitIdle(t, l)
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(l.Image + ".build")
	if strings.TrimSpace(string(raw)) != lispimage.BuildID() {
		t.Fatalf("build id %q", raw)
	}
	entries, _ := os.ReadDir(l.BuildDir())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".build-") {
			t.Fatalf("left %s", e.Name())
		}
	}
	if n := fakesbcl.Builds(calls); n != 1 {
		t.Fatalf("%d builds", n)
	}
	// A configured image is never built.
	l.UseImage("/nonexistent/om-engine")
	if err := l.Ensure(); err != nil || l.Auto || l.ExpectedBuild() != "" {
		t.Fatalf("configured image: %v %+v", err, l)
	}
}

// Concurrent clients build once: while one build holds the lock the others are told a build is
// running.
func TestBuildLock(t *testing.T) {
	root, calls := buildEnv(t, fakesbcl.Options{Sleep: 2})
	first := lispimage.FromEnv(root)
	building(t, first.Ensure(), "started now")
	for i := 0; i < 3; i++ {
		building(t, lispimage.FromEnv(root).Ensure(), "being built")
	}
	waitIdle(t, first)
	if err := lispimage.FromEnv(root).Ensure(); err != nil {
		t.Fatal(err)
	}
	if n := fakesbcl.Builds(calls); n != 1 {
		t.Fatalf("%d builds", n)
	}
}

// Without sbcl nothing is built and the reason says so; it is no build to wait for.
func TestBuildWithoutSBCL(t *testing.T) {
	root, _ := buildEnv(t, fakesbcl.Options{})
	t.Setenv("PATH", t.TempDir())
	err := lispimage.FromEnv(root).Ensure()
	ue, ok := lispimage.IsUnavailable(err)
	if !ok || ue.Building || !strings.Contains(ue.Reason, "no sbcl on PATH") {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "engine")); err == nil {
		t.Fatal("a build directory without sbcl")
	}
}

// A failed build leaves its log as a marker and is not tried again before FailedRetry; one that
// runs past BuildTimeout is killed and fails the same way.
func TestBuildFailsAndTimesOut(t *testing.T) {
	for name, o := range map[string]fakesbcl.Options{"fails": {Fail: true}, "times out": {Sleep: 30}} {
		t.Run(name, func(t *testing.T) {
			root, calls := buildEnv(t, o)
			timeout := lispimage.BuildTimeout
			lispimage.BuildTimeout = time.Second
			t.Cleanup(func() { lispimage.BuildTimeout = timeout })
			l := lispimage.FromEnv(root)
			start := time.Now()
			building(t, l.Ensure(), "started now")
			waitIdle(t, l)
			if time.Since(start) > 15*time.Second {
				t.Fatalf("the build ran %s", time.Since(start))
			}
			err := l.Ensure()
			ue, ok := lispimage.IsUnavailable(err)
			if !ok || ue.Building || !strings.Contains(ue.Reason, "failed") {
				t.Fatalf("after a failed build: %v", err)
			}
			if _, err := os.Stat(filepath.Join(l.BuildDir(), l.Build+".failed")); err != nil {
				t.Fatal(err)
			}
			if n := fakesbcl.Builds(calls); n != 1 {
				t.Fatalf("%d builds", n)
			}
			retry := lispimage.FailedRetry
			lispimage.FailedRetry = 0
			t.Cleanup(func() { lispimage.FailedRetry = retry })
			building(t, l.Ensure(), "started now")
			waitIdle(t, l)
		})
	}
}

// Old builds are pruned to the current one and the newest before it.
func TestPruneBuilds(t *testing.T) {
	root, _ := buildEnv(t, fakesbcl.Options{})
	l := lispimage.FromEnv(root)
	now := time.Now()
	for i, name := range []string{"old1", "old2", "old3"} {
		dir := filepath.Join(l.BuildDir(), name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		at := now.Add(-time.Duration(3-i) * time.Hour)
		if err := os.Chtimes(dir, at, at); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(l.BuildDir(), "old1.failed"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	building(t, l.Ensure(), "started now")
	waitIdle(t, l)
	if err := l.Ensure(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(l.BuildDir())
	var names []string
	for _, e := range entries {
		if e.Name() != "build.lock" {
			names = append(names, e.Name())
		}
	}
	if got := strings.Join(names, " "); got != strings.Join([]string{l.Build, "old3"}, " ") && got != strings.Join([]string{"old3", l.Build}, " ") {
		t.Fatalf("kept %q", got)
	}
}

// The image is the default unless OM_ENGINE or the engine setting says otherwise; "go" (or any
// other value) is the Go engine; a platform without the image's transport stays on Go and never
// builds.
func TestSelect(t *testing.T) {
	root := t.TempDir()
	t.Setenv(config.EngineEnv, "")
	if s := lispimage.Select(root); !s.Lisp || s.Explicit {
		t.Fatalf("default %+v", s)
	}
	for value, lisp := range map[string]bool{"go": false, "lisp": true, "process": false, "in-process": false, "golang": false} {
		t.Setenv(config.EngineEnv, value)
		if s := lispimage.Select(root); s.Lisp != lisp || !s.Explicit {
			t.Fatalf("OM_ENGINE=%s: %+v", value, s)
		}
	}
	t.Setenv(config.EngineEnv, "")
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(`{"observational-memory": {"engine": "go"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if s := lispimage.Select(root); s.Lisp || !s.Explicit {
		t.Fatalf("setting go: %+v", s)
	}
	t.Setenv(config.EngineEnv, "lisp")
	if s := lispimage.Select(root); !s.Lisp {
		t.Fatalf("OM_ENGINE over the setting: %+v", s)
	}

	supported := config.LispSupported
	config.LispSupported = false
	t.Cleanup(func() { config.LispSupported = supported })
	t.Setenv(config.EngineEnv, "")
	if s := lispimage.Select(t.TempDir()); s.Lisp {
		t.Fatalf("windows default %+v", s)
	}
	broot, _ := buildEnv(t, fakesbcl.Options{})
	if err := lispimage.FromEnv(broot).Ensure(); err == nil || strings.Contains(err.Error(), "started") {
		t.Fatalf("a build where the image has no transport: %v", err)
	}
}

// Every fallback is one line in the engine log, rotated past its limit.
func TestLogFailover(t *testing.T) {
	root := t.TempDir()
	lispimage.LogFailover(root, "turn-end", "no sbcl on PATH")
	raw, _ := os.ReadFile(lispimage.LogPath(root))
	if strings.Count(string(raw), "\n") != 1 || !strings.Contains(string(raw), "fallback: turn-end ran on the Go engine: no sbcl on PATH") {
		t.Fatalf("log %q", raw)
	}
	if err := os.WriteFile(lispimage.LogPath(root), make([]byte, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	lispimage.LogFailover(root, "compact", "x")
	if info, _ := os.Stat(lispimage.LogPath(root)); info.Size() > 200 {
		t.Fatalf("not rotated: %d", info.Size())
	}
	if _, err := os.Stat(lispimage.LogPath(root) + ".1"); err != nil {
		t.Fatal(err)
	}
}
