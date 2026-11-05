// Package enginerpc runs the memory engine as a long-lived process behind the memory/host
// interfaces (docs/engine-rpc.md): one JSON-RPC 2.0 connection over stdio, one message per
// line, with requests in both directions. The harness adapter sends the events it receives
// (Client); the engine (Serve, `om serve`) handles each with the same engine functions an
// in-process adapter calls, and asks the adapter for every host call those functions make.
package enginerpc

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"sync"
)

// JSON-RPC 2.0 error codes the connection uses.
const (
	CodeParseError     = -32700
	CodeInvalidRequest = -32600
	CodeMethodNotFound = -32601
	CodeInvalidParams  = -32602
	CodeInternalError  = -32603
	// CodeHostError is a host or engine call that failed: the error's message is the
	// callee's error text.
	CodeHostError = -32000
	// CodeRequestCancelled answers a request the caller cancelled before it finished
	// (the LSP code).
	CodeRequestCancelled = -32800
)

// CancelMethod is the notification that cancels a request in flight, in either direction:
// params {"id": <the request's id>}.
const CancelMethod = "$/cancelRequest"

// Error is a JSON-RPC error object; a call that failed returns one.
type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Message }

// ErrClosed is what a call returns once the connection is gone.
var ErrClosed = errors.New("enginerpc: connection closed")

// Handler answers one incoming request or notification. A notification's result is
// discarded. ctx is cancelled by the peer's $/cancelRequest and when the connection closes.
type Handler func(ctx context.Context, method string, params json.RawMessage) (any, error)

type wireMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type reply struct {
	result json.RawMessage
	err    *Error
}

// Peer is one end of the connection: it sends requests and notifications, matches the
// replies to its requests by id, and runs each incoming request in its own goroutine, so
// calls in both directions can be in flight at once (a host call made while the engine
// answers an event, an event while a host call is pending).
type Peer struct {
	r       *bufio.Reader
	w       io.Writer
	wmu     sync.Mutex
	handler Handler

	mu       sync.Mutex
	nextID   int64
	pending  map[int64]chan reply
	inflight map[string]context.CancelFunc
	closed   bool

	done chan struct{}
	err  error
	// base is cancelled when the connection closes; every handler's context derives from it.
	base   context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewPeer is a peer reading r and writing w; handler answers what the other end sends (nil
// answers every request with method not found). Call Run to start reading.
func NewPeer(r io.Reader, w io.Writer, handler Handler) *Peer {
	base, cancel := context.WithCancel(context.Background())
	return &Peer{r: bufio.NewReaderSize(r, 64*1024), w: w, handler: handler, pending: map[int64]chan reply{}, inflight: map[string]context.CancelFunc{}, done: make(chan struct{}), base: base, cancel: cancel}
}

// Done is closed when the connection has ended (Run returned).
func (p *Peer) Done() <-chan struct{} { return p.done }

// Err is why the connection ended: nil for a clean end of input.
func (p *Peer) Err() error {
	<-p.done
	return p.err
}

// Run reads messages until the input ends, then fails every pending call with ErrClosed,
// cancels the handlers still running and waits for them. It returns nil at end of input.
func (p *Peer) Run() error {
	var err error
	for {
		line, readErr := p.r.ReadBytes('\n')
		if len(line) > 0 {
			p.dispatch(line)
		}
		if readErr != nil {
			if !errors.Is(readErr, io.EOF) {
				err = readErr
			}
			break
		}
	}
	p.mu.Lock()
	p.closed = true
	pending := p.pending
	p.pending = map[int64]chan reply{}
	p.mu.Unlock()
	for _, ch := range pending {
		ch <- reply{err: &Error{Code: CodeInternalError, Message: ErrClosed.Error()}}
	}
	p.cancel()
	p.wg.Wait()
	p.err = err
	close(p.done)
	return err
}

func (p *Peer) dispatch(line []byte) {
	var msg wireMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		if len(trimSpace(line)) == 0 {
			return
		}
		_ = p.send(wireMessage{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &Error{Code: CodeParseError, Message: "parse error"}})
		return
	}
	switch {
	case msg.Method != "" && len(msg.ID) > 0 && string(msg.ID) != "null":
		p.serve(msg)
	case msg.Method != "":
		p.notification(msg)
	case len(msg.ID) > 0:
		id, err := strconv.ParseInt(string(msg.ID), 10, 64)
		if err != nil {
			return
		}
		p.mu.Lock()
		ch, ok := p.pending[id]
		delete(p.pending, id)
		p.mu.Unlock()
		if ok {
			ch <- reply{result: msg.Result, err: msg.Error}
		}
	}
}

func trimSpace(b []byte) []byte {
	for len(b) > 0 && (b[0] == ' ' || b[0] == '\n' || b[0] == '\r' || b[0] == '\t') {
		b = b[1:]
	}
	for len(b) > 0 && (b[len(b)-1] == ' ' || b[len(b)-1] == '\n' || b[len(b)-1] == '\r' || b[len(b)-1] == '\t') {
		b = b[:len(b)-1]
	}
	return b
}

func (p *Peer) notification(msg wireMessage) {
	if msg.Method == CancelMethod {
		var params struct {
			ID json.RawMessage `json:"id"`
		}
		if json.Unmarshal(msg.Params, &params) == nil {
			p.mu.Lock()
			cancel := p.inflight[string(params.ID)]
			p.mu.Unlock()
			if cancel != nil {
				cancel()
			}
		}
		return
	}
	if p.handler == nil {
		return
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() { _ = recover() }()
		_, _ = p.handler(p.base, msg.Method, msg.Params)
	}()
}

func (p *Peer) serve(msg wireMessage) {
	ctx, cancel := context.WithCancel(p.base)
	key := string(msg.ID)
	p.mu.Lock()
	p.inflight[key] = cancel
	p.mu.Unlock()
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() {
			p.mu.Lock()
			delete(p.inflight, key)
			p.mu.Unlock()
			cancel()
		}()
		result, err := p.call(ctx, msg.Method, msg.Params)
		out := wireMessage{JSONRPC: "2.0", ID: msg.ID}
		if err == nil {
			encoded, encErr := json.Marshal(result)
			if encErr != nil {
				err = &Error{Code: CodeInternalError, Message: "result does not encode: " + encErr.Error()}
			} else {
				out.Result = encoded
			}
		}
		if err != nil {
			var rpcErr *Error
			if !errors.As(err, &rpcErr) {
				code := CodeHostError
				if ctx.Err() != nil && errors.Is(err, context.Canceled) {
					code = CodeRequestCancelled
				}
				rpcErr = &Error{Code: code, Message: err.Error()}
			}
			out.Result, out.Error = nil, rpcErr
		}
		_ = p.send(out)
	}()
}

// call runs the handler, turning a panic into an internal error.
func (p *Peer) call(ctx context.Context, method string, params json.RawMessage) (result any, err error) {
	defer func() {
		if r := recover(); r != nil {
			result, err = nil, &Error{Code: CodeInternalError, Message: fmt.Sprintf("panic in %s: %v", method, r)}
		}
	}()
	if p.handler == nil {
		return nil, &Error{Code: CodeMethodNotFound, Message: "method not found: " + method}
	}
	return p.handler(ctx, method, params)
}

func (p *Peer) send(msg wireMessage) error {
	encoded, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	p.wmu.Lock()
	defer p.wmu.Unlock()
	_, err = p.w.Write(encoded)
	return err
}

// Notify sends a notification.
func (p *Peer) Notify(method string, params any) error {
	encoded, err := json.Marshal(params)
	if err != nil {
		return err
	}
	return p.send(wireMessage{JSONRPC: "2.0", Method: method, Params: encoded})
}

// Call sends a request and waits for its reply, decoding the result into result (when not
// nil). When ctx is cancelled first, it sends $/cancelRequest and goes on waiting: the other
// end answers every request it received, cancelled or not, so the reply (a result, or a
// CodeRequestCancelled error) still arrives, unless the connection closes, which fails the
// call with ErrClosed.
func (p *Peer) Call(ctx context.Context, method string, params any, result any) error {
	encoded, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("enginerpc: %s params: %w", method, err)
	}
	ch := make(chan reply, 1)
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return ErrClosed
	}
	p.nextID++
	id := p.nextID
	p.pending[id] = ch
	p.mu.Unlock()
	rawID := json.RawMessage(strconv.FormatInt(id, 10))
	if err := p.send(wireMessage{JSONRPC: "2.0", ID: rawID, Method: method, Params: encoded}); err != nil {
		p.mu.Lock()
		delete(p.pending, id)
		p.mu.Unlock()
		return fmt.Errorf("enginerpc: send %s: %w", method, err)
	}
	var r reply
	select {
	case r = <-ch:
	case <-ctx.Done():
		_ = p.Notify(CancelMethod, map[string]any{"id": rawID})
		r = <-ch
	}
	if r.err != nil {
		if r.err.Message == ErrClosed.Error() {
			return ErrClosed
		}
		if r.err.Code == CodeRequestCancelled && ctx.Err() != nil {
			return ctx.Err()
		}
		return r.err
	}
	if result != nil && len(r.result) > 0 {
		if err := json.Unmarshal(r.result, result); err != nil {
			return fmt.Errorf("enginerpc: %s result: %w", method, err)
		}
	}
	return nil
}
