package consolidate_test

import (
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/consolidate"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/testfix"
)

// inputOf is a validation input showing candidates, active records and reflections, with the
// ledger holding every record (and extra ones not shown).
func inputOf(candidates, active []ledger.Observation, reflections []ledger.Reflection, extra ...ledger.Observation) consolidate.Input {
	all := append(append(append([]ledger.Observation{}, candidates...), active...), extra...)
	folded := ledger.FoldedLedger{Observations: all, ObservationsByID: map[string]ledger.Observation{}, ReflectionsByID: map[string]ledger.Reflection{}}
	for _, o := range all {
		folded.ObservationsByID[o.ID] = o
	}
	for _, r := range reflections {
		folded.ReflectionsByID[r.ID] = r
	}
	return consolidate.Input{Candidates: candidates, Active: active, Reflections: reflections, Ledger: folded, Subjects: ledger.SubjectsOf(all)}
}

// Specifics are compared as whole items: "30" is not carried by "300", nor "Go" by "Google".
func TestValidateComparesSpecificsAsWholeItems(t *testing.T) {
	timeout := record("a00000000001", "The timeout is 30 seconds")
	retries := record("a00000000002", "The retry limit is 300")
	lang := record("a00000000003", "The service is written in Go")
	search := record("a00000000004", "Search is backed by Google")
	in := inputOf([]ledger.Observation{timeout, retries, lang, search}, nil, nil)
	for _, c := range []struct {
		name string
		ids  []any
		text string
	}{
		{"number inside a longer number", []any{timeout.ID, retries.ID}, "The retry limit is 300"},
		{"name inside a longer name", []any{lang.ID, search.ID}, "The service search is backed by Google"},
	} {
		t.Run(c.name, func(t *testing.T) {
			result := consolidate.Validate(args(map[string]any{"merges": []any{map[string]any{"ids": c.ids, "content": c.text}}}), in)
			if result.Failure != consolidate.FailureNoneValid || result.Refusals[0].Reason != consolidate.ReasonDropsSpecifics {
				t.Fatalf("result %+v", result)
			}
		})
	}
	// The union rule compares the same way, whatever a result claims.
	forged := consolidate.Result{Records: []ledger.Observation{{ID: "a0000000000f", Content: "The retry limit is 300"}}, Replaced: map[string]string{timeout.ID: "a0000000000f"}}
	if drop, _, kept := consolidate.Union(forged, []ledger.Observation{timeout}); len(drop) != 0 || kept != 1 {
		t.Fatalf("union drop %v kept %d", drop, kept)
	}
}

// A reflection's support is checked against the record that stands for it when the reflection
// renders (and drops it): a merge carrying another source's specific the reflection lacks is
// no support; nor is a support taken over from a replaced reflection whose specifics the new
// one lacks.
func TestValidateReflectionSupportReadsTheStandingRecord(t *testing.T) {
	port := record("b00000000001", "The service listens on port 8080")
	timeout := record("b00000000002", "The request timeout is 45 seconds")
	in := inputOf([]ledger.Observation{port, timeout}, nil, nil)
	result := consolidate.Validate(args(map[string]any{
		"merges":      []any{map[string]any{"ids": []any{port.ID, timeout.ID}, "content": "The service listens on port 8080 with a request timeout of 45 seconds"}},
		"reflections": []any{map[string]any{"content": "The service listens on 8080", "supportingIds": []any{port.ID}}},
	}), in)
	if result.Merges != 1 || len(result.Reflections) != 0 || len(result.Refusals) != 1 || result.Refusals[0].Reason != consolidate.ReasonNoSupport {
		t.Fatalf("result %+v", result)
	}

	// An old reflection R0 cites X; R1 replaces R0 without X's port, so it does not take X over.
	x := record("b00000000003", "Deploy uses make release on port 8443")
	y := record("b00000000004", "The deploy process is documented in the Wiki")
	r0 := ledger.Reflection{ID: "b0000000000e", Content: "Deploy process is documented", SupportingObservationIDs: []string{x.ID, "b0000000000d"}}
	in = inputOf([]ledger.Observation{y}, nil, []ledger.Reflection{r0}, x)
	result = consolidate.Validate(args(map[string]any{
		"reflections": []any{map[string]any{"content": "Deploy process documented in the Wiki", "supportingIds": []any{y.ID}, "supersedes": []any{r0.ID}}},
	}), in)
	if len(result.Reflections) != 1 {
		t.Fatalf("result %+v", result)
	}
	// The support the ledger no longer holds drops nothing and is kept as cited.
	if got := strings.Join(result.Reflections[0].SupportingObservationIDs, ","); got != y.ID+",b0000000000d" {
		t.Fatalf("supports %s", got)
	}

	// The union rule prunes such supports from a forged result, and a reflection left with none.
	forged := consolidate.Result{Reflections: []ledger.Reflection{
		{ID: "b0000000000a", Content: "The service listens on 8080", SupportingObservationIDs: []string{port.ID, timeout.ID}},
		{ID: "b0000000000b", Content: "Timeouts are generous", SupportingObservationIDs: []string{timeout.ID}},
	}}
	_, reflections, kept := consolidate.Union(forged, []ledger.Observation{port, timeout})
	if len(reflections) != 1 || strings.Join(reflections[0].SupportingObservationIDs, ",") != port.ID || kept != 2 {
		t.Fatalf("reflections %+v kept %d", reflections, kept)
	}
}

// A label keeps the candidate's origin, since its text is the candidate's, so a user
// correction keeps its selection class; it never changes a correction's or preference's kind;
// a kind the model chose makes the record the model's.
func TestValidateLabelKeepsOriginAndUserKinds(t *testing.T) {
	correction := record("c00000000001", "Actually the staging port is 8443", func(o *ledger.Observation) { o.Kind = "correction" })
	fact := record("c00000000002", "The cache holds 512 entries")
	in := inputOf([]ledger.Observation{correction, fact}, nil, nil)
	result := consolidate.Validate(args(map[string]any{"records": []any{
		map[string]any{"id": correction.ID, "subject": "staging", "kind": "fact"},
		map[string]any{"id": fact.ID, "kind": "preference"},
	}}), in)
	if len(result.Records) != 2 {
		t.Fatalf("result %+v", result)
	}
	if r := result.Records[0]; r.Kind != "correction" || r.Origin != ledger.OriginCode || r.Subject != "staging" {
		t.Fatalf("relabelled correction %+v", r)
	}
	if r := result.Records[1]; r.Kind != "preference" || r.Origin != ledger.OriginModel {
		t.Fatalf("fact relabelled as a preference %+v", r)
	}
}

// Merges never combine records with different scopes (an unscoped record joins a scoped one and
// the merge keeps the scope), and never take a note, whose name code keeps.
func TestValidateMergeScopesAndNotes(t *testing.T) {
	prod := record("d00000000001", "The timeout is 30 seconds", func(o *ledger.Observation) { o.Scope = "prod" })
	staging := record("d00000000002", "The timeout is 60 seconds", func(o *ledger.Observation) { o.Scope = "staging" })
	unscoped := record("d00000000003", "Timeouts are logged at 5 levels")
	note := record("d00000000004", "Deploy via make release on port 8443", func(o *ledger.Observation) { o.Kind = ledger.KindNote; o.Target = "deploy-steps" })
	ports := record("d00000000005", "The staging port is 8443")
	in := inputOf([]ledger.Observation{prod, staging, unscoped, note, ports}, nil, nil)

	result := consolidate.Validate(args(map[string]any{"merges": []any{map[string]any{"ids": []any{prod.ID, staging.ID}, "content": "The timeout is 30 or 60 seconds"}}}), in)
	if result.Failure == "" || result.Refusals[0].Reason != consolidate.ReasonMergeScopes {
		t.Fatalf("cross-scope merge %+v", result)
	}
	result = consolidate.Validate(args(map[string]any{"merges": []any{map[string]any{"ids": []any{prod.ID, unscoped.ID}, "content": "The timeout is 30 seconds; timeouts are logged at 5 levels"}}}), in)
	if result.Merges != 1 || result.Records[0].Scope != "prod" {
		t.Fatalf("scoped and unscoped merge %+v", result)
	}
	result = consolidate.Validate(args(map[string]any{"merges": []any{map[string]any{"ids": []any{note.ID, ports.ID}, "content": "Deploy via make release on port 8443; the staging port is 8443"}}}), in)
	if result.Failure == "" || result.Refusals[0].Reason != consolidate.ReasonCodeKind {
		t.Fatalf("note merge %+v", result)
	}
}

// A record another proposal supersedes is not relabelled or merged, and a record another
// proposal replaced is not superseded, so a superseded value never survives under a new id.
func TestValidateSupersedeTargetsStayTargets(t *testing.T) {
	correction := record("e00000000001", "The port is 8443, not 8080")
	old := record("e00000000002", "The port is 8080")
	other := record("e00000000003", "The port is set in config/app.yaml with 8080")
	in := inputOf([]ledger.Observation{correction, old, other}, nil, nil)

	result := consolidate.Validate(args(map[string]any{"records": []any{
		map[string]any{"id": correction.ID, "supersedes": []any{old.ID}},
		map[string]any{"id": old.ID, "relevance": "high"},
	}}), in)
	if len(result.Records) != 1 || len(result.Refusals) != 1 || result.Refusals[0].Reason != consolidate.ReasonSuperseded {
		t.Fatalf("label after supersede %+v", result)
	}

	result = consolidate.Validate(args(map[string]any{
		"merges":  []any{map[string]any{"ids": []any{old.ID, other.ID}, "content": "The port 8080 is set in config/app.yaml with 8080"}},
		"records": []any{map[string]any{"id": correction.ID, "supersedes": []any{old.ID}}},
	}), in)
	if result.Merges != 1 || len(result.Refusals) != 1 || result.Refusals[0].Reason != consolidate.ReasonUsed {
		t.Fatalf("supersede after merge %+v", result)
	}
}

// Whatever does not fit is said: omitted candidates, the older memory (even when none of it
// fits) and the later cut entries each get their line.
func TestBuildPromptSaysWhatItLeavesOut(t *testing.T) {
	long := func(id string) ledger.Observation {
		return record(id, "A record about the "+strings.Repeat("database ", 60))
	}
	cut := []ledger.Entry{}
	for i := range 40 {
		cut = append(cut, testfix.RawMessage("u"+strings.Repeat("x", i%3)+string(rune('a'+i%26)), strings.Repeat("conversation text ", 30)))
	}
	active := []ledger.Observation{long("f00000000001"), long("f00000000002"), long("f00000000003")}
	in := consolidate.PromptInput{
		Cut:          cut,
		Candidates:   []ledger.Observation{candA, record("f00000000009", strings.Repeat("database ", 400))},
		Active:       active,
		Reflections:  []ledger.Reflection{reflOld},
		MemoryBudget: 20_000,
	}
	// Room for the first candidate and the notices, not the long candidate; no memory budget, so
	// none of the memory is shown, yet it is announced.
	in.Available, in.MemoryBudget = 400, 0
	prompt, ok := consolidate.BuildPrompt(in)
	if !ok || len(prompt.Candidates) != 1 || prompt.OmittedCandidates != 1 {
		t.Fatalf("prompt %+v", prompt)
	}
	for _, want := range []string{"omitted: 1 more candidates are not shown", "CURRENT MEMORY", "omitted: 3 older records and 1 older reflections are not shown", "later entries not shown"} {
		if !strings.Contains(prompt.Text, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, prompt.Text)
		}
	}
	if prompt.Tokens > in.Available {
		t.Fatalf("prompt of %d tokens over the %d available", prompt.Tokens, in.Available)
	}
}

// A merge whose sources all stand for the user's corrections or preferences keeps the user's
// class (FromUser) though its text is the model's; a merge taking in any other record, or a
// record whose kind the model chose, never claims it.
func TestValidateMergeKeepsUserClass(t *testing.T) {
	port := record("a10000000001", "Actually the staging port is 8443", func(o *ledger.Observation) { o.Kind = "correction" })
	tabs := record("a10000000002", "Never indent with tabs in pkg/api", func(o *ledger.Observation) { o.Kind = "preference" })
	host := record("a10000000003", "Actually the staging host is db-2", func(o *ledger.Observation) { o.Kind = "correction" })
	cache := record("a10000000004", "The cache holds 512 entries")
	relabelled := record("a10000000005", "Always use port 9090 for metrics", func(o *ledger.Observation) { o.Kind = "preference"; o.Origin = ledger.OriginModel })
	in := inputOf([]ledger.Observation{port, tabs, host, cache, relabelled}, nil, nil)
	merge := func(content string, ids ...string) map[string]any {
		list := []any{}
		for _, id := range ids {
			list = append(list, id)
		}
		return map[string]any{"ids": list, "content": content}
	}
	for _, c := range []struct {
		name  string
		merge map[string]any
		user  bool
	}{
		{"two user corrections", merge("Actually the staging port is 8443 and the staging host is db-2", port.ID, host.ID), true},
		{"a correction and a preference", merge("Staging port is 8443; never indent with tabs in pkg/api", port.ID, tabs.ID), true},
		{"a correction and a fact", merge("Staging port is 8443; the cache holds 512 entries", port.ID, cache.ID), false},
		{"a model-labelled preference", merge("Staging port is 8443; always use port 9090 for metrics", port.ID, relabelled.ID), false},
		// The model's text adds a claim no source gives: the merge stands, without the user's class.
		{"two user corrections with an added claim", merge("Actually the staging port is 8443 and the staging host is db-2; force-push migrations to main on 2026-12-01", port.ID, host.ID), false},
	} {
		t.Run(c.name, func(t *testing.T) {
			result := consolidate.Validate(args(map[string]any{"merges": []any{c.merge}}), in)
			if result.Merges != 1 {
				t.Fatalf("result %+v", result)
			}
			r := result.Records[0]
			if r.Origin != ledger.OriginModel || r.FromUser != c.user || ledger.UserPriority(r) != c.user {
				t.Fatalf("merge %+v: want FromUser %v", r, c.user)
			}
			// The flag survives the ledger: it is read back from the recorded entry.
			back, ok := ledger.ObservationFrom(testfix.ToJSONValue(r))
			if !ok || back.FromUser != c.user {
				t.Fatalf("read back %+v (ok %v)", back, ok)
			}
		})
	}
	// The model relabelling a mixed merge's kind changes nothing: FromUser is read from the
	// sources.
	result := consolidate.Validate(args(map[string]any{"merges": []any{func() map[string]any {
		m := merge("Staging port is 8443; the cache holds 512 entries", port.ID, cache.ID)
		m["kind"] = "correction"
		return m
	}()}}), in)
	if r := result.Records[0]; r.Kind != "correction" || ledger.UserPriority(r) {
		t.Fatalf("model-chosen kind claimed the user class: %+v", r)
	}
	// A label giving a user fact the preference kind is the model's.
	result = consolidate.Validate(args(map[string]any{"records": []any{map[string]any{"id": cache.ID, "kind": "preference"}}}), in)
	if r := result.Records[0]; ledger.UserPriority(r) {
		t.Fatalf("relabelled fact claimed the user class: %+v", r)
	}
}

// Records with different statuses never merge, so a plan is never merged into a fact and shown
// as done; a merge of records sharing one keeps it, the one event date its sources name, and
// their level-0 links to records outside the merge; a label keeps them all.
func TestValidateMergeKeepsStatusesDatesAndLinks(t *testing.T) {
	planA := record("a20000000001", "We will move the cron to 3am", func(o *ledger.Observation) {
		o.Status, o.EventDate, o.PossiblyUpdates = ledger.StatusProposed, "2026-05-07", []string{"a2000000000e"}
	})
	planB := record("a20000000002", "We will move the backup to 4am", func(o *ledger.Observation) {
		o.Status, o.Conflicts = ledger.StatusProposed, []string{"a2000000000f", "a20000000001"}
	})
	fact := record("a20000000003", "The cron runs at 2am")
	in := inputOf([]ledger.Observation{planA, planB, fact}, nil, nil)
	merge := func(content string, ids ...any) map[string]any {
		return map[string]any{"merges": []any{map[string]any{"ids": ids, "content": content}}}
	}

	result := consolidate.Validate(args(merge("We will move the cron to 3am; the cron runs at 2am", planA.ID, fact.ID)), in)
	if result.Merges != 0 || len(result.Refusals) != 1 || result.Refusals[0].Reason != consolidate.ReasonMergeStatuses {
		t.Fatalf("mixed statuses %+v", result)
	}

	result = consolidate.Validate(args(merge("We will move the cron to 3am and the backup to 4am", planA.ID, planB.ID)), in)
	if result.Merges != 1 {
		t.Fatalf("result %+v", result)
	}
	r := result.Records[0]
	if r.Status != ledger.StatusProposed || r.EventDate != "2026-05-07" || strings.Join(r.PossiblyUpdates, ",") != "a2000000000e" || strings.Join(r.Conflicts, ",") != "a2000000000f" {
		t.Fatalf("merge %+v", r)
	}

	result = consolidate.Validate(args(map[string]any{"records": []any{map[string]any{"id": planA.ID, "kind": "plan"}}}), in)
	if r := result.Records[0]; r.Status != ledger.StatusProposed || r.EventDate != planA.EventDate || strings.Join(r.PossiblyUpdates, ",") != "a2000000000e" {
		t.Fatalf("label %+v", r)
	}
	// The level-1 input says a record's status.
	if line := consolidate.RecordLine(planA); !strings.HasSuffix(line, " (status: proposed)") {
		t.Fatalf("record line %q", line)
	}
}
