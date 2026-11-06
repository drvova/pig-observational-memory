// Package fakesbcl writes a fake sbcl for tests of the first-use build (memory/lispimage): a
// shell script that answers --version with the pinned release, records each build it runs, and
// "builds" by copying a given executable to the image path (OM_IMAGE_OUTPUT) with the build id
// beside it, so no test depends on the machine having SBCL.
package fakesbcl

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Options say how the fake builds.
type Options struct {
	// Image is the executable's content the build writes (a script that runs a fake image).
	Image string
	// BuildID is what the build writes beside the image.
	BuildID string
	// Sleep is how many seconds a build takes.
	Sleep int
	// Fail makes every build fail (after Sleep).
	Fail bool
}

// Write puts the fake sbcl in dir (dir goes first on PATH) and returns the file that counts its
// builds: one line per build.
func Write(dir string, o Options) (calls string, err error) {
	calls = filepath.Join(dir, "sbcl-builds")
	image := filepath.Join(dir, "fake-image")
	if err := os.WriteFile(image, []byte(o.Image), 0o700); err != nil {
		return "", err
	}
	q := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	script := fmt.Sprintf(`#!/bin/sh
case "${1:-}" in --version) echo "SBCL 2.6.10"; exit 0;; esac
echo build >> %s
sleep %d 3>&-
`, q(calls), o.Sleep)
	if o.Fail {
		script += "echo 'fake sbcl: build failed' >&2\nexit 1\n"
	} else {
		script += fmt.Sprintf(`cp %s "$OM_IMAGE_OUTPUT" && chmod 700 "$OM_IMAGE_OUTPUT" && echo %s > "$OM_IMAGE_OUTPUT.build"
`, q(image), q(o.BuildID))
	}
	return calls, os.WriteFile(filepath.Join(dir, "sbcl"), []byte(script), 0o700)
}

// Builds is how many builds the fake ran.
func Builds(calls string) int {
	raw, err := os.ReadFile(calls)
	if err != nil {
		return 0
	}
	return strings.Count(string(raw), "\n")
}
