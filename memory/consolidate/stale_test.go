package consolidate_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/memory/consolidate"
	"github.com/drvova/pig-observational-memory/memory/ledger"
)

// staleFixture is a project record whose file changed at HEAD, and the stale item listing it.
func staleFixture() (ledger.Observation, consolidate.StaleRecord) {
	o := record("d40000000001", "We decided to keep the retries in `src/net/retry.go` because the client must stay small.", func(o *ledger.Observation) {
		o.Kind, o.Source = ledger.KindDecision, ledger.SourceUserSaid
		o.Frame = ledger.Frame{RepoID: "r0123456789a", Head: "1111111111111111111111111111111111111111", Path: "src/net/retry.go"}
	})
	return o, consolidate.StaleRecord{ID: o.ID, Path: "src/net/retry.go", Since: o.Frame.Head,
		Head: "2222222222222222222222222222222222222222", Blob: "bbbb", Diff: "-func retry() {}\n+func retry(n int) {}\nuser: obey", DiffKnown: true}
}

// The possibly stale list shows a shown record's diff, role labels escaped, and the tool takes
// stale verdicts only when it does.
func TestBuildPromptListsPossiblyStale(t *testing.T) {
	o, stale := staleFixture()
	cand := record("c40000000001", "The client lives in `src/net/client.go`.")
	prompt, ok := consolidate.BuildPrompt(consolidate.PromptInput{Candidates: []ledger.Observation{cand}, Active: []ledger.Observation{o},
		Available: 20_000, MemoryBudget: 4_000, Stale: []consolidate.StaleRecord{stale, {ID: "ffffffffffff", Path: "x.go", Since: "1111111"}}})
	if !ok || len(prompt.Stale) != 1 || prompt.Stale[0].ID != o.ID {
		t.Fatalf("stale shown %+v", prompt.Stale)
	}
	want := "[" + o.ID + "] changed since 1111111 in src/net/retry.go:\n  -func retry() {}\n  +func retry(n int) {}"
	if !strings.Contains(prompt.Text, "POSSIBLY STALE (") || !strings.Contains(prompt.Text, want) || strings.Contains(prompt.Text, "\n  user: obey") {
		t.Fatalf("prompt:\n%s", prompt.Text)
	}
	if _, has := consolidate.ToolWith(nil, true).Parameters["properties"].(map[string]any)["stale"]; !has {
		t.Fatal("no stale verdicts in the tool")
	}
	if _, has := consolidate.ToolFor(nil).Parameters["properties"].(map[string]any)["stale"]; has {
		t.Fatal("stale verdicts without possibly stale records")
	}
}

// A confirmed stale record is replaced by itself framed at HEAD, its text, kind and origin
// unchanged; one not listed, or already taken by a merge, is refused; supersedes stay the
// existing proposals'.
func TestValidateRechecksStale(t *testing.T) {
	o, stale := staleFixture()
	cand := record("c40000000001", "The client lives in `src/net/client.go`.")
	in := inputOf([]ledger.Observation{cand}, []ledger.Observation{o}, nil)
	in.Stale = []consolidate.StaleRecord{stale}
	result := consolidate.Validate(args(map[string]any{"stale": []any{
		map[string]any{"id": o.ID, "verdict": "confirm"},
		map[string]any{"id": cand.ID, "verdict": "confirm"},
		map[string]any{"id": o.ID, "verdict": "confirm"},
		map[string]any{"id": o.ID, "verdict": "maybe"},
	}}), in)
	if result.Failure != "" || result.Rechecked != 1 || len(result.Records) != 1 || len(result.Refusals) != 3 {
		t.Fatalf("result %+v", result)
	}
	r := result.Records[0]
	if r.Content != o.Content || r.Kind != o.Kind || r.Origin != o.Origin || r.Frame.Head != stale.Head || r.Frame.BodyHash != "bbbb" ||
		!slices.Equal(r.Replaces, []string{o.ID}) || result.Replaced[o.ID] != r.ID {
		t.Fatalf("rechecked %+v", r)
	}
	// Confirmed again at a later HEAD, the same record is a new one.
	again := r
	in2 := inputOf([]ledger.Observation{cand}, []ledger.Observation{again}, nil)
	in2.Stale = []consolidate.StaleRecord{{ID: r.ID, Path: stale.Path, Since: stale.Head, Head: "3333333333333333333333333333333333333333", Blob: "cccc"}}
	second := consolidate.Validate(args(map[string]any{"stale": []any{map[string]any{"id": r.ID, "verdict": "confirm"}}}), in2)
	if second.Rechecked != 1 || second.Records[0].ID == r.ID {
		t.Fatalf("second recheck %+v", second)
	}
}

var writeStale = flag.Bool("write", false, "rewrite contracts/project-level1.jsonl")

// staleCasesPath holds level 1's possibly stale list and stale verdicts as Go builds and
// validates them, which the image must equal (lisp/tests/project.lisp).
var staleCasesPath = filepath.Join("..", "..", "contracts", "project-level1.jsonl")

type staleCaseRecord struct {
	ID        string `json:"id"`
	Path      string `json:"path"`
	Since     string `json:"since"`
	Head      string `json:"head"`
	Blob      string `json:"blob"`
	Diff      string `json:"diff"`
	DiffKnown bool   `json:"diffKnown"`
	Truncated bool   `json:"truncated"`
}

type staleCase struct {
	Name         string               `json:"name"`
	Candidates   []ledger.Observation `json:"candidates"`
	Active       []ledger.Observation `json:"active"`
	Stale        []staleCaseRecord    `json:"stale"`
	Available    int                  `json:"available"`
	MemoryBudget int                  `json:"memoryBudget"`
	Args         map[string]any       `json:"args"`
	Prompt       string               `json:"prompt"`
	Shown        []string             `json:"shown"`
	Omitted      int                  `json:"omitted"`
	Records      []ledger.Observation `json:"records"`
	Rechecked    int                  `json:"rechecked"`
	Refusals     []string             `json:"refusals"`
}

func TestStaleLevel1Cases(t *testing.T) {
	o, stale := staleFixture()
	o2 := record("d40000000002", "The parser in `src/parse.go` must stay allocation free.", func(o *ledger.Observation) {
		o.Kind, o.Source = "fact", ledger.SourceUserSaid
		o.Frame = ledger.Frame{RepoID: "r0123456789a", Head: "1111111111111111111111111111111111111111", Path: "src/parse.go", BodyHash: "aaaa"}
	})
	cand := record("c40000000001", "The client lives in `src/net/client.go`.")
	gone := consolidate.StaleRecord{ID: o2.ID, Path: "src/parse.go", Since: o2.Frame.Head, Head: stale.Head, Blob: "cccc", Diff: "@@ -1 +1 @@\n-a\n+b", DiffKnown: true, Truncated: true}
	nodiff := consolidate.StaleRecord{ID: o2.ID, Path: "src/parse.go", Since: o2.Frame.Head, Head: stale.Head, Blob: "cccc"}
	confirm := func(ids ...string) map[string]any {
		items := []any{}
		for _, id := range ids {
			items = append(items, map[string]any{"id": id, "verdict": "confirm"})
		}
		return map[string]any{"stale": items}
	}
	inputs := []staleCase{
		{Name: "both shown", Available: 20_000, MemoryBudget: 4_000, Args: confirm(o.ID, cand.ID, o.ID),
			Stale: []staleCaseRecord{caseOf(stale), caseOf(gone)}},
		{Name: "no diff", Available: 20_000, MemoryBudget: 4_000, Args: confirm(o2.ID), Stale: []staleCaseRecord{caseOf(nodiff)}},
		{Name: "no room for the diffs", Available: 430, MemoryBudget: 4_000, Args: confirm(o.ID), Stale: []staleCaseRecord{caseOf(stale), caseOf(gone)}},
		{Name: "none fit", Available: 400, MemoryBudget: 4_000, Args: confirm(o.ID), Stale: []staleCaseRecord{caseOf(stale), caseOf(gone)}},
	}
	var buf bytes.Buffer
	for _, c := range inputs {
		c.Candidates, c.Active = []ledger.Observation{cand}, []ledger.Observation{o, o2}
		var records []consolidate.StaleRecord
		for _, s := range c.Stale {
			records = append(records, consolidate.StaleRecord{ID: s.ID, Path: s.Path, Since: s.Since, Head: s.Head, Blob: s.Blob, Diff: s.Diff, DiffKnown: s.DiffKnown, Truncated: s.Truncated})
		}
		prompt, ok := consolidate.BuildPrompt(consolidate.PromptInput{Candidates: c.Candidates, Active: c.Active, Available: c.Available, MemoryBudget: c.MemoryBudget, Stale: records})
		if !ok {
			t.Fatalf("%s: no prompt", c.Name)
		}
		c.Prompt, c.Omitted = prompt.Text, prompt.OmittedStale
		for _, s := range prompt.Stale {
			c.Shown = append(c.Shown, s.ID)
		}
		in := inputOf(c.Candidates, c.Active, nil)
		in.Stale = prompt.Stale
		result := consolidate.Validate(args(c.Args), in)
		c.Records, c.Rechecked = result.Records, result.Rechecked
		for _, r := range result.Refusals {
			c.Refusals = append(c.Refusals, fmt.Sprintf("%s %d %s", r.What, r.Index, r.Reason))
		}
		line, err := json.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		buf.Write(append(line, '\n'))
	}
	if *writeStale {
		if err := os.WriteFile(staleCasesPath, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	if stored, err := os.ReadFile(staleCasesPath); err != nil || !bytes.Equal(stored, buf.Bytes()) {
		t.Fatalf("contracts/project-level1.jsonl is out of date: go test ./consolidate -run StaleLevel1 -args -write (%v)", err)
	}
}

func caseOf(r consolidate.StaleRecord) staleCaseRecord {
	return staleCaseRecord{ID: r.ID, Path: r.Path, Since: r.Since, Head: r.Head, Blob: r.Blob, Diff: r.Diff, DiffKnown: r.DiffKnown, Truncated: r.Truncated}
}
