package consolidate

import (
	"slices"
	"strings"
)

// Harness definitions proposed in level 1 (docs/memory-v2.md, "Self-built harness"): under
// memory v2 the only place the model defines part of its own harness is the level-1 request,
// and only when code found the harness evidence changed since the last offer (the hook decides,
// rules.ProposalDue). Validation here is structural: what the input offered, one definition per
// name, text present. The hook then holds each through the layer's checks (layer.Store.Check,
// rules.CheckFromModel, harness.CheckModelDefinition), and settles it only on evidence a later
// compaction produces.

// DefinitionOffer is what one level-1 request may define, nil when it may define nothing.
type DefinitionOffer struct {
	// Kinds are the definition kinds offered (layer kinds that act under memory v2).
	Kinds []string
	// Detectors and Remedies are what a proposed rule may name; a nil Remedies offers no rule.
	Detectors, Remedies []string
	// Evidence is the harness evidence the input shows: what code counted, the rules and the
	// definitions that stand.
	Evidence string
}

// Definition is one harness definition level 1 proposed, structurally valid.
type Definition struct {
	Kind, Name, Text string
}

// RuleProposal is one rule level 1 proposed: a detector and a code remedy.
type RuleProposal struct {
	Detector, Remedy string
}

// headingHarness opens the harness evidence section.
const headingHarness = "HARNESS EVIDENCE (what code counted against this memory since your last proposal, and the definitions that stand; you may define in definitions and propose in rules, each held until a later compaction proves it):"

// harnessNotShown stands for the harness evidence when it does not fit; nothing may then be
// defined.
const harnessNotShown = "(not shown: no room; define nothing this time)"

// ReasonNotOffered refuses a definition or rule the input did not offer.
const ReasonNotOffered = "defines what the input did not offer (definitions are offered only with HARNESS EVIDENCE)"

// ReasonDefinedTwice refuses a second definition of one kind and name, or a second identical
// rule.
const ReasonDefinedTwice = "defines a name another proposal of this output already defines"

// definitionsSchema is the tool's definitions property for an offer.
func definitionsSchema(offer DefinitionOffer) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
		"kind": enumOf(offer.Kinds, "The definition kind."),
		"name": map[string]any{"type": "string", "description": "Lowercase letters, digits and hyphens; one home per name: defining it again replaces it."},
		"text": map[string]any{"type": "string", "description": "The declarative text, in your own words: instructions (added after the seed, which they extend and never replace), a detector or a remedy catalogue."},
	}, "required": []any{"kind", "name", "text"}}}
}

// rulesSchema is the tool's rules property for an offer.
func rulesSchema(offer DefinitionOffer) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "object", "properties": map[string]any{
		"detector": enumOf(offer.Detectors, "The detector whose count the rule answers."),
		"remedy":   enumOf(offer.Remedies, "The code remedy it applies."),
	}, "required": []any{"detector", "remedy"}}}
}

// definitions validates the definitions items: refused all when nothing was offered, else each
// needs an offered kind, a name and a text, once per kind and name.
func (v *validator) definitions(items []any) {
	seen := map[string]bool{}
	for i, item := range items {
		m, ok := item.(map[string]any)
		switch {
		case v.in.Offer == nil:
			v.refuse(RefusalDefinition, i, ReasonNotOffered)
			continue
		case !ok:
			v.refuse(RefusalDefinition, i, ReasonMalformed)
			continue
		}
		d := Definition{Kind: oneLine(m["kind"]), Name: oneLine(m["name"])}
		d.Text, _ = m["text"].(string)
		switch {
		case !slices.Contains(v.in.Offer.Kinds, d.Kind):
			v.refuse(RefusalDefinition, i, ReasonNotOffered)
		case d.Name == "" || strings.TrimSpace(d.Text) == "":
			v.refuse(RefusalDefinition, i, ReasonEmpty)
		case seen[d.Kind+"/"+d.Name]:
			v.refuse(RefusalDefinition, i, ReasonDefinedTwice)
		default:
			seen[d.Kind+"/"+d.Name] = true
			v.result.Definitions = append(v.result.Definitions, d)
		}
	}
}

// rules validates the rules items: refused all when no rule was offered, else each names an
// offered detector and remedy, once.
func (v *validator) rules(items []any) {
	seen := map[RuleProposal]bool{}
	for i, item := range items {
		m, ok := item.(map[string]any)
		switch {
		case v.in.Offer == nil || len(v.in.Offer.Remedies) == 0:
			v.refuse(RefusalRule, i, ReasonNotOffered)
			continue
		case !ok:
			v.refuse(RefusalRule, i, ReasonMalformed)
			continue
		}
		r := RuleProposal{Detector: oneLine(m["detector"]), Remedy: oneLine(m["remedy"])}
		switch {
		case !slices.Contains(v.in.Offer.Detectors, r.Detector) || !slices.Contains(v.in.Offer.Remedies, r.Remedy):
			v.refuse(RefusalRule, i, ReasonNotOffered)
		case seen[r]:
			v.refuse(RefusalRule, i, ReasonDefinedTwice)
		default:
			seen[r] = true
			v.result.Rules = append(v.result.Rules, r)
		}
	}
}
