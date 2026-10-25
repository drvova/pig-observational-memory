package consolidate_test

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/consolidate"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/kernel"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// record is a code record with content and sources.
func record(id, content string, modify ...func(*ledger.Observation)) ledger.Observation {
	o := ledger.Observation{ID: id, Content: content, Timestamp: "2026-10-01 10:00", Relevance: "medium", SourceEntryIDs: []string{"e-" + id}, Origin: ledger.OriginCode, Kind: "fact"}
	for _, m := range modify {
		m(&o)
	}
	return o
}

// The fixture memory: three candidates, one active record and one reflection.
var (
	candA   = record("aaaaaaaaaaaa", "The deploy uses port 8080 on staging", func(o *ledger.Observation) { o.Subject = "deploy" })
	candB   = record("bbbbbbbbbbbb", "Staging deploys run from the Makefile", func(o *ledger.Observation) { o.Subject = "deploy" })
	candC   = record("cccccccccccc", "go test failed in pkg/api", func(o *ledger.Observation) { o.Kind = ledger.KindAttempt; o.Outcome = ledger.OutcomeFailed })
	active  = record("dddddddddddd", "The user prefers Go over Python", func(o *ledger.Observation) { o.Kind = "preference"; o.Origin = "" })
	reflOld = ledger.Reflection{ID: "eeeeeeeeeeee", Content: "Deploys target staging on port 8080", SupportingObservationIDs: []string{"ffffffffffff"}}
)

func fixtureInput() consolidate.Input {
	observations := []ledger.Observation{candA, candB, candC, active}
	folded := ledger.FoldedLedger{Observations: observations, ObservationsByID: map[string]ledger.Observation{}, ReflectionsByID: map[string]ledger.Reflection{reflOld.ID: reflOld}}
	for _, o := range observations {
		folded.ObservationsByID[o.ID] = o
	}
	return consolidate.Input{
		Candidates:  []ledger.Observation{candA, candB, candC},
		Active:      []ledger.Observation{active},
		Reflections: []ledger.Reflection{reflOld},
		Ledger:      folded,
		Subjects:    ledger.SubjectsOf(observations),
	}
}

// args is a tool call's arguments as decoded JSON.
func args(m map[string]any) map[string]any { return testfix.ToJSONValue(m).(map[string]any) }

// Weak and malicious outputs: each is refused, and its records stand.
func TestValidateRefusesBadProposals(t *testing.T) {
	for _, c := range []struct {
		name    string
		output  map[string]any
		failure string
		reason  string
	}{
		{"foreign id in a label", map[string]any{"records": []any{map[string]any{"id": "999999999999", "subject": "x"}}}, consolidate.FailureNoneValid, consolidate.ReasonForeignID},
		{"label of a non-candidate", map[string]any{"records": []any{map[string]any{"id": active.ID, "subject": "x"}}}, consolidate.FailureNoneValid, consolidate.ReasonNotCandidate},
		{"foreign id in a merge", map[string]any{"merges": []any{map[string]any{"ids": []any{candA.ID, "999999999999"}, "content": "The deploy uses port 8080 on staging"}}}, consolidate.FailureNoneValid, consolidate.ReasonForeignID},
		{"merge dropping a specific", map[string]any{"merges": []any{map[string]any{"ids": []any{candA.ID, candB.ID}, "content": "Staging deploys run from the Makefile"}}}, consolidate.FailureNoneValid, consolidate.ReasonDropsSpecifics},
		{"merge of one", map[string]any{"merges": []any{map[string]any{"ids": []any{candA.ID, candA.ID}, "content": "x"}}}, consolidate.FailureNoneValid, consolidate.ReasonTooFew},
		{"merge of an attempt", map[string]any{"merges": []any{map[string]any{"ids": []any{candA.ID, candC.ID}, "content": "The deploy uses port 8080 on staging; go test failed in pkg/api"}}}, consolidate.FailureNoneValid, consolidate.ReasonCodeKind},
		{"empty merge text", map[string]any{"merges": []any{map[string]any{"ids": []any{candA.ID, candB.ID}, "content": "  "}}}, consolidate.FailureNoneValid, consolidate.ReasonEmpty},
		{"supersede of a foreign id", map[string]any{"records": []any{map[string]any{"id": candB.ID, "supersedes": []any{"999999999999"}}}}, consolidate.FailureNoneValid, consolidate.ReasonForeignID},
		{"supersede dropping a specific", map[string]any{"records": []any{map[string]any{"id": candB.ID, "supersedes": []any{candA.ID}}}}, consolidate.FailureNoneValid, consolidate.ReasonDropsSpecifics},
		{"self supersede", map[string]any{"records": []any{map[string]any{"id": candA.ID, "supersedes": []any{candA.ID}}}}, consolidate.FailureNoneValid, consolidate.ReasonSelf},
		{"reflection citing a foreign id", map[string]any{"reflections": []any{map[string]any{"content": "Deploys go to staging", "supportingIds": []any{"999999999999"}}}}, consolidate.FailureNoneValid, consolidate.ReasonForeignID},
		{"reflection carrying no support's specifics", map[string]any{"reflections": []any{map[string]any{"content": "Deploys are scripted", "supportingIds": []any{candA.ID}}}}, consolidate.FailureNoneValid, consolidate.ReasonNoSupport},
		{"reflection replacing one and dropping its specifics", map[string]any{"reflections": []any{map[string]any{"content": "Deploys run from the Makefile", "supportingIds": []any{candB.ID}, "supersedes": []any{reflOld.ID}}}}, consolidate.FailureNoneValid, consolidate.ReasonDropsSpecifics},
		{"malformed items", map[string]any{"records": []any{"x"}, "merges": []any{1}}, consolidate.FailureNoneValid, consolidate.ReasonMalformed},
		{"empty output", map[string]any{}, consolidate.FailureNothing, ""},
		{"empty lists", map[string]any{"records": []any{}, "merges": []any{}}, consolidate.FailureNothing, ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			result := consolidate.Validate(args(c.output), fixtureInput())
			if result.Failure != c.failure {
				t.Fatalf("failure %q, want %q (refusals %+v)", result.Failure, c.failure, result.Refusals)
			}
			if c.reason != "" && (len(result.Refusals) == 0 || result.Refusals[0].Reason != c.reason) {
				t.Fatalf("refusals %+v, want %q", result.Refusals, c.reason)
			}
			if len(result.Records)+len(result.Reflections)+len(result.Aliases)+len(result.Replaced) != 0 {
				t.Fatalf("a failure recorded something: %+v", result)
			}
		})
	}
	if result := consolidate.Validate(nil, fixtureInput()); result.Failure != consolidate.FailureNoCall {
		t.Fatalf("no call: %+v", result)
	}
}

// A good output: a label, a merge whose sources code sets, a reflection and an alias.
func TestValidateAcceptsAndSetsProvenance(t *testing.T) {
	in := fixtureInput()
	result := consolidate.Validate(args(map[string]any{
		"merges": []any{map[string]any{"ids": []any{candA.ID, candB.ID}, "content": "Staging deploys run from the Makefile on port 8080", "relevance": "high", "sourceEntryIds": []any{"forged"}}},
		"records": []any{
			map[string]any{"id": candC.ID, "outcome": "worked", "kind": "plan", "subject": "Tests/API"},
			// The merge already took candA: a second proposal for it is refused.
			map[string]any{"id": candA.ID, "subject": "ports"},
		},
		"reflections": []any{map[string]any{"content": "Deploys target staging on port 8080 via the Makefile", "supportingIds": []any{candA.ID}, "supersedes": []any{reflOld.ID}}},
	}), in)
	if result.Failure != "" || result.Merges != 1 || len(result.Records) != 2 || len(result.Reflections) != 1 {
		t.Fatalf("result %+v", result)
	}
	if len(result.Refusals) != 1 || result.Refusals[0].What != "record" || result.Refusals[0].Reason != consolidate.ReasonUsed {
		t.Fatalf("refusals %+v", result.Refusals)
	}
	merged := result.Records[0]
	if strings.Join(merged.SourceEntryIDs, ",") != "e-aaaaaaaaaaaa,e-bbbbbbbbbbbb" || merged.Origin != ledger.OriginModel || merged.Relevance != "high" || merged.Subject != "deploy" {
		t.Fatalf("merge %+v", merged)
	}
	if result.Replaced[candA.ID] != merged.ID || result.Replaced[candB.ID] != merged.ID {
		t.Fatalf("replaced %v", result.Replaced)
	}
	labelled := result.Records[1]
	// The attempt keeps its kind and text; its outcome and subject are the label's.
	if labelled.Kind != ledger.KindAttempt || labelled.Outcome != ledger.OutcomeWorked || labelled.Content != candC.Content || labelled.Subject != "tests/api" || labelled.ID == candC.ID {
		t.Fatalf("label %+v", labelled)
	}
	reflection := result.Reflections[0]
	// Support cited through the merge reads as the merged record, and the replaced reflection's
	// support joins it.
	if strings.Join(reflection.SupportingObservationIDs, ",") != merged.ID+",ffffffffffff" || strings.Join(reflection.Supersedes, ",") != reflOld.ID {
		t.Fatalf("reflection %+v", reflection)
	}
	if active := ledger.ActiveReflections([]ledger.Reflection{reflOld, reflection}); len(active) != 1 || active[0].ID != reflection.ID {
		t.Fatalf("active reflections %+v", active)
	}
	drop, _, kept := consolidate.Union(result, append(append([]ledger.Observation{}, in.Candidates...), in.Active...))
	if strings.Join(drop, ",") != candA.ID+","+candB.ID+","+candC.ID || kept != 0 {
		t.Fatalf("drop %v kept %d", drop, kept)
	}
}

// Duplicate merges: the second naming a record the first took is refused.
func TestValidateRefusesDuplicateMerges(t *testing.T) {
	merge := map[string]any{"ids": []any{candA.ID, candB.ID}, "content": "Staging deploys run from the Makefile on port 8080"}
	result := consolidate.Validate(args(map[string]any{"merges": []any{merge, merge}}), fixtureInput())
	if result.Merges != 1 || len(result.Refusals) != 1 || result.Refusals[0].Reason != consolidate.ReasonUsed {
		t.Fatalf("result %+v", result)
	}
}

// A supersede cycle, through the ledger or through the output itself, is refused.
func TestValidateRefusesSupersedeCycles(t *testing.T) {
	in := fixtureInput()
	x := record("111111111111", "The port is 8080, not 9090")
	y := record("222222222222", "The port is 9090, not 8080", func(o *ledger.Observation) { o.Supersedes = []string{x.ID} })
	in.Candidates = []ledger.Observation{x, y}
	in.Ledger.ObservationsByID[x.ID], in.Ledger.ObservationsByID[y.ID] = x, y
	in.Ledger.Observations = append(in.Ledger.Observations, x, y)
	// The ledger has y superseding x; x superseding y closes the cycle.
	result := consolidate.Validate(args(map[string]any{"records": []any{map[string]any{"id": x.ID, "supersedes": []any{y.ID}}}}), in)
	if result.Failure == "" || result.Refusals[0].Reason != consolidate.ReasonCycle {
		t.Fatalf("ledger cycle: %+v", result)
	}

	// Within one output: z replaces w, then w replaces z.
	z := record("333333333333", "The limit is 5 and 6")
	w := record("444444444444", "The limit is 6 and 5")
	in.Candidates = []ledger.Observation{z, w}
	result = consolidate.Validate(args(map[string]any{"records": []any{
		map[string]any{"id": z.ID, "supersedes": []any{w.ID}},
		map[string]any{"id": w.ID, "supersedes": []any{z.ID}},
	}}), in)
	// w is superseded by the accepted proposal, so the second is refused before it could close
	// the cycle: a superseded record is never relabelled.
	if len(result.Records) != 1 || len(result.Refusals) != 1 || result.Refusals[0].Reason != consolidate.ReasonSuperseded {
		t.Fatalf("output cycle: %+v", result)
	}

	// Different scopes never replace each other.
	s1 := record("555555555555", "Rent is 900", func(o *ledger.Observation) { o.Scope = "berlin" })
	s2 := record("666666666666", "Rent is 900 now", func(o *ledger.Observation) { o.Scope = "paris" })
	in.Candidates = []ledger.Observation{s1, s2}
	result = consolidate.Validate(args(map[string]any{"records": []any{map[string]any{"id": s2.ID, "supersedes": []any{s1.ID}}}}), in)
	if result.Failure == "" || result.Refusals[0].Reason != consolidate.ReasonCrossScope {
		t.Fatalf("cross scope: %+v", result)
	}
}

// A label that changes nothing accounts for its candidate and records nothing; aliases go
// through the kernel's alias check.
func TestValidateUnchangedLabelsAndAliases(t *testing.T) {
	result := consolidate.Validate(args(map[string]any{"records": []any{map[string]any{"id": candA.ID, "subject": "deploy", "kind": "fact"}}}), fixtureInput())
	if result.Failure != "" || result.Unchanged != 1 || len(result.Records) != 0 || len(result.Replaced) != 0 {
		t.Fatalf("unchanged: %+v", result)
	}
	result = consolidate.Validate(args(map[string]any{"aliases": []any{
		map[string]any{"from": "deploy", "to": "deploy"},
		map[string]any{"from": "nowhere", "to": "deploy"},
	}}), fixtureInput())
	if result.Failure != consolidate.FailureNoneValid || len(result.Refusals) != 2 {
		t.Fatalf("aliases: %+v", result)
	}
}

// The union rule keeps a record whose replacement does not carry its specifics, whatever the
// result claims.
func TestUnionKeepsUnaccountedSpecifics(t *testing.T) {
	forged := consolidate.Result{
		Records:  []ledger.Observation{{ID: "777777777777", Content: "Deploys are scripted"}},
		Replaced: map[string]string{candA.ID: "777777777777", candB.ID: "888888888888"},
	}
	drop, _, kept := consolidate.Union(forged, []ledger.Observation{candA, candB, candC})
	if len(drop) != 0 || kept != 2 {
		t.Fatalf("drop %v kept %d", drop, kept)
	}
}

// The prompt shows the cut range, the candidates, the current memory newest first under the
// summary budget with the older part counted as omitted, and the instructions; it is not built
// when no candidate fits.
func TestBuildPromptBudgets(t *testing.T) {
	older := record("999999999999", "An older record about the "+strings.Repeat("database ", 40))
	in := consolidate.PromptInput{
		Cut:          []ledger.Entry{testfix.RawMessage("u1", "Deploy to staging on port 8080")},
		Candidates:   []ledger.Observation{candA, candB},
		Active:       []ledger.Observation{older, active},
		Reflections:  []ledger.Reflection{reflOld},
		Instructions: "Keep the ports",
		Available:    10_000,
		MemoryBudget: 110,
	}
	prompt, ok := consolidate.BuildPrompt(in)
	if !ok || len(prompt.Candidates) != 2 || prompt.CutShown != 1 {
		t.Fatalf("prompt %+v", prompt)
	}
	for _, want := range []string{"[Source entry id: u1]", candA.ID, candB.ID, active.ID, reflOld.ID, "omitted: 1 older records and 0 older reflections", "INSTRUCTIONS FOR THIS COMPACTION (from the user):\nKeep the ports"} {
		if !strings.Contains(prompt.Text, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt.Text)
		}
	}
	if strings.Contains(prompt.Text, "["+older.ID+"]") || prompt.OmittedActive != 1 {
		t.Fatalf("the older record was shown:\n%s", prompt.Text)
	}
	if strings.Index(prompt.Text, "COMPACTED CONVERSATION") > strings.Index(prompt.Text, "CANDIDATES") || strings.Index(prompt.Text, "CURRENT MEMORY") > strings.Index(prompt.Text, "INSTRUCTIONS") {
		t.Fatalf("sections out of order:\n%s", prompt.Text)
	}

	in.Available = 20
	if _, ok := consolidate.BuildPrompt(in); ok {
		t.Fatal("built a prompt with no room for any candidate")
	}
	in.Available, in.Candidates = 10_000, nil
	if _, ok := consolidate.BuildPrompt(in); ok {
		t.Fatal("built a prompt with no candidates")
	}
}

// The kernel's level-1 rules name the forced tool.
func TestSystemPromptIsKernelOwned(t *testing.T) {
	system := consolidate.SystemPrompt()
	if !strings.Contains(system, consolidate.ToolName) || strings.TrimRight(system, "\n") != strings.TrimRight(kernel.Text(kernel.TextConsolidateRules), "\n") {
		t.Fatalf("system prompt:\n%s", system)
	}
	if _, owned := kernel.Owns("instructions", kernel.TextConsolidateRules); !owned {
		t.Fatal("the level-1 rules are not kernel-owned")
	}
	if consolidate.Tool().Name != consolidate.ToolName {
		t.Fatal("tool name")
	}
}
