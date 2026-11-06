package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/config"
	"github.com/drvova/pig-observational-memory/memory/lispimage"
)

// fakeImageEnv makes the test binary, started as the engine image, serve as a fake one
// (runFakeImage): the client's start, wait and retry run against a real process.
const fakeImageEnv = "OM_TEST_FAKE_IMAGE"

func TestMain(m *testing.M) {
	if os.Getenv(fakeImageEnv) == "1" {
		os.Exit(runFakeImage(os.Args[1:]))
	}
	// The suites assume the Go engine unless a test chooses the image: the image is the default
	// only where sbcl can build it, which no test may depend on. OM_ENGINE=lisp in the
	// environment runs them on the image (the switch gate, docs/lisp-core.md).
	if os.Getenv(EngineEnv) == "" {
		os.Setenv(EngineEnv, config.EngineGo)
	}
	os.Exit(m.Run())
}

// fakeRequest is one request a fake image received.
type fakeRequest struct {
	Method string
	Params map[string]any
}

// fakeAnswer answers a request: a result, or an error code and message.
type fakeAnswer func(method string, params map[string]any) (result any, code int, message string)

// serveFake answers JSON-RPC lines on ln until it closes; requests are appended to *log.
// stop, when set, is called after the reply to image/stop.
func serveFake(ln net.Listener, answer fakeAnswer, mu *sync.Mutex, log *[]fakeRequest, onRequest func(), stop func()) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer conn.Close()
			r := bufio.NewReader(conn)
			for {
				line, err := r.ReadBytes('\n')
				if err != nil {
					return
				}
				if onRequest != nil {
					onRequest()
				}
				var req struct {
					ID     any            `json:"id"`
					Method string         `json:"method"`
					Params map[string]any `json:"params"`
				}
				if json.Unmarshal(line, &req) != nil {
					return
				}
				resp := map[string]any{"jsonrpc": "2.0", "id": req.ID}
				if req.Method != "initialize" {
					mu.Lock()
					*log = append(*log, fakeRequest{req.Method, req.Params})
					mu.Unlock()
				}
				if req.Method == "initialize" {
					// The client's build check (not logged): the embedded sources' id, else
					// OM_TEST_FAKE_BUILD's.
					build := lispimage.BuildID()
					if b := os.Getenv("OM_TEST_FAKE_BUILD"); b != "" {
						build = b
					}
					resp["result"] = map[string]any{"protocolVersion": 1, "buildId": build}
				} else if req.Method == "image/stop" {
					resp["result"] = map[string]any{}
				} else if result, code, message := answer(req.Method, req.Params); code != 0 {
					resp["error"] = map[string]any{"code": code, "message": message}
				} else {
					resp["result"] = result
				}
				out, _ := json.Marshal(resp)
				if _, err := conn.Write(append(out, '\n')); err != nil {
					return
				}
				if req.Method == "image/stop" && stop != nil {
					stop()
					return
				}
			}
		}()
	}
}

// defaultFakeAnswer ports nothing but turn-end, compact, before-compact, call and MCP's ping.
func defaultFakeAnswer(method string, params map[string]any) (any, int, string) {
	switch method {
	case "turn-end":
		return map[string]any{"appended": 1, "remaining": []string{"settle-curated"}}, 0, ""
	case "compact":
		return map[string]any{"text": "SUMMARY of " + fmt.Sprint(params["session_id"])}, 0, ""
	case "before-compact":
		return map[string]any{"compaction": map[string]any{"summary": "S", "firstKeptEntryId": params["first_kept_entry_id"]}}, 0, ""
	case "call":
		return map[string]any{"text": "called", "isError": false}, 0, ""
	case "mcp":
		msg, _ := params["message"].(map[string]any)
		if msg["method"] == "ping" {
			return map[string]any{"message": map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": map[string]any{"fake": true}}}, 0, ""
		}
	}
	return nil, codeNotPorted, "om engine: " + method + " is not ported to the Lisp engine yet"
}

// runFakeImage is the fake image's main: --root, --socket and --idle-seconds as the real one
// takes them. Each start is logged to <root>/fake-starts; with OM_TEST_FAKE_CRASH=1 the first
// start exits on its first request, as a crashed image would, leaving its socket behind.
func runFakeImage(args []string) int {
	opts := map[string]string{}
	for i := 0; i+1 < len(args); i += 2 {
		opts[args[i]] = args[i+1]
	}
	root, socket := opts["--root"], opts["--socket"]
	f, _ := os.OpenFile(filepath.Join(root, "fake-starts"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	fmt.Fprintf(f, "start idle=%s\n", opts["--idle-seconds"])
	f.Close()
	crash := false
	if os.Getenv("OM_TEST_FAKE_CRASH") == "1" {
		marker := filepath.Join(root, "crashed")
		if _, err := os.Stat(marker); err != nil {
			os.WriteFile(marker, nil, 0o600)
			crash = true
		}
	}
	// The real image's lock beside the socket: held for the process's life, exit 3 when taken.
	if _, ok := lispimage.TryLock((&lispimage.Launcher{Socket: socket}).LockPath()); !ok {
		return 3
	}
	os.Remove(socket)
	ln, err := net.Listen("unix", socket)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if l, ok := ln.(*net.UnixListener); ok {
		l.SetUnlinkOnClose(!crash)
	}
	var mu sync.Mutex
	var log []fakeRequest
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }
	onRequest := func() {
		if crash {
			os.Exit(1)
		}
	}
	go serveFake(ln, defaultFakeAnswer, &mu, &log, onRequest, stop)
	// The fake never outlives its test: image/stop, or ten seconds.
	select {
	case <-done:
	case <-time.After(10 * time.Second):
	}
	ln.Close()
	return 0
}

// fakeServer runs a fake image in this process on a socket under the test's root.
func fakeServer(t *testing.T, root string, answer fakeAnswer) (socket string, requests func() []fakeRequest) {
	t.Helper()
	socket = filepath.Join(root, "engine.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	var mu sync.Mutex
	var log []fakeRequest
	go serveFake(ln, answer, &mu, &log, nil, nil)
	return socket, func() []fakeRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]fakeRequest(nil), log...)
	}
}

func omErr(t *testing.T, stdin string, args ...string) (string, string, int) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := Run(args, strings.NewReader(stdin), &out, &errOut)
	return out.String(), errOut.String(), code
}

func genericFixture(t *testing.T, home string) string {
	t.Helper()
	gen := filepath.Join(home, "g.jsonl")
	var lines []string
	for i := range 20 {
		lines = append(lines, fmt.Sprintf(`{"id":"m%d","role":"user","text":"Host h-%d serves region r-%d on port %d.","timestamp":"2026-05-01T10:00:00Z"}`, i, i, i, 9000+i))
	}
	if err := os.WriteFile(gen, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, _ := json.Marshal(map[string]any{"harness": "mine", "session_id": "g1", "transcript_path": gen, "cwd": home})
	return string(p)
}

// With OM_ENGINE=go (the kill switch) om never touches the socket: the Go engine runs as before.
func TestLispModeOff(t *testing.T) {
	root, home := env(t)
	t.Setenv(EngineEnv, "go")
	socket, requests := fakeServer(t, root, defaultFakeAnswer)
	t.Setenv(LispSocketEnv, socket)
	p := genericFixture(t, home)
	om(t, p, "hook", "generic", "turn-end")
	if summary := om(t, p, "hook", "generic", "compact"); !strings.Contains(summary, "9000") {
		t.Fatalf("summary %s", summary)
	}
	if n := len(requests()); n != 0 {
		t.Fatalf("%d requests reached the socket", n)
	}
}

// With OM_ENGINE=lisp every event is forwarded with the session's fields; what the image has
// not ported runs on the Go engine, unless falling back is off; the steps the image names as
// remaining run in Go after it.
func TestLispForwardsEvents(t *testing.T) {
	root, home := env(t)
	t.Setenv(EngineEnv, "lisp")
	t.Setenv("PI_OBSERVATIONAL_MEMORY_PASSIVE", "0")
	socket, requests := fakeServer(t, root, defaultFakeAnswer)
	t.Setenv(LispSocketEnv, socket)
	p := genericFixture(t, home)

	if out := om(t, p, "hook", "generic", "turn-end"); out != "" {
		t.Fatalf("turn-end printed %q", out)
	}
	reqs := requests()
	if len(reqs) != 1 || reqs[0].Method != "turn-end" {
		t.Fatalf("requests %+v", reqs)
	}
	got := reqs[0].Params
	if got["root"] != root || got["harness"] != "mine" || got["session_id"] != "g1" || got["format"] != "generic" || got["cwd"] != home || !strings.HasSuffix(fmt.Sprint(got["transcript_path"]), "g.jsonl") {
		t.Fatalf("params %v", got)
	}
	if env, _ := got["env"].(map[string]any); env["PI_OBSERVATIONAL_MEMORY_PASSIVE"] != "0" {
		t.Fatalf("env %v", got["env"])
	}
	if _, err := os.Stat(filepath.Join(root, "sessions", "mine", "g1.jsonl")); err == nil {
		t.Fatal("the Go engine extracted: the image answered turn-end")
	}
	if s, ok := session(root, "mine", home); !ok || s.ID != "g1" {
		t.Fatalf("the current session was not recorded: %+v", s)
	}

	if out := om(t, p, "hook", "generic", "compact"); out != "SUMMARY of g1\n" {
		t.Fatalf("compact printed %q", out)
	}
	// session-start is not ported: the Go engine answers it.
	if out := om(t, p, "hook", "generic", "session-start"); !strings.Contains(out, "MCP server om") {
		t.Fatalf("session-start fell back to %q", out)
	}
	if reqs := requests(); reqs[len(reqs)-1].Method != "session-start" || reqs[len(reqs)-1].Params["with_summary"] != true {
		t.Fatalf("session-start params %+v", reqs[len(reqs)-1])
	}
	t.Setenv(LispFallbackEnv, "0")
	if out, errOut, code := omErr(t, p, "hook", "generic", "session-start"); code != 1 || out != "" || !strings.Contains(errOut, "session-start is not ported") {
		t.Fatalf("no fallback: exit %d out %q err %q", code, out, errOut)
	}
	t.Setenv(LispFallbackEnv, "")

	// Pi's before-compact carries its cut and prints the image's compaction.
	pp, _ := json.Marshal(map[string]any{"session_id": "p1", "transcript_path": filepath.Join(home, "pi.jsonl"), "cwd": home, "first_kept_entry_id": "e80", "tokens_before": 12})
	var res struct {
		Compaction map[string]any `json:"compaction"`
	}
	if err := json.Unmarshal([]byte(om(t, string(pp), "hook", "pi", "before-compact")), &res); err != nil || res.Compaction["firstKeptEntryId"] != "e80" {
		t.Fatalf("pi before-compact %+v %v", res, err)
	}
	if last := requests()[len(requests())-1]; last.Params["harness"] != "pi" || last.Params["format"] != "pi" || last.Params["tokens_before"] != float64(12) {
		t.Fatalf("pi params %+v", last)
	}

	// A hook family: Claude Code's Stop is turn-end, PreCompact is compact with the trigger.
	tr := filepath.Join(home, "t.jsonl")
	claudeTranscript(t, tr, 3)
	cp, _ := json.Marshal(map[string]any{"session_id": "s1", "transcript_path": tr, "cwd": home, "trigger": "auto", "source": "compact"})
	om(t, string(cp), "hook", "claude-code", "Stop")
	om(t, string(cp), "hook", "claude-code", "PreCompact")
	reqs = requests()
	stop, pre := reqs[len(reqs)-2], reqs[len(reqs)-1]
	if stop.Method != "turn-end" || stop.Params["format"] != "claude-code" || pre.Method != "compact" || pre.Params["reason"] != "auto" {
		t.Fatalf("family requests %+v %+v", stop, pre)
	}
	var start struct {
		Out struct {
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(om(t, string(cp), "hook", "claude-code", "SessionStart")), &start); err != nil || !strings.Contains(start.Out.Context, "MCP server om") {
		t.Fatalf("family session-start %+v %v", start, err)
	}
	if last := requests()[len(requests())-1]; last.Method != "session-start" || last.Params["with_summary"] != true {
		t.Fatalf("family session-start params %+v", last)
	}

	// MCP: the image answers ping; tools/list is not ported, so om answers it.
	in := `{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n" + `{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" + `{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n"
	lines := strings.Split(strings.TrimSpace(om(t, in, "mcp", "--harness", "claude-code", "--dir", home)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], `"fake":true`) || !strings.Contains(lines[1], `"name":"memory"`) {
		t.Fatalf("mcp %q", lines)
	}
	if out := om(t, `{"code":"1"}`, "call", "--harness", "mine", "--dir", home); !strings.Contains(out, `"text":"called"`) {
		t.Fatalf("call %q", out)
	}
	last := requests()[len(requests())-1]
	if args, _ := last.Params["arguments"].(map[string]any); last.Method != "call" || args["code"] != "1" {
		t.Fatalf("call request %+v", last)
	}
}

// The image is started when the socket is absent, waited for, and reused by later events; an
// image that dies mid-request is started again and the request retried once.
func TestLispStartsImage(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, crash := range []bool{false, true} {
		t.Run("crash="+strconv.FormatBool(crash), func(t *testing.T) {
			root, home := env(t)
			t.Setenv(EngineEnv, "lisp")
			t.Setenv(LispImageEnv, exe)
			t.Setenv(LispSocketEnv, "")
			t.Setenv(LispIdleEnv, "7")
			t.Setenv(fakeImageEnv, "1")
			t.Setenv("OM_TEST_FAKE_CRASH", map[bool]string{true: "1"}[crash])
			p := genericFixture(t, home)
			if out := om(t, p, "hook", "generic", "compact"); out != "SUMMARY of g1\n" {
				t.Fatalf("compact %q", out)
			}
			om(t, p, "hook", "generic", "compact")
			starts, _ := os.ReadFile(filepath.Join(root, "fake-starts"))
			want := "start idle=7\n"
			if crash {
				want += want
			}
			if string(starts) != want {
				t.Fatalf("starts %q, want %q", starts, want)
			}
			if _, err := os.Stat(filepath.Join(root, "engine.log")); err != nil {
				t.Fatal(err)
			}
			c := newLispClient(root)
			if err := c.call("image/stop", map[string]any{}, nil); err != nil {
				t.Fatal(err)
			}
			c.close()
		})
	}
}

// With no image running and none configured, an event runs on the Go engine; with falling back
// off it fails with what to set.
func TestLispNoImage(t *testing.T) {
	_, home := env(t)
	t.Setenv(EngineEnv, "lisp")
	t.Setenv(LispImageEnv, "")
	t.Setenv(LispSocketEnv, "")
	p := genericFixture(t, home)
	om(t, p, "hook", "generic", "turn-end")
	if summary := om(t, p, "hook", "generic", "compact"); !strings.Contains(summary, "9000") {
		t.Fatalf("summary on the Go engine %s", summary)
	}
	t.Setenv(LispFallbackEnv, "0")
	if _, errOut, code := omErr(t, p, "hook", "generic", "turn-end"); code != 1 || !strings.Contains(errOut, LispImageEnv) {
		t.Fatalf("exit %d: %s", code, errOut)
	}
}

// An image that cannot start is unreachable (the Go engine may run instead); other errors are
// not. An image that dies after a request was written is retried, never replaced by a Go run
// (TestLispStartsImage's crash case).
func TestLispUnreachableImage(t *testing.T) {
	if !isUnreachable(&unreachableError{errors.New("x")}) || isUnreachable(errors.New("x")) {
		t.Fatal("isUnreachable")
	}
	root, _ := env(t)
	t.Setenv(EngineEnv, "lisp")
	t.Setenv(LispImageEnv, filepath.Join(root, "no-such-image"))
	t.Setenv(LispSocketEnv, "")
	c := newLispClient(root)
	if err := c.call("ping", map[string]any{}, nil); !isUnreachable(err) {
		t.Fatalf("missing image: %v", err)
	}
}
