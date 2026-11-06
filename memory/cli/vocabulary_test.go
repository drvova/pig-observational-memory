package cli

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/transcript"
)

// The transcript templates in the data find the same files as memory/transcript's own path
// functions, with and without each harness's environment variables.
func TestTranscriptTemplatesMatchReaders(t *testing.T) {
	cases := []struct {
		harness string
		env     []string
		want    func(cwd, session string) string
	}{
		{"cline", []string{"CLINE_DATA_DIR"}, transcript.ClineMessagesPath},
		{"copilot", []string{"COPILOT_HOME"}, func(_, s string) string { return transcript.CopilotEventsPath(s) }},
		{"cortex-code", []string{"SNOWFLAKE_HOME"}, transcript.CortexHistoryPath},
		{"augment", nil, transcript.AuggieSessionPath},
		{"kimi-cli", []string{"KIMI_SHARE_DIR"}, transcript.KimiWirePath},
		{"kode", []string{"KODE_CONFIG_DIR", "ANYKODE_CONFIG_DIR"}, transcript.KodeSessionPath},
		{"goose", []string{"GOOSE_PATH_ROOT"}, transcript.GooseDBPath},
		{"amazon-q", []string{"XDG_DATA_HOME"}, transcript.AmazonQDBPath},
		{"kiro", []string{"KIRO_HOME"}, transcript.KiroSessionPath},
		{"crush", nil, transcript.CrushDBPath},
	}
	for _, c := range cases {
		t.Run(c.harness, func(t *testing.T) {
			_, home := env(t)
			from := registry[c.harness].Hooks.TranscriptFrom
			if from == nil {
				t.Fatal("no transcriptFrom")
			}
			cwd := filepath.Join(home, "w", "my app.x")
			// Every subset of the variables, set to a directory.
			for mask := 0; mask < 1<<len(c.env); mask++ {
				for i, k := range c.env {
					v := ""
					if mask&(1<<i) != 0 {
						v = filepath.Join(home, "env", k)
					}
					t.Setenv(k, v)
				}
				for _, s := range []string{"s1", "../a b", "a$HOME~"} {
					if got, want := from(cwd, s), c.want(cwd, s); got != want {
						t.Errorf("env %b session %q: data %q, reader %q", mask, s, got, want)
					}
				}
			}
		})
	}
}

// A template inserts payload values as they are, and an empty one (or an unset variable with
// no default) yields no path.
func TestTranscriptTemplate(t *testing.T) {
	_, home := env(t)
	t.Setenv("OM_T_SET", "/set")
	t.Setenv("OM_T_UNSET", "")
	sum := md5.Sum([]byte("/w"))
	for _, c := range []struct{ tmpl, cwd, session, want string }{
		{"~/x/${session}.jsonl", "/w", "$HOME", filepath.Join(home, "x", "$HOME.jsonl")},
		{"${OM_T_SET}/${md5(cwd)}", "/w", "s", filepath.Join("/set", hex.EncodeToString(sum[:]))},
		{"${OM_T_UNSET:-~/d}/${slug(cwd)}", "/w/a.b c", "s", filepath.Join(home, "d", "-w-a-b-c")},
		{"${OM_T_SET:-~/d}/x", "", "", "/set/x"},
		{"${OM_T_UNSET}/x", "/w", "s", ""},
		{"~/${session}", "/w", "", ""},
		{"~/${md5(cwd)}", "", "s", ""},
	} {
		got, _ := transcriptTemplate(c.tmpl, c.cwd, c.session)
		if got != c.want {
			t.Errorf("%s (%q, %q): %q, want %q", c.tmpl, c.cwd, c.session, got, c.want)
		}
	}
	if _, err := transcriptTemplate("${unclosed", "", ""); err == nil || err == errNoValue {
		t.Errorf("an unclosed variable: %v", err)
	}
}

// Letta's session candidates: a conversation unless it is the default, else the agent's.
func TestSessionCandidates(t *testing.T) {
	from := registry["letta-code"].Hooks.SessionFrom
	for raw, want := range map[string]string{
		`{"conversation_id": "c1", "agent_id": "a1"}`:      "conversation:c1",
		`{"conversation_id": "default", "agent_id": "a1"}`: "default:a1",
		`{"conversation_id": "default"}`:                   "",
		`{}`:                                               "",
	} {
		if got := from(decodeRaw(t, raw)); got != want {
			t.Errorf("%s: %q, want %q", raw, got, want)
		}
	}
	if got := registry["cline"].Hooks.SessionFrom(decodeRaw(t, `{"sessionContext": {"rootSessionId": "r1"}}`)); got != "r1" {
		t.Errorf("cline: %q", got)
	}
}

func decodeRaw(t *testing.T, s string) map[string]any {
	t.Helper()
	p, err := decodeHookPayload(strings.NewReader(s), nil)
	if err != nil {
		t.Fatal(err)
	}
	return p.Raw
}

// The loader refuses what it cannot interpret.
func TestLoadDataRefuses(t *testing.T) {
	saved := registry
	defer func() { registry = saved }()
	hook := `"hooks": {"events": {"turn-end": "Stop"}, "transcript": "generic"`
	for name, entry := range map[string]string{
		"unknown field":       `"class": "none", "colour": 1`,
		"unknown resolver":    `"class": "none", ` + hook + `, "transcriptFrom": [{"resolver": "guess"}]}`,
		"unknown session":     `"class": "none", ` + hook + `, "sessionFrom": [{"resolver": "guess"}]}`,
		"unknown mapper":      `"class": "none", ` + hook + `, "record": {"mapper": "guess"}}`,
		"record role":         `"class": "none", ` + hook + `, "record": {"fields": {"p": "system"}}}`,
		"unknown event":       `"class": "none", "hooks": {"events": {"turn": "Stop"}, "transcript": "generic"}`,
		"unknown format":      `"class": "none", "hooks": {"events": {"turn-end": "Stop"}, "transcript": "rot13"}`,
		"unknown kind":        `"class": "none", "install": [{"kind": "curl"}]`,
		"missing asset":       `"class": "none", "install": [{"kind": "plugin-asset", "dir": "~/p", "asset": "assets/none.js"}]`,
		"bad template":        `"class": "none", ` + hook + `, "transcriptFrom": [{"path": "~/${session"}]}`,
		"unless without find": `"class": "none", ` + hook + `, "recordUnlessFound": true, "record": {"fields": {"prompt": "user"}}}`,
		"skeleton not yaml":   `"class": "file", "path": {"linux": "~/x"}, "format": "json", "key": ["k"], "entry": {}, "skeleton": {"a": 1}`,
	} {
		registry = map[string]Harness{}
		err := loadData([]byte(`[{"name": "x", "title": "X", ` + entry + `}]`))
		if err == nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Install steps run in order and are undone in reverse; print expands its directory and om's
// path escaped as Go quotes it.
func TestInstallSteps(t *testing.T) {
	_, home := env(t)
	exe := `/opt/o'm "x"\y/om`
	var out strings.Builder
	w := writer{out: &out}
	if err := registry["goose"].Custom(w, exe, false); err != nil {
		t.Fatal(err)
	}
	w.removing = true
	if err := registry["goose"].Custom(w, exe, true); err != nil {
		t.Fatal(err)
	}
	plugin := filepath.Join(home, ".agents", "plugins", "om")
	want := fmt.Sprintf("write %[1]s/plugin.json\nwrite %[1]s/hooks/hooks.json\nremove %[1]s/hooks/hooks.json\nremove %[1]s/plugin.json\n", plugin)
	if out.String() != want {
		t.Errorf("goose steps:\n%s\nwant\n%s", out.String(), want)
	}
	out.Reset()
	if err := registry["amazon-q"].Custom(writer{out: &out}, exe, false); err != nil {
		t.Fatal(err)
	}
	cmd := strconv.Quote(shellQuote(exe) + " hook amazon-q stop")
	if !strings.Contains(out.String(), filepath.Join(home, ".aws", "amazonq", "cli-agents")+", under") || !strings.Contains(out.String(), `"command": `+cmd+`, "timeout_ms": 120000`) {
		t.Errorf("amazon-q print:\n%s", out.String())
	}
}
