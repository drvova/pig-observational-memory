package enginerpc_test

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/commands"
	"github.com/drvova/pig-observational-memory/memory/engine"
	"github.com/drvova/pig-observational-memory/memory/enginerpc"
	"github.com/drvova/pig-observational-memory/memory/hooks"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/lispimage"
	"github.com/drvova/pig-observational-memory/memory/omtool"
	"github.com/drvova/pig-observational-memory/memory/runtime"
	"github.com/drvova/pig-observational-memory/memory/testfix"
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
// socket, with no curated store (so the harness detectors and the compaction's evidence, which
// the image has not ported, do not run on either path) and level 1 off: session_start, both
// turn ends, the compaction on PiG's cut and the context event run on the image, and every
// appended entry, the compaction and every edited context message equal the in-process path's.
// The memory tool call runs on the image too, the scenario's Go pointer
// (om.History(om.Search("loader"))) run as the Lisp call it stands for in a child SBCL: its text
// is the Lisp tool's, so it is checked on its own (checkLispTool). Nothing falls back.
func TestLispEngineMatchesInProcess(t *testing.T) {
	image := buildLispImage(t)
	agentDir := settingsDir(t)
	want := scenario(t, newInProcess())
	log := &lockedLog{}
	got := scenario(t, overRPC{t: t, c: startLispEngine(t, image, agentDir, log)})
	checkLispTool(t, got)
	got.Tool, got.ToolText = want.Tool, want.ToolText
	compareTranscripts(t, "lisp", want, got)
	if lines := log.lines(); len(lines) != 0 {
		t.Fatalf("fallbacks:\n%s", strings.Join(lines, "\n"))
	}
}

// checkLispTool checks the scenario's memory tool call as the image answered it: the Go pointer
// ran as its Lisp call over the branch, and the details carry what level 0 and the detectors
// read back (the outcome, the search).
func checkLispTool(t *testing.T, got transcript) {
	t.Helper()
	if !strings.HasPrefix(got.ToolText, `;; om.History(om.Search("loader")) runs here as (history :search "loader")`+"\n") ||
		!strings.Contains(got.ToolText, "loader") {
		t.Fatalf("tool text:\n%s", got.ToolText)
	}
	d := omtool.DetailsFrom(got.Tool)
	if d == nil || d.Outcome != omtool.OutcomeOK || d.Code != `om.History(om.Search("loader"))` || !slices.Equal(d.Searches, []string{"loader"}) {
		t.Fatalf("tool details %+v", got.Tool)
	}
}

// TestLispMemoryTool runs the memory tool over engine-rpc on the image with no fallback: the
// initialize answer is the Lisp text, and model Lisp reads the branch, prints, errors and is
// refused in the shapes the adapter records (omtool.Details), each in a child SBCL.
func TestLispMemoryTool(t *testing.T) {
	image := buildLispImage(t)
	root := t.TempDir()
	l := &lispimage.Launcher{Root: root, Socket: filepath.Join(root, "engine.sock"), Image: image, Idle: 60, StartTimeout: 30 * time.Second}
	client := enginerpc.NewClient(enginerpc.SocketDialer(l), enginerpc.InitializeParams{AgentDir: settingsDir(t), ConfigDirName: ".pig"})
	client.RequireBuild(l.ExpectedBuild())
	spec, err := client.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = client.Close()
		stopImage(t, l)
	})
	if want := enginerpc.LispMemoryToolSpec(); spec.Description != want.Description || spec.PromptSnippet != want.PromptSnippet {
		t.Fatalf("the image serves %+v", spec)
	}
	h := newHost(codingTurn("p", "fix the loader"))
	for _, c := range []struct {
		code, text, outcome string
		isError             bool
	}{
		{`(format t "~a~%" (mapcar #'turn-role (history :search "loader")))`, "", omtool.OutcomeOK, false},
		{`(+ 40 2)`, "42", omtool.OutcomeOK, false},
		{`(car 1)`, "Error: ", omtool.OutcomeRuntime, true},
		{`(eval 1)`, "Error: refused: eval is denied in model code", omtool.OutcomeCompile, true},
		{`(recall "<id>")`, "Error: nothing ran: `<id>` is a placeholder", omtool.OutcomeCompile, true},
	} {
		res, err := client.ToolCall(h, omtool.ToolName, map[string]any{"code": c.code})
		if err != nil {
			t.Fatalf("%s: %v", c.code, err)
		}
		d := omtool.DetailsFrom(res.Details)
		if res.IsError != c.isError || !strings.HasPrefix(res.Text, c.text) || d == nil || d.Outcome != c.outcome || d.Code != c.code {
			t.Errorf("%s: %+v (details %+v)", c.code, res, res.Details)
		}
	}
	res, err := client.ToolCall(h, omtool.ToolName, map[string]any{"code": ""})
	if err != nil || res.IsError || !strings.HasPrefix(res.Text, "The memory tool runs Common Lisp") {
		t.Fatalf("manual: %+v %v", res, err)
	}
}

// TestLispBeforeCompactCancellation is TestBeforeCompactCancellationCrossesTheBoundary against
// the image: with level 1 on and a session model, the image makes the level-1 request itself
// (exactly one), PiG's cancellation crosses to its models/streamResult in flight, and the answer
// equals the in-process path's; nothing falls back. lisp/tests/level1.lisp checks the cancel's
// crossing from the image's side (level1-cancel-crosses-to-the-stream).
func TestLispBeforeCompactCancellation(t *testing.T) {
	image := buildLispImage(t)
	agentDir := settingsDir(t)
	must(t, os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"observational-memory": {"consolidate": true}}`), 0o600))
	log := &lockedLog{}
	client := startLispEngine(t, image, agentDir, log)
	want := cancelledCompaction(t, "in-process", newInProcess())
	got := cancelledCompaction(t, "lisp", overRPC{t: t, c: client})
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("in-process %v, lisp %v", want, got)
	}
	if lines := log.lines(); len(lines) != 0 {
		t.Fatalf("fallbacks:\n%s", strings.Join(lines, "\n"))
	}
}

// TestLispCommandsAndRendering is TestCommandsAndRendering against the image: /om:status falls
// back (not ported); the system-prompt section and /om:view run on the image, and the view's
// notifications equal the in-process path's.
func TestLispCommandsAndRendering(t *testing.T) {
	image := buildLispImage(t)
	agentDir := settingsDir(t)
	log := &lockedLog{}
	client := startLispEngine(t, image, agentDir, log)
	h := newHost(codingTurn("p", "fix the loader"))
	must(t, client.SessionStart(h))
	must(t, client.Command(h, enginerpc.CommandStatus, ""))
	if len(h.Messages()) != 1 || !strings.Contains(h.Messages()[0], "emory") {
		t.Fatalf("status %q", h.Messages())
	}
	// /om:status runs on the image (docs/lisp-port-status.md): every line before its own code
	// section is the in-process report's.
	inProcess, err := commands.StatusLines(runtime.New(), newHost(codingTurn("p", "fix the loader")))
	must(t, err)
	if got, want := withoutCodeSection(h.Messages()[0]), strings.Join(inProcess, "\n"); got != want {
		t.Fatalf("status:\nlisp: %s\ngo:   %s", got, want)
	}
	sections, err := client.BeforeAgentStart(h)
	must(t, err)
	if sections[enginerpc.MemorySection] != engine.ToolNoteLine {
		t.Fatalf("sections %v", sections)
	}
	if lines := log.lines(); len(lines) != 0 {
		t.Fatalf("fallbacks:\n%s", strings.Join(lines, "\n"))
	}
	view := func(run func(h *testfix.FakeHost, args string)) []testfix.Notification {
		h := newHost(codingTurn("p", "fix the loader"))
		hooks.OnTurnEnd(runtime.New(), h)
		for _, args := range []string{"", "full", "full", "bogus"} {
			run(h, args)
		}
		return h.Notifications
	}
	rt := runtime.New()
	want := view(func(h *testfix.FakeHost, args string) { must(t, commands.RunView(rt, h, args, nil)) })
	got := view(func(h *testfix.FakeHost, args string) { must(t, client.Command(h, enginerpc.CommandView, args)) })
	if len(want) != 4 || !reflect.DeepEqual(want, got) {
		t.Fatalf("view: in-process %q, lisp %q", want, got)
	}
	if len(log.lines()) != 0 {
		t.Fatalf("the view fell back:\n%s", strings.Join(log.lines(), "\n"))
	}
}

// withoutCodeSection is a status report without its code section, which the image redefines for
// the Lisp layer (docs/lisp-port-status.md); everything before it is compared with Go's.
func withoutCodeSection(report string) string {
	if i := strings.Index(report, "\n\n── Code ──"); i >= 0 {
		return report[:i]
	}
	return report
}

// TestLispTriggerMatchesInProcess runs the compaction trigger at agent_settled with PiG's
// auto-compaction off on both paths: over the threshold it asks PiG to compact, a "Nothing to
// compact" answer suspends it at that tip, and a new entry lifts the suspension; the host's
// compact calls and the notifications are the same.
func TestLispTriggerMatchesInProcess(t *testing.T) {
	image := buildLispImage(t)
	agentDir := settingsDir(t)
	must(t, os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"compaction": {"enabled": false, "reserveTokens": 20000}, "observational-memory": {"consolidate": false}}`), 0o600))
	log := &lockedLog{}
	client := startLispEngine(t, image, agentDir, log)
	run := func(settle func(h *testfix.FakeHost)) (int, []testfix.Notification) {
		h := newHost(codingTurn("p", "fix the loader"))
		tokens := 190_000
		h.Usage = &host.ContextUsage{Tokens: &tokens, ContextWindow: 200_000}
		h.CompactFn = func(callbacks host.CompactCallbacks) {
			callbacks.OnError(errors.New("Nothing to compact (session too small)"))
		}
		settle(h)
		settle(h)
		h.SetBranch(append(h.Entries, testfix.RawMessage("x1", "next")))
		settle(h)
		return h.CompactCount(), h.Notifications
	}
	rt := runtime.New()
	inProcess := func(h *testfix.FakeHost) {
		if done := hooks.MaybeTriggerCompaction(rt, h); done != nil {
			<-done
		}
		engine.SettleCurated(h)
	}
	overLisp := func(h *testfix.FakeHost) {
		before := len(h.Messages())
		must(t, client.AgentSettled(h))
		// The deferred step answers later: wait for its notification when the trigger fired. It
		// takes up to IdleRecheckAttempts × IdleRecheckDelay plus a host round trip; the deadline
		// is generous so a loaded machine (parallel race runs) does not fail it, and the loop
		// returns as soon as the step finishes.
		if n := len(h.Messages()); n > before {
			for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
				if len(h.Messages()) > n {
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("the deferred compaction step did not finish")
		}
	}
	wantCalls, want := run(inProcess)
	gotCalls, got := run(overLisp)
	if wantCalls != 2 || gotCalls != wantCalls || !reflect.DeepEqual(want, got) {
		t.Fatalf("in-process %d %q, lisp %d %q", wantCalls, want, gotCalls, got)
	}
	if len(log.lines()) != 0 {
		t.Fatalf("fallbacks:\n%s", strings.Join(log.lines(), "\n"))
	}
}
