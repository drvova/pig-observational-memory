package consolidate_test

import (
	"slices"
	"strings"
	"testing"

	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/consolidate"
	"github.com/drvova/pig-observational-memory/extensions/observational-memory/internal/ledger"
)

// linkRecords are an old price, a later price that may update it, and an unrelated record.
func linkRecords() (old, later, other ledger.Observation) {
	old = record("c30000000001", "The Pro plan costs $40 a month", func(o *ledger.Observation) { o.Subject = "pricing" })
	later = record("c30000000002", "The Pro plan costs $45 a month", func(o *ledger.Observation) {
		o.Subject, o.PossiblyUpdates = "pricing", []string{old.ID}
	})
	other = record("c30000000003", "The Team plan costs $90 a month", func(o *ledger.Observation) { o.Subject = "pricing" })
	return old, later, other
}

func linkVerdict(later, earlier, verdict string) map[string]any {
	return map[string]any{"id": later, "updates": earlier, "verdict": verdict}
}

// A confirmed possible update becomes a supersede on the later record, which keeps its text and
// origin and replaces the record it was; a rejected one leaves the link; either way no other
// link moves and nothing is dropped by the verdict itself.
func TestValidateSettlesPossibleUpdates(t *testing.T) {
	old, later, other := linkRecords()
	in := inputOf([]ledger.Observation{later, other}, []ledger.Observation{old}, nil)
	in.Links = []consolidate.Link{{Later: later.ID, Earlier: old.ID}}

	result := consolidate.Validate(args(map[string]any{"links": []any{linkVerdict(later.ID, old.ID, consolidate.VerdictConfirm)}}), in)
	if result.Failure != "" || len(result.Records) != 1 || result.Confirmed != 1 || result.LinkRecords != 1 || result.Merges != 0 {
		t.Fatalf("confirm %+v", result)
	}
	r := result.Records[0]
	if r.Content != later.Content || r.Origin != later.Origin || !slices.Equal(r.Supersedes, []string{old.ID}) || len(r.PossiblyUpdates) != 0 {
		t.Fatalf("confirmed record %+v", r)
	}
	if result.Replaced[later.ID] != r.ID || result.Replaced[old.ID] != "" {
		t.Fatalf("replaced %v", result.Replaced)
	}
	// The union rule drops only the later record, whose replacement carries its text; the
	// earlier one leaves when the record superseding it renders.
	if drop, _, kept := consolidate.Union(result, []ledger.Observation{old, later, other}); !slices.Equal(drop, []string{later.ID}) || kept != 0 {
		t.Fatalf("union drop %v kept %d", drop, kept)
	}

	result = consolidate.Validate(args(map[string]any{"links": []any{linkVerdict(later.ID, old.ID, consolidate.VerdictReject)}}), in)
	if result.Failure != "" || len(result.Records) != 1 || result.Rejected != 1 {
		t.Fatalf("reject %+v", result)
	}
	if r := result.Records[0]; len(r.Supersedes) != 0 || len(r.PossiblyUpdates) != 0 || r.Content != later.Content {
		t.Fatalf("rejected record %+v", r)
	}
	// Settling gives a new id: a record with the link and one without are different records.
	confirmed := consolidate.Validate(args(map[string]any{"links": []any{linkVerdict(later.ID, old.ID, consolidate.VerdictConfirm)}}), in).Records[0].ID
	if result.Records[0].ID == confirmed || result.Records[0].ID == later.ID {
		t.Fatalf("ids %s %s %s", result.Records[0].ID, confirmed, later.ID)
	}
}

// Verdicts name only listed pairs, once each, with a verdict; a refused verdict leaves the link
// unsettled and is the whole output's only proposal, so the output fails and level 0 stands.
func TestValidateRefusesBadVerdicts(t *testing.T) {
	old, later, other := linkRecords()
	in := inputOf([]ledger.Observation{later, other}, []ledger.Observation{old}, nil)
	in.Links = []consolidate.Link{{Later: later.ID, Earlier: old.ID}}
	for _, c := range []struct {
		name  string
		links []any
		want  string
	}{
		{"unlisted pair", []any{linkVerdict(other.ID, old.ID, consolidate.VerdictConfirm)}, consolidate.ReasonNotLink},
		{"reversed pair", []any{linkVerdict(old.ID, later.ID, consolidate.VerdictConfirm)}, consolidate.ReasonNotLink},
		{"no verdict", []any{linkVerdict(later.ID, old.ID, "maybe")}, consolidate.ReasonVerdict},
		{"not an object", []any{"confirm"}, consolidate.ReasonMalformed},
	} {
		t.Run(c.name, func(t *testing.T) {
			result := consolidate.Validate(args(map[string]any{"links": c.links}), in)
			if result.Failure != consolidate.FailureNoneValid || len(result.Refusals) != 1 || result.Refusals[0].What != "link" || result.Refusals[0].Reason != c.want {
				t.Fatalf("result %+v", result)
			}
		})
	}
	// A second verdict on the same pair is refused; the first stands.
	result := consolidate.Validate(args(map[string]any{"links": []any{
		linkVerdict(later.ID, old.ID, consolidate.VerdictReject),
		linkVerdict(later.ID, old.ID, consolidate.VerdictConfirm),
	}}), in)
	if result.Rejected != 1 || result.Confirmed != 0 || len(result.Refusals) != 1 || result.Refusals[0].Reason != consolidate.ReasonLinkSettled {
		t.Fatalf("duplicate %+v", result)
	}
}

// A confirmed update may drop only the values it updates: every other specific of the earlier
// record must be carried, and the scopes must match.
func TestValidateConfirmKeepsOtherSpecifics(t *testing.T) {
	old := record("c40000000001", "Deploy Atlas to /srv/a with Nginx", func(o *ledger.Observation) { o.Subject = "deploy" })
	later := record("c40000000002", "Deploy Atlas to /srv/b", func(o *ledger.Observation) {
		o.Subject, o.PossiblyUpdates = "deploy", []string{old.ID}
	})
	in := inputOf([]ledger.Observation{later}, []ledger.Observation{old}, nil)
	in.Links = []consolidate.Link{{Later: later.ID, Earlier: old.ID}}
	result := consolidate.Validate(args(map[string]any{"links": []any{linkVerdict(later.ID, old.ID, consolidate.VerdictConfirm)}}), in)
	if result.Failure != consolidate.FailureNoneValid || result.Refusals[0].Reason != consolidate.ReasonDropsSpecifics {
		t.Fatalf("dropped Nginx: %+v", result)
	}
	// Carrying it, the path's update is confirmed.
	later.Content = "Deploy Atlas to /srv/b with Nginx"
	in = inputOf([]ledger.Observation{later}, []ledger.Observation{old}, nil)
	in.Links = []consolidate.Link{{Later: later.ID, Earlier: old.ID}}
	if result := consolidate.Validate(args(map[string]any{"links": []any{linkVerdict(later.ID, old.ID, consolidate.VerdictConfirm)}}), in); result.Confirmed != 1 {
		t.Fatalf("confirm %+v", result)
	}
	// Different scopes never replace each other.
	scoped := later
	scoped.Scope = "staging"
	oldScoped := old
	oldScoped.Scope = "production"
	in = inputOf([]ledger.Observation{scoped}, []ledger.Observation{oldScoped}, nil)
	in.Links = []consolidate.Link{{Later: later.ID, Earlier: old.ID}}
	if result := consolidate.Validate(args(map[string]any{"links": []any{linkVerdict(later.ID, old.ID, consolidate.VerdictConfirm)}}), in); result.Confirmed != 0 || result.Refusals[0].Reason != consolidate.ReasonCrossScope {
		t.Fatalf("cross scope %+v", result)
	}
}

// A verdict is applied to the record that stands for its later record: a label or a merge
// taking it carries the settled link; a merge joining both records refuses the verdict; a
// later record another proposal supersedes takes none.
func TestValidateVerdictsFollowLabelsAndMerges(t *testing.T) {
	old, later, other := linkRecords()
	in := inputOf([]ledger.Observation{later, other}, []ledger.Observation{old}, nil)
	in.Links = []consolidate.Link{{Later: later.ID, Earlier: old.ID}}
	confirm := []any{linkVerdict(later.ID, old.ID, consolidate.VerdictConfirm)}

	result := consolidate.Validate(args(map[string]any{"records": []any{map[string]any{"id": later.ID, "relevance": "high"}}, "links": confirm}), in)
	if len(result.Records) != 1 || result.Confirmed != 1 || result.LinkRecords != 0 {
		t.Fatalf("label %+v", result)
	}
	if r := result.Records[0]; r.Relevance != "high" || !slices.Equal(r.Supersedes, []string{old.ID}) || len(r.PossiblyUpdates) != 0 {
		t.Fatalf("labelled record %+v", r)
	}

	result = consolidate.Validate(args(map[string]any{
		"merges": []any{map[string]any{"ids": []any{later.ID, other.ID}, "content": "The Pro plan costs $45 a month and the Team plan costs $90 a month"}},
		"links":  confirm,
	}), in)
	if result.Merges != 1 || result.Confirmed != 1 || !slices.Contains(result.Records[0].Supersedes, old.ID) {
		t.Fatalf("merge %+v", result)
	}

	result = consolidate.Validate(args(map[string]any{
		"merges": []any{map[string]any{"ids": []any{later.ID, old.ID}, "content": "The Pro plan costs $45 a month, earlier $40 a month"}},
		"links":  confirm,
	}), in)
	if result.Merges != 1 || result.Confirmed != 0 || len(result.Refusals) != 1 || result.Refusals[0].Reason != consolidate.ReasonLinkMerged {
		t.Fatalf("merge of both %+v", result)
	}

	restated := record("c30000000004", "Since May the Pro plan costs $45 a month", func(o *ledger.Observation) { o.Subject = "pricing" })
	in = inputOf([]ledger.Observation{later, restated}, []ledger.Observation{old}, nil)
	in.Links = []consolidate.Link{{Later: later.ID, Earlier: old.ID}}
	result = consolidate.Validate(args(map[string]any{
		"records": []any{map[string]any{"id": restated.ID, "supersedes": []any{later.ID}}},
		"links":   confirm,
	}), in)
	if result.Confirmed != 0 || len(result.Records) != 1 || len(result.Refusals) != 1 || result.Refusals[0].Reason != consolidate.ReasonSuperseded {
		t.Fatalf("superseded later record %+v", result)
	}
}

// The level-1 input lists a possible update only when it shows both records, and says how many
// it could not list; the listed pairs are the ones validation accepts.
func TestBuildPromptListsPossibleUpdates(t *testing.T) {
	old, later, other := linkRecords()
	stale := record("c30000000009", "The Pro plan cost $35 a month", func(o *ledger.Observation) { o.Subject = "pricing" })
	later.PossiblyUpdates = []string{old.ID, stale.ID}
	in := consolidate.PromptInput{Candidates: []ledger.Observation{later, other}, Active: []ledger.Observation{old}, Available: 100_000, MemoryBudget: 100_000}
	prompt, ok := consolidate.BuildPrompt(in)
	if !ok || !slices.Equal(prompt.Links, []consolidate.Link{{Later: later.ID, Earlier: old.ID}}) || !strings.Contains(prompt.Text, later.ID+" may update "+old.ID) || strings.Contains(prompt.Text, stale.ID) {
		t.Fatalf("links %v:\n%s", prompt.Links, prompt.Text)
	}
	// With the earlier record not shown, nothing is listed.
	in.Active = nil
	if prompt, _ := consolidate.BuildPrompt(in); len(prompt.Links) != 0 || strings.Contains(prompt.Text, "POSSIBLE UPDATES") {
		t.Fatalf("listed without the earlier record:\n%s", prompt.Text)
	}
	// Under every room, a link is listed or counted as omitted, and the prompt fits.
	in.Active = []ledger.Observation{old}
	sawOmitted := false
	for available := 100; available <= 600; available += 5 {
		in.Available = available
		prompt, ok := consolidate.BuildPrompt(in)
		if !ok {
			continue
		}
		if prompt.Tokens > available {
			t.Fatalf("available %d: %d tokens", available, prompt.Tokens)
		}
		shownBoth := slices.ContainsFunc(prompt.Candidates, func(o ledger.Observation) bool { return o.ID == later.ID }) && slices.ContainsFunc(prompt.Active, func(o ledger.Observation) bool { return o.ID == old.ID })
		if shownBoth && len(prompt.Links)+prompt.OmittedLinks != 1 {
			t.Fatalf("available %d: links %v omitted %d", available, prompt.Links, prompt.OmittedLinks)
		}
		if prompt.OmittedLinks > 0 {
			sawOmitted = true
			if !strings.Contains(prompt.Text, "1 more possible updates are not shown") {
				t.Fatalf("omitted link not announced:\n%s", prompt.Text)
			}
		}
	}
	if !sawOmitted {
		t.Fatal("no room left a link out")
	}
}

// A planned or conditional value never replaces a stated one: a confirm of such a pair, or a
// supersede the model proposes onto a stated record from a plan, is refused, and the link stays.
func TestValidateRefusesPlansReplacingFacts(t *testing.T) {
	old, later, other := linkRecords()
	later.Content, later.Status = "We will make the Pro plan cost $45 a month", ledger.StatusProposed
	in := inputOf([]ledger.Observation{later, other}, []ledger.Observation{old}, nil)
	in.Links = []consolidate.Link{{Later: later.ID, Earlier: old.ID}}
	result := consolidate.Validate(args(map[string]any{"links": []any{linkVerdict(later.ID, old.ID, consolidate.VerdictConfirm)}}), in)
	if result.Confirmed != 0 || len(result.Refusals) != 1 || result.Refusals[0].Reason != consolidate.ReasonStatusReplace {
		t.Fatalf("confirm of a plan onto a fact %+v", result)
	}
	// A rejection still settles it.
	if result := consolidate.Validate(args(map[string]any{"links": []any{linkVerdict(later.ID, old.ID, consolidate.VerdictReject)}}), in); result.Rejected != 1 {
		t.Fatalf("reject %+v", result)
	}
	// The same through a label's supersedes.
	label := map[string]any{"id": later.ID, "subject": "pricing plans", "supersedes": []any{old.ID}}
	result = consolidate.Validate(args(map[string]any{"records": []any{label}}), in)
	if len(result.Refusals) != 1 || result.Refusals[0].Reason != consolidate.ReasonStatusReplace {
		t.Fatalf("label supersede of a fact by a plan %+v", result)
	}
}

// A level-1 merge keeps the one event date its sources name, never a date one source gives when
// another names dates but no one date.
func TestValidateMergeEventDate(t *testing.T) {
	deploy := record("c40000000001", "The billing deploy is tomorrow [=2026-10-21]", func(o *ledger.Observation) { o.Subject, o.EventDate = "billing", "2026-10-21" })
	freeze := record("c40000000002", "The billing freeze starts yesterday [=2026-10-19] and the demo is today [=2026-10-20]", func(o *ledger.Observation) { o.Subject = "billing" })
	plain := record("c40000000003", "The billing service runs on Stripe v2", func(o *ledger.Observation) { o.Subject = "billing" })
	in := inputOf([]ledger.Observation{deploy, freeze, plain}, nil, nil)
	merge := func(content string, ids ...any) map[string]any {
		return map[string]any{"merges": []any{map[string]any{"ids": ids, "content": content}}}
	}
	result := consolidate.Validate(args(merge(deploy.Content+"; "+freeze.Content, deploy.ID, freeze.ID)), in)
	if result.Merges != 1 || result.Records[0].EventDate != "" {
		t.Fatalf("a source naming several dates leaves none: %+v", result)
	}
	result = consolidate.Validate(args(merge(deploy.Content+"; "+plain.Content, deploy.ID, plain.ID)), in)
	if result.Merges != 1 || result.Records[0].EventDate != "2026-10-21" {
		t.Fatalf("a source naming no date keeps the other's: %+v", result)
	}
}
