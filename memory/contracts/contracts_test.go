package contracts

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drvova/pig-observational-memory/memory/internal/jsonschema"
	"github.com/drvova/pig-observational-memory/memory/ledger"
)

var (
	schemaDir = filepath.Join("..", "..", "contracts", "schemas")
	goldenDir = filepath.Join("..", "..", "contracts", "golden")
	realDir   = filepath.Join("..", "transcript", "testdata", "real")
)

// clock is the fixed time every contract run stamps its entries with.
var clock = time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)

func fixedNow() time.Time { return clock }

// fixture is one real transcript in memory/transcript/testdata/real and the reader om reads
// it with (transcript.Formats). The SQL dumps (they need python3 to load) and Goose's single
// message rows (not a transcript) are left out.
type fixture struct{ file, format string }

var fixtures = []fixture{
	{"claude-code-compaction.jsonl", "claude-code"}, {"claude-code-preserved.jsonl", "claude-code"},
	{"cline-cli.messages.json", "cline"}, {"cline-string-content.messages.json", "cline"},
	{"codex-compacted.jsonl", "codex"}, {"codex-rollout.jsonl", "codex"},
	{"copilot-cli-events.jsonl", "copilot"}, {"copilot-user-message.json", "copilot"},
	{"cortex.history.jsonl", "cortex"}, {"cursor-agent-cli.jsonl", "cursor"}, {"cursor-ide.jsonl", "cursor"},
	{"droid-compaction.jsonl", "droid"}, {"droid-fork.jsonl", "droid"},
	{"gemini-jsonl.jsonl", "gemini"}, {"gemini-legacy-session.json", "gemini"}, {"gemini-set-patch.jsonl", "gemini"},
	{"kimi-legacy-wire.jsonl", "kimi"}, {"kimi-wire.jsonl", "kimi"},
	{"kiro-v2.jsonl", "kiro"}, {"kiro-v3.messages.jsonl", "kiro"},
	{"neovate-tool.jsonl", "neovate"}, {"oh-my-pi-session.jsonl", "pi"}, {"open-interpreter-rollout.jsonl", "codex"},
	{"openclaude-preserved-segment.jsonl", "claude-code"}, {"openclaude-snip.jsonl", "claude-code"},
	{"pi-compactions.jsonl", "pi"}, {"pi-tool-calls.jsonl", "pi"}, {"qoder-cli.jsonl", "qoder"},
	{"qwen-chat-records.jsonl", "qwen"}, {"qwen-chat.jsonl", "qwen"},
}

// name is the fixture's golden directory: its file name without the last extension.
func (f fixture) name() string {
	base := filepath.Base(f.file)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// path is the fixture's transcript: f.file under the real transcripts, or f.file itself when it
// is absolute (a transcript a test generated).
func (f fixture) path(t *testing.T) string {
	t.Helper()
	if filepath.IsAbs(f.file) {
		return f.file
	}
	p, err := filepath.Abs(filepath.Join(realDir, f.file))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// Every real transcript fixture is in the list (or deliberately left out), so a new one gets a
// golden directory.
func TestFixtureListComplete(t *testing.T) {
	listed := map[string]bool{}
	for _, f := range fixtures {
		listed[f.file] = true
	}
	files, err := os.ReadDir(realDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range files {
		n := e.Name()
		if listed[n] || n == "README.md" || strings.HasSuffix(n, ".sql") || strings.HasPrefix(n, "goose-message-") {
			continue
		}
		t.Errorf("%s is not in the contract fixtures", n)
	}
}

func loadSchemas(t *testing.T) *jsonschema.Set {
	t.Helper()
	s, err := jsonschema.Load(schemaDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Names()) == 0 {
		t.Fatalf("no schemas under %s", schemaDir)
	}
	return s
}

// entrySchemas are the schemas of the session entries the engine writes, by custom type.
var entrySchemas = map[string]string{
	ledger.OMObservationsRecorded: "entry-observations-recorded.schema.json",
	ledger.OMReflectionsRecorded:  "entry-reflections-recorded.schema.json",
	ledger.OMObservationsDropped:  "entry-observations-dropped.schema.json",
	ledger.OMSubjectsAliased:      "entry-subjects-aliased.schema.json",
	ledger.OMExtracted:            "entry-extracted.schema.json",
	ledger.OMSleep:                "entry-sleep.schema.json",
	"om.cut":                      "entry-cut.schema.json",
	"om.summary.handed":           "entry-summary-handed.schema.json",
	"om.erased":                   "entry-erased.schema.json",
	"om.cue":                      "entry-cue.schema.json",
	ledger.OMRevisionsRecorded:    "entry-revisions-recorded.schema.json",
	ledger.OMHypothesesRecorded:   "entry-hypotheses-recorded.schema.json",
}

// entrySchema is the schema of a decoded session entry the engine wrote; false for an entry
// that is not the engine's (a message, a host's own compaction, another extension's entry, the
// TypeScript extension's om.observation, which the ledger ignores).
func entrySchema(m map[string]any) (string, bool) {
	switch m["type"] {
	case "compaction":
		if d, _ := m["details"].(map[string]any); d != nil && d["type"] == ledger.OMFolded {
			return "entry-compaction.schema.json", true
		}
	case "custom":
		ct, _ := m["customType"].(string)
		s, ok := entrySchemas[ct]
		return s, ok
	}
	return "", false
}

// check validates v against schema and reports every violation under where.
func check(t *testing.T, set *jsonschema.Set, schema, where string, v any) {
	t.Helper()
	for _, err := range set.Validate(schema, v) {
		t.Errorf("%s (%s): %v", where, schema, err)
	}
}

// jsonValue is v as decoded JSON.
func jsonValue(t *testing.T, v any) any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// storedFile says how a file the engine writes is checked: its schema, and whether it is
// one JSON value, one per line, or a curated memory's front matter.
type storedFile struct {
	schema string
	mode   string // "json", "lines", "frontmatter" or "skip"
}

// classify maps a path under the engine's root (slash-separated) to its schema. Lock and
// temporary files, the rows-codec definition files and the debug log are not JSON contracts.
func classify(rel string) (storedFile, bool) {
	base := filepath.Base(rel)
	parts := strings.Split(rel, "/")
	memories := strings.Index(rel, "observational-memory/memories/")
	switch {
	case strings.HasSuffix(base, ".lock") || strings.HasSuffix(base, ".tmp") || base == ".lock":
		return storedFile{mode: "skip"}, true
	case strings.HasSuffix(base, ".rows") || strings.HasSuffix(base, ".ndjson"):
		return storedFile{mode: "skip"}, true
	case len(parts) == 3 && parts[0] == "sessions" && strings.HasSuffix(base, ".jsonl"):
		return storedFile{"neutral-store-line.schema.json", "lines"}, true
	case len(parts) == 2 && parts[0] == "sleep" && base == "state.json":
		return storedFile{"sleep-state.schema.json", "json"}, true
	case len(parts) == 3 && parts[0] == "current" && strings.HasSuffix(base, ".json"):
		return storedFile{"current-session.schema.json", "json"}, true
	case len(parts) == 3 && parts[0] == "transcripts" && strings.HasSuffix(base, ".jsonl"):
		return storedFile{"generic-transcript-line.schema.json", "lines"}, true
	case len(parts) >= 2 && parts[len(parts)-2] == "repos" && strings.HasSuffix(base, ".json") &&
		(len(parts) == 2 || parts[len(parts)-3] == "observational-memory"):
		return storedFile{"repo-id.schema.json", "json"}, true
	case memories >= 0:
		inner := strings.Split(rel[memories+len("observational-memory/memories/"):], "/")
		switch {
		case len(inner) == 1 && strings.HasSuffix(base, ".md"):
			return storedFile{"curated-memory.schema.json", "frontmatter"}, true
		case len(inner) == 1 && base == "usage.jsonl":
			return storedFile{"curated-usage-line.schema.json", "lines"}, true
		case len(inner) == 2 && inner[0] == "core" && base == "log.jsonl":
			return storedFile{"core-line.schema.json", "lines"}, true
		case len(inner) == 2 && inner[0] == "core" && base == "consumed.jsonl":
			return storedFile{"core-consumed.schema.json", "lines"}, true
		case len(inner) == 2 && inner[0] == "models" && strings.HasSuffix(base, ".jsonl"):
			return storedFile{"model-event.schema.json", "lines"}, true
		case len(inner) == 3 && inner[0] == "harness" && base == "manifest.jsonl":
			return storedFile{"layer-generation.schema.json", "lines"}, true
		case len(inner) == 4 && inner[0] == "harness" && inner[2] == "held" && base == "index.json":
			return storedFile{"layer-held-index.schema.json", "json"}, true
		case len(inner) == 2 && inner[0] == "harness" && base == "floor.jsonl":
			return storedFile{"floor-lowering.schema.json", "lines"}, true
		case len(inner) == 3 && inner[0] == "code" && base == "units.json":
			return storedFile{"code-units.schema.json", "json"}, true
		}
	}
	return storedFile{}, false
}

// frontMatter reads a curated memory file's front matter as the store's parser does: one
// key: value per line, split at the first colon.
func frontMatter(data []byte) (map[string]any, error) {
	rest, ok := strings.CutPrefix(string(data), "---\n")
	if !ok {
		return nil, fmt.Errorf("no front matter")
	}
	header, _, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		if header, ok = strings.CutSuffix(rest, "\n---"); !ok {
			return nil, fmt.Errorf("unterminated front matter")
		}
	}
	out := map[string]any{}
	for _, line := range strings.Split(header, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out, nil
}

// validateTree checks every file under root against its schema and returns how many files of
// each schema it checked. A file no schema covers fails the test, so a new stored file gets a
// schema.
func validateTree(t *testing.T, set *jsonschema.Set, root string) map[string]int {
	t.Helper()
	seen := map[string]int{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		f, ok := classify(rel)
		if !ok {
			t.Errorf("%s: no schema covers this stored file", rel)
			return nil
		}
		if f.mode == "skip" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		seen[f.schema]++
		switch f.mode {
		case "json":
			for _, e := range set.ValidateJSON(f.schema, data) {
				t.Errorf("%s (%s): %v", rel, f.schema, e)
			}
		case "frontmatter":
			fm, err := frontMatter(data)
			if err != nil {
				t.Errorf("%s: %v", rel, err)
				return nil
			}
			check(t, set, f.schema, rel, fm)
		case "lines":
			scanner := bufio.NewScanner(bytes.NewReader(data))
			scanner.Buffer(make([]byte, 0, 64*1024), 64*1024*1024)
			for n := 1; scanner.Scan(); n++ {
				where := fmt.Sprintf("%s:%d", rel, n)
				var v any
				if err := json.Unmarshal(scanner.Bytes(), &v); err != nil {
					t.Errorf("%s: %v", where, err)
					continue
				}
				check(t, set, f.schema, where, v)
				if f.schema != "neutral-store-line.schema.json" {
					continue
				}
				entry, _ := v.(map[string]any)["entry"].(map[string]any)
				schema, ok := entrySchema(entry)
				if !ok {
					t.Errorf("%s: the store holds an entry with no schema: %v %v", where, entry["type"], entry["customType"])
					continue
				}
				seen[schema]++
				check(t, set, schema, where, entry)
			}
			if err := scanner.Err(); err != nil {
				t.Errorf("%s: %v", rel, err)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return seen
}
