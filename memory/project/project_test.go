package project

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/host"
	"github.com/drvova/pig-observational-memory/memory/ledger"
)

var write = flag.Bool("write", false, "rewrite contracts/project.jsonl")

// casesPath is the contract's cases, which the Lisp port reads too (lisp/tests/project.lisp).
var casesPath = filepath.Join("..", "..", "contracts", "project.jsonl")

// Case is one contract case: a symbol case (Text and the symbol SymbolIn finds), or a freshness
// case (Records, Repo and the view's Answer, with the Query asked, the Verdicts given, and the
// body hashes Stamp sets from StampAnswer).
type Case struct {
	Name     string                `json:"name"`
	Text     *string               `json:"text,omitempty"`
	Symbol   string                `json:"symbol,omitempty"`
	Records  []ledger.Observation  `json:"records,omitempty"`
	Repo     *host.Repo            `json:"repo,omitempty"`
	Answer   *host.StructureAnswer `json:"answer,omitempty"`
	Query    *host.StructureQuery  `json:"query,omitempty"`
	Verdicts []CaseVerdict         `json:"verdicts,omitempty"`
	Diffs    *host.StructureQuery  `json:"diffs,omitempty"`
	Stamped  map[string]string     `json:"stamped,omitempty"`
}

// CaseVerdict is a verdict as the cases pin it.
type CaseVerdict struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

func text(s string) *string { return &s }

func rec(id, kind, source, content string, frame ledger.Frame) ledger.Observation {
	return ledger.Observation{ID: id, Content: content, Timestamp: "2026-11-01 10:00", Relevance: "high", SourceEntryIDs: []string{"e-" + id},
		Kind: ledger.ObservationKind(kind), Source: source, Frame: frame}
}

const (
	repoID = "r0123456789a"
	head1  = "1111111111111111111111111111111111111111"
	head2  = "2222222222222222222222222222222222222222"
	blobA  = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	blobB  = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

// inputs are the cases' inputs; their outputs are this package's, checked by hand when they
// change (TestProjectCasesPinned pins the ones that matter).
func inputs() []Case {
	repo := host.Repo{ID: repoID, Root: "/w", Head: head2}
	at1 := ledger.Frame{RepoID: repoID, Head: head1, Path: "config/load.go", Symbol: "parseConfig"}
	stamped := at1
	stamped.BodyHash = blobA
	return []Case{
		{Name: "symbol camel", Text: text("`parseConfig` in `config/load.go` reads the YAML")},
		{Name: "symbol call", Text: text("call `run()` first")},
		{Name: "symbol snake", Text: text("the `load_config` helper")},
		{Name: "symbol two", Text: text("`parseConfig` and `LoadConfig`")},
		{Name: "symbol repeated", Text: text("`parseConfig` then `parseConfig()` again")},
		{Name: "symbol plain word", Text: text("run `make` and `Config`")},
		{Name: "symbol dotted", Text: text("use `config.Load` and `x/y`")},
		{Name: "symbol leading digit", Text: text("`2fastFoo`")},
		{Name: "symbol unclosed", Text: text("`parseConfig")},
		{Name: "symbol empty", Text: text("")},
		{
			Name: "gone path", Repo: &repo,
			Records: []ledger.Observation{rec("00000000aa01", "fact", ledger.SourceUserSaid, "`parseConfig` in `config/load.go` reads the YAML", at1)},
			Answer: &host.StructureAnswer{RepoID: repoID, Head: head2,
				Paths:   []host.PathAnswer{{Path: "config/load.go", Known: true}},
				Symbols: []host.SymbolAnswer{{Path: "config/load.go", Symbol: "parseConfig", Known: true}},
				Blobs:   []host.BlobAnswer{{Path: "config/load.go", At: head1, Known: true, Blob: blobA}}},
		},
		{
			Name: "gone symbol", Repo: &repo,
			Records: []ledger.Observation{rec("00000000aa01", "decision", ledger.SourceUserSaid, "we decided to keep `parseConfig` pure", ledger.Frame{RepoID: repoID, Head: head1, Symbol: "parseConfig"})},
			Answer:  &host.StructureAnswer{RepoID: repoID, Head: head2, Symbols: []host.SymbolAnswer{{Symbol: "parseConfig", Known: true}}},
		},
		{
			Name: "changed by blob at the commit", Repo: &repo,
			Records: []ledger.Observation{rec("00000000aa01", "convention", ledger.SourceToolObserved, "Makefile runs go test", ledger.Frame{RepoID: repoID, Head: head1, Path: "Makefile"})},
			Answer: &host.StructureAnswer{RepoID: repoID, Head: head2,
				Paths: []host.PathAnswer{{Path: "Makefile", Known: true, Exists: true, Blob: blobB}},
				Blobs: []host.BlobAnswer{{Path: "Makefile", At: head1, Known: true, Blob: blobA}}},
		},
		{
			Name: "changed by body hash", Repo: &repo,
			Records: []ledger.Observation{rec("00000000aa01", "fact", ledger.SourceUserSaid, "`parseConfig` in `config/load.go` reads the YAML", stamped)},
			Answer: &host.StructureAnswer{RepoID: repoID, Head: head2,
				Paths:   []host.PathAnswer{{Path: "config/load.go", Known: true, Exists: true, Blob: blobB}},
				Symbols: []host.SymbolAnswer{{Path: "config/load.go", Symbol: "parseConfig", Known: true, Found: true}}},
		},
		{
			Name: "unchanged", Repo: &repo,
			Records: []ledger.Observation{rec("00000000aa01", "fact", ledger.SourceUserSaid, "`parseConfig` in `config/load.go` reads the YAML", stamped)},
			Answer: &host.StructureAnswer{RepoID: repoID, Head: head2,
				Paths:   []host.PathAnswer{{Path: "config/load.go", Known: true, Exists: true, Blob: blobA}},
				Symbols: []host.SymbolAnswer{{Path: "config/load.go", Symbol: "parseConfig", Known: true, Found: true}}},
		},
		{
			Name: "unknown is no verdict", Repo: &repo,
			Records: []ledger.Observation{rec("00000000aa01", "fact", ledger.SourceUserSaid, "`parseConfig` in `config/load.go` reads the YAML", at1)},
			Answer: &host.StructureAnswer{RepoID: repoID, Head: head2, Truncated: true,
				Paths:   []host.PathAnswer{{Path: "config/load.go"}},
				Symbols: []host.SymbolAnswer{{Path: "config/load.go", Symbol: "parseConfig"}},
				Blobs:   []host.BlobAnswer{{Path: "config/load.go", At: head1}}},
		},
		{
			Name: "not project records", Repo: &repo,
			Records: []ledger.Observation{
				rec("00000000bb01", "fact", ledger.SourceAssistantClaim, "`parseConfig` is in `config/load.go`", at1),
				rec("00000000cc01", "file", ledger.SourceToolObserved, "config/load.go edited", at1),
				rec("00000000dd01", "fact", ledger.SourceUserSaid, "another repo", ledger.Frame{RepoID: "other", Head: head1, Path: "config/load.go"}),
				rec("00000000ee01", "fact", ledger.SourceUserSaid, "no anchor", ledger.Frame{RepoID: repoID, Head: head1}),
				rec("00000000ff01", "fact", ledger.SourceUserSaid, "absolute", ledger.Frame{RepoID: repoID, Head: head1, Path: "/etc/hosts"}),
			},
			Answer: &host.StructureAnswer{RepoID: repoID, Head: head2},
		},
		{
			Name: "stamp at the framed head", Repo: &host.Repo{ID: repoID, Root: "/w", Head: head1},
			Records: []ledger.Observation{
				rec("00000000aa01", "decision", ledger.SourceUserSaid, "we decided on `config/load.go` because", ledger.Frame{RepoID: repoID, Head: head1, Path: "config/load.go"}),
				rec("00000000aa02", "decision", ledger.SourceUserSaid, "an older frame", ledger.Frame{RepoID: repoID, Head: head2, Path: "config/load.go"}),
			},
			Answer: &host.StructureAnswer{RepoID: repoID, Head: head1, Paths: []host.PathAnswer{{Path: "config/load.go", Known: true, Exists: true, Blob: blobA}}},
		},
	}
}

// outputs fills a case's outputs.
func outputs(c Case) Case {
	if c.Text != nil {
		c.Symbol = SymbolIn(*c.Text)
		return c
	}
	q := Query(c.Records, *c.Repo)
	c.Query = &q
	verdicts := Verdicts(c.Records, *c.Repo, *c.Answer)
	for _, v := range verdicts {
		c.Verdicts = append(c.Verdicts, CaseVerdict{ID: v.ID, Kind: v.Kind, Text: v.Text()})
	}
	if d := DiffQuery(verdicts); !Empty(d) {
		c.Diffs = &d
	}
	records := append([]ledger.Observation(nil), c.Records...)
	if Stamp(records, *c.Answer) {
		c.Stamped = map[string]string{}
		for _, o := range records {
			if o.Frame.BodyHash != "" {
				c.Stamped[o.ID] = o.Frame.BodyHash
			}
		}
	}
	return c
}

func encodeCases(cases []Case) []byte {
	var buf bytes.Buffer
	for _, c := range cases {
		line, err := json.Marshal(c)
		if err != nil {
			panic(err)
		}
		buf.Write(line)
		buf.WriteByte('\n')
	}
	return buf.Bytes()
}

func TestProjectCases(t *testing.T) {
	var cases []Case
	for _, c := range inputs() {
		cases = append(cases, outputs(c))
	}
	data := encodeCases(cases)
	if *write {
		if err := os.WriteFile(casesPath, data, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	stored, err := os.ReadFile(casesPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stored, data) {
		t.Fatalf("contracts/project.jsonl is out of date: go test ./project -args -write")
	}
}

func TestProjectCasesPinned(t *testing.T) {
	want := map[string]any{
		"symbol camel":                  "parseConfig",
		"symbol call":                   "run",
		"symbol snake":                  "load_config",
		"symbol two":                    "",
		"symbol repeated":               "parseConfig",
		"symbol plain word":             "",
		"symbol dotted":                 "",
		"gone path":                     []CaseVerdict{{ID: "00000000aa01", Kind: Gone, Text: "gone at 2222222"}},
		"gone symbol":                   []CaseVerdict{{ID: "00000000aa01", Kind: Gone, Text: "gone at 2222222"}},
		"changed by blob at the commit": []CaseVerdict{{ID: "00000000aa01", Kind: Changed, Text: "changed since 1111111, unverified"}},
		"changed by body hash":          []CaseVerdict{{ID: "00000000aa01", Kind: Changed, Text: "changed since 1111111, unverified"}},
		"unchanged":                     []CaseVerdict(nil),
		"unknown is no verdict":         []CaseVerdict(nil),
		"not project records":           []CaseVerdict(nil),
	}
	for _, c := range inputs() {
		c = outputs(c)
		w, ok := want[c.Name]
		if !ok {
			continue
		}
		var got any = c.Verdicts
		if c.Text != nil {
			got = c.Symbol
		}
		if !reflect.DeepEqual(got, w) {
			t.Errorf("%s: got %#v, want %#v", c.Name, got, w)
		}
		if c.Name == "not project records" && !Empty(*c.Query) {
			t.Errorf("asked about records that are none: %+v", c.Query)
		}
	}
	c := outputs(inputs()[len(inputs())-1])
	if !reflect.DeepEqual(c.Stamped, map[string]string{"00000000aa01": blobA}) {
		t.Errorf("stamped %v", c.Stamped)
	}
}
