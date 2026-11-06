package enginerpc_test

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/enginerpc"
	"github.com/drvova/pig-observational-memory/memory/lispimage"
)

// buildLispImage builds the SBCL engine image (lisp/build-image.sh) into a temporary directory,
// or skips without OM_LISP_PARITY=1 or sbcl.
func buildLispImage(t *testing.T) string {
	t.Helper()
	if os.Getenv("OM_LISP_PARITY") != "1" {
		t.Skip("set OM_LISP_PARITY=1 to build the SBCL engine image and run engine-rpc against it")
	}
	if _, err := exec.LookPath("sbcl"); err != nil {
		t.Skip("sbcl is not on PATH")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "lisp", "build-image.sh"))
	if err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(t.TempDir(), "om-engine")
	if out, err := exec.Command("sh", script, image).CombinedOutput(); err != nil {
		t.Fatalf("build-image: %v\n%s", err, out)
	}
	return image
}

// lockedLog is a log the client and the test read at once.
type lockedLog struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *lockedLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedLog) lines() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	text := strings.TrimSpace(l.b.String())
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// startLispEngine starts the image on a fresh root and connects a client the PiG adapter's
// "engine": "lisp" mode builds: the socket dialer, the image's build id required, and the Go
// engine (an in-process Server over pipes) as the fallback, logging to log.
func startLispEngine(t *testing.T, image, agentDir string, log *lockedLog) *enginerpc.Client {
	t.Helper()
	root := t.TempDir()
	l := &lispimage.Launcher{Root: root, Socket: filepath.Join(root, "engine.sock"), Image: image, Idle: 60, StartTimeout: 30 * time.Second}
	params := enginerpc.InitializeParams{AgentDir: agentDir, ConfigDirName: ".pig"}
	client := enginerpc.NewClient(enginerpc.SocketDialer(l), params)
	client.RequireBuild(l.ExpectedBuild())
	fallback := enginerpc.NewClient(enginerpc.PipeDialer(func(r io.Reader, w io.Writer) error { return enginerpc.NewServer().Serve(r, w) }), params)
	client.SetFallback(fallback, log)
	spec, err := client.Start()
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "memory" || spec.Description == "" {
		t.Fatalf("tool spec %+v", spec)
	}
	t.Cleanup(func() {
		_ = client.Close()
		_ = fallback.Close()
		stopImage(t, l)
	})
	return client
}

// stopImage stops the image (image/stop) and waits for its pid file to go.
func stopImage(t *testing.T, l *lispimage.Launcher) {
	conn, err := l.Dial()
	if err != nil {
		return
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(`{"jsonrpc":"2.0","id":1,"method":"image/stop"}` + "\n")); err != nil {
		t.Errorf("image/stop: %v", err)
		return
	}
	_, _ = bufio.NewReader(conn).ReadString('\n')
	for range 100 {
		if _, err := os.Stat(filepath.Join(l.Root, "engine.pid")); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("the image did not exit")
}

// TestLispEngineMatchesInProcess runs the engine-rpc scenario against the Lisp image over its
// socket, with no curated store (so the harness detectors, which the image has not ported, do
// not run on either path): session_start, both turn ends and the context event run on the
// image, and every appended entry and edited context message equals the in-process path's.
// The compaction and the memory tool call, which the image has not ported, are refused before
// any host write and run on the Go fallback, each logged once; no other event falls back.
func TestLispEngineMatchesInProcess(t *testing.T) {
	image := buildLispImage(t)
	agentDir := settingsDir(t)
	want := scenario(t, newInProcess())
	log := &lockedLog{}
	got := scenario(t, overRPC{t: t, c: startLispEngine(t, image, agentDir, log)})
	compareTranscripts(t, "lisp", want, got)
	lines := log.lines()
	if len(lines) != 2 || !strings.Contains(lines[0], enginerpc.MethodSessionBeforeCompact+": not ported") || !strings.Contains(lines[1], enginerpc.MethodToolCall+": not ported") {
		t.Fatalf("fallbacks:\n%s", strings.Join(lines, "\n"))
	}
}
