package ledger

import "github.com/drvova/pig-observational-memory/memory/insecure"

// HeldRule is the rule of the security-weakening pattern list (insecure.Match;
// docs/brain-memory.md, decision 19) a preference record matches: a record of kind preference or
// correction, whoever wrote it, or a level-1 merge of the user's own (FromUser), whose content
// matches. "" for any other record. A held record is kept and listed in /om:status, never
// rendered in a summary, so a reflection resting on one is held too (HeldReflection).
func HeldRule(o Observation) string {
	if o.Kind != "preference" && o.Kind != "correction" && !o.FromUser {
		return ""
	}
	rule, _ := insecure.Match(o.Content)
	return rule
}

// HeldReflection is the rule of the first held record (HeldRule) a reflection cites, among the
// records byID holds; "" when it cites none.
func HeldReflection(r Reflection, byID map[string]Observation) string {
	for _, id := range r.SupportingObservationIDs {
		if o, ok := byID[id]; ok {
			if rule := HeldRule(o); rule != "" {
				return rule
			}
		}
	}
	return ""
}

// WithoutHeld are observations and reflections without the held ones (HeldRule, HeldReflection),
// in order; byID resolves a reflection's records (FoldedLedger.ObservationsByID).
func WithoutHeld(observations []Observation, reflections []Reflection, byID map[string]Observation) ([]Observation, []Reflection) {
	obs := make([]Observation, 0, len(observations))
	for _, o := range observations {
		if HeldRule(o) == "" {
			obs = append(obs, o)
		}
	}
	refs := make([]Reflection, 0, len(reflections))
	for _, r := range reflections {
		if HeldReflection(r, byID) == "" {
			refs = append(refs, r)
		}
	}
	return obs, refs
}
