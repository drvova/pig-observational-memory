//go:build unix

package cli

import (
	"os/exec"
	"syscall"
)

// detach runs the image in a session of its own, so it outlives the hook that started it and
// a signal to the hook's process group does not reach it.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }
