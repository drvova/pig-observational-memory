//go:build linux

package sandbox

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// helperEnv names the mode of TestReapHelper when this binary runs as a stand-in extension.
const helperEnv = "OM_SANDBOX_REAP_HELPER"

// TestReapHelper is not a test: run as a subprocess with helperEnv set, it plays the extension,
// starts a child (idle, or busy in a loop), prints the child's pid and waits to be killed.
func TestReapHelper(t *testing.T) {
	mode := os.Getenv(helperEnv)
	if mode == "" {
		t.Skip("a helper process for the reap tests")
	}
	s := New(Config{noPdeathsig: strings.HasSuffix(mode, "-eof")})
	if r := runCode(t, context.Background(), s, "1", nil); r.Outcome != OutcomeOK {
		fmt.Println("error", r.Err)
		os.Exit(1)
	}
	pid := livePID(s)
	if strings.HasPrefix(mode, "busy") {
		p := mustCheck(t, "for {}")
		go s.Run(context.Background(), Request{Program: p}, fake{})
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Println("pid", pid)
	select {}
}

// When the extension dies some other way than PiG's group kill, the child dies too: at once by
// Pdeathsig on Linux, and by EOF on the protocol pipe everywhere, idle or busy in a loop.
func TestChildDiesWithTheExtension(t *testing.T) {
	for _, mode := range []string{"idle", "busy", "idle-eof", "busy-eof"} {
		t.Run(mode, func(t *testing.T) {
			cmd := exec.Command(os.Args[0], "-test.run=^TestReapHelper$", "-test.v")
			cmd.Env = append(os.Environ(), helperEnv+"="+mode)
			// Its own process group, so killing it is not PiG's group kill: only the helper dies.
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			out, err := cmd.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			defer cmd.Process.Kill()
			pid := 0
			sc := bufio.NewScanner(out)
			deadline := time.Now().Add(time.Minute)
			for pid == 0 && time.Now().Before(deadline) && sc.Scan() {
				if f := strings.Fields(sc.Text()); len(f) == 2 && f[0] == "pid" {
					pid, _ = strconv.Atoi(f[1])
				}
			}
			if pid == 0 {
				t.Fatal("the helper printed no child pid")
			}
			go func() {
				for sc.Scan() {
				}
			}()
			if gone(pid) {
				t.Fatal("the child is not running")
			}
			if err := cmd.Process.Kill(); err != nil {
				t.Fatal(err)
			}
			_ = cmd.Wait()
			if !waitGone(pid, 10*time.Second) {
				syscall.Kill(pid, syscall.SIGKILL)
				t.Fatalf("child %d outlived the extension", pid)
			}
		})
	}
}
