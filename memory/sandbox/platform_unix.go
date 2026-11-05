//go:build unix && !linux

package sandbox

import (
	"runtime"
	"syscall"
)

// sysProcAttr is the child's process attributes. Outside Linux there is no Pdeathsig: EOF on
// the protocol pipe is the death signal. No Setpgid: the child stays in the extension's process
// group.
func sysProcAttr(bool) *syscall.SysProcAttr { return &syscall.SysProcAttr{} }

// setOOMScore has nothing to set outside Linux: a runaway allocation can reach the system's own
// policy (macOS's memory-pressure killer may pick another process), as the manual says.
func setOOMScore() string { return "no out-of-memory preference on " + runtime.GOOS }
