package cli

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/drvova/pig-observational-memory/memory/config"
	"github.com/drvova/pig-observational-memory/memory/engine"
	"github.com/drvova/pig-observational-memory/memory/lispimage"
)

// The Lisp engine's client mode (docs/lisp-port-image-client.md): with OM_ENGINE=lisp, om
// forwards hook events, MCP messages and om call to the user's SBCL image over a unix socket,
// starting the image when the socket is absent or refuses. Without the variable nothing here
// runs and om is the Go engine, as before.
const (
	// EngineEnv selects the engine: "lisp" is the image; anything else is the Go engine.
	EngineEnv = "OM_ENGINE"
	// LispImageEnv is the image's executable (else the lispImage setting).
	LispImageEnv = lispimage.ImageEnv
	// LispSocketEnv moves the socket (default <root>/engine.sock).
	LispSocketEnv = lispimage.SocketEnv
	// LispIdleEnv is how many seconds the image stays up with no client (else the
	// lispIdleSeconds setting, else 900; 0 never exits).
	LispIdleEnv = lispimage.IdleEnv
	// LispFallbackEnv set to 0 makes a method the image has not ported, or an image that cannot
	// be started or reached, an error instead of a run of the Go engine.
	LispFallbackEnv = lispimage.FallbackEnv
)

// retryWait bounds how long a retry after a lost connection waits for the failed image to exit
// (lispimage.Launcher.WaitGone).
var retryWait = 5 * time.Second

// codeNotPorted is the image's error for a method whose engine work is not ported yet.
const codeNotPorted = -32004

// lispPassEnv are the client's environment variables the image reads per request (its own
// environment is the first client's, not this hook's).
var lispPassEnv = []string{config.PassiveEnv}

// lispError is an error answer from the image.
type lispError struct {
	Code    int
	Message string
}

func (e *lispError) Error() string { return e.Message }

// unreachableError is a failure to start or reach the image before any request was written:
// nothing ran, so the Go engine can run the event instead without doing anything twice. A
// failure after a request was written is not one, since the image may have acted on it.
type unreachableError struct{ err error }

func (e *unreachableError) Error() string { return e.err.Error() }
func (e *unreachableError) Unwrap() error { return e.err }

func isUnreachable(err error) bool {
	var ue *unreachableError
	return errors.As(err, &ue)
}

func isNotPorted(err error) bool {
	var le *lispError
	return errors.As(err, &le) && le.Code == codeNotPorted
}

// lispClient is one connection to the image, opened on first use and kept for the process
// (the MCP server sends every message over one connection). The launcher (memory/lispimage)
// finds and starts the image, as it does for the PiG adapter's socket dialer.
type lispClient struct {
	launcher *lispimage.Launcher
	fallback bool
	conn     net.Conn
	r        *bufio.Reader
	id       int
}

// newLispClient is the client of the image for root, or nil when om is not in Lisp mode.
func newLispClient(root string) *lispClient {
	if os.Getenv(EngineEnv) != "lisp" {
		return nil
	}
	return &lispClient{launcher: lispimage.FromEnv(root), fallback: lispimage.FallbackOn()}
}

func (c *lispClient) close() {
	if c.conn != nil {
		c.conn.Close()
		c.conn, c.r = nil, nil
	}
}

func (c *lispClient) use(conn net.Conn) {
	c.conn, c.r = conn, bufio.NewReaderSize(conn, 64*1024)
}

func (c *lispClient) dial() error {
	conn, err := c.launcher.Dial()
	if err != nil {
		return err
	}
	c.use(conn)
	return nil
}

// start runs the image and waits for it to listen (lispimage.Launcher.Start).
func (c *lispClient) start() error {
	conn, err := c.launcher.Start()
	if err != nil {
		if c.launcher.Image == "" {
			return fmt.Errorf("om: OM_ENGINE=lisp and no engine image listens on %s; set %s (or lispImage in %s)", c.launcher.Socket, LispImageEnv, filepath.Join(c.launcher.Root, "settings.json"))
		}
		return err
	}
	c.use(conn)
	return nil
}

// roundTrip sends one request and reads its answer on the open connection.
func (c *lispClient) roundTrip(method string, params any, result any) (transport error, err error) {
	c.id++
	line, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": c.id, "method": method, "params": params})
	if err != nil {
		return nil, err
	}
	if _, err := c.conn.Write(append(line, '\n')); err != nil {
		return err, nil
	}
	reply, err := c.r.ReadBytes('\n')
	if err != nil {
		return err, nil
	}
	var resp struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
	}
	if err := json.Unmarshal(reply, &resp); err != nil {
		return fmt.Errorf("bad answer %q: %w", strings.TrimSpace(string(reply)), err), nil
	}
	if resp.ID != c.id {
		return fmt.Errorf("answer to request %d, want %d", resp.ID, c.id), nil
	}
	if resp.Error != nil {
		return nil, &lispError{Code: resp.Error.Code, Message: resp.Error.Message}
	}
	if result == nil || len(resp.Result) == 0 {
		return nil, nil
	}
	return nil, json.Unmarshal(resp.Result, result)
}

// call runs method on the image: it connects (starting the image when the socket is absent or
// refuses), and when the connection fails it starts the image again and retries once.
func (c *lispClient) call(method string, params any, result any) error {
	for attempt := 0; ; attempt++ {
		if c.conn == nil {
			if err := c.dial(); err != nil {
				if err := c.start(); err != nil {
					if attempt == 0 {
						return &unreachableError{err}
					}
					return err
				}
			}
		}
		transport, err := c.roundTrip(method, params, result)
		if transport == nil {
			return err
		}
		c.close()
		if attempt > 0 {
			return fmt.Errorf("om: the engine image on %s: %w", c.launcher.Socket, transport)
		}
		// The image that failed may still be exiting, its listen backlog still accepting: the
		// retry waits until its lock is free, so it reaches a fresh image.
		c.launcher.WaitGone(retryWait)
	}
}

// lispResult is what the image answers an event with: the text to print (a context or a
// summary, without the final newline), a compaction for before-compact, and the Go steps the
// image has not ported that the client still runs after it (engine.SettleCurated).
type lispResult struct {
	Text string `json:"text"`
	// Compaction is kept as the image wrote it: its details are in ledger.MemoryDetails' field
	// order, which a decoded map would lose.
	Compaction json.RawMessage `json:"compaction"`
	Remaining  []string        `json:"remaining"`
	// Notice marks a view answer that is a note or the usage line, shown and never copied.
	Notice bool `json:"notice"`
}

// sessionParams are the session fields every event carries, plus the client's environment the
// image reads (lispPassEnv).
func (a *app) sessionParams(s Session, extra map[string]any) map[string]any {
	envs := map[string]any{}
	for _, k := range lispPassEnv {
		if v, ok := os.LookupEnv(k); ok {
			envs[k] = v
		}
	}
	p := map[string]any{"root": a.root, "harness": s.Harness, "session_id": s.ID, "transcript_path": s.Transcript, "format": s.Format, "cwd": s.Cwd, "env": envs}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// viaLisp runs one event on the image. handled is false when om is not in Lisp mode, or when
// the image has not ported the event and falling back is on: the caller then runs the Go
// engine, as without the image.
func (a *app) viaLisp(event string, s Session, extra map[string]any) (res lispResult, handled bool, err error) {
	if a.lisp == nil {
		return res, false, nil
	}
	if s.ID == "" {
		return res, true, errors.New("om: no session id")
	}
	if err := remember(a.root, s); err != nil {
		return res, true, err
	}
	err = a.lisp.call(event, a.sessionParams(s, extra), &res)
	if a.fallsBack(err) {
		return res, false, nil
	}
	if err != nil {
		return res, true, err
	}
	return res, true, a.runRemaining(s, res.Remaining)
}

// fallsBack reports whether the Go engine runs instead of the image: falling back is on and the
// image has not ported the method, or could not be started or reached before the request was
// written. An unreachable image is noted in om's log, since every event then runs on Go.
func (a *app) fallsBack(err error) bool {
	if !a.lisp.fallback {
		return false
	}
	if isUnreachable(err) {
		if a.log != nil {
			fmt.Fprintf(a.log, "om: the engine image is unreachable, running the Go engine: %v\n", err)
		}
		return true
	}
	return isNotPorted(err)
}

// runRemaining runs the Go steps the image named as not ported, under the session's engine
// lock, as the Go engine runs them after its own work.
func (a *app) runRemaining(s Session, steps []string) error {
	if len(steps) == 0 {
		return nil
	}
	h, err := s.Host(a.root, a.log)
	if err != nil {
		return err
	}
	release, err := lockEngine(h)
	if err != nil {
		return err
	}
	defer release()
	for _, step := range steps {
		switch step {
		case "migrate-layer":
			engine.MigrateLayer(h)
		case "settle-curated":
			engine.SettleCurated(h)
		default:
			return fmt.Errorf("om: the engine image asks for an unknown step %q", step)
		}
	}
	return nil
}

// mcpViaLisp forwards one MCP message to the image: ok false means om answers it itself (not
// in Lisp mode, or not ported with falling back on).
func (a *app) mcpViaLisp(harnessName, dir string, raw []byte) (answer json.RawMessage, ok bool, err error) {
	if a.lisp == nil {
		return nil, false, nil
	}
	var res struct {
		Message json.RawMessage `json:"message"`
	}
	err = a.lisp.call("mcp", map[string]any{"root": a.root, "harness": harnessName, "cwd": dir, "message": json.RawMessage(raw)}, &res)
	if a.fallsBack(err) {
		return nil, false, nil
	}
	return res.Message, true, err
}

// callViaLisp forwards om call's arguments to the image: {"text", "isError"}.
func (a *app) callViaLisp(harnessName, dir string, arguments map[string]any) (res map[string]any, ok bool, err error) {
	if a.lisp == nil {
		return nil, false, nil
	}
	err = a.lisp.call("call", map[string]any{"root": a.root, "harness": harnessName, "cwd": dir, "arguments": arguments}, &res)
	if a.fallsBack(err) {
		return nil, false, nil
	}
	return res, true, err
}
