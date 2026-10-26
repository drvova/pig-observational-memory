package observationalmemory

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/commands"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/config"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/curated"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/harness"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/host"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/legacyquery"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/om"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omtool"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/rules"
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

// One memory tool, taking code and optional strings, is the advertised set; commands are only
// the read-only views. No setting gates it: it registers with curated memory off too.
func TestRegistersOneMemoryTool(t *testing.T) {
	reg := registered(t)
	if len(reg.Tools) != 1 || reg.Tools[0].Name != omtool.ToolName {
		t.Fatalf("tools %+v, want exactly the memory tool", reg.Tools)
	}
	tool := reg.Tools[0]
	if tool.Description != omtool.Description {
		t.Fatalf("description %q", tool.Description)
	}
	properties, _ := tool.Parameters["properties"].(map[string]any)
	required, _ := tool.Parameters["required"].([]any)
	if len(properties) != 2 || properties["code"] == nil || properties["strings"] == nil || len(required) != 1 || required[0] != "code" {
		t.Fatalf("parameters %+v, want a required code string and optional strings", tool.Parameters)
	}
	commands := map[string]bool{}
	for _, c := range reg.Commands {
		commands[c.Name] = true
	}
	if !commands["om:status"] || !commands["om:view"] || commands["om:memory"] || len(commands) != 2 {
		t.Fatalf("commands %v, want om:status and om:view only", commands)
	}
	// No configuration field turns the tool on or off.
	fields := reflect.TypeOf(config.Config{})
	for i := 0; i < fields.NumField(); i++ {
		name := strings.ToLower(fields.Field(i).Name)
		if strings.Contains(name, "codetool") || strings.Contains(name, "sandbox") || strings.Contains(name, "memorytool") {
			t.Fatalf("config field %s gates the tool", fields.Field(i).Name)
		}
	}
}

// The advertised spec is fixed text: what the extension registers is memoryToolSpec, built from
// omtool's constants alone (so no profile or store can change it); no profile registers the tool
// again or deactivates it, under two profiles with different codecs and before and after a
// definition is kept; the description and schema name no om symbol, and no part carries a form of
// the removed query language.
func TestAdvertisedSpecIsFixed(t *testing.T) {
	h := testfix.NewFakeHost(testfix.RawMessage("u1", "hello"))
	h.Home = t.TempDir()
	h.Model = testfix.Model("p", "m")
	store := curated.New(curated.DefaultDir(h.Home))
	manager := harness.NewManager(func(host.ToolHost) (curated.Call, bool) { return curated.Call{Store: store, Source: "s"}, true })
	t.Cleanup(manager.Namespace().Reset)
	reg := registered(t)
	spec := memoryToolSpec(manager)
	if len(reg.Tools) != 1 || reg.Tools[0].Description != spec.Description || reg.Tools[0].Name != spec.Name {
		t.Fatalf("the registered tool is not memoryToolSpec: %+v", reg.Tools)
	}
	want, _ := json.Marshal(spec.Parameters)
	if got, _ := json.Marshal(reg.Tools[0].Parameters); string(got) != string(want) {
		t.Fatalf("registered parameters %s, want %s", got, want)
	}
	h.Active = []string{omtool.ToolName}
	stillActive := func(when string) {
		t.Helper()
		active, _ := h.ActiveTools()
		if !slices.Contains(active, omtool.ToolName) {
			t.Fatalf("%s: the memory tool was deactivated: %v", when, active)
		}
	}
	stillActive("the first profile")
	// Another model whose profile switched the codec (a switch-codec rule).
	if _, err := rules.Add(store, rules.Rule{Detector: rules.DetectDecodeFailure, Remedy: rules.RemedySwitchCodec, Models: []string{"q/other"}, Engine: curated.EngineV2}); err != nil {
		t.Fatal(err)
	}
	h.Model = testfix.Model("q", "other")
	if l, _ := harness.For(h, config.Defaults, h.Model); l.Profile().Codec == tables.Rows.Name() {
		t.Fatal("the test needs a profile with another codec")
	}
	stillActive("a profile with another codec")
	if r := manager.RunMemory(h, map[string]any{"code": "func up(s string) string { return strings.ToUpper(s) }"}); len(r.Details.Kept) != 1 {
		t.Fatalf("keep: %s", r.Text)
	}
	stillActive("after a keep")
	if _, ok := h.RegisteredSpec(omtool.ToolName); ok {
		t.Fatal("a profile registered the memory tool again")
	}
	schema, _ := json.Marshal(omtool.Parameters)
	for _, part := range []string{omtool.Description, string(schema)} {
		if strings.Contains(part, "om.") {
			t.Fatalf("%q names om", part)
		}
		for _, g := range om.ManualGroups {
			for _, name := range g.Names {
				if regexp.MustCompile(`\b` + name + `\b`).MatchString(part) {
					t.Fatalf("%q names om.%s", part, name)
				}
			}
		}
	}
	all := strings.Join(append([]string{omtool.Label, omtool.Description, string(schema), omtool.PromptSnippet}, omtool.PromptGuidelines...), "\n")
	forms := []string{"recall <", "| search", "filter"}
	for _, source := range legacyquery.Sources {
		forms = append(forms, source+" |")
	}
	for _, form := range forms {
		if strings.Contains(all, form) {
			t.Fatalf("the spec carries the old form %q:\n%s", form, all)
		}
	}
	for _, g := range omtool.PromptGuidelines {
		if !strings.Contains(g, omtool.ToolName) {
			t.Fatalf("guideline does not name the tool: %q", g)
		}
	}
}

// The same code over the same memory prints the same bytes under two session models with
// different profiles, om.Layer and om.Info included, and the manual is the same.
func TestCodeResultsAreTheSameForEveryModel(t *testing.T) {
	h := testfix.NewFakeHost(testfix.RawMessage("u1", "I bake sourdough."), testfix.RawMessage("u2", "next"))
	h.Home = t.TempDir()
	store := curated.New(curated.DefaultDir(h.Home))
	if _, err := rules.Add(store, rules.Rule{Detector: rules.DetectDecodeFailure, Remedy: rules.RemedySwitchCodec, Models: []string{"q/other"}, Engine: curated.EngineV2}); err != nil {
		t.Fatal(err)
	}
	for _, m := range []string{"p/m", "q/other"} {
		_ = store.RecordModel(curated.ModelEvent{Model: m, Kind: curated.ModelKindPass, Stage: "compaction", Engine: curated.EngineV2, Tokens: 100})
	}
	rt := runtime.New()
	manager := harness.NewManager(func(host.ToolHost) (curated.Call, bool) { return curated.Call{Store: store, Source: "s"}, true })
	manager.Info = func(h host.ToolHost) func() om.StatusInfo { return statusInfo(rt, h) }
	t.Cleanup(manager.Namespace().Reset)
	code := map[string]any{"code": "fmt.Println(om.History(om.Search(`sourdough`)))\nfmt.Println(om.Layer())\nfmt.Println(om.Info())\nom.Notes().Len()"}
	// Each call records a usage event, which om.Info then counts: every model's run starts from
	// the same store.
	snapshot := filepath.Join(t.TempDir(), "store")
	if err := os.CopyFS(snapshot, os.DirFS(store.Dir)); err != nil {
		t.Fatal(err)
	}
	var outs, manuals []string
	for _, model := range []host.Model{testfix.Model("p", "m"), testfix.Model("q", "other", map[string]any{"contextWindow": float64(8000)})} {
		if err := os.RemoveAll(store.Dir); err != nil {
			t.Fatal(err)
		}
		if err := os.CopyFS(store.Dir, os.DirFS(snapshot)); err != nil {
			t.Fatal(err)
		}
		h.Model = model
		r := manager.RunMemory(h, code)
		if r.IsError {
			t.Fatalf("%s", r.Text)
		}
		outs = append(outs, r.Text)
		manuals = append(manuals, manager.RunMemory(h, map[string]any{"code": "  \n"}).Text)
	}
	if outs[0] != outs[1] || !strings.Contains(outs[0], "u1") {
		t.Fatalf("results differ by model:\n%s\n---\n%s", outs[0], outs[1])
	}
	if manuals[0] != manuals[1] || !strings.HasPrefix(manuals[0], "── The memory tool ──") {
		t.Fatalf("manuals differ:\n%s", manuals[0])
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

// The code tool's om.Info closure answers with commands.Info: the shared /om:status figures.
func TestStatusInfoClosure(t *testing.T) {
	rt := runtime.New()
	h := testfix.NewFakeHost(testfix.RawMessage("u1", "hello"))
	info := statusInfo(rt, h)()
	want, err := commands.Info(rt, h)
	if err != nil || info.String() != want.String() || !strings.Contains(info.String(), "── Memory ──") {
		t.Fatalf("%q %v", info.String(), err)
	}
	h.BranchErr = errors.New("unreadable")
	if got := statusInfo(rt, h)(); len(got.Lines) != 1 || !strings.Contains(got.Lines[0], "unreadable") {
		t.Fatalf("%+v", got)
	}
}
