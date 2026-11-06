package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestLispImageEndToEnd builds the real engine image (lisp/build-image.sh), runs a Claude Code
// session (SessionStart, Stop twice, PreCompact, SessionStart after it, the MCP server), a
// generic one (turn-end twice, compact, om view, a turn end after the compaction, a second
// compact, session-start) and a Pi one (session-start, turn-end, before-compact on Pi's cut over
// a session file with Pi's own compactions) with OM_ENGINE=lisp, and compares everything printed
// and stored with the same sequence on the Go engine. The image runs the hooks and compactions
// (from the generic view on with OM_LISP_FALLBACK=0, so nothing falls back); the MCP server is
// not ported yet and falls back to the Go engine, over the store the image wrote. Ids and the
// times entries were appended at are normalized; nothing else is.
func TestLispImageEndToEnd(t *testing.T) {
	if os.Getenv("OM_LISP_PARITY") != "1" {
		t.Skip("set OM_LISP_PARITY=1 to build the SBCL engine image and run hooks against it")
	}
	if _, err := exec.LookPath("sbcl"); err != nil {
		t.Skip("sbcl is not on PATH")
	}
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("no sh")
	}
	script, err := filepath.Abs(filepath.Join("..", "..", "lisp", "build-image.sh"))
	if err != nil {
		t.Fatal(err)
	}
	image := filepath.Join(t.TempDir(), "om-engine")
	start := time.Now()
	if out, err := exec.Command(shell, script, image).CombinedOutput(); err != nil {
		t.Fatalf("build-image: %v\n%s", err, out)
	}
	info, err := os.Stat(image)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("image built in %s, %d MB", time.Since(start).Round(time.Millisecond), info.Size()>>20)

	want := lispFlow(t, "")
	got := lispFlow(t, image)
	t.Logf("%d normalized lines compared", strings.Count(want, "\n"))
	if got != want {
		gl, wl := strings.Split(got, "\n"), strings.Split(want, "\n")
		for i := 0; i < len(gl) || i < len(wl); i++ {
			var g, w string
			if i < len(gl) {
				g = gl[i]
			}
			if i < len(wl) {
				w = wl[i]
			}
			if g != w {
				t.Fatalf("line %d differs:\nlisp: %s\ngo:   %s", i+1, g, w)
			}
		}
	}
}

// lispFlow runs the sequence on a fresh root, on the image when image is set, and returns
// everything it printed and stored, normalized.
func lispFlow(t *testing.T, image string) string {
	root, home := env(t)
	t.Setenv(LispSocketEnv, "")
	t.Setenv(LispFallbackEnv, "")
	if image == "" {
		t.Setenv(EngineEnv, "")
	} else {
		t.Setenv(EngineEnv, "lisp")
		t.Setenv(LispImageEnv, image)
		t.Setenv(LispIdleEnv, "60")
		t.Cleanup(func() {
			c := newLispClient(root)
			if err := c.call("image/stop", map[string]any{}, nil); err != nil {
				t.Errorf("image/stop: %v", err)
			}
			c.close()
			for range 100 {
				if _, err := os.Stat(filepath.Join(root, "engine.pid")); err != nil {
					return
				}
				time.Sleep(20 * time.Millisecond)
			}
			t.Error("the image did not exit")
		})
	}
	var out strings.Builder
	section := func(name, text string) { fmt.Fprintf(&out, "== %s\n%s\n", name, text) }
	store := func(harness, id string) string {
		data, _ := os.ReadFile(filepath.Join(root, "sessions", harness, id+".jsonl"))
		return string(data)
	}

	proj := filepath.Join(home, "proj")
	tr := filepath.Join(home, "t.jsonl")
	claudeTranscript(t, tr, 60)
	payload := func(source string) string {
		b, _ := json.Marshal(map[string]any{"session_id": "s1", "transcript_path": tr, "cwd": proj, "source": source, "trigger": "auto"})
		return string(b)
	}
	section("SessionStart", om(t, payload("startup"), "hook", "claude-code", "SessionStart"))
	om(t, payload(""), "hook", "claude-code", "Stop")
	section("store after Stop", store("claude-code", "s1"))
	if image != "" {
		c := newLispClient(root)
		var status struct {
			Sessions int `json:"sessions"`
		}
		if err := c.call("status", map[string]any{}, &status); err != nil || status.Sessions != 1 {
			t.Fatalf("the image did not run the turn end: %+v %v", status, err)
		}
		c.close()
	}
	om(t, payload(""), "hook", "claude-code", "Stop")
	section("store after the second Stop", store("claude-code", "s1"))
	section("PreCompact", om(t, payload(""), "hook", "claude-code", "PreCompact"))
	section("SessionStart after compaction", om(t, payload("compact"), "hook", "claude-code", "SessionStart"))
	mcp := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		"{\"jsonrpc\":\"2.0\",\"id\":4,\"method\":\"tools/call\",\"params\":{\"name\":\"memory\",\"arguments\":{\"code\":\"om.History(om.Search(`svc-42 port`))\"}}}",
	}, "\n") + "\n"
	section("MCP", om(t, mcp, "mcp", "--harness", "claude-code", "--dir", proj))
	section("store", store("claude-code", "s1"))

	gen := filepath.Join(home, "g.jsonl")
	var lines []string
	for i := range 40 {
		lines = append(lines, fmt.Sprintf(`{"id":"m%d","role":"user","text":"Host h-%d serves region r-%d on port %d and rotates its logs in /var/log/h%d every %d days.","timestamp":"2026-05-01T10:00:00Z"}`, i, i, i, 9000+i, i, i+3))
		lines = append(lines, fmt.Sprintf(`{"id":"r%d","role":"assistant","text":"Noted: h-%d, region r-%d, port %d.","timestamp":"2026-05-01T10:00:05Z"}`, i, i, i, 9000+i))
	}
	if err := os.WriteFile(gen, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, _ := json.Marshal(map[string]any{"harness": "mine", "session_id": "g1", "transcript_path": gen, "cwd": home})
	om(t, string(p), "hook", "generic", "turn-end")
	om(t, string(p), "hook", "generic", "turn-end")
	section("generic store", store("mine", "g1"))
	section("generic compact", om(t, string(p), "hook", "generic", "compact"))
	section("generic store after compact", store("mine", "g1"))

	// From here on every event is ported: the image answers each one, with no fallback.
	if image != "" {
		t.Setenv(LispFallbackEnv, "0")
	}
	section("view after compact", om(t, "", "view", "--harness", "mine", "--dir", home))
	section("view full", om(t, "", "view", "--harness", "mine", "--dir", home, "full"))
	section("view usage", om(t, "", "view", "--harness", "mine", "--dir", home, "everything"))
	// Level 0 after a compaction, a second compaction, and the summary at the next start.
	for i := 40; i < 48; i++ {
		lines = append(lines, fmt.Sprintf(`{"id":"m%d","role":"user","text":"Host h-%d moved to region r-%d and now serves port %d.","timestamp":"2026-05-02T10:00:00Z"}`, i, i, i, 9000+i))
		lines = append(lines, fmt.Sprintf(`{"id":"r%d","role":"assistant","text":"Noted: h-%d moved.","timestamp":"2026-05-02T10:00:05Z"}`, i, i))
	}
	if err := os.WriteFile(gen, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	om(t, string(p), "hook", "generic", "turn-end")
	section("generic store after a turn end past the compaction", store("mine", "g1"))
	section("generic second compact", om(t, string(p), "hook", "generic", "compact"))
	section("generic session start", om(t, string(p), "hook", "generic", "session-start"))
	section("generic store after the second compact", store("mine", "g1"))

	// Pi's protocol: a session file with Pi's own compactions, compacted on Pi's cut and not
	// stored (the harness keeps it), its summary record stored once.
	data, err := os.ReadFile(filepath.Join("..", "transcript", "testdata", "real", "pi-compactions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	pi := filepath.Join(home, "pi.jsonl")
	if err := os.WriteFile(pi, data, 0o600); err != nil {
		t.Fatal(err)
	}
	pp, _ := json.Marshal(map[string]any{"session_id": "p1", "transcript_path": pi, "cwd": home, "reason": "threshold", "first_kept_entry_id": "eaa61939"})
	section("pi session start", om(t, string(pp), "hook", "pi", "session-start"))
	om(t, string(pp), "hook", "pi", "turn-end")
	section("pi before-compact", om(t, string(pp), "hook", "pi", "before-compact"))
	section("pi store", store("pi", "p1"))
	return normalizeFlow(out.String(), root, home)
}

var (
	flowIDPattern = regexp.MustCompile(`om[0-9a-f]{8}`)
	// flowStampPattern is an entry's append time: neutral.Host stamps entries with the time in
	// milliseconds, and the fixtures' own times are in May.
	flowStampPattern = regexp.MustCompile(`"timestamp":"2026-(0[6-9]|1[0-2])-\d\dT\d\d:\d\d:\d\d\.\d{3}Z"`)
)

// normalizeFlow names the random entry ids by order of first appearance, blanks the append
// times, and replaces the run's own directories.
func normalizeFlow(s, root, home string) string {
	s = strings.ReplaceAll(s, root, "<root>")
	s = strings.ReplaceAll(s, home, "<home>")
	s = flowStampPattern.ReplaceAllString(s, `"timestamp":"<now>"`)
	names := map[string]string{}
	return flowIDPattern.ReplaceAllStringFunc(s, func(id string) string {
		if n, ok := names[id]; ok {
			return n
		}
		names[id] = fmt.Sprintf("<id%d>", len(names))
		return names[id]
	})
}
