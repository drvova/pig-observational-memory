//go:build unix

package lispimage

import (
	"os"
	"os/exec"
	"syscall"
)

// detach runs the image in a session of its own, so it outlives the hook that started it and
// a signal to the hook's process group does not reach it.
func detach(cmd *exec.Cmd) { cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} }

// TryLock takes the exclusive flock(2) the image holds on path, without waiting: release and
// true when it is free (no live image holds it), false when another open holds it.
func TryLock(path string) (release func(), ok bool) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, false
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, true
}
