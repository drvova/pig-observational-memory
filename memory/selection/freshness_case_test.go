package selection_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/extract"
	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/project"
	"github.com/drvova/pig-observational-memory/memory/selection"
	"github.com/drvova/pig-observational-memory/memory/transcript"
)

var writeFreshness = flag.Bool("write-freshness", false, "rewrite contracts/project-render.jsonl")

// freshnessCasesPath holds the summaries with freshness verdicts both engines must render byte for
// byte (lisp/tests/project.lisp reads it).
var freshnessCasesPath = filepath.Join("..", "..", "contracts", "project-render.jsonl")

// freshnessCase is a generic transcript, the repository its records are framed in, the HEAD
// structure view's answer and a budget, with the summary they give.
type freshnessCase struct {
	Name    string               `json:"name"`
	Lines   []map[string]any     `json:"lines"`
	Repo    host.Repo            `json:"repo"`
	Answer  host.StructureAnswer `json:"answer"`
	Budget  int                  `json:"budget"`
	Summary string               `json:"summary"`
	Format  string               `json:"format,omitempty"`
}

func freshnessInputs() []freshnessCase {
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
	return []freshnessCase{
		{Name: "tables", Lines: lines, Repo: repo, Answer: answer, Budget: 4000},
		{Name: "lines", Lines: lines, Repo: repo, Answer: answer, Budget: 4000, Format: ledger.FormatLines},
	}
}

// renderFreshness is the Go engine's summary of c: level 0 over its transcript framed at the
// first commit, the verdicts against the answer, and the render.
func renderFreshness(t *testing.T, c freshnessCase) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.jsonl")
	var buf bytes.Buffer
	for _, l := range c.Lines {
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
		Frame: ledger.Frame{RepoID: c.Repo.ID, Head: "1111111111111111111111111111111111111111"}, RepoRoot: c.Repo.Root})
	records := result.Observations()
	verdicts := project.Verdicts(records, c.Repo, c.Answer)
	if len(verdicts) != 2 {
		t.Fatalf("%d verdicts: %+v", len(verdicts), verdicts)
	}
	return selection.Render(selection.Input{Observations: records, Folded: ledger.FoldLedger(entries, ""), Branch: entries,
		Budget: c.Budget, Format: c.Format, Freshness: project.Texts(verdicts)}).Text
}

func TestFreshnessRenderCases(t *testing.T) {
	pinClock(t)
	var buf bytes.Buffer
	for _, c := range freshnessInputs() {
		c.Summary = renderFreshness(t, c)
		for _, want := range []string{"(gone at 2222222)", "(changed since 1111111, unverified)", "- fresh: "} {
			if !strings.Contains(c.Summary, want) {
				t.Fatalf("%s: no %q in\n%s", c.Name, want, c.Summary)
			}
		}
		data, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(append(data, '\n'))
	}
	if *writeFreshness {
		if err := os.WriteFile(freshnessCasesPath, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	stored, err := os.ReadFile(freshnessCasesPath)
	if err != nil || !bytes.Equal(stored, buf.Bytes()) {
		t.Fatalf("contracts/project-render.jsonl is out of date: go test ./selection -run FreshnessRender -args -write-freshness (%v)", err)
	}
}
