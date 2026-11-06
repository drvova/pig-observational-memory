package extract

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
	"github.com/drvova/pig-observational-memory/memory/session"
	tf "github.com/drvova/pig-observational-memory/memory/testfix"
)

var writeEncoding = flag.Bool("write", false, "rewrite contracts/encoding.jsonl")

// encodingCasesPath holds the encoding cases with Go's records, which the engine image must
// equal byte for byte (lisp/tests/encoding.lisp).
var encodingCasesPath = filepath.Join("..", "..", "contracts", "encoding.jsonl")

// encodingCase is one branch and what level 0 must record from it: the kinds of its records in
// order ("kind" or "kind/status"), "" for none.
type encodingCase struct {
	name    string
	entries []session.Entry
	want    []string
}

func says(text string) []session.Entry { return []session.Entry{user("u1", text)} }

// reads is a user request, an assistant read of path and its result text.
func reads(path, text string, failed bool) []session.Entry {
	return []session.Entry{
		user("u1", "Look at the repository."),
		assistant("a1", call{"c1", "read", map[string]any{"path": path}}),
		result("r1", "c1", "read", text, failed),
	}
}

const workflow = `name: ci
on: [push]
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: go vet ./...
      - name: test
        run: |
          go test -race ./...
          # a comment
          make lint
      - run: "go vet ./..."
`

const makefile = `GO ?= go
BIN := bin/app
.PHONY: build test lint
all: build test

build: $(BIN)
	$(GO) build -o $(BIN) ./cmd/app

test lint:
	$(GO) test ./...
bin/app: main.go
%.o: %.c
`

func encodingCases() []encodingCase {
	return []encodingCase{
		{"scope don't touch", says("Don't touch `migrations/`."), []string{"preference"}},
		{"scope pronoun", says("Don't touch it."), nil},
		{"scope leave alone", says("Leave the vendor directory alone while you refactor."), []string{"preference"}},
		{"scope only modify", says("Only modify files under `internal/api/`."), []string{"preference"}},
		{"scope stay out", says("Stay out of deploy/ for now."), []string{"preference"}},
		{"never edit", says("Never edit `go.sum` by hand."), []string{"preference"}},
		{"always bump", says("Always bump the chart version when you change `values.yaml`."), []string{"preference"}},
		{"substitution instead", says("Use `pnpm` instead of `npm`."), []string{"preference"}},
		{"substitution not", says("Use tabs, not spaces."), []string{"preference"}},
		{"substitution naming a specific is a correction", says("Use ruff, not flake8."), []string{"correction"}},
		{"prefer over", says("Prefer table tests over subtests."), []string{"preference"}},
		{"from now on", says("From now on, run `make lint` after every change."), []string{"preference"}},
		{"before commit", says("Before you commit, run `make test`."), []string{"preference"}},
		{"make sure before", says("Make sure to rebase on `main` before you push."), []string{"preference"}},
		{"after each change", says("After each change run the linter."), []string{"preference"}},
		{"team always", says("We always rebase instead of merging."), []string{"preference"}},
		{"team convention", says("Our convention is snake_case for SQL columns."), []string{"preference"}},
		{"conditional addressed to the agent", says("If you change the schema, run `make gen` before you push."), []string{"preference"}},
		{"question", says("Should I edit `config.yaml`?"), nil},
		{"what if", says("What if we never touched `db/`?"), nil},
		{"hypothetical", says("If we never touch `db/` the migration breaks."), nil},
		{"rule not withdrawn by a retraction", says("Don't touch `migrations/`. Actually, scrap that."), []string{"preference"}},
		{"decision with reason", says("We decided to use sqlc over gorm because the queries need compile-time checks."), []string{"decision"}},
		{"decision let's", says("Let's go with pgx because it avoids reflection on the hot path."), []string{"decision/proposed"}},
		{"decision rejected", says("We rejected gorm because of the reflection cost."), []string{"decision"}},
		{"decision not made", says("We haven't decided on the logger yet."), nil},
		{"decision question", says("Should we go with chi?"), nil},
		{"decision withdrawn", says("Let's go with zerolog because it is fast. Never mind."), []string{"decision/proposed", "correction/retracted"}},
		{"decision corrected", []session.Entry{
			user("u1", "We decided to use Redis for the cache."),
			user("u2", "Actually, we use Memcached, not Redis."),
		}, []string{"decision", "correction"}},
		{"assistant decision", []session.Entry{
			user("u1", "Which logger should the service use?"),
			assistant("a1", "I'll use zerolog because its allocations stay flat under load."),
		}, []string{"assistant/proposed"}},
		{"assistant restating the user's decision", []session.Entry{
			user("u1", "We decided to use sqlc because the queries need compile-time checks."),
			assistant("a1", "We'll use sqlc since the queries need compile-time checks."),
		}, []string{"decision"}},
		{"assistant rule is no rule", []session.Entry{
			user("u1", "Set up the repository."),
			assistant("a1", "Don't touch `migrations/` while the release is out."),
		}, nil},
		{"lockfile", reads("package-lock.json", "{\n  \"lockfileVersion\": 3\n}", false), []string{"convention", "file"}},
		{"lockfile read failed", reads("pnpm-lock.yaml", "ENOENT: no such file", true), []string{"attempt"}},
		{"go.mod", reads("/work/app/go.mod", "module example.com/app\n\ngo 1.26\n", false), []string{"convention", "file"}},
		{"prettier", reads("web/.prettierrc", "{\"semi\": false}", false), []string{"convention", "file"}},
		{"editorconfig", reads(".editorconfig", "root = true\n\n[*]\nindent_style = space\nindent_size = 2\nend_of_line = lf\n\n[Makefile]\nindent_style = tab\n", false), []string{"convention", "file"}},
		{"pyproject", reads("pyproject.toml", "[project]\nname = \"x\"\n\n[tool.ruff]\nline-length = 100\n\n[tool.black]\nline-length = 100\n", false), []string{"convention", "file"}},
		{"pyproject without tools", reads("pyproject.toml", "[project]\nname = \"x\"\n", false), []string{"file"}},
		{"workflow", reads(".github/workflows/ci.yml", workflow, false), []string{"convention", "file"}},
		{"workflow with line numbers", reads(".github/workflows/ci.yml", "     1→jobs:\n     2→  t:\n     3→    steps:\n     4→      - run: make ci\n", false), []string{"convention", "file"}},
		{"makefile", reads("Makefile", makefile, false), []string{"convention", "file"}},
		{"source file", reads("src/main.go", "package main\n", false), []string{"file"}},
		{"cat is no read tool", []session.Entry{
			user("u1", "Look at the repository."),
			assistant("a1", bash("c1", "cat Makefile")),
			result("r1", "c1", "bash", makefile, false),
		}, nil},
		{"read twice, then changed", []session.Entry{
			user("u1", "Look at the lockfile."),
			assistant("a1", call{"c1", "read", map[string]any{"path": "yarn.lock"}}),
			result("r1", "c1", "read", "# yarn v1\n", false),
			assistant("a2", call{"c2", "read", map[string]any{"path": "yarn.lock"}}),
			result("r2", "c2", "read", "# yarn v1\n", false),
			assistant("a3", call{"c3", "Read", map[string]any{"file_path": "/work/app/yarn.lock"}}),
			result("r3", "c3", "Read", "# yarn v1\nleft-pad@1.3.0\n", false),
		}, []string{"convention", "convention", "file"}},
	}
}

func kindsOf(r Result) []string {
	var out []string
	for _, o := range r.Observations() {
		k := string(o.Kind)
		if o.Status != "" {
			k += "/" + o.Status
		}
		out = append(out, k)
	}
	return out
}

// The encoding cases record what they should, and contracts/encoding.jsonl holds Go's records
// for each (rewritten with -args -write).
func TestEncodingCases(t *testing.T) {
	var b bytes.Buffer
	for _, c := range encodingCases() {
		r := run(c.entries)
		if got := kindsOf(r); !slices.Equal(got, c.want) {
			t.Errorf("%s: kinds %v, want %v\n%s", c.name, got, c.want, strings.Join(contents(r), "\n"))
		}
		maps := []map[string]any{}
		for _, e := range c.entries {
			maps = append(maps, e.ToMap())
		}
		records := []string{}
		for _, o := range r.Observations() {
			line, err := json.Marshal(o)
			if err != nil {
				t.Fatal(err)
			}
			records = append(records, string(line))
		}
		line, err := json.Marshal(map[string]any{"name": c.name, "cwd": cwd, "entries": maps, "records": records})
		if err != nil {
			t.Fatal(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	if *writeEncoding {
		if err := os.WriteFile(encodingCasesPath, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(encodingCasesPath)
	if err != nil || !bytes.Equal(want, b.Bytes()) {
		t.Fatalf("contracts/encoding.jsonl is stale; rerun with -args -write and review the diff (%v)", err)
	}
}

// A convention names its file and the hash of what was read, and says what the file sets.
func TestConventionContents(t *testing.T) {
	for _, c := range []struct{ path, text, want string }{
		{"package-lock.json", "{}", "package manager npm"},
		{"go.sum", "x", "package manager go modules"},
		{"go.mod", "module x", "formatter gofmt (implied by go.mod)"},
		{".editorconfig", "[*]\nindent_style = space\nindent_size = 2\n[*.go]\nindent_style = tab\nindent_size = 2\n", "editorconfig indent_style=space, indent_size=2, indent_style=tab"},
		{"pyproject.toml", "[tool.black]\n[tool.ruff.lint]\n", "formatter black; linter ruff"},
		{".github/workflows/ci.yml", workflow, "CI runs `go vet ./...`; `go test -race ./...`; `make lint`"},
		{"Makefile", makefile, "make targets all, build, test, lint"},
		{"docs/Makefile.md", makefile, ""},
		{"ci.yml", workflow, ""},
	} {
		if got := conventionOf(c.path, c.text); got != c.want {
			t.Errorf("%s: %q, want %q", c.path, got, c.want)
		}
	}
	r := run(reads(".github/workflows/ci.yml", workflow, false))
	o := find(t, r, ledger.KindConvention, "CI runs")
	if o.Target != ".github/workflows/ci.yml" || o.Tool != "read" || !strings.HasSuffix(o.Content, "(repo file `.github/workflows/ci.yml`, sha256 "+o.Content[len(o.Content)-13:len(o.Content)-1]+")") {
		t.Fatalf("convention %+v", o)
	}
	if ledger.UserPriority(o) {
		t.Fatalf("a repository file has the user's authority: %+v", o)
	}
}

// A convention read again with the same content is not recorded twice across extractions, and
// one read with other content supersedes the ledger's.
func TestConventionAcrossExtractions(t *testing.T) {
	first := reads("Cargo.lock", "version = 3\n", false)
	r := run(first)
	o := find(t, r, ledger.KindConvention, "package manager cargo")
	branch := append(append([]session.Entry{}, first...), recorded("o1", r)...)
	branch = append(branch, assistant("a2", call{"c2", "read", map[string]any{"path": "Cargo.lock"}}), result("r2", "c2", "read", "version = 3\n", false))
	in := func(i *Input) { i.Start = len(first) + 1 }
	if again := run(branch, in); len(again.Observations()) != 0 {
		t.Fatalf("recorded again: %v", contents(again))
	}
	branch = append(branch, assistant("a3", call{"c3", "read", map[string]any{"path": "Cargo.lock"}}), result("r3", "c3", "read", "version = 4\n", false))
	changed := run(branch, in)
	n := find(t, changed, ledger.KindConvention, "package manager cargo")
	if !slices.Equal(n.Supersedes, []string{o.ID}) {
		t.Fatalf("supersedes %v, want %s", n.Supersedes, o.ID)
	}
}

// recorded is the om.observations.recorded entry holding r's records.
func recorded(id string, r Result) []session.Entry {
	out := []session.Entry{}
	for _, batch := range r.Batches {
		out = append(out, tf.Entry(map[string]any{"type": "custom", "id": id, "parentId": nil, "timestamp": tf.DefaultTimestamp,
			"customType": ledger.OMObservationsRecorded, "data": ledger.ObservationsRecordedData{Observations: batch.Observations, CoversUpToID: batch.CoversUpToID}}))
	}
	return out
}

// Rule and decision records keep their keys: the code spans and bare paths they name.
func TestRuleRecordsCarryTheirKeys(t *testing.T) {
	r := run(says("Don't touch `migrations/` or db/seeds/ while you work on this."))
	o := find(t, r, "preference", "migrations")
	if got := ledger.RecordKeys(o.Content); !slices.Equal(got, []string{"migrations/", "db/seeds/"}) {
		t.Fatalf("keys %v", got)
	}
}
