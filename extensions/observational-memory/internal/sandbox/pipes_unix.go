//go:build unix

package sandbox

import (
	"errors"
	"io"
	"os"
	"os/exec"
)

// attachPipes passes the protocol pipes as the child's fds 3 (host to child) and 4 (child to
// host). Every other descriptor of the extension's is close-on-exec, so none reaches the child.
func attachPipes(cmd *exec.Cmd, toChild, fromChild *os.File) []string {
	cmd.ExtraFiles = []*os.File{toChild, fromChild}
	return nil
}

// childPipes opens the protocol pipes in the child. Our child main opens them only after every
// init in the binary has run.
func childPipes([]string) (io.Reader, io.Writer, error) {
	in, out := os.NewFile(3, "om-from-host"), os.NewFile(4, "om-to-host")
	if _, err := in.Stat(); err != nil {
		return nil, nil, errors.New("no protocol pipe on fd 3")
	}
	if _, err := out.Stat(); err != nil {
		return nil, nil, errors.New("no protocol pipe on fd 4")
	}
	return in, out, nil
}
