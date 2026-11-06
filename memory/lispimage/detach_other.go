//go:build !unix

package lispimage

import (
	"os"
	"os/exec"
)

// detach does nothing here: the image is measured on Linux only, and Windows (a named pipe,
// a detached process) is open (docs/lisp-port-image-client.md).
func detach(cmd *exec.Cmd) {}

// lockFile never holds a lock here: Ensure refuses to build on this platform before it asks.
func lockFile(path string) (*os.File, bool) { return nil, false }

// TryLock always succeeds here: the image's lock is unix-only so far.
func TryLock(path string) (release func(), ok bool) { return func() {}, true }
