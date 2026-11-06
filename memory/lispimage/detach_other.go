//go:build !unix

package lispimage

import "os/exec"

// detach does nothing here: the image is measured on Linux only, and Windows (a named pipe,
// a detached process) is open (docs/lisp-port-image-client.md).
func detach(cmd *exec.Cmd) {}

// TryLock always succeeds here: the image's lock is unix-only so far.
func TryLock(path string) (release func(), ok bool) { return func() {}, true }
