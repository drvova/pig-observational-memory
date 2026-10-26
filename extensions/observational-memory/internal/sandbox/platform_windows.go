//go:build windows

package sandbox

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// sysProcAttr is the child's process attributes. On Windows PiG's job object kills the child
// with the extension, and EOF on the protocol pipe is the death signal.
func sysProcAttr(bool) *syscall.SysProcAttr { return &syscall.SysProcAttr{} }

// setOOMScore has nothing to set on Windows: an allocation fails in the child once commit runs
// out, which crashes it, but other processes may fail allocations meanwhile, as the manual says.
func setOOMScore() string { return "no out-of-memory preference on windows" }

// attachPipes passes the two protocol pipes as inherited handles, their values given as the
// arguments after the mode. stdin and stdout stay NUL, so a sibling init printing to stdout
// cannot corrupt the protocol. This path is compiled by GOOS=windows go vet and not tested.
func attachPipes(cmd *exec.Cmd, toChild, fromChild *os.File) []string {
	in, out := syscall.Handle(toChild.Fd()), syscall.Handle(fromChild.Fd())
	cmd.SysProcAttr.AdditionalInheritedHandles = []syscall.Handle{in, out}
	return []string{strconv.FormatUint(uint64(in), 10), strconv.FormatUint(uint64(out), 10)}
}

// childPipes opens the protocol pipes from the handle values after the mode.
func childPipes(args []string) (io.Reader, io.Writer, error) {
	if len(args) < 2 {
		return nil, nil, errors.New("no protocol handles")
	}
	in, err := strconv.ParseUint(args[0], 10, 64)
	if err != nil {
		return nil, nil, err
	}
	out, err := strconv.ParseUint(args[1], 10, 64)
	if err != nil {
		return nil, nil, err
	}
	return os.NewFile(uintptr(in), "om-from-host"), os.NewFile(uintptr(out), "om-to-host"), nil
}
