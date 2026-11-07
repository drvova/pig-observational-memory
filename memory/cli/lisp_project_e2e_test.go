package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitPath is git, looked up before a test clears PATH.
var gitPath, _ = exec.LookPath("git")

// projectFlow is lispFlow's project-knowledge part (docs/brain-memory.md, decision 20): a
// generic session in a real git repository under home whose records name two files; level 0
// frames them at the first commit, a second commit changes one and deletes the other, and the
// compaction's summary prints both freshness verdicts. The memory tool then prints the notes
// with their verdicts and pages HEAD's files, on the image with the Lisp tool and on the Go
// engine with the Go tool, printing the same bytes. The image never runs git: om answers its
// host/structure calls (cli.withView). Commits are dated and authored alike, so both flows read
// the same commits.
func projectFlow(t *testing.T, home string, lisp bool, section func(name, text string), store func() string) {
	t.Helper()
	if gitPath == "" {
		t.Fatal("git is not on PATH: the project flow needs a real repository")
	}
	// env clears PATH; om's side of the HEAD view (memory/gitrepo) runs git.
	t.Setenv("PATH", filepath.Dir(gitPath))
	repo := filepath.Join(home, "repo")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command(gitPath, append([]string{"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=om", "GIT_AUTHOR_EMAIL=om@example.org", "GIT_COMMITTER_NAME=om", "GIT_COMMITTER_EMAIL=om@example.org",
			"GIT_AUTHOR_DATE=2026-11-01T10:00:00Z", "GIT_COMMITTER_DATE=2026-11-01T10:00:00Z")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(name, text string) {
		t.Helper()
		p := filepath.Join(repo, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(repo, 0o700); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "-b", "main")
	write("config/load.go", "package config\n\nfunc parseConfig() {}\n")
	write("src/net/retry.go", "package net\n\nfunc retry() {}\n")
	write("src/main.go", "package main\n")
	write("README.md", "repo\n")
	git("add", "-A")
	git("commit", "-q", "-m", "first")

	tr := filepath.Join(home, "r.jsonl")
	// The assistant's long replies give the compaction a removed source large enough for a
	// summary budget that shows both records.
	pad := strings.Repeat("Reading through the loader and the client to see how they fit together. ", 60)
	lines := []string{
		`{"id":"u1","role":"user","text":"` + "`parseConfig` in `config/load.go` reads the YAML." + `","timestamp":"2026-11-01T10:00:00Z"}`,
		`{"id":"a1","role":"assistant","text":"Noted. ` + pad + `","timestamp":"2026-11-01T10:01:00Z"}`,
		`{"id":"u2","role":"user","text":"We decided to keep the retries in ` + "`src/net/retry.go`" + ` because the client must stay small.","timestamp":"2026-11-01T10:02:00Z"}`,
		`{"id":"a2","role":"assistant","text":"Understood. ` + pad + `","timestamp":"2026-11-01T10:03:00Z"}`,
	}
	if err := os.WriteFile(tr, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p, _ := json.Marshal(map[string]any{"harness": "mine", "session_id": "r1", "transcript_path": tr, "cwd": repo})
	om(t, string(p), "hook", "generic", "turn-end")
	section("project store after level 0", store())

	write("src/net/retry.go", "package net\n\nfunc retry() { backoff() }\n")
	if err := os.Remove(filepath.Join(repo, "config", "load.go")); err != nil {
		t.Fatal(err)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "second")

	summary := om(t, string(p), "hook", "generic", "compact")
	for _, want := range []string{"(changed since ", ", unverified)", "(gone at "} {
		if !strings.Contains(summary, want) {
			t.Fatalf("lisp %v: no %q in the summary:\n%s", lisp, want, summary)
		}
	}
	section("project compact", summary)

	// The tool's reads: the notes with their verdicts, then HEAD's files under src/ a page of one
	// at a time, the next page through the Go pointer the page prints.
	code := "for _, n := range om.Notes().Rows {\n\tfmt.Println(n.Line())\n}\nv, _ := om.Head(\"src/\", om.Limit(1))\nfmt.Print(v)\n"
	if lisp {
		code = `(dolist (n (notes)) (show n)) (show (head "src/" :limit 1))`
	}
	tool := func(code string) string {
		t.Helper()
		in, _ := json.Marshal(map[string]any{"code": code})
		var res struct {
			Text    string `json:"text"`
			IsError bool   `json:"isError"`
		}
		if err := json.Unmarshal([]byte(om(t, string(in), "call", "--harness", "mine", "--dir", repo)), &res); err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Fatalf("lisp %v: %s: %s", lisp, code, res.Text)
		}
		return res.Text
	}
	reads := tool(code)
	if !strings.Contains(reads, ", unverified) u2\n") || !strings.Contains(reads, "next: om.Head(\"src/\", om.Offset(1), om.Limit(1))") {
		t.Fatalf("lisp %v: the tool's reads:\n%s", lisp, reads)
	}
	section("project tool reads", reads)
	next := tool(`om.Head("src/", om.Offset(1), om.Limit(1))`)
	if lisp {
		// The image names the Lisp call the Go pointer runs as on its first line.
		first, rest, _ := strings.Cut(next, "\n")
		if first != `;; om.Head("src/", om.Offset(1), om.Limit(1)) runs here as (head "src/" :offset 1 :limit 1)` {
			t.Fatalf("the pointer's line: %q", first)
		}
		next = rest
	}
	section("project next page", strings.TrimRight(next, "\n"))
}
