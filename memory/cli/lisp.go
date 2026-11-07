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
	"github.com/drvova/pig-observational-memory/memory/enginerpc"
	"github.com/drvova/pig-observational-memory/memory/gitrepo"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/lispimage"
)

// The Lisp engine's client mode (docs/lisp-port-image-client.md, docs/lisp-core.md "The
// switch"): by default (lispimage.Select) om forwards hook events, MCP messages and om call to
// the user's SBCL image over a unix socket, starting the image when the socket is absent or
// refuses, and building it from the embedded sources on first use. OM_ENGINE=go (or "engine":
// "go" in the root's settings, or a platform without the image's transport) is the Go engine,
// and then nothing here runs.
const (
	// EngineEnv selects the engine: "lisp" is the image, "go" (or any other value) the Go
	// engine; unset, the engine setting, else the image.
	EngineEnv = config.EngineEnv
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
	// explicit is true when OM_ENGINE or the engine setting chose the image (not the default).
	explicit bool
	conn     net.Conn
	r        *bufio.Reader
	id       int
	// view answers the image's host/structure calls during one request (withView), nil
	// answering none.
	view func(q host.StructureQuery) (host.StructureAnswer, bool, error)
}

// newLispClient is the client of the image for root, or nil when om is not in Lisp mode.
func newLispClient(root string) *lispClient {
	sel := lispimage.Select(root)
	if !sel.Lisp {
		return nil
	}
	return &lispClient{launcher: lispimage.FromEnv(root), fallback: lispimage.FallbackOn(), explicit: sel.Explicit}
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

// connect opens the connection: to the image listening on the socket, else to one it starts.
// When the build the image must report is known (the embedded build, or a .build file beside a
// configured image), it asks the image for its build id first (initialize) and stops an image
// built from other sources (an om upgraded under a running image), then starts the current one.
// Everything here comes before the event's request, so a failure is a failover.
func (c *lispClient) connect() error {
	if err := c.dial(); err != nil {
		if err := c.start(); err != nil {
			return err
		}
	}
	want := c.launcher.ExpectedBuild()
	if want == "" {
		return nil
	}
	for stopped := false; ; stopped = true {
		var res struct {
			BuildID string `json:"buildId"`
		}
		transport, err := c.roundTrip("initialize", map[string]any{"protocolVersion": 1}, &res)
		if transport != nil || err != nil {
			c.close()
			if transport != nil {
				return transport
			}
			return err
		}
		if res.BuildID == want {
			return nil
		}
		if stopped {
			c.close()
			return fmt.Errorf("om: the engine image on %s reports build %q, not %q", c.launcher.Socket, res.BuildID, want)
		}
		_, _ = c.roundTrip("image/stop", map[string]any{}, nil)
		c.close()
		c.launcher.WaitGone(retryWait)
		if err := c.start(); err != nil {
			return err
		}
	}
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
	var resp struct {
		ID     int             `json:"id"`
		Result json.RawMessage `json:"result"`
		Error  *rpcError       `json:"error"`
		// A request of the image's own (host/structure) carries a method, and its id is the
		// image's, answered before the answer to ours is read.
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	for {
		reply, err := c.r.ReadBytes('\n')
		if err != nil {
			return err, nil
		}
		resp.ID, resp.Result, resp.Error, resp.Method, resp.Params = 0, nil, nil, "", nil
		if err := json.Unmarshal(reply, &resp); err != nil {
			return fmt.Errorf("bad answer %q: %w", strings.TrimSpace(string(reply)), err), nil
		}
		if resp.Method == "" {
			break
		}
		var call struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(reply, &call)
		if len(call.ID) == 0 || string(call.ID) == "null" {
			continue // a notification: nothing to answer
		}
		if err := c.answerHost(call.ID, resp.Method, resp.Params); err != nil {
			return err, nil
		}
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

// answerHost answers one host call the image made while it ran our request: host/structure
// from the request's view (c.view, the repository om read; docs/engine-rpc.md), and an error for
// every other method or with no view. Only a transport failure is returned.
func (c *lispClient) answerHost(id json.RawMessage, method string, params json.RawMessage) error {
	reply := map[string]any{"jsonrpc": "2.0", "id": id}
	switch {
	case method == enginerpc.MethodStructure && c.view != nil:
		var p enginerpc.StructureParams
		if err := json.Unmarshal(params, &p); err != nil {
			reply["error"] = rpcError{Code: -32602, Message: "om: " + err.Error()}
			break
		}
		var res enginerpc.StructureResult
		if a, ok, err := c.view(p.Query); err == nil && ok {
			res.Answer = &a
		}
		reply["result"] = res
	default:
		reply["error"] = rpcError{Code: -32601, Message: "om: the client answers no " + method}
	}
	line, err := json.Marshal(reply)
	if err != nil {
		return err
	}
	_, err = c.conn.Write(append(line, '\n'))
	return err
}

// call runs method on the image: it connects (starting the image when the socket is absent or
// refuses), and when the connection fails it starts the image again and retries once.
func (c *lispClient) call(method string, params any, result any) error {
	for attempt := 0; ; attempt++ {
		if c.conn == nil {
			if err := c.connect(); err != nil {
				if attempt == 0 {
					return &unreachableError{err}
				}
				return err
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
	// Notices are the info messages the image's own steps would tell the user (the curated
	// settle's, harness.NotifySettled), written to om's log as neutral.Host.Notify writes them.
	Notices []string `json:"notices"`
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
	params := a.sessionParams(s, extra)
	switch event {
	case "turn-end", "compact", "before-compact", "prompt":
		defer a.withView(params, s.Cwd)()
	}
	err = a.lisp.call(event, params, &res)
	if a.fallsBack(event, err) {
		return res, false, nil
	}
	if err != nil {
		return res, true, err
	}
	if a.log != nil {
		for _, notice := range res.Notices {
			fmt.Fprintln(a.log, notice)
		}
	}
	return res, true, a.runRemaining(s, res.Remaining)
}

// withView hands the image the repository holding cwd and its HEAD structure view, as
// neutral.Host's Repo and Structure hand them to the Go engine: the repo param (level 0's frame)
// and "structure": true, so the image asks host/structure on the connection while it runs the
// request (project records' body hashes and freshness verdicts, om.Head), answered here with
// gitrepo.Structure. The image never runs git. The returned function ends the view.
func (a *app) withView(params map[string]any, cwd string) func() {
	repo, ok, err := (gitrepo.Reader{Store: a.root}).Read(cwd)
	if err != nil || !ok {
		return func() {}
	}
	params["repo"] = repo
	params["structure"] = true
	a.lisp.view = func(q host.StructureQuery) (host.StructureAnswer, bool, error) {
		return gitrepo.Structure(cwd, repo, q)
	}
	return func() { a.lisp.view = nil }
}

// fallsBack reports whether the Go engine runs instead of the image: falling back is on and the
// image has not ported the method, or could not be started or reached before the request was
// written (no sbcl to build it, a build still running, a start that fails). Every fallback is
// one line in <root>/engine.log with its reason; with OM_ENGINE=lisp (or the setting) an
// unreachable image is noted in om's log too, as before the image was the default.
func (a *app) fallsBack(event string, err error) bool {
	if !a.lisp.fallback {
		return false
	}
	if isUnreachable(err) {
		lispimage.LogFailover(a.root, event, err.Error())
		if a.log != nil && a.lisp.explicit {
			fmt.Fprintf(a.log, "om: the engine image is unreachable, running the Go engine: %v\n", err)
		}
		return true
	}
	if isNotPorted(err) {
		lispimage.LogFailover(a.root, event, "not ported: "+err.Error())
		return true
	}
	return false
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
			// The image migrates the layer itself; it leaves the step to Go only under a
			// lowered kernel floor (lisp/src/server/migration.lisp).
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
	params := a.toolParams(harnessName, dir, map[string]any{"message": json.RawMessage(raw)})
	var message struct {
		Method string `json:"method"`
	}
	if json.Unmarshal(raw, &message) == nil && message.Method == "tools/call" {
		defer a.withView(params, params["cwd"].(string))()
	}
	err = a.lisp.call("mcp", params, &res)
	if a.fallsBack("mcp", err) {
		return nil, false, nil
	}
	return res.Message, true, err
}

// callViaLisp forwards om call's arguments to the image: {"text", "isError"}.
func (a *app) callViaLisp(harnessName, dir string, arguments map[string]any) (res map[string]any, ok bool, err error) {
	if a.lisp == nil {
		return nil, false, nil
	}
	params := a.toolParams(harnessName, dir, map[string]any{"arguments": arguments})
	defer a.withView(params, params["cwd"].(string))()
	err = a.lisp.call("call", params, &res)
	if a.fallsBack("call", err) {
		return nil, false, nil
	}
	return res, true, err
}

// toolParams are a memory tool request's params for the image: the session callTool would read
// (the current one in dir, else the scratch session of an MCP client with no transcript), as
// sessionParams gives an event's, so the image reads the branch the Go engine would.
func (a *app) toolParams(harnessName, dir string, extra map[string]any) map[string]any {
	s, ok := session(a.root, harnessName, dir)
	if !ok {
		s = scratchSession(harnessName, dir)
	}
	return a.sessionParams(s, extra)
}
