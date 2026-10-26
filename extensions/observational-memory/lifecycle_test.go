package observationalmemory

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"
)

// wireHost is a minimal PiG over a pipe: it reads the register frame, sends ready, and serves
// the extension's host calls with answer while a request it sent is pending.
type wireHost struct {
	t    *testing.T
	c    net.Conn
	next int
	reg  struct {
		Handlers []struct {
			Event     string `json:"event"`
			HandlerID int    `json:"handler_id"`
		} `json:"handlers"`
	}
	answer func(method string, args json.RawMessage) any
	calls  []string
}

func (w *wireHost) write(v any) {
	w.t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		w.t.Fatal(err)
	}
	var header [4]byte
	binary.BigEndian.PutUint32(header[:], uint32(len(b)))
	if _, err := w.c.Write(append(header[:], b...)); err != nil {
		w.t.Fatal(err)
	}
}

type wireFrame struct {
	Type     string          `json:"type"`
	ID       string          `json:"id"`
	Register json.RawMessage `json:"register"`
	Response *struct {
		Result json.RawMessage `json:"result"`
		Error  json.RawMessage `json:"error"`
	} `json:"response"`
	Call *struct {
		Method string          `json:"method"`
		Args   json.RawMessage `json:"args"`
	} `json:"call"`
}

func (w *wireHost) read() wireFrame {
	w.t.Helper()
	var header [4]byte
	if _, err := io.ReadFull(w.c, header[:]); err != nil {
		w.t.Fatal(err)
	}
	body := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(w.c, body); err != nil {
		w.t.Fatal(err)
	}
	var f wireFrame
	if err := json.Unmarshal(body, &f); err != nil {
		w.t.Fatalf("%s: %v", body, err)
	}
	return f
}

// request sends one request and serves calls until its response.
func (w *wireHost) request(req map[string]any) json.RawMessage {
	w.t.Helper()
	w.next++
	id := "r" + strconv.Itoa(w.next)
	w.write(map[string]any{"type": "request", "id": id, "request": req})
	for {
		f := w.read()
		switch f.Type {
		case "call":
			w.calls = append(w.calls, f.Call.Method)
			result, _ := json.Marshal(w.answer(f.Call.Method, f.Call.Args))
			w.write(map[string]any{"type": "call_result", "id": f.ID, "call_result": map[string]any{"result": json.RawMessage(result)}})
		case "response":
			if f.ID == id {
				if f.Response != nil && len(f.Response.Error) > 0 && string(f.Response.Error) != "null" {
					w.t.Fatalf("request %v: %s", req, f.Response.Error)
				}
				if f.Response == nil {
					return nil
				}
				return f.Response.Result
			}
		}
	}
}

// connect runs the factory's extension against a wireHost whose PiG home is a temporary
// directory, so nothing reaches the user's config, and sends ready.
func connect(t *testing.T) *wireHost {
	t.Helper()
	t.Setenv("PIG_HOME", t.TempDir())
	hostSide, extSide := net.Pipe()
	done := make(chan error, 1)
	go func() { done <- Extension().RunWithConn(extSide) }()
	_ = hostSide.SetDeadline(time.Now().Add(60 * time.Second))
	w := &wireHost{t: t, c: hostSide, answer: func(string, json.RawMessage) any { return nil }}
	t.Cleanup(func() {
		_ = hostSide.Close()
		<-done
	})
	f := w.read()
	if f.Type != "register" {
		t.Fatalf("first frame %s", f.Type)
	}
	if err := json.Unmarshal(f.Register, &w.reg); err != nil {
		t.Fatal(err)
	}
	w.write(map[string]any{"type": "ready", "ready": map[string]any{"cwd": t.TempDir(), "model": "m"}})
	return w
}

// event dispatches name to every handler the extension registered for it.
func (w *wireHost) event(name string) {
	w.t.Helper()
	n := 0
	for _, h := range w.reg.Handlers {
		if h.Event == name {
			w.request(map[string]any{"method": "event", "event": name, "handler_id": h.HandlerID, "args": map[string]any{}})
			n++
		}
	}
	if n == 0 {
		w.t.Fatalf("no handler for %s", name)
	}
}

// memory runs one memory tool call and returns its text.
func (w *wireHost) memory(code string) string {
	w.t.Helper()
	raw := w.request(map[string]any{"method": "tool_call", "tool": "memory", "tool_call_id": "t" + strconv.Itoa(w.next), "args": map[string]any{"code": code}})
	var result struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		w.t.Fatalf("%s: %v", raw, err)
	}
	var text string
	if err := json.Unmarshal(result.Content, &text); err == nil {
		return text
	}
	var parts []struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(result.Content, &parts); err != nil {
		w.t.Fatalf("content %s: %v", result.Content, err)
	}
	for _, p := range parts {
		text += p.Text
	}
	return text
}

// The registered session_start and session_shutdown handlers reset the namespace (decision 2):
// a variable from one conversation is gone in the next while a kept definition still answers.
// The calls go through the registered extension, as PiG makes them.
func TestSessionBoundariesResetTheNamespace(t *testing.T) {
	w := connect(t)
	if got := w.memory("y := 41\nfunc shout() string { return `hi` }"); !strings.Contains(got, "kept: shout") {
		t.Fatalf("keep: %q", got)
	}
	if got := w.memory("y + 1"); got != "42" {
		t.Fatalf("the variable in the same conversation: %q", got)
	}
	w.event("session_start")
	if got := w.memory("y"); !strings.Contains(got, "undefined: y") {
		t.Fatalf("the variable survived session_start: %q", got)
	}
	if got := w.memory("shout()"); got != "hi" {
		t.Fatalf("the kept definition after session_start: %q", got)
	}
	if got := w.memory("z := 1\nz"); got != "1" {
		t.Fatalf("z: %q", got)
	}
	w.event("session_shutdown")
	if got := w.memory("z"); !strings.Contains(got, "undefined: z") {
		t.Fatalf("the namespace outlived session_shutdown: %q", got)
	}
	if got := w.memory("shout()"); got != "hi" {
		t.Fatalf("the kept definition after session_shutdown: %q", got)
	}
	w.event("session_shutdown")
}
