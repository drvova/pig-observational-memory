//go:build windows

package sandbox

import "os"

// gone reports whether a process no longer runs.
func gone(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return true
	}
	p.Release()
	return false
}
