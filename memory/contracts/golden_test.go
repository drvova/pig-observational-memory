package contracts

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/hooks"
	"github.com/drvova/pig-observational-memory/memory/hostinfo"
	"github.com/drvova/pig-observational-memory/memory/neutral"
	"github.com/drvova/pig-observational-memory/memory/runtime"
	"github.com/drvova/pig-observational-memory/memory/transcript"
)

var write = flag.Bool("write", false, "rewrite the golden corpus under contracts/golden")

// The golden files of one fixture (contracts/README.md, "Golden corpus").
const (
	recordsFile    = "records.jsonl"
	compactionFile = "compaction.jsonl"
	summaryFile    = "summary.md"
)

// golden is one fixture's run: the store lines level 0 wrote at a turn end, the lines the
// compaction after it wrote, and the compaction's summary, with the store's entry ids
// normalized (normalizeIDs).
type golden struct {
	records, compaction, summary string
}

// runGolden runs the engine over a fixture as one hook process does for each event, in one
// process: level 0 at a turn end, then the harness compacts (neutral.Host.Compaction, the cut
// marker at the tip). The clock is fixed; the root is fresh, so no curated memory or layer is
// read.
func runGolden(t *testing.T, f fixture) golden {
	t.Helper()
	root := t.TempDir()
	old := hostinfo.AgentDir
	hostinfo.AgentDir = func() (string, error) { return root, nil }
	defer func() { hostinfo.AgentDir = old }()
	format, ok := transcript.Formats[f.format]
	if !ok {
		t.Fatalf("%s: no reader %q", f.file, f.format)
	}
	h := &neutral.Host{Session: f.name(), Dir: "/work", Root: root, Transcript: transcript.File{Path: f.path(t), Format: format}, Store: neutral.StoreFor(root, "golden", f.name()), Now: fixedNow, HarnessName: "golden"}
	rt := runtime.New()
	hooks.OnTurnEnd(rt, h)
	turn, err := os.ReadFile(h.Store.Path)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	c, err := h.Compaction(context.Background(), rt, "manual")
	if err != nil {
		t.Fatalf("%s: %v", f.file, err)
	}
	all, err := os.ReadFile(h.Store.Path)
	if err != nil || !bytes.HasPrefix(all, turn) {
		t.Fatalf("%s: the store was rewritten, not appended to: %v", f.file, err)
	}
	g := golden{records: string(turn), compaction: string(all[len(turn):]), summary: c.Summary + "\n"}
	return normalizeIDs(t, g, all)
}

// storeID is the neutral store's entry id ("om" and 8 random hex digits, neutral/host.go newID).
var storeID = regexp.MustCompile(`^om[0-9a-f]{8}$`)

// normalizeIDs replaces each entry id the neutral store minted with "om-" and its ordinal in
// the store (om-1 for the first line's entry), wherever it occurs, so the corpus does not
// depend on the random ids. No other value is random: record ids are hashes of their content,
// transcript ids are the harness's, and timestamps come from the transcript or the fixed clock.
func normalizeIDs(t *testing.T, g golden, store []byte) golden {
	t.Helper()
	var pairs []string
	for n, line := range bytes.Split(bytes.TrimSpace(store), []byte("\n")) {
		var st neutral.Stored
		if err := json.Unmarshal(line, &st); err != nil {
			t.Fatal(err)
		}
		id, _ := st.Entry["id"].(string)
		if !storeID.MatchString(id) {
			t.Fatalf("store line %d: id %q is not a store id", n+1, id)
		}
		pairs = append(pairs, `"`+id+`"`, fmt.Sprintf(`"om-%d"`, n+1), id, fmt.Sprintf("om-%d", n+1))
	}
	r := strings.NewReplacer(pairs...)
	return golden{records: r.Replace(g.records), compaction: r.Replace(g.compaction), summary: r.Replace(g.summary)}
}

// Each fixture's level-0 records, its compaction entries and its rendered summary match the
// golden corpus byte for byte (after id normalization), and a second run gives the same bytes.
// With -write, the corpus is rewritten instead.
func TestGolden(t *testing.T) {
	for _, f := range fixtures {
		t.Run(f.name(), func(t *testing.T) {
			g := runGolden(t, f)
			if again := runGolden(t, f); again != g {
				t.Fatalf("two runs differ")
			}
			dir := filepath.Join(goldenDir, f.name())
			files := map[string]string{recordsFile: g.records, compactionFile: g.compaction, summaryFile: g.summary}
			if *write {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				for name, content := range files {
					if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				return
			}
			for name, content := range files {
				want, err := os.ReadFile(filepath.Join(dir, name))
				if err != nil {
					t.Fatalf("%v (go test ./contracts -args -write writes the corpus)", err)
				}
				if string(want) != content {
					t.Errorf("%s/%s differs from the engine's output:\n%s", f.name(), name, firstDiff(string(want), content))
				}
			}
		})
	}
	if *write {
		return
	}
	// No golden directory is left from a fixture that is gone.
	dirs, err := os.ReadDir(goldenDir)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range fixtures {
		names[f.name()] = true
	}
	for _, d := range dirs {
		if d.IsDir() && !names[d.Name()] {
			t.Errorf("contracts/golden/%s has no fixture", d.Name())
		}
	}
}

// firstDiff shows the first line where want and got differ.
func firstDiff(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(w) || i < len(g); i++ {
		var a, b string
		if i < len(w) {
			a = w[i]
		}
		if i < len(g) {
			b = g[i]
		}
		if a != b {
			return fmt.Sprintf("line %d:\n- %.300s\n+ %.300s", i+1, a, b)
		}
	}
	return "(same lines)"
}
