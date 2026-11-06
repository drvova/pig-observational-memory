package lispimage

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/memory/config"
)

// The first-use build (docs/lisp-build.md): with no image configured, the launcher's image is
// <root>/engine/<BuildID>/om-engine, built from the embedded sources with the sbcl on PATH. A
// build runs detached and bounded, never inside the event that needs it: that event fails over
// to the Go engine (an *UnavailableError, before the image acted) and a later one finds the image.

// ImageName is the executable's name inside a build directory.
const ImageName = "om-engine"

var (
	// BuildTimeout bounds one build: the builder kills sbcl after it.
	BuildTimeout = 10 * time.Minute
	// FailedRetry is how long a failed build is not tried again (its log stays in
	// <root>/engine/<id>.failed meanwhile).
	FailedRetry = time.Hour
	// KeepBuilds is how many build directories stay: the current and the one before it.
	KeepBuilds = 2
)

// UnavailableError is an image the launcher cannot start yet: no sbcl to build it, a build in
// progress or just started, a recent build that failed. Nothing ran, so the caller's Go engine
// runs the event (a failover); Building says whether a later event may find the image.
type UnavailableError struct {
	Reason   string
	Building bool
}

func (e *UnavailableError) Error() string { return "om: the engine image is unavailable: " + e.Reason }

// IsUnavailable reports whether err is an *UnavailableError, and returns it.
func IsUnavailable(err error) (*UnavailableError, bool) {
	var ue *UnavailableError
	ok := errors.As(err, &ue)
	return ue, ok
}

// BuildDir is where builds of the embedded sources live: <root>/engine.
func (l *Launcher) BuildDir() string { return filepath.Join(l.Root, "engine") }

// buildScript runs detached in its own session, holding the build lock on fd 3 (inherited, so
// the lock lasts exactly as long as the builder and its sbcl): it builds into the temporary
// directory, kills sbcl after the bound, and renames the image directory into place (atomic on
// one file system), or leaves the build's log as the failure marker.
const buildScript = `set -u
tmp=$1 final=$2 failed=$3 sbcl=$4 limit=$5
PATH=$(dirname "$sbcl"):$PATH
XDG_CACHE_HOME=$tmp/cache
export PATH XDG_CACHE_HOME
/bin/sh "$tmp/lisp/build-image.sh" "$tmp/image/om-engine" >"$tmp/build.log" 2>&1 </dev/null &
pid=$!
( trap 'kill $s 2>/dev/null; exit 0' TERM; sleep "$limit" & s=$!; wait $s; kill "$pid" 2>/dev/null ) 3>&- &
watch=$!
ok=0
wait "$pid" && [ -x "$tmp/image/om-engine" ] && [ -s "$tmp/image/om-engine.build" ] && ok=1
kill "$watch" 2>/dev/null
if [ "$ok" = 1 ] && { [ -e "$final" ] || mv "$tmp/image" "$final"; }; then
  rm -f "$failed"
  rm -rf "$tmp"
  exit 0
fi
cp "$tmp/build.log" "$failed" 2>/dev/null || echo "build failed" >"$failed"
rm -rf "$tmp"
exit 1
`

// usable reports whether the built image is in place with its build id; an image whose id is
// another is an error, since a rebuild would build the same.
func (l *Launcher) usable() (bool, error) {
	info, err := os.Stat(l.Image)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return false, nil
	}
	raw, err := os.ReadFile(l.Image + ".build")
	if err != nil {
		return false, nil
	}
	if got := strings.TrimSpace(string(raw)); got != l.Build {
		return false, &UnavailableError{Reason: fmt.Sprintf("the image built in %s reports build %s, not %s", filepath.Dir(l.Image), got, l.Build)}
	}
	return true, nil
}

// Ensure makes sure the launcher's image exists. With a configured image it does nothing.
// With the embedded build it prunes old builds when the image is in place; otherwise it starts
// a detached build when it can and returns an *UnavailableError saying why the image cannot run
// now. It never waits for a build.
func (l *Launcher) Ensure() error {
	if !l.Auto {
		return nil
	}
	if ok, err := l.usable(); ok || err != nil {
		if ok && !l.pruned {
			l.pruned = true
			l.Prune()
		}
		return err
	}
	dir := l.BuildDir()
	failed := filepath.Join(dir, l.Build+".failed")
	if info, err := os.Stat(failed); err == nil && time.Since(info.ModTime()) < FailedRetry {
		return &UnavailableError{Reason: fmt.Sprintf("building the engine image failed (see %s); trying again after %s", failed, info.ModTime().Add(FailedRetry).Format(time.Kitchen))}
	}
	if !config.LispSupported {
		return &UnavailableError{Reason: "the engine image is not built on this platform (set " + ImageEnv + ")"}
	}
	sbcl, err := exec.LookPath("sbcl")
	if err != nil {
		return &UnavailableError{Reason: "no sbcl on PATH to build the engine image from (install SBCL, or set " + ImageEnv + ")"}
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return &UnavailableError{Reason: err.Error()}
	}
	lock, ok := lockFile(filepath.Join(dir, "build.lock"))
	if !ok {
		return &UnavailableError{Reason: "the engine image is being built", Building: true}
	}
	defer lock.Close()
	if ok, err := l.usable(); ok || err != nil {
		return err
	}
	// No build runs while this holds the lock: what an earlier, killed one left goes.
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), ".build-") {
				_ = os.RemoveAll(filepath.Join(dir, e.Name()))
			}
		}
	}
	tmp, err := os.MkdirTemp(dir, ".build-"+l.Build+"-")
	if err != nil {
		return &UnavailableError{Reason: err.Error()}
	}
	if err := extractSources(filepath.Join(tmp, "lisp")); err != nil {
		_ = os.RemoveAll(tmp)
		return &UnavailableError{Reason: "writing the engine sources: " + err.Error()}
	}
	if err := os.Mkdir(filepath.Join(tmp, "image"), 0o700); err != nil {
		_ = os.RemoveAll(tmp)
		return &UnavailableError{Reason: err.Error()}
	}
	limit := int(BuildTimeout / time.Second)
	if limit < 1 {
		limit = 1
	}
	cmd := exec.Command("/bin/sh", "-c", buildScript, "om-build", tmp, filepath.Dir(l.Image), failed, sbcl, strconv.Itoa(limit))
	cmd.ExtraFiles = []*os.File{lock}
	detach(cmd)
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(tmp)
		return &UnavailableError{Reason: "starting the engine image build: " + err.Error()}
	}
	// Reaped in a long-lived process (the PiG adapter); a hook's process exits first, and the
	// builder, in a session of its own, carries on.
	go func() { _ = cmd.Wait() }()
	return &UnavailableError{Reason: fmt.Sprintf("building the engine image %s with %s (started now, log in %s)", l.Build, sbcl, filepath.Join(tmp, "build.log")), Building: true}
}

// Prune removes the build directories beyond the KeepBuilds newest (the current one always
// stays), and the failure markers of builds that are gone.
func (l *Launcher) Prune() {
	dir := l.BuildDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	type build struct {
		name string
		mod  time.Time
	}
	var builds []build
	kept := map[string]bool{}
	for _, e := range entries {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		if e.Name() == l.Build {
			kept[e.Name()] = true
			continue
		}
		builds = append(builds, build{e.Name(), info.ModTime()})
	}
	sort.Slice(builds, func(i, j int) bool { return builds[i].mod.After(builds[j].mod) })
	for i, b := range builds {
		if i+len(kept) < KeepBuilds {
			kept[b.name] = true
			continue
		}
		_ = os.RemoveAll(filepath.Join(dir, b.name))
	}
	for _, e := range entries {
		if name, ok := strings.CutSuffix(e.Name(), ".failed"); ok && !kept[name] && name != l.Build {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}
