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

// oomWatch is a no-op here: the allocate-until-killed test relies on the kernel.
func oomWatch(int, int64) (stop func()) { return func() {} }
