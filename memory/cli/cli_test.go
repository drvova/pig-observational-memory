package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/hostinfo"
	"github.com/drvova/pig-observational-memory/memory/testfix"
)

// env gives each test its own root and home, and restores what useRoot set.
func env(t *testing.T) (root, home string) {
	t.Helper()
	root, home = t.TempDir(), t.TempDir()
	t.Setenv(RootEnv, root)
	t.Setenv("HOME", home)
	for _, k := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "XDG_CONFIG_HOME", "PI_CODING_AGENT_DIR", "CLAUDE_PROJECT_DIR", "CLINE_MCP_SETTINGS_PATH", "COPILOT_HOME", "APPDATA", "USERPROFILE"} {
		t.Setenv(k, "")
	}
	t.Setenv("PATH", "")
	agent, cfg := hostinfo.AgentDir, hostinfo.ConfigDirName
	t.Cleanup(func() { hostinfo.AgentDir, hostinfo.ConfigDirName = agent, cfg })
	return root, home
}

func om(t *testing.T, stdin string, args ...string) string {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := Run(args, strings.NewReader(stdin), &out, &errOut); code != 0 {
		t.Fatalf("om %v: exit %d: %s", args, code, errOut.String())
	}
	return out.String()
}

// claudeTranscript writes a Claude Code transcript of n exchanges, each user line naming a
// service, its port and its cache.
func claudeTranscript(t *testing.T, path string, n int) {
	t.Helper()
	var lines []string
	parent := "null"
	for i := range n {
		u, _ := json.Marshal(map[string]any{"type": "user", "uuid": fmt.Sprintf("u%d", i), "parentUuid": json.RawMessage(parent), "timestamp": "2026-05-01T10:00:00.000Z", "message": map[string]any{"role": "user", "content": fmt.Sprintf("Service svc-%d listens on port %d and caches in /var/cache/svc%d.", i, 8000+i, i)}})
		a, _ := json.Marshal(map[string]any{"type": "assistant", "uuid": fmt.Sprintf("a%d", i), "parentUuid": fmt.Sprintf("u%d", i), "timestamp": "2026-05-01T10:00:30.000Z", "message": map[string]any{"id": fmt.Sprintf("m%d", i), "role": "assistant", "content": []any{map[string]any{"type": "text", "text": fmt.Sprintf("Noted: svc-%d on port %d.", i, 8000+i)}}}})
		lines = append(lines, string(u), string(a))
		parent = fmt.Sprintf("%q", fmt.Sprintf("a%d", i))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// The Claude Code flow: SessionStart hands the tool guidance, Stop stores level-0 records,
// PreCompact stores the engine's compaction, and the SessionStart after it hands the summary;
// the MCP server answers the memory tool for the session the hooks recorded, with no request.
func TestClaudeCodeFlow(t *testing.T) {
	counter := testfix.CountRequests(t)
	root, home := env(t)
	proj := filepath.Join(home, "proj")
	tr := filepath.Join(home, "t.jsonl")
	claudeTranscript(t, tr, 60)
	payload := func(source string) string {
		b, _ := json.Marshal(map[string]any{"session_id": "s1", "transcript_path": tr, "cwd": proj, "source": source, "trigger": "auto"})
		return string(b)
	}
	var start struct {
		Out struct {
			Event   string `json:"hookEventName"`
			Context string `json:"additionalContext"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(om(t, payload("startup"), "hook", "claude-code", "SessionStart")), &start); err != nil || start.Out.Event != "SessionStart" || !strings.Contains(start.Out.Context, "MCP server om") || strings.Contains(start.Out.Context, "before the compaction") {
		t.Fatalf("start %+v %v", start, err)
	}
	om(t, payload(""), "hook", "claude-code", "Stop")
	store := filepath.Join(root, "sessions", "claude-code", "s1.jsonl")
	if data, err := os.ReadFile(store); err != nil || !strings.Contains(string(data), "om.observations.recorded") {
		t.Fatalf("no level-0 batch in the store: %v", err)
	}
	om(t, payload(""), "hook", "claude-code", "PreCompact")
	if err := json.Unmarshal([]byte(om(t, payload("compact"), "hook", "claude-code", "SessionStart")), &start); err != nil || !strings.Contains(start.Out.Context, "before the compaction") || !strings.Contains(start.Out.Context, "8042") {
		t.Fatalf("after compaction %v:\n%s", err, start.Out.Context)
	}
	if data, _ := os.ReadFile(tr); strings.Contains(string(data), "om.") {
		t.Fatal("the transcript was written")
	}
	in := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
		"{\"jsonrpc\":\"2.0\",\"id\":3,\"method\":\"tools/call\",\"params\":{\"name\":\"memory\",\"arguments\":{\"code\":\"om.History(om.Search(`svc-42 port`))\"}}}",
		`{"jsonrpc":"2.0","id":4,"method":"nope"}`,
	}, "\n") + "\n"
	out := om(t, in, "mcp", "--harness", "claude-code", "--dir", proj)
	var replies []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		var r map[string]any
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("reply %q: %v", line, err)
		}
		replies = append(replies, r)
	}
	if len(replies) != 4 {
		t.Fatalf("replies %v", replies)
	}
	tools := replies[1]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["name"] != "memory" {
		t.Fatalf("tools %v", tools)
	}
	call := replies[2]["result"].(map[string]any)
	text := call["content"].([]any)[0].(map[string]any)["text"].(string)
	if call["isError"] != false || !strings.Contains(text, "8042") {
		t.Fatalf("call %v", call)
	}
	if replies[3]["error"] == nil {
		t.Fatal("an unknown method answered")
	}
	if counter.Total() != 0 {
		t.Fatalf("requests %v", counter.ByStage())
	}
}

// The MCP server finds the session before any hook ran: the newest transcript Claude Code wrote
// for the directory.
func TestDiscoverClaudeCode(t *testing.T) {
	_, home := env(t)
	proj := filepath.Join(home, "my.proj")
	claudeTranscript(t, filepath.Join(home, ".claude", "projects", strings.ReplaceAll(strings.ReplaceAll(proj, "/", "-"), ".", "-"), "abc.jsonl"), 3)
	s, ok := session(filepath.Join(home, "root"), "claude-code", proj)
	if !ok || s.ID != "abc" || s.Format != "claude-code" {
		t.Fatalf("discovered %+v %v", s, ok)
	}
	// Claude Code writes every character but an ASCII letter or digit as -, one per UTF-16 code
	// unit: an underscore, a space, an accented letter, an emoji (two).
	homeSlug := regexp.MustCompile(`[^a-zA-Z0-9]`).ReplaceAllString(home, "-")
	for _, c := range []struct{ dir, folder string }{
		{"my_proj", "-my-proj"}, {"my proj", "-my-proj"}, {"café", "-caf-"}, {"日本😀x", "-----x"},
	} {
		claudeTranscript(t, filepath.Join(home, ".claude", "projects", homeSlug+c.folder, c.dir+".jsonl"), 3)
		if s, ok := session(filepath.Join(home, "root"), "claude-code", filepath.Join(home, c.dir)); !ok || s.ID != c.dir {
			t.Errorf("%q: discovered %+v %v", c.dir, s, ok)
		}
	}
}

// Codex's notify program runs level 0 over the thread's rollout.
func TestCodexNotify(t *testing.T) {
	root, home := env(t)
	codex := filepath.Join(home, ".codex")
	rollout := filepath.Join(codex, "sessions", "2026", "05", "01", "rollout-2026-05-01T10-00-00-th1.jsonl")
	var lines []string
	lines = append(lines, `{"type":"session_meta","payload":{"id":"th1","cwd":"/p"}}`)
	for i := range 30 {
		lines = append(lines, fmt.Sprintf(`{"timestamp":"2026-05-01T10:00:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"Queue q-%d drains into bucket b-%d every %d minutes."}]}}`, i, i, i+5))
	}
	if err := os.MkdirAll(filepath.Dir(rollout), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(rollout, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	om(t, "", "hook", "codex", "notify", `{"type":"agent-turn-complete","thread-id":"th1","cwd":"/p"}`)
	if data, err := os.ReadFile(filepath.Join(root, "sessions", "codex", "th1.jsonl")); err != nil || !strings.Contains(string(data), "b-17") {
		t.Fatalf("codex store %v", err)
	}
	if s, ok := session(root, "codex", "/p"); !ok || s.Transcript != rollout {
		t.Fatalf("codex session %+v", s)
	}
}

// The generic protocol: turn-end records, compact prints the summary; Pi's before-compact
// answers on Pi's own cut with the compaction for Pi to store (not stored by om).
func TestGenericAndPi(t *testing.T) {
	root, home := env(t)
	gen := filepath.Join(home, "g.jsonl")
	var lines []string
	for i := range 80 {
		lines = append(lines, fmt.Sprintf(`{"id":"m%d","role":"user","text":"Host h-%d serves region r-%d on port %d and rotates its logs in /var/log/h%d every %d days.","timestamp":"2026-05-01T10:00:00Z"}`, i, i, i, 9000+i, i, i+3))
		lines = append(lines, fmt.Sprintf(`{"id":"r%d","role":"assistant","text":"Noted: h-%d, region r-%d, port %d, logs in /var/log/h%d.","timestamp":"2026-05-01T10:00:05Z"}`, i, i, i, 9000+i, i))
	}
	os.WriteFile(gen, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	p, _ := json.Marshal(map[string]any{"harness": "mine", "session_id": "g1", "transcript_path": gen, "cwd": home})
	om(t, string(p), "hook", "generic", "turn-end")
	if summary := om(t, string(p), "hook", "generic", "compact"); !strings.Contains(summary, "9033") {
		t.Fatalf("generic summary:\n%s", summary)
	}
	pi := filepath.Join(home, "pi.jsonl")
	lines = []string{`{"type":"session","id":"hdr"}`}
	parent := "null"
	for i := range 90 {
		lines = append(lines, fmt.Sprintf(`{"type":"message","id":"e%d","parentId":%s,"timestamp":"2026-05-01T10:00:00.000Z","message":{"role":"user","content":"Disk d-%d mounts at /mnt/d%d with quota %dG and is backed up to vault v-%d every %d hours."}}`, i, parent, i, i, i+10, i, i+1))
		parent = fmt.Sprintf(`"e%d"`, i)
	}
	os.WriteFile(pi, []byte(strings.Join(lines, "\n")+"\n"), 0o600)
	pp, _ := json.Marshal(map[string]any{"session_id": "p1", "transcript_path": pi, "cwd": home, "first_kept_entry_id": "e80", "tokens_before": 0})
	om(t, string(pp), "hook", "pi", "turn-end")
	var res struct {
		Compaction struct {
			Summary   string `json:"summary"`
			FirstKept string `json:"firstKeptEntryId"`
		} `json:"compaction"`
	}
	if err := json.Unmarshal([]byte(om(t, string(pp), "hook", "pi", "before-compact")), &res); err != nil || res.Compaction.FirstKept != "e80" || !strings.Contains(res.Compaction.Summary, "/mnt/d") || !strings.Contains(res.Compaction.Summary, "om.Notes(om.From(") {
		t.Fatalf("pi compaction %+v %v", res, err)
	}
	if data, _ := os.ReadFile(filepath.Join(root, "sessions", "pi", "p1.jsonl")); strings.Contains(string(data), `"compaction"`) {
		t.Fatal("om stored Pi's compaction")
	}
	spec := om(t, "", "tool-spec")
	if !strings.Contains(spec, `"name":"memory"`) || !strings.Contains(spec, `"parameters"`) {
		t.Fatalf("tool spec %s", spec)
	}
	call := om(t, "{\"code\":\"om.History(om.Search(`d-7`))\"}", "call", "--harness", "pi", "--dir", home)
	if !strings.Contains(call, "/mnt/d7") || !strings.Contains(call, `"isError":false`) {
		t.Fatalf("call %s", call)
	}
}

// install merges om into each harness's config beside what is there, is idempotent, and
// uninstall leaves the config as it was.
func TestInstallRoundTrip(t *testing.T) {
	_, home := env(t)
	claude := filepath.Join(home, ".claude", "settings.json")
	codex := filepath.Join(home, ".codex", "config.toml")
	os.MkdirAll(filepath.Dir(claude), 0o700)
	os.MkdirAll(filepath.Dir(codex), 0o700)
	os.WriteFile(claude, []byte(`{"model":"x","hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo mine"}]}]}}`), 0o600)
	os.WriteFile(codex, []byte("model = \"y\"\n\n[profiles.a]\nmodel = \"z\"\n"), 0o600)
	for _, h := range []string{"claude-code", "codex", "opencode", "pi"} {
		if out := om(t, "", "install", "--dry-run", h); strings.Contains(out, "unchanged") {
			t.Fatalf("dry run of %s: %s", h, out)
		}
	}
	if data, _ := os.ReadFile(claude); strings.Contains(string(data), "hook claude-code") {
		t.Fatal("a dry run wrote")
	}
	for _, h := range []string{"claude-code", "codex", "opencode", "pi"} {
		om(t, "", "install", h)
	}
	data, _ := os.ReadFile(claude)
	var s map[string]any
	json.Unmarshal(data, &s)
	stop := s["hooks"].(map[string]any)["Stop"].([]any)
	if len(stop) != 2 || s["model"] != "x" {
		t.Fatalf("claude settings %s", data)
	}
	toml, _ := os.ReadFile(codex)
	if !strings.HasPrefix(string(toml), "notify = [") || !strings.Contains(string(toml), "[mcp_servers.om]") || !strings.Contains(string(toml), "[profiles.a]") {
		t.Fatalf("codex config:\n%s", toml)
	}
	for _, p := range []string{filepath.Join(home, ".config", "opencode", "plugin", "om.js"), filepath.Join(home, ".pi", "agent", "extensions", "om.ts")} {
		if b, err := os.ReadFile(p); err != nil || strings.Contains(string(b), "__OM_") {
			t.Fatalf("%s: %v", p, err)
		}
	}
	if out := om(t, "", "install", "claude-code"); !strings.Contains(out, "unchanged") {
		t.Fatalf("reinstall %s", out)
	}
	for _, h := range []string{"claude-code", "codex", "opencode", "pi"} {
		om(t, "", "uninstall", h)
	}
	data, _ = os.ReadFile(claude)
	json.Unmarshal(data, &s)
	if strings.Contains(string(data), "hook claude-code") || len(s["hooks"].(map[string]any)["Stop"].([]any)) != 1 {
		t.Fatalf("after uninstall %s", data)
	}
	if toml, _ = os.ReadFile(codex); string(toml) != "model = \"y\"\n\n[profiles.a]\nmodel = \"z\"\n" {
		t.Fatalf("codex after uninstall:\n%q", toml)
	}
}
