package enginerpc_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/cli"
	"github.com/drvova/pig-observational-memory/memory/curated"
	"github.com/drvova/pig-observational-memory/memory/engine"
	"github.com/drvova/pig-observational-memory/memory/enginerpc"
	"github.com/drvova/pig-observational-memory/memory/harness"
	"github.com/drvova/pig-observational-memory/memory/hooks"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/hostinfo"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/runtime"
	"github.com/drvova/pig-observational-memory/memory/session"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// serveEnv makes this test binary run `om serve` instead of its tests, so the end-to-end
// tests drive a real engine process over its stdin and stdout.
const serveEnv = "ENGINERPC_TEST_SERVE"

func TestMain(m *testing.M) {
	if os.Getenv(serveEnv) == "1" {
		os.Exit(cli.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

// driver runs the engine for the PiG adapter's events: in this process (the adapter's
// default path) or behind the RPC in an `om serve` process.
type driver interface {
	sessionStart(h *testfix.FakeHost)
	turnEnd(h *testfix.FakeHost)
	beforeCompact(h *testfix.FakeHost, data map[string]any) map[string]any
	context(h *testfix.FakeHost, messages []map[string]any)
	tool(h *testfix.FakeHost, params map[string]any) host.ToolResult
}

// inProcess is what the PiG adapter's handlers call, without the SDK.
type inProcess struct {
	rt      *runtime.Runtime
	pin     *curated.Pin
	manager *harness.Manager
}

func newInProcess() *inProcess {
	d := &inProcess{rt: runtime.New(), pin: &curated.Pin{}}
	d.manager = engine.NewManager(d.rt, d.pin)
	return d
}

func (d *inProcess) sessionStart(h *testfix.FakeHost) {
	d.rt.ReloadConfig()
	engine.SessionStart(d.rt, d.manager, d.pin, h)
}

func (d *inProcess) turnEnd(h *testfix.FakeHost) { hooks.OnTurnEnd(d.rt, h) }

func (d *inProcess) beforeCompact(h *testfix.FakeHost, data map[string]any) map[string]any {
	return engine.BeforeCompact(h.RequestContext(), d.rt, h, data, func(v any) error { _, err := json.Marshal(v); return err })
}

func (d *inProcess) context(h *testfix.FakeHost, messages []map[string]any) {
	hooks.OnContext(d.rt, h, messages)
}

func (d *inProcess) tool(h *testfix.FakeHost, params map[string]any) host.ToolResult {
	r := d.manager.RunMemory(h, params)
	return host.ToolResult{Text: r.Text, IsError: r.IsError, Details: r.Details}
}

// overRPC is the client against an `om serve` process.
type overRPC struct {
	t *testing.T
	c *enginerpc.Client
}

func (d overRPC) sessionStart(h *testfix.FakeHost) { must(d.t, d.c.SessionStart(h)) }
func (d overRPC) turnEnd(h *testfix.FakeHost) {
	_, _, err := d.c.TurnEnd(h)
	must(d.t, err)
}
func (d overRPC) beforeCompact(h *testfix.FakeHost, data map[string]any) map[string]any {
	result, _, _, err := d.c.BeforeCompact(h, data)
	must(d.t, err)
	return result
}
func (d overRPC) context(h *testfix.FakeHost, messages []map[string]any) {
	must(d.t, d.c.Context(h, messages))
}
func (d overRPC) tool(h *testfix.FakeHost, params map[string]any) host.ToolResult {
	r, err := d.c.ToolCall(h, "memory", params)
	must(d.t, err)
	return r
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// settingsDir is an agent directory whose settings turn level 1 off, so neither path makes a
// model request, and points this process's engine (the in-process path) at it; the engine
// process gets it at initialize.
func settingsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	settings := `{"observational-memory": {"consolidate": false}}`
	must(t, os.WriteFile(filepath.Join(dir, "settings.json"), []byte(settings), 0o600))
	agentDir, dirName := hostinfo.AgentDir, hostinfo.ConfigDirName
	hostinfo.AgentDir = func() (string, error) { return dir, nil }
	hostinfo.ConfigDirName = func() (string, error) { return ".pig", nil }
	t.Cleanup(func() { hostinfo.AgentDir, hostinfo.ConfigDirName = agentDir, dirName })
	return dir
}

// startEngine starts this test binary as `om serve` and connects a client.
func startEngine(t testing.TB, agentDir string) *enginerpc.Client {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv(serveEnv, "1")
	t.Setenv("OM_HOME", t.TempDir())
	client := enginerpc.NewClient(enginerpc.ProcessDialer(exe, os.Stderr), enginerpc.InitializeParams{AgentDir: agentDir, ConfigDirName: ".pig"})
	spec, err := client.Start()
	if err != nil {
		t.Fatal(err)
	}
	if spec.Name != "memory" || spec.Description == "" {
		t.Fatalf("tool spec %+v", spec)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func message(id string, m map[string]any) session.Entry { return testfix.MessageEntry(id, m) }

// codingTurn is one request with a failing test run, an edit and a passing run.
func codingTurn(prefix, request string) []session.Entry {
	call := func(id, name string, args map[string]any, text string) session.Entry {
		return message(prefix+"a"+id, map[string]any{"role": "assistant", "stopReason": "toolUse", "content": []any{
			map[string]any{"type": "text", "text": text},
			map[string]any{"type": "toolCall", "id": prefix + "c" + id, "name": name, "arguments": args},
		}})
	}
	result := func(id, name, text string, isError bool) session.Entry {
		return message(prefix+"t"+id, map[string]any{"role": "toolResult", "toolCallId": prefix + "c" + id, "toolName": name, "content": []any{map[string]any{"type": "text", "text": text}}, "isError": isError})
	}
	return []session.Entry{
		testfix.RawMessage(prefix+"u", request),
		call("1", "bash", map[string]any{"command": "go test ./..."}, "Running the suite to find the failing case."),
		result("1", "bash", "--- FAIL: TestLoad"+strings.Repeat("\n    loader_test.go:12: unexpected key", 400)+"\nCommand exited with code 1", true),
		call("2", "edit", map[string]any{"path": "loader.go"}, "Fixing the key check in loader.go."),
		result("2", "edit", "ok", false),
		call("3", "bash", map[string]any{"command": "go test ./..."}, ""),
		result("3", "bash", "ok", false),
		message(prefix+"done", map[string]any{"role": "assistant", "stopReason": "stop", "content": []any{map[string]any{"type": "text", "text": "Fixed: " + request}}}),
	}
}

func newHost(entries []session.Entry) *testfix.FakeHost {
	h := testfix.NewFakeHost(slices.Clone(entries)...)
	h.Model = testfix.Model("test-provider", "session", map[string]any{"contextWindow": 200_000, "maxTokens": 32_000})
	h.ModelsFake.Add(h.Model, testfix.KeyAuth("test-key"))
	// The writer and frame cross the boundary (host/harness, host/repo) and are written alike.
	h.HarnessName = "pig"
	h.RepoInfo = &host.Repo{ID: "0123456789ab", Root: h.Cwd(), CommonDir: h.Cwd() + "/.git", Remote: "example.org/team/project",
		RootCommit: "1111111111111111111111111111111111111111", Branch: "main", Head: "2222222222222222222222222222222222222222"}
	return h
}

func maps(entries []session.Entry) []any {
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, testfix.ToJSONValue(e.ToMap()))
	}
	return out
}

// contextMessages is the context PiG sends for a request: the compaction summary, then every
// message from the latest compaction's firstKeptEntryId on, as fresh copies.
func contextMessages(entries []session.Entry) []map[string]any {
	start := ledger.CompactionRangeStartIndex(entries)
	out := []map[string]any{{"role": "compactionSummary", "summary": "summary"}}
	for _, entry := range entries[start:] {
		if entry.Type == "message" {
			out = append(out, testfix.ToJSONValue(entry.Message).(map[string]any))
		}
	}
	return out
}

// transcript is what one driver produced from the scenario.
type transcript struct {
	Appended      any
	Notifications []testfix.Notification
	Compaction    any
	Context       any
	Tool          any
	ToolText      string
}

// scenario drives turn_end over two turns, a compaction with a cut inside the branch, the
// context event of the next request and a memory tool call.
func scenario(t *testing.T, d driver) transcript {
	t.Helper()
	entries := append(codingTurn("p", "fix the loader"), codingTurn("q", "and the saver")...)
	h := newHost(entries[:8])
	d.sessionStart(h)
	d.turnEnd(h)
	h.SetBranch(append(h.Entries, entries[8:]...))
	d.turnEnd(h)

	branch, _ := h.GetBranch()
	data := map[string]any{
		"preparation": map[string]any{
			"firstKeptEntryId": "qu", "tokensBefore": float64(30_000), "isSplitTurn": false,
			"messagesToSummarize": maps(entries[:8]),
			"settings":            map[string]any{"enabled": true, "reserveTokens": float64(16_384), "keepRecentTokens": float64(20_000)},
		},
		"branchEntries": maps(branch),
		"reason":        "threshold",
	}
	result := d.beforeCompact(h, data)
	compaction, _ := result["compaction"].(map[string]any)
	if compaction == nil || compaction["firstKeptEntryId"] != "qu" {
		t.Fatalf("compaction %v", result)
	}
	// PiG writes the compaction entry on its cut.
	encoded := testfix.ToJSONValue(compaction).(map[string]any)
	summary, _ := encoded["summary"].(string)
	h.SetBranch(append(h.Entries, testfix.CompactionEntry("k1", testfix.CompactionArgs{FirstKeptEntryID: "qu", Summary: summary, Details: encoded["details"]})))
	h.SetBranch(append(h.Entries, codingTurn("r", "and the printer")...))

	messages := contextMessages(h.Entries)
	d.context(h, messages)

	tool := d.tool(h, map[string]any{"code": `om.History(om.Search("loader"))`})
	if tool.IsError {
		t.Fatalf("tool call: %s", tool.Text)
	}
	return transcript{
		Appended:      testfix.ToJSONValue(h.Appended),
		Notifications: h.Notifications,
		Compaction:    testfix.ToJSONValue(result),
		Context:       testfix.ToJSONValue(messages),
		Tool:          testfix.ToJSONValue(tool.Details),
		ToolText:      tool.Text,
	}
}

// TestProcessEngineMatchesInProcess runs the same scenario through the in-process path and
// through `om serve` over a pipe: every appended entry, notification, compaction, edited
// context message and tool result is the same.
func TestProcessEngineMatchesInProcess(t *testing.T) {
	agentDir := settingsDir(t)
	want := scenario(t, newInProcess())
	got := scenario(t, overRPC{t: t, c: startEngine(t, agentDir)})
	compareTranscripts(t, "process", want, got)
}

// compareTranscripts fails t for every field of the scenario's transcript that differs
// between the in-process path (want) and the path named name (got).
func compareTranscripts(t *testing.T, name string, want, got transcript) {
	t.Helper()
	if len(want.Appended.([]any)) == 0 {
		t.Fatal("level 0 appended nothing: the scenario tests nothing")
	}
	if appended, _ := json.Marshal(want.Appended); !strings.Contains(string(appended), `"path":"loader.go","repoId":"0123456789ab"`) || !strings.Contains(string(appended), `"harness":"pig","model":"test-provider/session"`) {
		t.Fatalf("level 0 wrote no frame or writer: the encoding fields are untested\n%s", clip(appended))
	}
	if !strings.Contains(fmt.Sprint(want.Context), "[thinned]") {
		t.Fatal("the context event thinned nothing: the scenario tests nothing")
	}
	for _, field := range []struct {
		name      string
		want, got any
	}{
		{"appended", want.Appended, got.Appended},
		{"notifications", want.Notifications, got.Notifications},
		{"compaction", want.Compaction, got.Compaction},
		{"context", want.Context, got.Context},
		{"tool details", want.Tool, got.Tool},
		{"tool text", want.ToolText, got.ToolText},
	} {
		if !reflect.DeepEqual(asGo(field.want), asGo(field.got)) {
			a, _ := json.MarshalIndent(field.want, "", " ")
			b, _ := json.MarshalIndent(field.got, "", " ")
			t.Errorf("%s differ:\nin-process %s\n%-10s %s", field.name, clip(a), name, clip(b))
		}
	}
}

func clip(b []byte) string {
	if len(b) > 3000 {
		return string(b[:3000]) + "…"
	}
	return string(b)
}

// A cancelled session_before_compact request is cancelled in the engine too ($/cancelRequest),
// and so is the level-1 request it has in flight on the host (the stream's drain, which blocks
// until its context ends): the call returns, with the in-process path's answer for a request
// cancelled mid-way (nothing: PiG has abandoned it).
func TestBeforeCompactCancellationCrossesTheBoundary(t *testing.T) {
	agentDir := settingsDir(t)
	must(t, os.WriteFile(filepath.Join(agentDir, "settings.json"), []byte(`{"observational-memory": {"consolidate": true}}`), 0o600))
	client := startEngine(t, agentDir)
	want := cancelledCompaction(t, "in-process", newInProcess())
	got := cancelledCompaction(t, "process", overRPC{t: t, c: client})
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("in-process %v, process %v", want, got)
	}
}

// cancelledCompaction runs a compaction with level 1 on through d and cancels its request once
// level 1's request is in flight; it returns the answer.
func cancelledCompaction(t *testing.T, name string, d driver) map[string]any {
	t.Helper()
	entries := append(codingTurn("p", "fix the loader"), codingTurn("q", "and the saver")...)
	h := newHost(entries)
	h.ModelsFake.Streams.Block = true
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h.Ctx = ctx
	d.sessionStart(h)
	data := map[string]any{
		"preparation": map[string]any{
			"firstKeptEntryId": "qu", "tokensBefore": float64(30_000),
			"settings": map[string]any{"enabled": true, "reserveTokens": float64(16_384), "keepRecentTokens": float64(20_000)},
		},
		"branchEntries": maps(entries),
		"reason":        "threshold",
	}
	go func() {
		// Cancel once level 1's request is in flight.
		for h.ModelsFake.Streams.Calls() == 0 {
			time.Sleep(time.Millisecond)
		}
		cancel()
	}()
	done := make(chan map[string]any, 1)
	go func() { done <- d.beforeCompact(h, data) }()
	select {
	case result := <-done:
		if h.ModelsFake.Streams.Calls() != 1 {
			t.Fatalf("%s: level 1 made %d requests", name, h.ModelsFake.Streams.Calls())
		}
		return result
	case <-time.After(30 * time.Second):
		t.Fatalf("%s: the cancelled compaction did not return", name)
	}
	return nil
}

// TestCommandsAndRendering runs /om:status and the renderers over the connection.
func TestCommandsAndRendering(t *testing.T) {
	client := startEngine(t, settingsDir(t))
	h := newHost(codingTurn("p", "fix the loader"))
	must(t, client.SessionStart(h))
	must(t, client.Command(h, enginerpc.CommandStatus, ""))
	if len(h.Messages()) != 1 || !strings.Contains(h.Messages()[0], "emory") {
		t.Fatalf("status %q", h.Messages())
	}
	sections, err := client.BeforeAgentStart(h)
	must(t, err)
	if sections[enginerpc.MemorySection] != engine.ToolNoteLine {
		t.Fatalf("sections %v", sections)
	}
}

// percentile is the p-th percentile of sorted durations.
func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	i := int(p * float64(len(sorted)-1))
	return sorted[i]
}

// TestRoundTripLatency measures turn_end and context on a mid-size branch (30 coding turns,
// 240 entries, about 0.5 MB) in-process and over the pipe. It runs only with
// ENGINERPC_LATENCY=1 (the numbers mean little under -race) and logs median and p95.
func TestRoundTripLatency(t *testing.T) {
	if os.Getenv("ENGINERPC_LATENCY") != "1" {
		t.Skip("set ENGINERPC_LATENCY=1 to measure")
	}
	agentDir := settingsDir(t)
	var entries []session.Entry
	for i := range 30 {
		entries = append(entries, codingTurn(fmt.Sprintf("t%d", i), fmt.Sprintf("fix module %d", i))...)
	}
	// A compaction keeping the last ten turns, so the context event has aged results to thin.
	keep := entries[len(entries)-80].ID
	entries = append(entries[:len(entries)-80:len(entries)-80], append([]session.Entry{testfix.CompactionEntry("k1", testfix.CompactionArgs{FirstKeptEntryID: keep, Summary: "summary"})}, entries[len(entries)-80:]...)...)
	size, _ := json.Marshal(maps(entries))
	const n = 200
	measure := func(name string, d driver) {
		h := newHost(entries)
		d.sessionStart(h)
		d.turnEnd(h)
		turn := make([]time.Duration, 0, n)
		for i := range n {
			h.SetBranch(append(h.Entries, testfix.RawMessage(fmt.Sprintf("x%d", i), "next")))
			start := time.Now()
			d.turnEnd(h)
			turn = append(turn, time.Since(start))
		}
		ctxTimes := make([]time.Duration, 0, n)
		for range n {
			messages := contextMessages(h.Entries)
			start := time.Now()
			d.context(h, messages)
			ctxTimes = append(ctxTimes, time.Since(start))
		}
		sort.Slice(turn, func(i, j int) bool { return turn[i] < turn[j] })
		sort.Slice(ctxTimes, func(i, j int) bool { return ctxTimes[i] < ctxTimes[j] })
		t.Logf("%-10s turn_end median %v p95 %v | context median %v p95 %v", name, percentile(turn, 0.5), percentile(turn, 0.95), percentile(ctxTimes, 0.5), percentile(ctxTimes, 0.95))
	}
	t.Logf("branch: %d entries, %d bytes; context: %d messages", len(entries), len(size), len(contextMessages(entries)))
	measure("in-process", newInProcess())
	measure("process", overRPC{t: t, c: startEngine(t, agentDir)})
}
