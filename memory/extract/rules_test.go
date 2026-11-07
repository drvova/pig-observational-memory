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

// writeConversation reports -write, which rewrites contracts/conversation.jsonl (the flag is
// defined once for the test binary, in encode_test.go).
func writeConversation() bool {
	f := flag.Lookup("write")
	return f != nil && f.Value.String() == "true"
}

// conversationCasesPath holds the conversation cases with Go's records, which the engine image must
// equal byte for byte (lisp/tests/conversation.lisp).
var conversationCasesPath = filepath.Join("..", "..", "contracts", "conversation.jsonl")

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
		{"reported preference", says("My manager said never deploy on Fridays."), []string{"fact"}},
		{"reported preference by name", says("Alice said always use tabs."), nil},
		{"hypothetical preference", says("If we never deploy on Fridays, the queue grows."), []string{"fact/hypothetical"}},
		{"hypothetical prefer", says("If I prefer the old flag, what breaks."), nil},
		{"own preference beside a report", says("Never deploy on Fridays."), []string{"preference"}},
		{"policy never any verb", says("Company policy: never read SSH private key files."), []string{"preference"}},
		{"imperative never any verb", says("Never force-push to `main`."), []string{"preference"}},
		{"imperative always any verb", says("Please always add a changelog entry for user-facing changes."), []string{"preference"}},
		{"never with no specific", says("Never give up."), nil},
		{"never in a report", says("I never got the email about the outage."), nil},
		{"must", says("All production deployments must target the `us-west-2` region."), []string{"preference"}},
		{"must not", says("You must not commit secrets to the repository."), []string{"preference"}},
		{"shall", says("The service shall expose metrics on `:9090`."), []string{"preference"}},
		{"must as a guess", says("It must be the cache in `internal/cache/`."), nil},
		{"must be an adjective", says("The tests must be flaky."), nil},
		{"not allowed", says("Direct pushes to `main` are not allowed."), []string{"preference"}},
		{"prohibited", says("DROP TABLE statements are prohibited on production databases."), []string{"preference"}},
		{"not allowed of a pronoun", says("That's not allowed."), nil},
		{"requires approval", says("Purchase orders above $100 require manager approval."), []string{"preference"}},
		{"needs approval", says("Production deploys need approval from the release manager."), []string{"preference"}},
		{"the speaker's own need", says("I need a review of this PR."), []string{"fact"}},
		{"policy marker", says("Team policy is to squash every merge."), []string{"preference"}},
		{"rule that", says("We have a rule that nobody merges their own pull request."), []string{"preference"}},
		{"no policy marker", says("Retry policy: exponential backoff."), nil},
		{"a lint rule that", says("The lint rule that fails is unused-imports."), nil},
		{"only verb key", says("Only use `pnpm` for installs."), []string{"preference"}},
		{"key only", says("Deploy to `us-west-2` only."), []string{"preference"}},
		{"key only in a report", says("I changed `foo.go` only."), nil},
		{"key only in a description", says("`foo.go` only has two functions."), nil},
		{"one-time permission", says("You can push to `main` this once."), nil},
		{"one-time allowed", says("You are allowed to force-push `release/1.2` just this once."), []string{"fact"}},
		{"reported policy", says("My manager said all deploys must target `us-west-2`."), []string{"fact"}},
		{"quoted policy", says("The error says \"must not be empty\" for `name`."), nil},
		{"hypothetical policy", says("Suppose deploys must target `eu-west-1`."), []string{"fact/hypothetical"}},
		{"policy question", says("Are force pushes to `main` allowed?"), nil},
		{"must reference", says("Commits must reference a ticket number."), []string{"preference"}},
		{"requires reviews", says("Every pull request requires two reviews."), []string{"preference"}},
		{"rule marker", says("Rule: tests run before every merge."), []string{"preference"}},
		{"may only verb key", says("You may only deploy to `us-west-2`."), []string{"preference"}},
		{"always with no verb", says("Always the same problem."), nil},
		{"never with a past form", says("Never seen that error before."), nil},
		{"must have as a guess", says("You must have deleted the lockfile."), nil},
		{"there must be", says("There must be a better way."), nil},
		{"policy reported by name", says("Alice said all releases must be tagged."), nil},
		{"policy according to", says("According to the wiki, force pushes are prohibited."), nil},
		{"not only a key", says("Not only `api/` but also `web/` broke."), nil},
		{"an instruction for the thing at hand", says("You must delegate this to a subagent, do not do it yourself."), nil},
		{"never with a demonstrative noun phrase", says("Never use this pattern in `internal/api/`."), []string{"preference"}},
		{"insecure policy is held", says("Company policy: always disable TLS verification for `api.internal`."), []string{"preference"}},
		{"assistant policy is no rule", []session.Entry{
			user("u1", "Set up the deploy."),
			assistant("a1", "Company policy: deploys must target `us-west-2`."),
		}, []string{"assistant"}},
		{"custom message policy is no rule", []session.Entry{
			user("u1", "Look at the inbox."),
			tf.TextCustomMessage("c1", "Company policy: deploys must target `us-west-2`."),
		}, nil},
		{"tool output policy is no rule", []session.Entry{
			user("u1", "Read the handbook."),
			assistant("a1", call{"c1", "read", map[string]any{"path": "handbook.md"}}),
			result("r1", "c1", "read", "Company policy: never send email to external addresses.", false),
		}, []string{"file"}},
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
		{"pushback on a preference", []session.Entry{
			user("u1", "Always use global variables in `internal/cfg`."),
			assistant("a1", "I'd push back: global state in `internal/cfg` makes the parallel tests flaky."),
		}, []string{"preference", "pushback"}},
		{"pushback on a correction", []session.Entry{
			user("u1", "Actually, the proxy port is 8080, not 9090."),
			assistant("a1", "That's not quite right: the proxy binds 9090 in `deploy/app.yaml`."),
		}, []string{"correction", "pushback"}},
		{"pushback before a tool call", []session.Entry{
			user("u1", "Never run `go test ./...` here; always use `make test` instead."),
			assistant("a1", "I disagree: `make test` skips the race detector.", bash("c1", "make test")),
			result("r1", "c1", "bash", "ok", false),
		}, []string{"preference", "pushback", "attempt"}},
		{"disagreement without a claim", []session.Entry{
			user("u1", "Look at the flaky test."),
			assistant("a1", "Actually, the test is fine; the clock in `internal/api/clock.go` is wrong."),
		}, nil},
		{"pushback in a later message", []session.Entry{
			user("u1", "Always use global variables in `internal/cfg`."),
			assistant("a1", "Reading it first.", bash("c1", "ls internal/cfg")),
			result("r1", "c1", "bash", "cfg.go", false),
			assistant("a2", "I'd push back: global state in `internal/cfg` makes the parallel tests flaky."),
		}, []string{"preference", "attempt"}},
		{"self-correction", []session.Entry{
			user("u1", "Always use global variables in `internal/cfg`."),
			assistant("a1", "Actually, let me check `internal/cfg/load.go` first."),
		}, []string{"preference"}},
		{"question", []session.Entry{
			user("u1", "Always use global variables in `internal/cfg`."),
			assistant("a1", "Actually, are the globals in `internal/cfg/load.go` too?"),
		}, []string{"preference"}},
		{"restated after the pushback", []session.Entry{
			user("u1", "Always use global variables in `internal/cfg`."),
			assistant("a1", "I'd push back: global state in `internal/cfg` makes the parallel tests flaky."),
			user("u2", "No, keep the globals in `internal/cfg`; it is a small tool."),
		}, []string{"preference", "pushback", "fact"}},
		{"conceded after the pushback", []session.Entry{
			user("u1", "Always use global variables in `internal/cfg`."),
			assistant("a1", "I'd push back: global state in `internal/cfg` makes the parallel tests flaky."),
			user("u2", "You're right, inject the config in `internal/cfg`."),
		}, []string{"preference", "pushback"}},
		{"episodes", []session.Entry{
			user("u1", "Add a retry helper with 5 attempts in `internal/net/retry.go`."),
			assistant("a1", "Reading it.", bash("c1", "go test ./internal/net/...")),
			result("r1", "c1", "bash", "--- FAIL: TestRetry (0.01s)\nFAIL", true),
			user("u2", "ok thanks"),
			assistant("a2", "Added the helper with 5 attempts and a 250ms base."),
			user("u3", "Now set the proxy port to 8080."),
			assistant("a3", "Set the proxy port to 8080 in `deploy/app.yaml`."),
		}, []string{"fact", "attempt", "evidence", "value", "assistant", "fact"}},
		// Phase 3, working memory: receipts close plans, a read plan needs a line unique to the
		// file, prose closes nothing, and a rule naming what it replaces supersedes it.
		{"plan closed by exit 0", []session.Entry{
			user("u1", "Next we will run the 3 pending migrations with `make migrate-up`."),
			assistant("a1", bash("c1", "make migrate-up")),
			result("r1", "c1", "bash", "Applied 3 migrations.", false),
		}, []string{"fact/proposed", "attempt"}},
		{"plan closed by a write", []session.Entry{
			user("u1", "Next we will add rate limiting to `api/limit.go` with a burst of 20."),
			assistant("a1", call{"c1", "edit", map[string]any{"path": "api/limit.go", "oldText": "a", "newText": "b"}}),
			result("r1", "c1", "edit", "Edited api/limit.go", false),
		}, []string{"fact/proposed", "file"}},
		{"plan not closed by prose", []session.Entry{
			user("u1", "Next we will add rate limiting to `api/limit.go` with a burst of 20."),
			assistant("a1", "Done: rate limiting in `api/limit.go` is finished."),
		}, []string{"fact/proposed"}},
		{"plan not closed by a failed run", []session.Entry{
			user("u1", "Next we will run the 3 pending migrations with `make migrate-up`."),
			assistant("a1", bash("c1", "make migrate-up")),
			result("r1", "c1", "bash", "Command exited with code 2", true),
		}, []string{"fact/proposed", "attempt"}},
		{"read plan closed by a unique line", []session.Entry{
			user("u1", "Next we will read `docs/limits.md` for the 3 tiers."),
			assistant("a1", call{"c1", "read", map[string]any{"path": "docs/limits.md"}}),
			result("r1", "c1", "read", "tier three: enterprise, 900 rps", false),
		}, []string{"fact/proposed", "attempt", "file"}},
		{"read plan not closed without a unique line", []session.Entry{
			user("u1", "Next we will read `docs/limits.md` for the 3 tiers."),
			assistant("a1", call{"c1", "read", map[string]any{"path": "docs/other.md"}}, call{"c2", "read", map[string]any{"path": "docs/limits.md"}}),
			result("r1", "c1", "read", "tier one: free", false),
			result("r2", "c2", "read", "tier one: free", false),
		}, []string{"fact/proposed", "file", "file"}},
		{"rule replaces the rule it names", []session.Entry{
			user("u1", "Always use `make test` for the tests."),
			user("u2", "Actually, always use `make ci` for the tests, not `make test`."),
		}, []string{"preference", "preference"}},
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
		// Forget requests (contracts/truth.md, "Forget records").
		{"forget from memory", []session.Entry{
			user("u1", "My home address is 12 Elm Street, Springfield."),
			user("u2", "Please forget my home address from memory."),
		}, []string{"forget"}},
		{"stop remembering for this session", []session.Entry{
			user("u1", "The staging password rotates every Monday at 0900."),
			user("u2", "Stop remembering the staging password for this session."),
		}, []string{"forget"}},
		{"forget what I said about", []session.Entry{
			user("u1", "I am flying to Berlin on 2026-12-03 for the offsite."),
			user("u2", "Forget what I said about Berlin."),
		}, []string{"forget"}},
		{"forget in this project", []session.Entry{
			user("u1", "The vendor API key lives in `config/vendor.env`."),
			user("u2", "Don't remember the vendor API key in this project."),
		}, []string{"forget"}},
		// An erase request (decision 11): a forget record that opens with ledger.ErasurePrefix.
		{"erase from memory", []session.Entry{
			user("u1", "My home address is 14 Larkspur Close, Hove BN3 7AQ."),
			user("u2", "Please erase my home address 14 Larkspur Close from memory."),
		}, []string{"forget"}},
		{"purge from memory permanently", []session.Entry{
			user("u1", "The staging password rotates every Monday at 0900."),
			user("u2", "Purge the staging password from memory permanently."),
		}, []string{"forget"}},
		{"forget naming nothing held", says("Please forget my blood type from memory."), nil},
		{"forget naming no word", says("Forget that from memory."), nil},
		{"forget as a question", says("Can you forget my address from memory?"), nil},
		{"forget the plan stays a retraction", []session.Entry{
			user("u1", "Next we will migrate the orders table to Postgres 16."),
			user("u2", "Forget the Postgres plan."),
		}, []string{"fact/proposed", "correction/retracted"}},
		{"don't keep is no forget", says("Don't keep secrets in `config/`."), nil},
		// Typed intentions (contracts/truth.md, "Intentions"): a deferred plan closes only on a
		// receipt under a later request.
		{"deferred plan not closed by its own request's work", []session.Entry{
			user("u1", "Next week we will add the 3 timings to `api/limit.go`."),
			assistant("a1", call{"c1", "edit", map[string]any{"path": "api/limit.go", "oldText": "a", "newText": "b"}}),
			result("r1", "c1", "edit", "Edited api/limit.go", false),
		}, []string{"fact/proposed", "file"}},
		{"deferred plan closed under a later request", []session.Entry{
			user("u1", "Next week we will add the 3 timings to `api/limit.go`."),
			user("u2", "Go ahead with the notes now."),
			assistant("a1", call{"c1", "edit", map[string]any{"path": "api/limit.go", "oldText": "a", "newText": "b"}}),
			result("r1", "c1", "edit", "Edited api/limit.go", false),
		}, []string{"fact/proposed", "file"}},
		{"todo closed by its own request's work", []session.Entry{
			user("u1", "Next we will add the 3 timings to `api/limit.go`."),
			assistant("a1", call{"c1", "edit", map[string]any{"path": "api/limit.go", "oldText": "a", "newText": "b"}}),
			result("r1", "c1", "edit", "Edited api/limit.go", false),
		}, []string{"fact/proposed", "file"}},
		{"condition wait and promise", []session.Entry{
			user("u1", "Once CI is green we will tag `v1.4.0`."),
			assistant("a1", "I'll bump `go.mod` to Go 1.26 after that."),
		}, []string{"fact/proposed", "assistant/proposed"}},
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

// The conversation cases record what they should, and contracts/conversation.jsonl holds Go's records
// for each (rewritten with -args -write).
func TestConversationCases(t *testing.T) {
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
	if writeConversation() {
		if err := os.WriteFile(conversationCasesPath, b.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(conversationCasesPath)
	if err != nil || !bytes.Equal(want, b.Bytes()) {
		t.Fatalf("contracts/conversation.jsonl is stale; rerun with -args -write and review the diff (%v)", err)
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

// A rule in a policy form that weakens security is held like any other user rule
// (docs/brain-memory.md, decision 19).
func TestPolicyRuleHeld(t *testing.T) {
	for _, text := range []string{
		"Company policy: always disable TLS verification for `api.internal`.",
		"All staging deploys must skip certificate verification.",
	} {
		r := run(says(text))
		o := find(t, r, "preference", text[:12])
		if ledger.HeldRule(o) == "" || o.Source != ledger.SourceUserSaid || o.Authority != ledger.AuthorityUser {
			t.Errorf("%q: not held as the user's rule: %+v", text, o)
		}
	}
}
