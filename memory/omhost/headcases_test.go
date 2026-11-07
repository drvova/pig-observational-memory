package omhost_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/extract"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/om"
	"github.com/drvova/pig-observational-memory/memory/om/omwire"
	"github.com/drvova/pig-observational-memory/memory/omhost"
	"github.com/drvova/pig-observational-memory/memory/session"
	tf "github.com/drvova/pig-observational-memory/memory/testfix"
	"github.com/drvova/pig-observational-memory/memory/transcript"
)

var writeHead = flag.Bool("write", false, "rewrite contracts/project-tool.jsonl")

// headCasesPath holds the memory tool's HEAD reads both engines must answer byte for byte: the
// notes with their freshness verdicts (om.Note.Line) and om.Head's pages (om.HeadView.String).
// lisp/tests/project.lisp reads it.
var headCasesPath = filepath.Join("..", "..", "contracts", "project-tool.jsonl")

// headCase is a branch (a generic transcript and the records level 0 wrote for it), the
// repository and a fake HEAD view (the answer to every non-list query, and the files a list
// pages), with what the reads print.
type headCase struct {
	Name    string               `json:"name"`
	Entries []map[string]any     `json:"entries"`
	Repo    host.Repo            `json:"repo"`
	Answer  host.StructureAnswer `json:"answer"`
	Files   []string             `json:"files"`
	// Truncated marks a tree larger than the host lists.
	Truncated bool `json:"truncated,omitempty"`
	// NoView makes the call one with no structure view (om.Head's error, no verdicts).
	NoView bool       `json:"noView,omitempty"`
	Notes  []string   `json:"notes"`
	Search []string   `json:"search,omitempty"`
	Found  []string   `json:"found"`
	Heads  []headRead `json:"heads"`
}

// headRead is one om.Head call and what it printed, or its error.
type headRead struct {
	Prefix string `json:"prefix"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
	Text   string `json:"text,omitempty"`
	Error  string `json:"error,omitempty"`
}

// fakeView answers a list query from Files (the paths under the prefix, in order, paged) and
// every other query with Answer, as lisp/tests/project.lisp's fake does.
type fakeView struct{ c headCase }

func (v fakeView) Structure(q host.StructureQuery) (host.StructureAnswer, bool, error) {
	if q.List == nil {
		return v.c.Answer, true, nil
	}
	var under []string
	for _, f := range v.c.Files {
		if strings.HasPrefix(f, q.List.Prefix) {
			under = append(under, f)
		}
	}
	a := host.StructureAnswer{RepoID: v.c.Answer.RepoID, Head: v.c.Answer.Head, Total: len(under), Truncated: v.c.Truncated}
	if q.List.Offset < len(under) {
		a.Files = under[q.List.Offset:min(len(under), q.List.Offset+q.List.Limit)]
	}
	return a, true, nil
}

func headInputs(t *testing.T) []headCase {
	const (
		head1 = "1111111111111111111111111111111111111111"
		head2 = "2222222222222222222222222222222222222222"
	)
	lines := []map[string]any{
		{"id": "u1", "timestamp": "2026-11-01T10:00:00Z", "role": "user", "text": "`parseConfig` in `config/load.go` reads the YAML."},
		{"id": "a1", "timestamp": "2026-11-01T10:01:00Z", "role": "assistant", "text": "Noted."},
		{"id": "u2", "timestamp": "2026-11-01T10:02:00Z", "role": "user", "text": "We decided to keep the retries in `src/net/retry.go` because the client must stay small."},
		{"id": "a2", "timestamp": "2026-11-01T10:03:00Z", "role": "assistant", "text": "Understood."},
		{"id": "u3", "timestamp": "2026-11-01T10:04:00Z", "role": "user", "text": "Always run `make test` before a push."},
	}
	repo := host.Repo{ID: "r0123456789a", Root: "/w", Head: head2}
	answer := host.StructureAnswer{RepoID: repo.ID, Head: head2,
		Paths:   []host.PathAnswer{{Path: "config/load.go", Known: true}, {Path: "src/net/retry.go", Known: true, Exists: true, Blob: "bbbb"}},
		Symbols: []host.SymbolAnswer{{Symbol: "parseConfig", Known: true}},
		Blobs:   []host.BlobAnswer{{Path: "config/load.go", At: head1, Known: true, Blob: "aaaa"}, {Path: "src/net/retry.go", At: head1, Known: true, Blob: "aaaa"}}}
	files := []string{"Makefile", "README.md", "a \"quoted\"/x.go", "a \"quoted\"/y.go", "src/main.go", "src/net/retry.go", "src/net/retry_test.go", "src/util.go"}
	entries := branchOf(t, lines, repo)
	heads := []headRead{{Prefix: ""}, {Prefix: "src/", Limit: 2}, {Prefix: "src/", Limit: 2, Offset: 2}, {Prefix: "src/", Offset: 3, Limit: 900},
		{Prefix: "src/", Offset: 9}, {Prefix: "nope/"}, {Prefix: "a \"quoted\"/", Limit: 1}, {Prefix: "", Limit: -1}}
	return []headCase{
		{Name: "verdicts and pages", Entries: entries, Repo: repo, Answer: answer, Files: files, Search: []string{"retries"}, Heads: heads},
		{Name: "a truncated tree", Entries: entries, Repo: repo, Answer: answer, Files: files, Truncated: true, Heads: heads[:3]},
		{Name: "no view", Entries: entries, Repo: repo, NoView: true, Search: []string{"retries"}, Heads: heads[:1]},
	}
}

// branchOf is the generic transcript's entries and, after them, the records level 0 takes from
// them framed at the first commit, as the hooks append them, as maps.
func branchOf(t *testing.T, lines []map[string]any, repo host.Repo) []map[string]any {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.jsonl")
	var buf bytes.Buffer
	for _, l := range lines {
		data, _ := json.Marshal(l)
		buf.Write(append(data, '\n'))
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	entries, err := transcript.File{Path: path, Format: transcript.Formats["generic"]}.Entries()
	if err != nil {
		t.Fatal(err)
	}
	result := extract.Extract(extract.Input{Entries: entries, Start: 0, End: len(entries), Ledger: ledger.FoldLedger(entries, ""),
		Frame: ledger.Frame{RepoID: repo.ID, Head: "1111111111111111111111111111111111111111"}, RepoRoot: repo.Root})
	for i, b := range result.Batches {
		if len(b.Observations) > 0 {
			entries = append(entries, tf.ObservationsRecordedEntry("rec"+string(rune('a'+i)), b.Observations, b.CoversUpToID))
		}
	}
	out := make([]map[string]any, len(entries))
	for i, e := range entries {
		out[i] = tf.ToJSONValue(e.ToMap()).(map[string]any)
	}
	return out
}

// runHeadCase is what the Go tool's reads print for c.
func runHeadCase(t *testing.T, c headCase) headCase {
	t.Helper()
	entries := make([]session.Entry, len(c.Entries))
	for i, m := range c.Entries {
		entries[i] = session.FromMap(m)
	}
	call := &omhost.Call{Entries: entries}
	if !c.NoView {
		repo := c.Repo
		call.Structure, call.Repo = fakeView{c}, func() (host.Repo, bool) { return repo, true }
	}
	lines := func(l om.NoteList) []string {
		out := []string{}
		for _, n := range l.Rows {
			out = append(out, n.Line())
			for _, w := range n.With {
				out = append(out, "  "+w.Line())
			}
		}
		return out
	}
	c.Notes = lines(must[om.NoteList](t, call, omwire.MNotes, omwire.Options{}))
	c.Found = []string{}
	if len(c.Search) > 0 {
		c.Found = lines(must[om.NoteList](t, call, omwire.MNotes, omwire.Options{Search: c.Search}))
	}
	for i, h := range c.Heads {
		o := omwire.Options{}
		if h.Limit != 0 {
			o.Limit = limit(h.Limit)
		}
		if h.Offset != 0 {
			o.Offset = limit(h.Offset)
		}
		raw, err := call.Call(t.Context(), omwire.MHead, mustJSON(t, []any{h.Prefix, o}))
		if err != nil {
			c.Heads[i].Text, c.Heads[i].Error = "", err.Error()
			continue
		}
		var v om.HeadView
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		c.Heads[i].Text = v.String()
	}
	return c
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestHeadToolCases(t *testing.T) {
	var buf bytes.Buffer
	for _, c := range headInputs(t) {
		c = runHeadCase(t, c)
		all := strings.Join(append(append([]string{}, c.Notes...), c.Found...), "\n")
		if !c.NoView {
			for _, want := range []string{"(gone at 2222222)", "(changed since 1111111, unverified)"} {
				if !strings.Contains(all, want) {
					t.Fatalf("%s: no %q in\n%s", c.Name, want, all)
				}
			}
			if !slices.ContainsFunc(c.Heads, func(h headRead) bool { return strings.Contains(h.Text, "next: om.Head(") }) {
				t.Fatalf("%s: no next page in %+v", c.Name, c.Heads)
			}
		} else if strings.Contains(all, "unverified") || c.Heads[0].Error == "" {
			t.Fatalf("%s: verdicts or a page with no view: %s %+v", c.Name, all, c.Heads)
		}
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(append(data, '\n'))
	}
	if *writeHead {
		if err := os.WriteFile(headCasesPath, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	stored, err := os.ReadFile(headCasesPath)
	if err != nil || !bytes.Equal(stored, buf.Bytes()) {
		t.Fatalf("contracts/project-tool.jsonl is out of date: go test ./omhost -run HeadToolCases -args -write (%v)", err)
	}
}
