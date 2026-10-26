package observationalmemory

import (
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
)

// wireHost is a minimal PiG over a pipe: it reads the register frame, sends ready, and serves
// the extension's host calls with answer while a request it sent is pending.
type wireHost struct {
	t    *testing.T
	c    net.Conn
	next int
	reg  struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
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
	return connectHome(t, t.TempDir())
}

// connectHome is connect with PiG's home at home, a temporary directory the test prepared.
func connectHome(t *testing.T, home string) *wireHost {
	t.Helper()
	t.Setenv("PIG_HOME", home)
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
	w.eventWith(name, map[string]any{})
}

// eventWith is event with the event's args.
func (w *wireHost) eventWith(name string, args map[string]any) {
	w.t.Helper()
	n := 0
	for _, h := range w.reg.Handlers {
		if h.Event == name {
			w.request(map[string]any{"method": "event", "event": name, "handler_id": h.HandlerID, "args": args})
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

// The defined tools are retired (docs/code-tool.md, step 8): with a definition an earlier
// release stored, the extension registers exactly the memory tool, and neither session_start nor
// before_agent_start registers a tool or changes the active list; the definition stays stored and
// om.Retired lists it with the snippet that replaces it.
func TestRetiredDefinedToolsAreNeverRegistered(t *testing.T) {
	home := t.TempDir()
	store := curated.New(curated.DefaultDir(home))
	if _, err := store.Define(curated.Definition{Name: "bakes", Query: `notes | search "sourdough"`, Description: "Bakes."}, "s0"); err != nil {
		t.Fatal(err)
	}
	w := connectHome(t, home)
	var tools []string
	for _, tool := range w.reg.Tools {
		tools = append(tools, tool.Name)
	}
	if !slices.Equal(tools, []string{"memory"}) {
		t.Fatalf("registered tools %v", tools)
	}
	w.event("session_start")
	w.eventWith("before_agent_start", map[string]any{"prompt": "hi", "systemPrompt": "", "systemPromptOptions": map[string]any{}})
	for _, method := range w.calls {
		if method == "registerTool" || method == "registerDeclaration" || method == "setActiveTools" {
			t.Fatalf("the extension called %s: %v", method, w.calls)
		}
	}
	got := w.memory("for _, q := range om.Retired().Rows { fmt.Println(q.Name, q.Snippet) }")
	if !strings.Contains(got, "bakes om.Notes(om.Search(") {
		t.Fatalf("om.Retired: %q", got)
	}
	if _, err := store.GetDefinition("bakes"); err != nil {
		t.Fatalf("the retired definition was removed: %v", err)
	}
}
