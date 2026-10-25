package observationalmemory

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/memquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/runtime"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/tables"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

func TestProjectRootIsTheEnclosingRepository(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(root, "pkg", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := projectRoot(sub); got != root {
		t.Fatalf("projectRoot(%s) = %s, want the repository root %s", sub, got, root)
	}
	plain, _ := filepath.EvalSymlinks(t.TempDir())
	if got := projectRoot(plain); got != plain {
		t.Fatalf("outside a repository the project is cwd itself, got %s", got)
	}
}

// registration is the register message an extension sends when it connects:
// the tools and commands PiG installs.
type registration struct {
	Tools []struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"tools"`
	Commands []struct {
		Name string `json:"name"`
	} `json:"commands"`
}

// registered connects the factory's extension to an in-memory host and reads
// its register message.
func registered(t *testing.T) registration {
	t.Helper()
	hostSide, extSide := net.Pipe()
	defer hostSide.Close()
	done := make(chan error, 1)
	go func() { done <- Extension().RunWithConn(extSide) }()
	_ = hostSide.SetDeadline(time.Now().Add(10 * time.Second))
	var header [4]byte
	if _, err := io.ReadFull(hostSide, header[:]); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, binary.BigEndian.Uint32(header[:]))
	if _, err := io.ReadFull(hostSide, body); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Type     string       `json:"type"`
		Register registration `json:"register"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil || envelope.Type != "register" {
		t.Fatalf("first frame %s: %v", body, err)
	}
	_ = hostSide.Close()
	<-done
	return envelope.Register
}

// One memory tool replaces recall and the six curated tools; commands are
// only the read-only views.
func TestRegistersOneMemoryTool(t *testing.T) {
	reg := registered(t)
	if len(reg.Tools) != 1 || reg.Tools[0].Name != memquery.ToolName {
		t.Fatalf("tools %+v, want exactly the memory tool", reg.Tools)
	}
	tool := reg.Tools[0]
	if !strings.Contains(tool.Description, tables.Codecs()[0].Spec()) || !strings.Contains(tool.Description, "recall <id>") {
		t.Fatalf("description lacks the grammar or the codec spec: %s", tool.Description)
	}
	properties, _ := tool.Parameters["properties"].(map[string]any)
	required, _ := tool.Parameters["required"].([]any)
	if len(properties) != 1 || properties["query"] == nil || len(required) != 1 || required[0] != "query" {
		t.Fatalf("parameters %+v, want one required query string", tool.Parameters)
	}
	commands := map[string]bool{}
	for _, c := range reg.Commands {
		commands[c.Name] = true
	}
	if !commands["om:status"] || !commands["om:view"] || commands["om:memory"] || len(commands) != 2 {
		t.Fatalf("commands %v, want om:status and om:view only", commands)
	}
}

// The memory tool and the harness read their configuration through
// curatedCall, which reads project settings only from a trusted project.
func TestCuratedCallReadsProjectSettingsOnlyWhenTrusted(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "agent")
	cwd := filepath.Join(root, "project")
	info, err := json.Marshal(map[string]string{"agentDir": agentDir, "configDirName": ".pig"})
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("PIG_EXT_HOST_INFO", string(info))
	for path, body := range map[string]string{
		filepath.Join(agentDir, "settings.json"):    `{"observational-memory": {"curatedMemoryIndexTokens": 700}}`,
		filepath.Join(cwd, ".pig", "settings.json"): `{"observational-memory": {"curatedMemoryIndexTokens": 512}}`,
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cases := []struct {
		name    string
		trusted bool
		err     error
		want    int
	}{
		{"trusted", true, nil, 512},
		{"untrusted", false, nil, 700},
		{"trust check failed", true, errors.New("host unavailable"), 700},
	}
	for _, c := range cases {
		h := testfix.NewFakeHost()
		h.WorkDir, h.Trusted, h.TrustErr = cwd, c.trusted, c.err
		_, cfg, _ := curatedCall(h)
		if cfg.CuratedMemoryIndexTokens != c.want {
			t.Fatalf("%s: curatedMemoryIndexTokens = %d, want %d", c.name, cfg.CuratedMemoryIndexTokens, c.want)
		}
	}
}

// The section leads with the one tool-note line, also when curated memory is off.
func TestMemorySectionText(t *testing.T) {
	if text := memorySectionText("INDEX", true); text != toolNoteLine+"\n\nINDEX" {
		t.Fatalf("on: %q", text)
	}
	if text := memorySectionText("", false); text != toolNoteLine {
		t.Fatalf("off: %q", text)
	}
	if text := memorySectionText("", true); text != toolNoteLine {
		t.Fatalf("empty index: %q", text)
	}
}

// The tool-note line is said to the user before every tool call and kept verbatim on the call's
// attempt record, so it must not make the model talk about memory, compaction or resuming (a real
// v2 session wrote "... to resume this work if the conversation is compacted" before every call).
func TestToolNoteLineNamesNoMemoryMachinery(t *testing.T) {
	lower := strings.ToLower(toolNoteLine)
	for _, word := range []string{"memory", "compact", "resum", "summar", "context"} {
		if strings.Contains(lower, word) {
			t.Fatalf("tool-note line mentions %q: %q", word, toolNoteLine)
		}
	}
	if !strings.Contains(lower, "tool call") || !strings.Contains(lower, "why") {
		t.Fatalf("tool-note line no longer asks what and why before a tool call: %q", toolNoteLine)
	}
}

// The context handler follows the configuration: subscribed once while wanted, unsubscribed in
// passive mode, so a passive session is sent no request context at all.
func TestContextSubscriptionFollowsPassive(t *testing.T) {
	subscribed, unsubscribed := 0, 0
	c := &contextSubscription{subscribe: func() func() {
		subscribed++
		return func() { unsubscribed++ }
	}}
	c.sync(false)
	c.sync(true)
	c.sync(true)
	if subscribed != 1 || unsubscribed != 0 {
		t.Fatalf("wanted: subscribed %d, unsubscribed %d", subscribed, unsubscribed)
	}
	c.sync(false)
	c.sync(false)
	if subscribed != 1 || unsubscribed != 1 {
		t.Fatalf("not wanted: subscribed %d, unsubscribed %d", subscribed, unsubscribed)
	}
	rt := runtime.New()
	cfg := config.Defaults
	rt.SetConfig(cfg)
	if !wantsContext(rt) {
		t.Fatal("the extension does not want the context")
	}
	cfg.Passive = true
	rt.SetConfig(cfg)
	if wantsContext(rt) {
		t.Fatal("passive mode wants the context")
	}
}
