package ledger

import "slices"

// The labels of a record (contracts/truth.md, "Labels"): whether the fold believes it, and if not
// why. The summary, the memory tool's search tags and level 1 all read this one labelling.
const (
	LabelBelieved   = "believed"
	LabelSuperseded = "superseded"
	LabelRetracted  = "retracted"
	LabelContested  = "contested"
	LabelHeld       = "held"
)

// LabelValues are the labels, in contracts/truth.md's order.
var LabelValues = []string{LabelBelieved, LabelSuperseded, LabelRetracted, LabelContested, LabelHeld}

// The three outcomes of a newer record against an older one on the same keys (Supersession).
const (
	// DecisionReplace: the newer replaces the older, which is labelled superseded and leaves the
	// current view (a correction, a retraction, level 1's settled update).
	DecisionReplace = "replace"
	// DecisionKeepBoth: both are kept as the history of a value; the newer is current and the
	// older is labelled superseded, never silently kept current.
	DecisionKeepBoth = "keep-both"
	// DecisionContested: neither wins; both are labelled contested and shown as a pair, never
	// settled by last-writer-wins.
	DecisionContested = "contested"
)

// Supersession is the three-way decision for later against earlier, two records level 0 linked
// on the same subject and scope (contracts/truth.md, "Supersession"); "" when they do not compete.
// It follows MaySupersede: a record whose status may not replace the other's never competes
// with it (a plan beside a fact), and only then:
//   - replace when later supersedes earlier;
//   - contested when exactly one negates a specific both carry (NegationConflict), or when later
//     gives a different value (UpdatesSpecific) on less authority than earlier's (AuthorityRank:
//     a tool result or the assistant against the user's words, the research's stale-fact case
//     turned around), since a lower authority never replaces a higher one;
//   - keep-both when later gives a different value on at least earlier's authority.
func Supersession(later, earlier Observation) string {
	switch {
	case !MaySupersede(later, earlier):
		return ""
	case slices.Contains(later.Supersedes, earlier.ID):
		return DecisionReplace
	case NegationConflict(later.Content, earlier.Content):
		return DecisionContested
	case !UpdatesSpecific(later.Content, earlier.Content):
		return ""
	case later.Authority != "" && earlier.Authority != "" && AuthorityRank(later.Authority) < AuthorityRank(earlier.Authority):
		return DecisionContested
	}
	return DecisionKeepBoth
}

// LabelsOf are the labels of records (in ledger order), by id, their links read from records
// themselves: the first that holds of
//   - retracted: its status, or a retraction among records withdraws it (RetractedIDs);
//   - superseded: a record supersedes it, or a later one's possible update of it is decided
//     keep-both (Supersession);
//   - held: it holds a preference that weakens security (HeldRule);
//   - contested: a claim standing contested (Contested), or one end of a conflict or of a possible
//     update decided contested;
//   - believed.
func LabelsOf(records []Observation) map[string]string {
	return labelsOf(records, records)
}

// labelsOf are the labels of records with the links read from from.
func labelsOf(records, from []Observation) map[string]string {
	byID := make(map[string]Observation, len(records))
	for _, o := range records {
		byID[o.ID] = o
	}
	superseded := map[string]bool{}
	contested := map[string]bool{}
	for id := range Contested(from) {
		contested[id] = true
	}
	for _, o := range from {
		for _, id := range o.Supersedes {
			if id != o.ID {
				superseded[id] = true
			}
		}
		for _, id := range o.Conflicts {
			if _, ok := byID[id]; ok && id != o.ID {
				contested[id], contested[o.ID] = true, true
			}
		}
		for _, id := range o.PossiblyUpdates {
			earlier, ok := byID[id]
			if !ok || id == o.ID {
				continue
			}
			switch Supersession(o, earlier) {
			case DecisionKeepBoth:
				superseded[id] = true
			case DecisionContested:
				contested[id], contested[o.ID] = true, true
			}
		}
	}
	retracted := RetractedIDs(from)
	out := make(map[string]string, len(records))
	for _, o := range records {
		out[o.ID] = LabelOf(o, superseded[o.ID], retracted[o.ID], contested[o.ID])
	}
	return out
}

// LabelOf is the first label that holds of o (LabelsOf), given whether a record supersedes it,
// a retraction withdraws it and it stands contested.
func LabelOf(o Observation, superseded, retracted, contested bool) string {
	switch {
	case retracted || o.Status == StatusRetracted:
		return LabelRetracted
	case superseded:
		return LabelSuperseded
	case HeldRule(o) != "":
		return LabelHeld
	case contested:
		return LabelContested
	}
	return LabelBelieved
}

// Labels are the labels of every record the fold holds, dropped ones included, by id: LabelsOf
// with the links read from the active records, a record level 1 replaced superseded, and one
// gone out (Truth.Out: a merge whose source was withdrawn) retracted. A forgotten record has no
// label: the fold does not hold it.
func (folded FoldedLedger) Labels() map[string]string {
	out := labelsOf(folded.Observations, folded.ActiveObservations)
	for id := range folded.ReplacedBy {
		if _, ok := out[id]; ok && out[id] != LabelRetracted {
			out[id] = LabelSuperseded
		}
	}
	for id := range folded.Truth.Out {
		if _, ok := out[id]; ok {
			out[id] = LabelRetracted
		}
	}
	for id, challenged := range folded.Challenged {
		if challenged && out[id] == LabelBelieved {
			out[id] = LabelContested
		}
	}
	for id := range folded.Contradicted {
		if out[id] == LabelBelieved {
			out[id] = LabelContested
		}
	}
	return out
}
