// Package snippettest runs the om snippets the extension prints (summaries, the level-1 prompt,
// thinning stubs) as memory tool code in a real namespace child, for tests (docs/code-tool.md,
// "The summary's snippets and their test"): the test binary re-executed, since it links
// internal/sandbox, whose init catches the child marker. It is imported only by tests.
package snippettest

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/codepolicy"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omhost"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/omtool"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/sandbox"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/session"
)

var (
	once sync.Once
	sup  *sandbox.Supervisor
)

// supervisor is the test binary's one namespace child; it holds no kept unit.
func supervisor() *sandbox.Supervisor {
	once.Do(func() {
		sup = sandbox.New(sandbox.Config{Mode: sandbox.ModeNamespace, Methods: sandbox.NamespaceMethods()})
	})
	return sup
}

// Result is what a list snippet returned: its ids, how many rows matched and were shown, and the
// list as text.
type Result struct {
	IDs              []string
	Matched, Showing int
	Text             string
}

// Cut reports whether the list was cut by its limit.
func (r Result) Cut() bool { return r.Showing < r.Matched }

// Run runs snippet, a read returning a list, as code over entries (the branch), and returns
// what it listed. The snippet must pass the code policy and run without error.
func Run(t testing.TB, entries []session.Entry, snippet string) Result {
	t.Helper()
	return RunWith(t, &omhost.Call{Entries: entries, Skip: []string{omtool.ToolName}}, snippet)
}

// RunWith is Run answered from the call context c (a curated store for om.Saved, say).
func RunWith(t testing.TB, c *omhost.Call, snippet string) Result {
	t.Helper()
	out := run(t, c, "l := "+snippet+"\nfmt.Println(l.Matched, l.Showing)\nfor _, id := range l.IDs() { fmt.Println(id) }\nfmt.Print(\"---\\n\", l.String())")
	head, text, _ := strings.Cut(out, "---\n")
	lines := strings.Split(strings.TrimRight(head, "\n"), "\n")
	var r Result
	if counts := strings.Fields(lines[0]); len(counts) == 2 {
		r.Matched, _ = strconv.Atoi(counts[0])
		r.Showing, _ = strconv.Atoi(counts[1])
	}
	r.IDs = append([]string{}, lines[1:]...)
	r.Text = text
	return r
}

// Recall runs snippet, an om.Recall, as code over entries and returns the text it read.
func Recall(t testing.TB, entries []session.Entry, snippet string) string {
	t.Helper()
	return run(t, &omhost.Call{Entries: entries, Skip: []string{omtool.ToolName}}, "r, err := "+snippet+"\nif err != nil { panic(err) }\nfmt.Print(r.String())")
}

// Try runs code over the call context c and returns everything the result holds (printed
// output, last value and error), whatever its outcome.
func Try(t testing.TB, c *omhost.Call, code string) string {
	t.Helper()
	p, err := codepolicy.Check(codepolicy.Input{Code: code})
	if err != nil {
		t.Fatalf("policy: %v\n%s", err, code)
	}
	res := supervisor().Run(context.Background(), sandbox.Request{Program: p}, c)
	return res.Out + res.Val + res.Err
}

func run(t testing.TB, c *omhost.Call, code string) string {
	t.Helper()
	p, err := codepolicy.Check(codepolicy.Input{Code: code})
	if err != nil {
		t.Fatalf("policy: %v\n%s", err, code)
	}
	res := supervisor().Run(context.Background(), sandbox.Request{Program: p}, c)
	if res.Outcome == sandbox.OutcomeCrashed && strings.Contains(res.Err, "signal: killed") {
		// The child is the out-of-memory killer's first pick (oom_score_adj 1000), so a busy
		// test machine may kill it between calls; the next call starts a fresh one.
		res = supervisor().Run(context.Background(), sandbox.Request{Program: p}, c)
	}
	if res.Outcome != sandbox.OutcomeOK {
		t.Fatalf("%s:\n%s %s", code, res.Outcome, res.Err)
	}
	return res.Out
}
