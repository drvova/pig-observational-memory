package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/curated"
	omapi "github.com/drvova/pig-observational-memory/memory/om"
	"github.com/drvova/pig-observational-memory/memory/omtool"
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
		t.Setenv(EngineEnv, "go")
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
	// On the image the MCP server serves the Lisp tool (docs/lisp-core.md, decision 10): its
	// text and its answers are the Lisp tool's, so they are checked on their own, not against
	// Go's.
	if mcpOut := om(t, mcp, "mcp", "--harness", "claude-code", "--dir", proj); image != "" {
		checkLispMCP(t, mcpOut)
	}
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

	// A curated store with memories (1b): the session start settles it (a staged memory
	// promoted, a correction on the branch flagging a memory) and hands the ranked index within
	// the budget the settings give it, on the image with no fallback.
	fixtureLines := curatedFixture(t, root, home, filepath.Join(root, "sessions", "mine", "g1.jsonl"))
	text, log := omLog(t, string(p), "hook", "generic", "session-start")
	section("generic session start with memories", text)
	section("its notices", log)
	section("the curated store after it", curatedStore(t, root, fixtureLines))
	// om status runs on the image (docs/lisp-port-status.md): everything before its code
	// section, which the image redefines for the Lisp layer, is Go's.
	status := om(t, "", "status", "--harness", "mine", "--dir", home)
	if i := strings.Index(status, "\n\n── Code ──"); i >= 0 {
		status = status[:i] + "\n"
	}
	section("status", status)

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

	projectFlow(t, home, image != "", section, func() string { return store("mine", "r1") })
	flow := out.String()
	if image != "" {
		flow = lispTextsAsGo(t, flow)
	}
	return normalizeFlow(flow, root, home)
}

// lispTextsAsGo puts Go's fixed texts back where the image's start contexts name the Lisp tool
// (decision 10): its lines, and the curated index's header and last line with the Lisp forms.
// Every other byte is compared with Go's. Each text is replaced as printed and as a JSON string
// (a hook's output).
func lispTextsAsGo(t *testing.T, flow string) string {
	t.Helper()
	block := func(snippet string, guidelines []string) string {
		return "- " + strings.Join(append([]string{snippet}, guidelines...), "\n- ")
	}
	forms := strings.NewReplacer(
		"om.Recall(`<name>`)", `(recall "<name>")`,
		"om.Saved(om.Search(`<words>`))", `(saved :search "<words>")`,
		"om.Save(`<name>`, `<type>`, `<description>`, om.Body(`<body>`))", `(save "<name>" "<type>" "<description>" :body "<body>")`,
		"om.EditQuoting or om.Quoting", ":quoting",
		"om.Edit", "edit", "om.ForgetMemory", "forget-memory", "om.Quoting", ":quoting",
		"om.Feedback(`<name>`, `<outcome>`, `<quote>`)", `(feedback "<name>" "<outcome>" "<quote>")`,
	)
	more := "find them with the memory tool's " + omapi.SnippetSavedSearch + ".)"
	pairs := [][2]string{
		{block(omtool.LispPromptSnippet, omtool.LispPromptGuidelines), block(omtool.PromptSnippet, omtool.PromptGuidelines)},
		{forms.Replace(curated.IndexHeader), curated.IndexHeader},
		{forms.Replace(more), more},
	}
	if !strings.Contains(flow, pairs[0][0]) {
		t.Fatal("the image's start context does not name the Lisp tool")
	}
	for _, p := range pairs {
		flow = strings.ReplaceAll(flow, p[0], p[1])
		lisp, _ := json.Marshal(p[0])
		golang, _ := json.Marshal(p[1])
		flow = strings.ReplaceAll(flow, strings.Trim(string(lisp), `"`), strings.Trim(string(golang), `"`))
	}
	return flow
}

// checkLispMCP checks the image's MCP answers (initialize, ping, tools/list, the tools/call of a
// Go pointer the summary prints): the Lisp tool's text, and the pointer run as its Lisp call
// over the session's branch, with no error.
func checkLispMCP(t *testing.T, out string) {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 4 {
		t.Fatalf("MCP answers:\n%s", out)
	}
	var answers [4]struct {
		Result map[string]any `json:"result"`
	}
	for i, l := range lines {
		if err := json.Unmarshal([]byte(l), &answers[i]); err != nil || answers[i].Result == nil {
			t.Fatalf("MCP answer %d: %s (%v)", i+1, l, err)
		}
	}
	instructions, _ := answers[0].Result["instructions"].(string)
	if !strings.HasPrefix(instructions, omtool.LispDescription+"\n\n"+omtool.LispPromptSnippet) {
		t.Fatalf("initialize: %s", lines[0])
	}
	tools, _ := answers[2].Result["tools"].([]any)
	if len(tools) != 1 || tools[0].(map[string]any)["description"] != instructions {
		t.Fatalf("tools/list: %s", lines[2])
	}
	content, _ := answers[3].Result["content"].([]any)
	if answers[3].Result["isError"] != false || len(content) != 1 {
		t.Fatalf("tools/call: %s", lines[3])
	}
	text, _ := content[0].(map[string]any)["text"].(string)
	if !strings.HasPrefix(text, ";; om.History(om.Search(`svc-42 port`)) runs here as (history :search \"svc-42 port\")\n") || !strings.Contains(text, "svc-42") {
		t.Fatalf("tools/call text:\n%s", text)
	}
}

// curatedFixture writes a curated store under root for a session in home and appends to the
// session's store a level-0 batch of two user corrections whose subjects name memories; it
// returns how many usage lines it wrote. It also caps the index's budget in the settings.
func curatedFixture(t *testing.T, root, home, sessionStore string) int {
	t.Helper()
	dir := filepath.Join(root, "observational-memory", "memories")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	project, err := filepath.EvalSymlinks(home)
	if err != nil {
		t.Fatal(err)
	}
	memory := func(name, front string) {
		text := "---\n" + front + "created: 2026-04-01T09:00:00Z\nupdated: 2026-04-01T09:00:00Z\n---\n"
		if err := os.WriteFile(filepath.Join(dir, name+".md"), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	memory("port-plan", "type: user\ndescription: Ports move in May\n")
	memory("green-tea", "type: user\ndescription: Drinks green tea\n")
	memory("make-build", "type: project\ndescription: Builds with make all\nproject: "+project+"\n")
	memory("other-project", "type: project\ndescription: Not this project\nproject: /elsewhere\n")
	memory("pinned-one", "type: feedback\ndescription: Pinned by the user\npinned: true\n")
	memory("staged-twice", "type: reference\ndescription: Staged in two sessions\nstaged: true\nstaged-by: s1, s2\n")
	memory("staged-solo", "type: lesson\ndescription: Staged once\nstaged: true\nstaged-by: s1\n")
	memory("broken", "type: lesson\ndescription: Has a field no memory takes\ncolor: blue\n")
	for i := range 12 {
		memory(fmt.Sprintf("note-%02d", i), fmt.Sprintf("type: lesson\ndescription: Lesson %d about hosts and the ports they serve\n", i))
	}
	usage := []string{
		`{"time":"2026-04-02T09:00:00Z","name":"green-tea","event":"view"}`,
		`{"time":"2026-04-02T09:00:00Z","name":"make-build","event":"helped","quote":"the build runs with make"}`,
		`{"time":"2026-04-02T09:00:00Z","name":"note-07","event":"view"}`,
		`{"time":"2026-04-02T09:00:00Z","name":"note-07","event":"view"}`,
		`{"time":"2026-04-02T09:00:00Z","name":"note-03","event":"harmed","quote":"that was wrong"}`,
	}
	if err := os.WriteFile(filepath.Join(dir, "usage.jsonl"), []byte(strings.Join(usage, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "settings.json"), []byte(`{"observational-memory":{"curatedMemoryIndexTokens":90}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(sessionStore)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	var last struct {
		Entry struct {
			ID string `json:"id"`
		} `json:"entry"`
	}
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &last); err != nil {
		t.Fatal(err)
	}
	tip := last.Entry.ID
	observation := func(id, subject string) map[string]any {
		return map[string]any{"id": id, "content": "Actually, the " + subject + " changed.", "timestamp": "2026-05-02 10:00", "relevance": "high",
			"sourceEntryIds": []string{"m45"}, "tokenCount": 6, "kind": "correction", "subject": subject, "origin": "code"}
	}
	line, _ := json.Marshal(map[string]any{"after": tip, "entry": map[string]any{
		"type": "custom", "customType": "om.observations.recorded", "id": "fixture01", "parentId": tip, "timestamp": "2026-05-02T10:00:06.000Z",
		"data": map[string]any{"observations": []any{observation("cccccccccc01", "port plan"), observation("cccccccccc02", "pinned one")}, "coversUpToId": "r45"},
	}})
	f, err := os.OpenFile(sessionStore, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(line, '\n')); err != nil {
		t.Fatal(err)
	}
	return len(usage)
}

// curatedStore prints the curated store's files in name order, with the time of each usage line
// after the fixture's first ones (written now) blanked.
func curatedStore(t *testing.T, root string, fixtureLines int) string {
	t.Helper()
	dir := filepath.Join(root, "observational-memory", "memories")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for _, e := range entries {
		if e.IsDir() || e.Name() == ".lock" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if e.Name() == "usage.jsonl" {
			lines := strings.Split(text, "\n")
			for i := fixtureLines; i < len(lines); i++ {
				lines[i] = usageTimePattern.ReplaceAllString(lines[i], `"time":"<now>"`)
			}
			text = strings.Join(lines, "\n")
		}
		fmt.Fprintf(&out, "-- %s\n%s", e.Name(), text)
	}
	return out.String()
}

// omLog is om with what it printed and what it logged.
func omLog(t *testing.T, stdin string, args ...string) (string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	if code := Run(args, strings.NewReader(stdin), &out, &errOut); code != 0 {
		t.Fatalf("om %v: exit %d: %s", args, code, errOut.String())
	}
	return out.String(), errOut.String()
}

var (
	usageTimePattern = regexp.MustCompile(`"time":"[^"]*"`)
	flowIDPattern    = regexp.MustCompile(`om[0-9a-f]{8}`)
	// flowStampPattern is an entry's append time: neutral.Host stamps entries with the time in
	// milliseconds, and the fixtures' own times are in May.
	flowStampPattern = regexp.MustCompile(`"timestamp":"2026-(0[6-9]|1[0-2])-\d\dT\d\d:\d\d:\d\d\.\d{3}Z"`)
)

// normalizeFlow names the random entry ids by order of first appearance, blanks the append
// times, replaces the run's own directories, and names the writer's engine (the one member the
// engines write differently by design, contracts/encoding.md) as one.
func normalizeFlow(s, root, home string) string {
	s = strings.ReplaceAll(s, `"engine":"lisp"`, `"engine":"<engine>"`)
	s = strings.ReplaceAll(s, `"engine":"go"`, `"engine":"<engine>"`)
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
