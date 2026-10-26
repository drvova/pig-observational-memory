package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/codepolicy"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om/omwire"
)

// Handler answers the om calls of the call in flight. It takes the call's context and stops on
// it; an answer given after the context ended is dropped.
type Handler interface {
	Call(ctx context.Context, method string, args json.RawMessage) (json.RawMessage, error)
}

// HandlerFunc is a Handler that is a function.
type HandlerFunc func(ctx context.Context, method string, args json.RawMessage) (json.RawMessage, error)

// Call calls f.
func (f HandlerFunc) Call(ctx context.Context, method string, args json.RawMessage) (json.RawMessage, error) {
	return f(ctx, method, args)
}

// Component is a strongly connected set of saved units, the smallest unit of isolation at
// replay: a forward reference, a method stored before its type and mutual recursion cannot load
// one unit at a time.
type Component struct {
	Units []Unit
	// Deps are the indexes of the earlier components this one refers to.
	Deps []int
}

// NamespaceMethods are the methods the namespace child may call: every om read and write, kept,
// forget and man. Str is answered inside the child.
func NamespaceMethods() []string {
	var out []string
	for _, m := range omwire.Methods {
		if m != omwire.MStr {
			out = append(out, m)
		}
	}
	return out
}

// Config is what a supervisor runs.
type Config struct {
	// Mode is the child's mode (ModeNamespace).
	Mode string
	// Methods are the om methods the host answers for this child; any other kills the child.
	Methods []string
	// Units returns the saved units in component order; it is called at every start.
	Units func() []Component

	// exe is the binary to re-execute, os.Executable() when empty (tests set it).
	exe string
	// noPdeathsig leaves Pdeathsig off, so a test sees EOF alone end the child.
	noPdeathsig bool
}

// Supervisor owns one long-lived child: it starts it lazily, replays the saved units, runs one
// call at a time, and kills it when the call's context ends.
type Supervisor struct {
	cfg Config
	// sem serializes calls: only the call holding it owns the child. A call whose context ends
	// while it waits stops waiting and never touches the child.
	sem chan struct{}

	mu  sync.Mutex
	cur *proc
	// resets counts the Kill calls, so a child booted while one came is dropped rather than
	// installed with the units read before the reset.
	resets      uint64
	gen         uint64
	callID      uint64
	starts      int
	lastCrash   string
	oom         string
	loadErrors  []LoadError
	quarantined map[string]LoadError
}

// LoadError is a component that did not load: failed (with the interpreter's message), a
// dependent of one that failed, or quarantined for crashing the child at replay.
type LoadError struct {
	Units []string
	Msg   string
}

// Status is the supervisor's recorded state; reading it starts, kills or replays nothing.
type Status struct {
	Generation uint64
	Starts     int
	Live       bool
	LastCrash  string
	// OOM is "set" when the child made itself the out-of-memory killer's first pick, else why
	// it did not.
	OOM         string
	LoadErrors  []LoadError
	Quarantined []LoadError
}

// New makes a supervisor; no child starts until a call needs one.
func New(cfg Config) *Supervisor {
	if cfg.Mode == "" {
		cfg.Mode = ModeNamespace
	}
	if cfg.Methods == nil {
		cfg.Methods = NamespaceMethods()
	}
	if cfg.Units == nil {
		cfg.Units = func() []Component { return nil }
	}
	return &Supervisor{cfg: cfg, sem: make(chan struct{}, 1), quarantined: map[string]LoadError{}}
}

// Status reports the recorded state.
func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := Status{Generation: s.gen, Starts: s.starts, Live: s.cur != nil, LastCrash: s.lastCrash, OOM: s.oom}
	st.LoadErrors = slices.Clone(s.loadErrors)
	for _, k := range sortedKeys(s.quarantined) {
		st.Quarantined = append(st.Quarantined, s.quarantined[k])
	}
	return st
}

// Kill stops the child, if one runs (session_start, session_shutdown, forget). A call running
// on it returns cancelled. The next call starts a new child.
func (s *Supervisor) Kill() {
	s.mu.Lock()
	p := s.cur
	s.cur = nil
	s.resets++
	s.mu.Unlock()
	if p != nil {
		p.kill()
	}
}

// Request is one call: the code as the policy checked and split it, and the named strings.
type Request struct {
	Program codepolicy.Program
	Strings map[string]string
}

// Result is what a call returned.
type Result struct {
	Outcome string
	// Out is what the code printed, Val the rendered last value (HasVal when there was one),
	// and Err the error; together within ResultChars. Cut counts the characters left out.
	Out, Val, Err string
	HasVal        bool
	Cut           int
	// Discarded counts the expression statements before the last whose values the code
	// dropped: only the last expression's value is shown. DiscardedFailed counts the failures
	// among them (a non-nil last error, or a seeded function's "error: " string), and
	// DiscardedErrs holds their texts within ResultChars together, so a refused write before
	// the last expression is still reported.
	Discarded       int
	DiscardedFailed int
	DiscardedErrs   []string
	// Compiled has one character per chunk: 'c' for a declaration that compiled, 'x' for one
	// that failed, '-' otherwise.
	Compiled string
	// Generation is the child generation that ran the call (0 when none did).
	Generation uint64
	// Restarted reports that the namespace restarts from the saved definitions, so unsaved
	// variables are gone: the call was cancelled or the child crashed.
	Restarted bool
	// Waited reports a call cancelled while it waited for another call: the child was not
	// touched.
	Waited bool
}

// Run runs one call on the child, starting it when none runs.
func (s *Supervisor) Run(ctx context.Context, req Request, h Handler) Result {
	// Imports are no-ops: every allow-listed package is pre-imported. The others run, and the
	// child's mask is mapped back onto every chunk.
	var run []wireChunk
	var index []int
	for i, c := range req.Program.Chunks {
		switch c.Kind {
		case codepolicy.Import:
			continue
		case codepolicy.Declaration:
			run = append(run, wireChunk{Kind: kindDeclaration, Src: c.Src, Line: c.Line, Names: c.Names()})
		default:
			run = append(run, wireChunk{Kind: kindStatement, Src: c.Src, Line: c.Line, Expr: c.DropsValue()})
		}
		index = append(index, i)
	}
	res := s.run(ctx, run, req.Program.LastExpr, req.Strings, h)
	if res.Compiled != "" {
		mask := []byte(strings.Repeat(string(maskNotRun), len(req.Program.Chunks)))
		for k, i := range index {
			if k < len(res.Compiled) {
				mask[i] = res.Compiled[k]
			}
		}
		res.Compiled = string(mask)
	}
	return res
}

// run runs chunks as they are; Run builds them from a checked program, and the tests use it to
// send code the policy would refuse.
func (s *Supervisor) run(ctx context.Context, chunks []wireChunk, last bool, strs map[string]string, h Handler) Result {
	waited := func() Result {
		return Result{Outcome: OutcomeCancelled, Err: "cancelled (" + ctx.Err().Error() + ") while another memory call ran; the namespace was not touched", Waited: true}
	}
	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return waited()
	}
	defer func() { <-s.sem }()
	if ctx.Err() != nil {
		return waited()
	}
	p, err := s.ensure(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return Result{Outcome: OutcomeCancelled, Err: cancelText(ctx), Restarted: true}
		}
		return Result{Outcome: OutcomeCrashed, Err: err.Error(), Restarted: true}
	}
	s.mu.Lock()
	s.callID++
	id := s.callID
	s.mu.Unlock()
	if err := p.send(msg{T: tRun, Gen: p.gen, Call: id, Chunks: chunks, Strings: strs, Last: last}, HostLineBytes); err != nil {
		return s.lost(p)
	}
	for {
		select {
		case <-ctx.Done():
			s.drop(p)
			return Result{Outcome: OutcomeCancelled, Err: cancelText(ctx), Generation: p.gen, Restarted: true}
		case m, ok := <-p.msgs:
			if !ok {
				return s.lost(p)
			}
			if m.Gen != p.gen || m.Call != id {
				s.drop(p)
				return s.crashed(p, fmt.Sprintf("a %s message from another child generation or call", m.T))
			}
			switch m.T {
			case tCall:
				if !slices.Contains(s.cfg.Methods, m.M) {
					s.drop(p)
					return s.crashed(p, fmt.Sprintf("the child called %q, which this child may not call", m.M))
				}
				if n, err := argChars(m.Args); err != nil || n > ArgChars {
					s.drop(p)
					return s.crashed(p, fmt.Sprintf("the arguments of %s were over the bound of %d characters", m.M, ArgChars))
				}
				if ctx.Err() != nil {
					continue
				}
				res, err := h.Call(ctx, m.M, m.Args)
				if ctx.Err() != nil {
					continue
				}
				if errors.Is(err, omwire.ErrBadArgs) {
					// The stubs always send what decodes: the child is corrupted.
					s.drop(p)
					return s.crashed(p, fmt.Sprintf("the arguments of %s do not decode into its parameters", m.M))
				}
				reply := msg{T: tReply, Gen: p.gen, Call: id, Seq: m.Seq, Res: res}
				if err != nil {
					reply.Res, reply.Err = nil, err.Error()
				}
				if err := p.send(reply, HostLineBytes); errors.Is(err, errLineOverBound) {
					err = p.send(msg{T: tReply, Gen: p.gen, Call: id, Seq: m.Seq, Err: "the answer was over the protocol bound"}, HostLineBytes)
					if err != nil {
						return s.lost(p)
					}
				} else if err != nil {
					return s.lost(p)
				}
			case tResult:
				res := Result{Outcome: m.Outcome, Out: m.Out, Val: m.Val, HasVal: m.HasVal, Err: m.Err, Cut: m.Cut, Compiled: m.Compiled, Discarded: m.Discarded, DiscardedFailed: m.DiscardedFailed, DiscardedErrs: m.DiscardedErrs, Generation: p.gen}
				if utf8.RuneCountInString(res.Out)+utf8.RuneCountInString(res.Val)+utf8.RuneCountInString(res.Err) > ResultChars {
					// The child keeps to the bound; the extension does not rely on it.
					res.Err, _ = cutChars(res.Err, ResultChars)
					res.Out, _ = cutChars(res.Out, ResultChars-utf8.RuneCountInString(res.Err))
					res.Val, _ = cutChars(res.Val, ResultChars-utf8.RuneCountInString(res.Err)-utf8.RuneCountInString(res.Out))
				}
				if m.Exit {
					// The child could not restore a failed declaration and exits; the next call
					// starts a clean one.
					s.drop(p)
					res.Restarted = true
				}
				return res
			default:
				s.drop(p)
				return s.crashed(p, fmt.Sprintf("an unexpected %s message", m.T))
			}
		}
	}
}

// lost is the result of a call whose child stopped answering, or whose pipe broke: cancelled
// when the host killed the child (Kill: a reset), crashed otherwise.
func (s *Supervisor) lost(p *proc) Result {
	if p.killedByHost.Load() {
		return Result{Outcome: OutcomeCancelled, Err: "the namespace was reset while the call ran", Generation: p.gen, Restarted: true}
	}
	return s.crashed(p, "")
}

func cancelText(ctx context.Context) string {
	return "cancelled (" + ctx.Err().Error() + "): the namespace restarts from the saved definitions, so unsaved variables are gone"
}

// drop kills p and forgets it.
func (s *Supervisor) drop(p *proc) {
	s.mu.Lock()
	if s.cur == p {
		s.cur = nil
	}
	s.mu.Unlock()
	p.kill()
}

// crashed waits for p to die and reports it, with why when the host killed it.
func (s *Supervisor) crashed(p *proc, why string) Result {
	<-p.done
	s.mu.Lock()
	if s.cur == p {
		s.cur = nil
	}
	s.mu.Unlock()
	text := p.report(why)
	s.mu.Lock()
	s.lastCrash = p.fatal()
	if why != "" {
		s.lastCrash = why
	}
	s.mu.Unlock()
	return Result{Outcome: OutcomeCrashed, Err: text, Generation: p.gen, Restarted: true}
}

// ensure returns the live child, starting one and replaying the saved units when none runs. A
// Kill that comes while the child boots finds no child to kill, so the booted child, replayed
// from units read before the reset, is dropped and another booted.
func (s *Supervisor) ensure(ctx context.Context) (*proc, error) {
	for {
		s.mu.Lock()
		p, resets := s.cur, s.resets
		s.mu.Unlock()
		if p != nil {
			return p, nil
		}
		p, err := s.boot(ctx)
		if err != nil {
			return nil, err
		}
		s.mu.Lock()
		if s.resets == resets {
			s.cur = p
			s.mu.Unlock()
			return p, nil
		}
		s.mu.Unlock()
		p.kill()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
}

// boot starts a child and replays the saved units. A component that fails to load is left
// out with every component that depends on it, transitively, and the child restarts without
// them until a start reports no failure. A child that dies during replay has the component
// that kills it found and quarantined.
func (s *Supervisor) boot(ctx context.Context) (*proc, error) {
	comps := s.cfg.Units()
	excluded := map[int]bool{}
	var loadErrors []LoadError
	// exclude leaves component i out with every component depending on it, transitively,
	// each dependent listed as "depends on" i's units.
	exclude := func(i int) {
		excluded[i] = true
		for _, d := range dependents(comps, i) {
			if !excluded[d] {
				excluded[d] = true
				loadErrors = append(loadErrors, LoadError{Units: unitNames(comps[d]), Msg: "depends on " + strings.Join(unitNames(comps[i]), ", ")})
			}
		}
	}
	for i := range comps {
		if _, ok := s.quarantine(comps[i]); ok && !excluded[i] {
			exclude(i)
		}
	}
	for range len(comps) + 2 {
		p, err := s.spawn()
		if err != nil {
			return nil, err
		}
		var send []wireComponent
		for i, c := range comps {
			if !excluded[i] {
				send = append(send, wireComponent{ID: i, Units: c.Units})
			}
		}
		ready, err := s.handshake(ctx, p, msg{T: tInit, Gen: p.gen, Components: send})
		if err != nil {
			if ctx.Err() != nil {
				s.drop(p)
				return nil, ctx.Err()
			}
			if len(send) == 0 {
				r := s.crashed(p, "")
				return nil, errors.New("the child crashed at start: " + r.Err)
			}
			// Crashed during replay: find the component.
			<-p.done
			found, ferr := s.isolate(ctx, comps, excluded)
			if ferr != nil {
				return nil, ferr
			}
			if found < 0 {
				return nil, errors.New("the child crashed at replay, and no single component crashes it: " + p.report(""))
			}
			exclude(found)
			continue
		}
		if len(ready.Failed) == 0 {
			s.mu.Lock()
			s.loadErrors = loadErrors
			s.oom = ready.OOM
			s.mu.Unlock()
			return p, nil
		}
		for _, f := range ready.Failed {
			if f.ID < 0 || f.ID >= len(comps) || excluded[f.ID] {
				continue
			}
			loadErrors = append(loadErrors, LoadError{Units: unitNames(comps[f.ID]), Msg: f.Msg})
			exclude(f.ID)
		}
		s.drop(p)
	}
	return nil, errors.New("the saved definitions did not load after a restart for each component")
}

// isolate finds the component that kills the child at replay: a child with no units receives
// each component in order until one kills it, which is quarantined. It returns that
// component's index, or -1 when none kills it.
func (s *Supervisor) isolate(ctx context.Context, comps []Component, excluded map[int]bool) (int, error) {
	p, err := s.spawn()
	if err != nil {
		return -1, err
	}
	if _, err := s.handshake(ctx, p, msg{T: tInit, Gen: p.gen}); err != nil {
		if ctx.Err() != nil {
			s.drop(p)
			return -1, ctx.Err()
		}
		r := s.crashed(p, "")
		return -1, errors.New("the child crashed at start: " + r.Err)
	}
	defer s.drop(p)
	for i, c := range comps {
		if excluded[i] {
			continue
		}
		_, err := s.handshake(ctx, p, msg{T: tDefine, Gen: p.gen, Components: []wireComponent{{ID: i, Units: c.Units}}})
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			return -1, ctx.Err()
		}
		<-p.done
		q := LoadError{Units: unitNames(c), Msg: "quarantined: it crashed the child at replay: " + p.fatal()}
		s.mu.Lock()
		s.quarantined[componentKey(c)] = q
		s.lastCrash = p.fatal()
		s.mu.Unlock()
		return i, nil
	}
	return -1, nil
}

// quarantine reports whether a component is quarantined: the same units, by name and source,
// that crashed a child at replay. Redefining or forgetting a unit changes the key.
func (s *Supervisor) quarantine(c Component) (LoadError, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	q, ok := s.quarantined[componentKey(c)]
	return q, ok
}

func componentKey(c Component) string {
	var b strings.Builder
	for _, u := range c.Units {
		b.WriteString(u.Name + "\x00" + u.Src + "\x00")
	}
	return b.String()
}

func unitNames(c Component) []string {
	out := make([]string, len(c.Units))
	for i, u := range c.Units {
		out[i] = u.Name
	}
	return out
}

// dependents are the components that depend on component i, transitively.
func dependents(comps []Component, i int) []int {
	seen := map[int]bool{i: true}
	var out []int
	for changed := true; changed; {
		changed = false
		for j, c := range comps {
			if seen[j] {
				continue
			}
			for _, d := range c.Deps {
				if seen[d] {
					seen[j] = true
					out = append(out, j)
					changed = true
					break
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// handshake sends an init or define and waits for ready under ctx. Any other answer, or the
// child's death, is an error.
func (s *Supervisor) handshake(ctx context.Context, p *proc, m msg) (msg, error) {
	if err := p.send(m, HostLineBytes); err != nil {
		return msg{}, err
	}
	select {
	case <-ctx.Done():
		return msg{}, ctx.Err()
	case r, ok := <-p.msgs:
		if !ok {
			return msg{}, errors.New("the child died")
		}
		if r.T != tReady || r.Gen != p.gen {
			p.kill()
			return msg{}, fmt.Errorf("the child answered %s with %s", m.T, r.T)
		}
		return r, nil
	}
}

// spawn starts one child process. Children are started from an ordinary goroutine that never
// locks its OS thread, since Linux's Pdeathsig fires when the forking thread exits
// (golang/go#27505).
func (s *Supervisor) spawn() (*proc, error) {
	exe := s.cfg.exe
	if exe == "" {
		var err error
		if exe, err = os.Executable(); err != nil {
			return nil, err
		}
	}
	toR, toW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	fromR, fromW, err := os.Pipe()
	if err != nil {
		toR.Close()
		toW.Close()
		return nil, err
	}
	s.mu.Lock()
	s.gen++
	s.starts++
	gen := s.gen
	s.mu.Unlock()
	p := &proc{gen: gen, toW: toW, msgs: make(chan msg, 16), done: make(chan struct{}), stderr: &stderrBuffer{max: 4 * ResultChars}}
	cmd := exec.Command(exe, ChildMarker, s.cfg.Mode)
	cmd.Env = []string{} // non-nil: inherit nothing, no keys, no PIG_EXT_SOCKET, no YAEGI_*
	cmd.Dir = string(os.PathSeparator)
	cmd.Stdin, cmd.Stdout = nil, nil // the null device
	cmd.Stderr = p.stderr
	cmd.SysProcAttr = sysProcAttr(!s.cfg.noPdeathsig)
	cmd.Args = append(cmd.Args, attachPipes(cmd, toR, fromW)...)
	// Start from a fresh goroutine: it is never locked to its OS thread, whatever the caller's
	// goroutine did, so the thread Pdeathsig watches lives as long as the runtime keeps it.
	started := make(chan error, 1)
	go func() { started <- cmd.Start() }()
	if err := <-started; err != nil {
		toR.Close()
		toW.Close()
		fromR.Close()
		fromW.Close()
		return nil, err
	}
	toR.Close()
	fromW.Close()
	p.cmd = cmd
	go p.read(fromR)
	go func() {
		p.waitErr = cmd.Wait()
		close(p.done)
	}()
	return p, nil
}

// proc is one child process as the extension sees it.
type proc struct {
	gen     uint64
	cmd     *exec.Cmd
	toW     *os.File
	writeMu sync.Mutex
	// msgs are the child's decoded lines; it is closed when the reader ends.
	msgs    chan msg
	readErr error
	done    chan struct{}
	waitErr error
	stderr  *stderrBuffer

	killedByHost atomic.Bool
	closeOnce    sync.Once
}

// read decodes the child's lines with the bounded reader. A line over the bound kills the
// child; the extension never holds more than one bound of it.
func (p *proc) read(r *os.File) {
	defer close(p.msgs)
	defer r.Close()
	lr := newLineReader(r, ChildLineBytes)
	for {
		m, err := lr.readMsg()
		if err != nil {
			if !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, os.ErrClosed) {
				p.readErr = err
				_ = p.cmd.Process.Kill()
			}
			return
		}
		p.msgs <- m
	}
}

// send writes one line to the child.
func (p *proc) send(m msg, max int) error {
	b, err := encodeLine(m, max)
	if err != nil {
		return err
	}
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_, err = p.toW.Write(b)
	return err
}

// kill kills the child and waits for it.
func (p *proc) kill() {
	p.killedByHost.Store(true)
	_ = p.cmd.Process.Kill()
	<-p.done
	p.closeOnce.Do(func() { p.toW.Close() })
}

// report is the crash report: why the host killed it, the exit status and the child's last
// stderr lines (its fatal-error lines), within one result.
func (p *proc) report(why string) string {
	<-p.done
	p.closeOnce.Do(func() { p.toW.Close() })
	var parts []string
	if why != "" {
		parts = append(parts, why)
	}
	if errors.Is(p.readErr, errLineOverBound) {
		parts = append(parts, "protocol message over bound")
	} else if p.readErr != nil {
		parts = append(parts, "protocol: "+p.readErr.Error())
	}
	status := "the child exited"
	if p.waitErr != nil {
		status += ": " + p.waitErr.Error()
	}
	parts = append(parts, status)
	if tail := strings.TrimSpace(p.stderr.String()); tail != "" {
		parts = append(parts, tail)
	}
	text, _ := cutChars(strings.Join(parts, "\n"), ResultChars)
	return text
}

// fatal is the one line that says why the child died: its first fatal-error line, else the
// protocol error or its exit status.
func (p *proc) fatal() string {
	<-p.done
	if tail := strings.TrimSpace(p.stderr.String()); tail != "" && fatalLine.MatchString(tail) {
		return firstLine(tail)
	}
	if errors.Is(p.readErr, errLineOverBound) {
		return "protocol message over bound"
	}
	if p.waitErr != nil {
		return "the child exited: " + p.waitErr.Error()
	}
	return "the child exited"
}

// pid is the child's process id (tests).
func (p *proc) pid() int { return p.cmd.Process.Pid }

// stderrBuffer keeps the first and the last max bytes the child writes to stderr; the rest is
// read and discarded, so a child writing more never blocks on its stderr. A runtime fatal error
// prints its line first and then every goroutine (a runtime throw forces the traceback whatever
// SetTraceback says), so the report picks the fatal-error lines out of both ends.
type stderrBuffer struct {
	mu   sync.Mutex
	max  int
	head []byte
	tail []byte
	more bool // bytes were discarded between head and tail
}

func (t *stderrBuffer) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(p)
	if room := t.max - len(t.head); room > 0 {
		k := min(room, len(p))
		t.head = append(t.head, p[:k]...)
		p = p[k:]
	}
	if len(p) > 0 {
		t.tail = append(t.tail, p...)
		if len(t.tail) > t.max {
			t.tail = append(t.tail[:0], t.tail[len(t.tail)-t.max:]...)
			t.more = true
		}
	}
	return n, nil
}

// fatalLine matches the lines a dying Go runtime or a panic prints first.
var fatalLine = regexp.MustCompile(`^(fatal error:|runtime:|panic:|\[signal|goroutine \d+ .*stack exceeds|om sandbox:)`)

// String is the child's fatal-error lines from both ends, else the last of what it wrote,
// within one result.
func (t *stderrBuffer) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var picked []string
	seen := map[string]bool{}
	for _, part := range [][]byte{t.head, t.tail} {
		for _, l := range strings.Split(string(part), "\n") {
			l = strings.TrimSpace(l)
			if fatalLine.MatchString(l) && !seen[l] {
				seen[l] = true
				picked = append(picked, l)
			}
		}
	}
	if len(picked) > 0 {
		text, _ := cutChars(strings.Join(picked, "\n"), ResultChars)
		return text
	}
	all := string(t.head)
	if t.more {
		all += "\n[...]\n"
	}
	return tailChars(all+string(t.tail), ResultChars)
}

// tailChars is the last n characters of s, a rune cut at its front dropped.
func tailChars(s string, n int) string {
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	for skip := utf8.RuneCountInString(s) - n; skip > 0; skip-- {
		_, size := utf8.DecodeRuneInString(s)
		s = s[size:]
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
