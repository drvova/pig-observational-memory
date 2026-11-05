//go:build linux

package sandbox

import (
	"os"
	"syscall"
)

// sysProcAttr is the child's process attributes. On Linux the child gets SIGKILL when the
// thread that started it exits (Pdeathsig); the supervisor starts children from an ordinary
// goroutine that never locks its thread, and EOF on the protocol pipe is the death signal on
// every platform, which Pdeathsig only makes faster. No Setpgid: the child stays in the
// extension's process group, which PiG kills as a group.
func sysProcAttr(pdeathsig bool) *syscall.SysProcAttr {
	a := &syscall.SysProcAttr{}
	if pdeathsig {
		a.Pdeathsig = syscall.SIGKILL
	}
	return a
}

// setOOMScore makes the child the out-of-memory killer's first pick, before PiG and the
// extension (docs/code-tool.md, decision 6). Raising one's own score needs no privilege.
func setOOMScore() string {
	if err := os.WriteFile("/proc/self/oom_score_adj", []byte("1000"), 0); err != nil {
		return err.Error()
	}
	return oomSet
}
