//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// gone reports whether a process no longer runs: absent, or a zombie nobody reaped yet.
func gone(pid int) bool {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return true
	}
	f := strings.Fields(string(b))
	return len(f) > 2 && (f[2] == "Z" || f[2] == "X")
}

// oomWatch stands in for the kernel's out-of-memory killer in a test: it sends the child the
// SIGKILL the killer would once its resident set passes limitKB, so an allocate-until-killed
// test never takes the machine (a CI runner) down with it. stop ends the watch.
func oomWatch(pid int, limitKB int64) (stop func()) {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(20 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
			}
			b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
			if err != nil {
				return
			}
			for _, line := range strings.Split(string(b), "\n") {
				if f := strings.Fields(line); len(f) >= 2 && f[0] == "VmRSS:" {
					if kb, err := strconv.ParseInt(f[1], 10, 64); err == nil && kb > limitKB {
						_ = syscall.Kill(pid, syscall.SIGKILL)
						return
					}
				}
			}
		}
	}()
	return func() { close(done) }
}
