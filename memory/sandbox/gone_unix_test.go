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
