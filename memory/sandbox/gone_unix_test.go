//go:build unix && !linux

package sandbox

import (
	"os"
	"syscall"
)

// gone reports whether a process no longer runs.
func gone(pid int) bool {
	p, err := os.FindProcess(pid)
	return err != nil || p.Signal(syscall.Signal(0)) != nil
}

// oomWatch is a no-op here: the allocate-until-killed test relies on the kernel.
func oomWatch(int, int64) (stop func()) { return func() {} }
