//go:build !unix

package cli

import "os/exec"

// detach does nothing here: the image is measured on Linux only, and Windows (a named pipe,
// a detached process) is open (docs/lisp-port-image-client.md).
func detach(cmd *exec.Cmd) {}
