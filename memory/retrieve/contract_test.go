package retrieve

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/ledger"
)

var write = flag.Bool("write", false, "rewrite contracts/tokenize.jsonl and contracts/retrieval.jsonl")

// The contract's cases, which lisp/tests/retrieval.lisp reads too.
var (
	tokenizePath  = filepath.Join("..", "..", "contracts", "tokenize.jsonl")
	retrievalPath = filepath.Join("..", "..", "contracts", "retrieval.jsonl")
)

// TokenCase is one tokenizer case: a text and what the tokenizer reads from it.
type TokenCase struct {
	Name       string   `json:"name"`
	Input      string   `json:"input"`
	Raw        []string `json:"raw"`
	Terms      []string `json:"terms"`
	Exact      []string `json:"exact"`
	ErrorLines []string `json:"errorLines"`
	Lookups    []string `json:"lookups"`
}

var tokenInputs = []struct{ name, input string }{
	{"camel case", "parseConfig reads the file"},
	{"pascal with acronym", "HTTPServer and XMLHttpRequest"},
	{"snake case", "max_retry_count is 3"},
	{"kebab case", "use --dry-run with kubectl-apply"},
	{"dotted", "call os.path.join and pkg.Func"},
	{"path segments", "edit internal/store/users_test.go now"},
	{"absolute path", "/etc/nginx/nginx.conf: no such file"},
	{"qualified", "test:TestInsertUsers failed"},
	{"digits stay with their part", "utf8Decode and base64URL and Retry3Times"},
	{"version", "upgrade to v2.6.10 from 1.22"},
	{"version suffix", "pin 1.0.0-rc.1 and 3.12.0.post1"},
	{"version in a sentence end", "SBCL 2.2.9."},
	{"sha", "revert 28f8fe2 and 9f2c1e0d4b7a"},
	{"not a sha", "deadbeef and 1234567 and cafe12"},
	{"plain numbers", "port 8080 and 42"},
	{"single letters dropped", "a b c x.y"},
	{"punctuation", "(foo), [bar]; {baz}! \"qux\""},
	{"non ascii word", "café_au_lait naïveTest"},
	{"error line", "store_test.go:42: pq: duplicate key value violates unique constraint \"users_pkey\""},
	{"error line with column", "main.go:3:7: undefined: parseConfig"},
	{"error lines", "ok\n--- FAIL: TestInsertUsers (0.02s)\npanic: runtime error: index out of range [3] with length 3"},
	{"short error line", "error"},
	{"not an error line", "everything passed"},
	{"white space", "  Error:\tconnection   refused  "},
	{"flags", "-v --count=1 -run 'Lisp|Sources'"},
	{"edges only", "... -- // ::"},
	{"underscore kept", "_private __init__"},
	{"version with v and sha", "v1.2 9f2c1e0"},
}

func tokenCases() []TokenCase {
	var out []TokenCase
	for _, in := range tokenInputs {
		terms, exact := Tokens(in.input)
		out = append(out, TokenCase{Name: in.name, Input: in.input, Raw: nonNil(RawTokens(in.input)), Terms: nonNil(terms), Exact: nonNil(exact), ErrorLines: nonNil(ErrorLines(in.input, false)), Lookups: nonNil(Lookups(in.input))})
	}
	return out
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// RetrievalCase is one search case: records in ledger order (their tags from TagsOf, their
// documents from DocOf over their own content), the searched strings, and the results.
type RetrievalCase struct {
	Name    string               `json:"name"`
	Records []ledger.Observation `json:"records,omitempty"`
	Search  []string             `json:"search,omitempty"`
	Partial bool                 `json:"partial"`
	Results []CaseResult         `json:"results"`
	// Log2 are [p, q, Log2Fixed(p, q)] triples.
	Log2 [][3]int64 `json:"log2,omitempty"`
}

// CaseResult is one result: the hit's id and tag, whether the exact index found it, and the ids
// merged into it.
type CaseResult struct {
	ID    string   `json:"id"`
	Tag   string   `json:"tag"`
	Exact bool     `json:"exact"`
	With  []string `json:"with"`
}

func rec(id, kind, content string, src ...string) ledger.Observation {
	return ledger.Observation{ID: id, Kind: ledger.ObservationKind(kind), Content: content, Timestamp: "2026-05-02 10:00", Relevance: "medium", SourceEntryIDs: src}
}

func with(o ledger.Observation, f func(*ledger.Observation)) ledger.Observation {
	f(&o)
	return o
}

type retrievalInput struct {
	name    string
	records []ledger.Observation
	search  []string
}

func retrievalInputs() []retrievalInput {
	keyed := func(o ledger.Observation, kind, v string) ledger.Observation {
		o.Keys.Add(kind, v)
		return o
	}
	port := rec("p1", "fact", "The billing service listens on port 7001.", "u1")
	port.Subject = "billing service"
	newer := rec("p2", "correction", "Actually, the billing service port is 7002, not 7001.", "u9")
	newer.Subject, newer.Supersedes = "billing service", []string{"p1"}
	var filler []ledger.Observation
	for i, w := range []string{"alpha", "beta", "gamma", "delta", "epsilon", "zeta"} {
		filler = append(filler, rec("f"+string(rune('1'+i)), "fact", "The "+w+" handler logs requests at debug level.", "u"+string(rune('2'+i))))
	}
	return []retrievalInput{
		{"identifier exact key", []ledger.Observation{
			rec("a1", "fact", "The client retries requests.", "u1"),
			keyed(rec("a2", "file", "Added `retryWithBackoff(maxAttempts=3)` to internal/net/retry.go.", "t2"), ledger.KeySymbols, "retryWithBackoff"),
			rec("a3", "fact", "Backoff is exponential.", "u3"),
		}, []string{"retryWithBackoff"}},
		{"identifier sub token", []ledger.Observation{
			rec("b1", "fact", "parseConfig reads settings.json.", "u1"),
			rec("b2", "fact", "The config parser is strict.", "u2"),
		}, []string{"parse_config"}},
		{"camel query finds snake", []ledger.Observation{
			rec("b3", "fact", "parse_config reads settings.json.", "u1"),
			rec("b4", "fact", "Parsing the config is strict.", "u2"),
		}, []string{"parseConfig"}},
		{"camel parts rank", []ledger.Observation{
			rec("c1", "fact", "HTTPServer starts on boot.", "u1"),
			rec("c2", "fact", "The server is an http server.", "u2"),
			rec("c3", "fact", "Nothing here.", "u3"),
		}, []string{"server"}},
		{"version exact only", []ledger.Observation{
			keyed(rec("v1", "fact", "Pinned SBCL to 2.6.10 in CI.", "u1"), ledger.KeyVersions, "2.6.10"),
			rec("v2", "fact", "Version 2 of the API drops field 6 and 10.", "u2"),
		}, []string{"2.6.10"}},
		{"sha exact only", []ledger.Observation{
			rec("s1", "commit", "Committed 28f8fe2: baseline re-run.", "t1"),
			rec("s2", "fact", "The baseline numbers moved.", "u2"),
		}, []string{"28f8fe2"}},
		{"error line exact", []ledger.Observation{
			rec("e1", "evidence", "--- FAIL: TestInsertUsers (0.02s)\n    users_test.go:42: pq: duplicate key value violates unique constraint \"users_pkey\"\nFAIL", "t1"),
			rec("e2", "fact", "The users table has a unique constraint on email.", "u2"),
			rec("e3", "fact", "A duplicate key in the cache is harmless.", "u3"),
		}, []string{"pq: duplicate key value violates unique constraint \"users_pkey\""}},
		{"value target", []ledger.Observation{
			with(rec("x1", "evidence", "--- FAIL: TestInsertUsers (0.02s)", "t1"), func(o *ledger.Observation) { o.Keys.Add(ledger.KeyTests, "TestInsertUsers") }),
			with(rec("x2", "value", "test TestInsertUsers", "t1"), func(o *ledger.Observation) { o.Target = "test:TestInsertUsers" }),
			rec("x3", "fact", "Insert users in bulk.", "u3"),
		}, []string{"TestInsertUsers"}},
		{"superseded below its update", append(append([]ledger.Observation{port}, filler...), newer), []string{"billing service port"}},
		{"retracted below current", []ledger.Observation{
			with(rec("r1", "fact", "We will deploy the canary on Monday.", "u1"), func(o *ledger.Observation) { o.Status = ledger.StatusProposed }),
			with(rec("r2", "fact", "Scratch that, no canary deploy.", "u2"), func(o *ledger.Observation) {
				o.Status, o.Supersedes = ledger.StatusRetracted, []string{"r1"}
			}),
			rec("r3", "fact", "The canary deploy runs from CI.", "u3"),
		}, []string{"canary deploy"}},
		{"held and contested tags", []ledger.Observation{
			rec("h1", "preference", "Always use curl -k against the dev box.", "u1"),
			with(rec("h2", "preference", "Always run the dev box tests with -race.", "u2"), func(o *ledger.Observation) { o.Hedge, o.Source = ledger.HedgeContested, ledger.SourceUserSaid }),
			rec("h3", "fact", "The dev box runs Linux.", "u3"),
		}, []string{"dev box"}},
		{"neighbours by source", []ledger.Observation{
			rec("n1", "fact", "Unrelated line.", "u0"),
			rec("n2", "value", "path internal/store/users.go", "t1"),
			keyed(rec("n3", "attempt", "go test ./internal/store failed.", "t1"), ledger.KeyPaths, "internal/store"),
			rec("n4", "value", "test TestInsertUsers", "t1"),
			rec("n5", "fact", "Another unrelated line.", "u5"),
		}, []string{"internal/store"}},
		{"neighbours by episode capped", func() []ledger.Observation {
			var out []ledger.Observation
			for i, c := range []string{"one", "two", "three", "four", "target timeout", "six", "seven", "eight"} {
				o := rec("ep"+string(rune('1'+i)), "fact", "Step "+c+".", "e"+string(rune('1'+i)))
				o.Episode = "E1"
				out = append(out, o)
			}
			return out
		}(), []string{"timeout"}},
		{"neighbour hit merged once", []ledger.Observation{
			with(rec("m1", "fact", "The flaky test is TestRetry.", "u1"), func(o *ledger.Observation) { o.Episode = "E" }),
			with(rec("m2", "fact", "TestRetry times out on CI.", "u2"), func(o *ledger.Observation) { o.Episode = "E" }),
			rec("m3", "fact", "TestRetry passes locally.", "u3"),
		}, []string{"TestRetry"}},
		{"partial most words first", []ledger.Observation{
			rec("q1", "fact", "The cache uses redis.", "u1"),
			rec("q2", "fact", "The cache evicts after an hour, redis config in cache.yaml.", "u2"),
			rec("q3", "fact", "Nothing relevant.", "u3"),
		}, []string{"redis cache eviction policy"}},
		{"two searches", []ledger.Observation{
			rec("w1", "fact", "make lint runs golangci-lint.", "u1"),
			rec("w2", "fact", "make test runs the race detector.", "u2"),
			rec("w3", "fact", "lint config lives in .golangci.yml.", "u3"),
		}, []string{"make", "lint"}},
		{"no match", []ledger.Observation{rec("z1", "fact", "Something.", "u1")}, []string{"nothing matches"}},
		{"newest first on ties", []ledger.Observation{
			rec("t1", "fact", "The worker pool size is 8.", "u1"),
			rec("t2", "fact", "The worker pool size is 8.", "u2"),
		}, []string{"worker pool"}},
	}
}

// hexIDs gives the records ids a ledger accepts (12 hex digits), in order of first use, links
// included.
func hexIDs(records []ledger.Observation) []ledger.Observation {
	ids := map[string]string{}
	id := func(short string) string {
		if v, ok := ids[short]; ok {
			return v
		}
		ids[short] = fmt.Sprintf("%012x", 0xa00+len(ids))
		return ids[short]
	}
	out := make([]ledger.Observation, len(records))
	for i, o := range records {
		o.ID = id(o.ID)
		var sup []string
		for _, s := range o.Supersedes {
			sup = append(sup, id(s))
		}
		o.Supersedes = sup
		out[i] = o
	}
	return out
}

func retrievalCases() []RetrievalCase {
	var out []RetrievalCase
	for _, in := range retrievalInputs() {
		in.records = hexIDs(in.records)
		tags := TagsOf(in.records)
		pool := make([]Doc, len(in.records))
		for i, o := range in.records {
			pool[i] = DocOf(o, o.Content, tags[o.ID])
		}
		results, partial := Search(pool, in.search)
		c := RetrievalCase{Name: in.name, Records: in.records, Search: in.search, Partial: partial, Results: []CaseResult{}}
		for _, r := range results {
			cr := CaseResult{ID: pool[r.Hit].ID, Tag: pool[r.Hit].Tag, Exact: r.Exact, With: []string{}}
			for _, w := range r.With {
				cr.With = append(cr.With, pool[w].ID)
			}
			c.Results = append(c.Results, cr)
		}
		out = append(out, c)
	}
	var log2 [][3]int64
	for _, pq := range [][2]int64{{3, 1}, {4, 1}, {5, 3}, {2000002, 1}, {2000002, 1999999}, {22, 7}, {1 << 20, 3}, {13, 11}, {7, 3}, {102, 1}} {
		log2 = append(log2, [3]int64{pq[0], pq[1], Log2Fixed(pq[0], pq[1])})
	}
	return append(out, RetrievalCase{Name: "log2 table", Results: []CaseResult{}, Log2: log2})
}

func encodeLines[T any](cases []T) []byte {
	var b bytes.Buffer
	for _, c := range cases {
		line, err := json.Marshal(c)
		if err != nil {
			panic(err)
		}
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// The contracts' cases are this package's output, byte for byte (go test ./retrieve -args -write
// rewrites them).
func TestContractCases(t *testing.T) {
	for _, f := range []struct {
		path string
		data []byte
	}{{tokenizePath, encodeLines(tokenCases())}, {retrievalPath, encodeLines(retrievalCases())}} {
		if *write {
			if err := os.WriteFile(f.path, f.data, 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		have, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(have, f.data) {
			t.Fatalf("%s is stale; rerun with -args -write and review the diff\n%s", f.path, f.data)
		}
	}
}
