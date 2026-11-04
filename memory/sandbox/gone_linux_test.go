//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"strings"
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
